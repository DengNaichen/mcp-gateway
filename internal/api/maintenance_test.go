package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/gateway"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/registry"
)

func testGateway(t *testing.T) (*gateway.Gateway, gateway.Services, string) {
	return testGatewayOptions(t, "", nil)
}
func testGatewayOptions(t *testing.T, command string, policy *sandbox.Policy) (*gateway.Gateway, gateway.Services, string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for adapter/process tests")
	}
	fixture, err := os.ReadFile("../maintenance/testdata/pi-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if command == "" {
		command = filepath.Join(t.TempDir(), "pi-test")
		if err := os.WriteFile(command, fixture, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mode := "local"
	if os.Getenv("GATEWAY_AUTH_FILE") != "" {
		mode = "docker"
	}
	t.Setenv("GATEWAY_EXECUTION_MODE", mode)
	if policy != nil {
		t.Setenv("GATEWAY_EXECUTION_MODE", policy.Mode)
		t.Setenv("GATEWAY_SANDBOX_IMAGE", policy.Image)
	}
	t.Setenv("GATEWAY_PI_COMMAND", command)
	g, svcs, err := gateway.New(t.TempDir(), "http://localhost:8788")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	useKeywordModel(t, g, svcs)
	if err := svcs.Registry.SaveServer(registry.Server{ID: "service", Name: "Service", Kind: "http", Endpoint: "http://127.0.0.1:1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(t.TempDir(), "api.json")
	putSource(t, reference, "v1", "")
	if err := svcs.Maintenance.SaveSource(maintenance.MaintenanceSource{ServerID: "service", Files: []string{reference}, IntervalMinutes: 60, TimeoutSeconds: 30, AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	setLogsAcceptance(t, svcs.Maintenance, "v1", "")
	return g, svcs, reference
}
func putSource(t *testing.T, path, version, mode string) {
	t.Helper()
	if err := writeJSON(path, map[string]string{"version": version, "mode": mode}); err != nil {
		t.Fatal(err)
	}
}
func waitRun(t *testing.T, m *maintenance.Maintainer, id string, status ...string) maintenance.MaintenanceRun {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := m.GetRun(id)
		if slices.Contains(status, r.Status) {
			return r
		}
		if r.Status == "failed" {
			t.Fatalf("run failed: %s", r.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := m.GetRun(id)
	t.Fatalf("timeout: %+v", r)
	return r
}
func enqueueTest(t *testing.T, m *maintenance.Maintainer, force bool) maintenance.MaintenanceRun {
	t.Helper()
	r, err := m.Run("service", force)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMaintenanceLifecycleAndRollback(t *testing.T) {
	g, svcs, reference := testGateway(t)
	m := svcs.Maintenance
	m.Start(context.Background())
	first := approveRun(t, m, enqueueTest(t, m, false).ID)
	if len(first.Tools) != 1 || !first.Tools[0].Enabled {
		t.Fatalf("bad published tools: %+v", first.Tools)
	}
	discovery, err := g.Discover(context.Background(), gateway.DiscoverRequest{Intent: "logs"})
	if err != nil || len(discovery.Tools) != 1 {
		t.Fatalf("discovery %v: %+v", err, discovery)
	}
	second := waitRun(t, m, enqueueTest(t, m, false).ID, "no_change")
	if second.Fingerprint != first.Fingerprint {
		t.Fatal("unchanged source fingerprint changed")
	}
	putSource(t, reference, "v2", "")
	setLogsAcceptance(t, m, "v2", "")
	third := approveRun(t, m, enqueueTest(t, m, false).ID)
	if third.Tools[0].Path != "/logs/v2" {
		t.Fatal("new definition not published")
	}
	if err := m.Publish(first.ID, true); err == nil {
		t.Fatal("stale rollback accepted")
	}
	if err := m.Publish(third.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := serverTools(svcs.Registry.Snapshot(), "service"); len(got) != 1 || got[0].Path != "/logs/v1" {
		t.Fatalf("rollback failed: %+v", got)
	}
	// A return to an older source snapshot must rebuild, not match any historical hash.
	fourth := approveRun(t, m, enqueueTest(t, m, false).ID)
	if fourth.Tools[0].Path != "/logs/v2" {
		t.Fatal("rolled-back input incorrectly skipped")
	}
	putSource(t, reference, "v3", "empty")
	setLogsAcceptance(t, m, "v3", "empty")
	empty := approveRun(t, m, enqueueTest(t, m, false).ID)
	if len(serverTools(svcs.Registry.Snapshot(), "service")) != 0 {
		t.Fatal("removed tools retained")
	}
	if err := m.Publish(empty.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(serverTools(svcs.Registry.Snapshot(), "service")) != 1 {
		t.Fatal("empty catalog rollback failed")
	}
}
func TestMaintenanceConflictInvalidAndCancellation(t *testing.T) {
	_, svcs, reference := testGateway(t)
	m := svcs.Maintenance
	s, _ := m.Source("service")
	s.AutoPublish = false
	if err := m.SaveSource(s); err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	r := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	server := r.Server
	server.Description = "manual edit"
	if err := svcs.Registry.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("published over concurrent edit")
	}
	if len(serverTools(svcs.Registry.Snapshot(), "service")) != 0 {
		t.Fatal("conflicting publish changed registry")
	}
	putSource(t, reference, "v2", "invalid")
	bad := waitRun(t, m, enqueueTest(t, m, false).ID, "failed")
	if !strings.Contains(bad.Error, "schema") {
		t.Fatalf("unexpected validation error: %s", bad.Error)
	}
	putSource(t, reference, "v3", "hang")
	running := enqueueTest(t, m, false)
	waitRun(t, m, running.ID, "running")
	if _, err := m.Run("service", false); err == nil {
		t.Fatal("overlapping task accepted")
	}
	if err := m.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, running.ID, "cancelled")
	putSource(t, reference, "v4", "")
	waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
}
func TestMaintenanceAdapterInvoke(t *testing.T) {
	g, svcs, reference := testGateway(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/logs/billing%2Fapi" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("wrong adapter request: %s", r.URL)
			w.WriteHeader(400)
			return
		}
		respond(w, 200, map[string]any{"logs": []string{"failure"}})
	}))
	defer upstream.Close()
	t.Setenv("SERVICE_TEST_TOKEN", "test-token")
	if err := svcs.Registry.SaveServer(registry.Server{ID: "service", Name: "Service", Kind: "http", Endpoint: upstream.URL, Enabled: true, Headers: map[string]string{"Authorization": "Bearer ${SERVICE_TEST_TOKEN}"}}); err != nil {
		t.Fatal(err)
	}
	putSource(t, reference, "v1", "adapter")
	setLogsAcceptance(t, svcs.Maintenance, "v1", "adapter")
	svcs.Maintenance.Start(context.Background())
	r := approveRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, false).ID)
	got, err := g.Invoke(context.Background(), gateway.InvokeRequest{ToolID: "service.logs", Arguments: map[string]any{"service": "billing/api"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustJSON(t, got.Data)), "failure") {
		t.Fatalf("bad result: %+v", got)
	}
	if _, err := g.Invoke(context.Background(), gateway.InvokeRequest{ToolID: "service.logs", Arguments: map[string]any{}}); err == nil {
		t.Fatal("invalid arguments accepted")
	}
	if r.Tools[0].Revision != r.ID {
		t.Fatal("adapter revision not pinned")
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNativeMCPMaintenance(t *testing.T) {
	g, svcs, _ := testGateway(t)
	upstream := httptest.NewServer(Handler(g, svcs))
	defer upstream.Close()
	if err := svcs.Registry.SaveServer(registry.Server{ID: "native", Name: "Native", Kind: "mcp", Endpoint: upstream.URL + "/demo/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svcs.Maintenance.SaveSource(maintenance.MaintenanceSource{ServerID: "native", IntervalMinutes: 60, TimeoutSeconds: 30, AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	svcs.Maintenance.Start(context.Background())
	r, err := svcs.Maintenance.Run("native", false)
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, svcs.Maintenance, r.ID, "ready")
	if len(result.Tools) == 0 {
		t.Fatal("native tools not synchronized")
	}
	for _, tool := range result.Tools {
		if tool.ReadOnly != (tool.Name != "restart_service") {
			t.Fatalf("read-only annotation was not preserved from upstream: %+v", tool)
		}
	}
}
func TestArtifactIntegrityAndRestart(t *testing.T) {
	g, svcs, reference := testGateway(t)
	m := svcs.Maintenance
	source, _ := m.Source("service")
	source.AutoPublish = false
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	putSource(t, reference, "v1", "adapter")
	setLogsAcceptance(t, m, "v1", "adapter")
	m.Start(context.Background())
	r := waitRun(t, m, enqueueTest(t, m, true).ID, "ready")
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	reloadedGateway, reloadedSvcs, err := gateway.New(svcs.DataDir, svcs.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reloadedGateway.Close()
	reloaded := reloadedSvcs.Maintenance
	m = reloaded

	reloadedRun, _ := reloaded.GetRun(r.ID)
	if reloadedRun.Status != "ready" || len(reloadedRun.Artifacts) == 0 {
		t.Fatal("validated draft did not survive reload")
	}
	path := filepath.Join(filepath.Join(svcs.DataDir, "maintenance", r.ID), "revision", "adapter.mjs")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("console.log('{}');"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("tampered revision published")
	}
	if len(serverTools(reloadedSvcs.Registry.Snapshot(), "service")) != 0 {
		t.Fatal("tampered revision affected registry")
	}
}
func TestMaintenanceUpdatesModelCandidates(t *testing.T) {
	// The fake model below answers only the yes/no stage.
	t.Setenv("GATEWAY_ROUTING", "flat")
	g, svcs, reference := testGateway(t)
	svcs.Maintenance.Start(context.Background())
	approveRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, false).ID)
	calls := 0
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var input struct {
			Candidates []registry.Tool `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload.Input), &input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(input.Candidates) != 1 || input.Candidates[0].ID != "service.logs" {
			t.Errorf("wrong model candidates: %+v", input.Candidates)
		}
		respond(w, 200, decisionFixture("selected", 0.9))
	}))
	defer modelServer.Close()
	settings, _ := svcs.Model.Config()
	settings.BaseURL = modelServer.URL
	settings.Model = "fixture"
	if err := g.SaveSettings(settings, "", false); err != nil {
		t.Fatal(err)
	}
	result, err := g.Discover(context.Background(), gateway.DiscoverRequest{Intent: "logs"})
	if err != nil || len(result.Tools) != 1 {
		t.Fatalf("model discovery: %+v %v", result, err)
	}
	putSource(t, reference, "v2", "empty")
	setLogsAcceptance(t, svcs.Maintenance, "v2", "empty")
	approveRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, false).ID)
	result, err = g.Discover(context.Background(), gateway.DiscoverRequest{Intent: "logs"})
	if err != nil || len(result.Tools) != 0 || result.Status != "no_match" || calls != 1 {
		t.Fatalf("stale tools reached router: %+v %v calls=%d", result, err, calls)
	}
}

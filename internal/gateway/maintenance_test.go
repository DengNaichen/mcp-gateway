package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for adapter/process tests")
	}
	g, err := New(t.TempDir(), "http://localhost:8788")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	fixture, err := os.ReadFile("testdata/pi-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "pi-test")
	if err := os.WriteFile(command, fixture, 0700); err != nil {
		t.Fatal(err)
	}
	g.execution.Mode = "local"
	g.maintenance.command = command
	if err := g.SaveServer(Server{ID: "service", Name: "Service", Kind: "http", Endpoint: "http://127.0.0.1:1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(t.TempDir(), "api.json")
	putSource(t, reference, "v1", "")
	if err := g.maintenance.SaveSource(MaintenanceSource{ServerID: "service", Files: []string{reference}, IntervalMinutes: 60, TimeoutSeconds: 30, AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	setLogsAcceptance(t, g, "v1", "")
	return g, reference
}
func putSource(t *testing.T, path, version, mode string) {
	t.Helper()
	if err := writeJSON(path, map[string]string{"version": version, "mode": mode}); err != nil {
		t.Fatal(err)
	}
}
func waitRun(t *testing.T, m *Maintainer, id string, status ...string) MaintenanceRun {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		r := m.runs[id]
		m.mu.Unlock()
		if slices.Contains(status, r.Status) {
			return r
		}
		if r.Status == "failed" {
			t.Fatalf("run failed: %s", r.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	t.Fatalf("timeout: %+v", r)
	return r
}
func enqueueTest(t *testing.T, m *Maintainer, force bool) MaintenanceRun {
	t.Helper()
	r, err := m.Run("service", force)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMaintenanceLifecycleAndRollback(t *testing.T) {
	g, reference := testGateway(t)
	g.StartMaintenance(context.Background())
	m := g.maintenance
	first := approveRun(t, m, enqueueTest(t, m, false).ID)
	if len(first.Tools) != 1 || !first.Tools[0].Enabled {
		t.Fatalf("bad published tools: %+v", first.Tools)
	}
	discovery, err := g.Discover(context.Background(), DiscoverRequest{Intent: "logs", Mode: "retrieval"})
	if err != nil || len(discovery.Tools) != 1 {
		t.Fatalf("discovery %v: %+v", err, discovery)
	}
	second := waitRun(t, m, enqueueTest(t, m, false).ID, "no_change")
	if second.Fingerprint != first.Fingerprint {
		t.Fatal("unchanged source fingerprint changed")
	}
	putSource(t, reference, "v2", "")
	setLogsAcceptance(t, g, "v2", "")
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
	if got := serverTools(g.Snapshot(), "service"); len(got) != 1 || got[0].Path != "/logs/v1" {
		t.Fatalf("rollback failed: %+v", got)
	}
	// A return to an older source snapshot must rebuild, not match any historical hash.
	fourth := approveRun(t, m, enqueueTest(t, m, false).ID)
	if fourth.Tools[0].Path != "/logs/v2" {
		t.Fatal("rolled-back input incorrectly skipped")
	}
	putSource(t, reference, "v3", "empty")
	setLogsAcceptance(t, g, "v3", "empty")
	empty := approveRun(t, m, enqueueTest(t, m, false).ID)
	if len(serverTools(g.Snapshot(), "service")) != 0 {
		t.Fatal("removed tools retained")
	}
	if err := m.Publish(empty.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(serverTools(g.Snapshot(), "service")) != 1 {
		t.Fatal("empty catalog rollback failed")
	}
}
func TestMaintenanceConflictInvalidAndCancellation(t *testing.T) {
	g, reference := testGateway(t)
	m := g.maintenance
	m.mu.Lock()
	s := m.sources["service"]
	m.mu.Unlock()
	s.AutoPublish = false
	if err := m.SaveSource(s); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	r := waitRun(t, m, enqueueTest(t, m, false).ID, "ready")
	server := r.Server
	server.Description = "manual edit"
	if err := g.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("published over concurrent edit")
	}
	if len(serverTools(g.Snapshot(), "service")) != 0 {
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
	g, reference := testGateway(t)
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
	if err := g.SaveServer(Server{ID: "service", Name: "Service", Kind: "http", Endpoint: upstream.URL, Enabled: true, Headers: map[string]string{"Authorization": "Bearer ${SERVICE_TEST_TOKEN}"}}); err != nil {
		t.Fatal(err)
	}
	putSource(t, reference, "v1", "adapter")
	setLogsAcceptance(t, g, "v1", "adapter")
	g.StartMaintenance(context.Background())
	r := approveRun(t, g.maintenance, enqueueTest(t, g.maintenance, false).ID)
	got, err := g.Invoke(context.Background(), InvokeRequest{ToolID: "service.logs", Arguments: map[string]any{"service": "billing/api"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustJSON(t, got.Data)), "failure") {
		t.Fatalf("bad result: %+v", got)
	}
	if _, err := g.Invoke(context.Background(), InvokeRequest{ToolID: "service.logs", Arguments: map[string]any{}}); err == nil {
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

func TestMaintenanceRecoveryAndSchedule(t *testing.T) {
	g, _ := testGateway(t)
	m := g.maintenance
	r := enqueueTest(t, m, false)
	m.mu.Lock()
	r.Status = "running"
	if err := m.saveRun(r); err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	reloaded, err := newMaintainer(g)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.runs[r.ID].Status != "interrupted" {
		t.Fatal("running task was not interrupted on restart")
	}
	m.mu.Lock()
	m.runs[r.ID] = reloaded.runs[r.ID]
	s := m.sources["service"]
	s.Enabled = true
	s.NextRunAt = time.Now().Add(-time.Minute)
	if err := m.saveSource(s); err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	g.StartMaintenance(context.Background())
	deadline := time.Now().Add(12 * time.Second)
	found := ""
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for id, run := range m.runs {
			if id != r.ID && run.ServerID == "service" {
				found = id
			}
		}
		m.mu.Unlock()
		if found != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found == "" {
		t.Fatal("due schedule did not create task")
	}
	waitRun(t, m, found, "ready")
}
func TestPiErrorAndTimeout(t *testing.T) {
	for _, mode := range []string{"error", "crash", "hang"} {
		t.Run(mode, func(t *testing.T) {
			g, reference := testGateway(t)
			putSource(t, reference, "v1", mode)
			m := g.maintenance
			r := enqueueTest(t, m, true)
			r.Status = "running"
			m.mu.Lock()
			if err := m.saveRun(r); err != nil {
				t.Fatal(err)
			}
			m.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := m.build(ctx, &r)
			if err == nil {
				t.Fatal("worker failure was accepted")
			}
			if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wrong timeout error: %v", err)
			}
		})
	}
}
func TestArtifactTraversalAndSymlink(t *testing.T) {
	dir := t.TempDir()
	if _, err := regularArtifact(dir, "../secret"); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "adapter.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := regularArtifact(dir, "adapter.mjs"); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestNativeMCPMaintenance(t *testing.T) {
	g, _ := testGateway(t)
	upstream := httptest.NewServer(g.Handler(os.DirFS(t.TempDir())))
	defer upstream.Close()
	if err := g.SaveServer(Server{ID: "native", Name: "Native", Kind: "mcp", Endpoint: upstream.URL + "/demo/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.maintenance.SaveSource(MaintenanceSource{ServerID: "native", IntervalMinutes: 60, TimeoutSeconds: 30, AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	r, err := g.maintenance.Run("native", false)
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, g.maintenance, r.ID, "ready")
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
	g, reference := testGateway(t)
	m := g.maintenance
	m.mu.Lock()
	source := m.sources["service"]
	m.mu.Unlock()
	source.AutoPublish = false
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	putSource(t, reference, "v1", "adapter")
	setLogsAcceptance(t, g, "v1", "adapter")
	g.StartMaintenance(context.Background())
	r := waitRun(t, m, enqueueTest(t, m, true).ID, "ready")
	reloaded, err := newMaintainer(g)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if reloaded.runs[r.ID].Status != "ready" || len(reloaded.runs[r.ID].Artifacts) == 0 {
		t.Fatal("validated draft did not survive reload")
	}
	path := filepath.Join(m.directory(r.ID), "revision", "adapter.mjs")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("console.log('{}');"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(r.ID, false); err == nil {
		t.Fatal("tampered revision published")
	}
	if len(serverTools(g.Snapshot(), "service")) != 0 {
		t.Fatal("tampered revision affected registry")
	}
}
func TestMaintenanceUpdatesModelCandidates(t *testing.T) {
	g, reference := testGateway(t)
	g.StartMaintenance(context.Background())
	approveRun(t, g.maintenance, enqueueTest(t, g.maintenance, false).ID)
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			Candidates []Candidate `json:"candidates"`
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
	defer model.Close()
	settings, _ := g.Config()
	settings.BaseURL = model.URL
	settings.Model = "fixture"
	settings.Mode = "model"
	if err := g.SaveSettings(settings, "", false); err != nil {
		t.Fatal(err)
	}
	result, err := g.Discover(context.Background(), DiscoverRequest{Intent: "logs"})
	if err != nil || len(result.Tools) != 1 {
		t.Fatalf("model discovery: %+v %v", result, err)
	}
	putSource(t, reference, "v2", "empty")
	setLogsAcceptance(t, g, "v2", "empty")
	approveRun(t, g.maintenance, enqueueTest(t, g.maintenance, false).ID)
	result, err = g.Discover(context.Background(), DiscoverRequest{Intent: "logs"})
	if err != nil || len(result.Tools) != 0 || result.Status != "no_match" || calls != 1 {
		t.Fatalf("stale tools reached router: %+v %v calls=%d", result, err, calls)
	}
}

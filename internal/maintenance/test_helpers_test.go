package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Unit tests use a registry and the real SQLite repositories, without importing
// Gateway. Cross-domain HTTP/routing/invocation tests stay beside its adapters.
type testServices struct{ catalog *registry.Service }

func (s testServices) Snapshot() Registry { return s.catalog.Snapshot() }
func (s testServices) UpdateCatalog(_ string, _ string, u func(Registry) (Registry, error), c func(Registry) error) error {
	return s.catalog.UpdateWithCommit(u, c)
}
func (s testServices) RoutingFingerprint(string) string { return "test-routing" }
func (s testServices) Discover(_ context.Context, c RoutingCase, r Registry) (RoutingResult, error) {
	out := RoutingResult{Status: "no_match", Tools: []string{}}
	for _, t := range r.Tools {
		if strings.Contains(c.Intent, t.Name) {
			out.Status = "selected"
			out.Tools = append(out.Tools, t.ID)
		}
	}
	return out, nil
}
func (s testServices) NativeTools(context.Context, Server) (iter.Seq2[*mcp.Tool, error], func(), error) {
	return nil, nil, fmt.Errorf("unexpected native discovery")
}
func (s testServices) CallMCP(context.Context, Server, Tool, map[string]any) (any, error) {
	return nil, fmt.Errorf("unexpected native invocation")
}
func (s testServices) CallHTTP(context.Context, Server, Tool, map[string]any) (any, error) {
	return nil, fmt.Errorf("unexpected HTTP invocation")
}
func (s testServices) RedactResponse(v any, _ Server) any                    { return v }
func (s testServices) ErrorDetail(err error) string                          { return err.Error() }
func (s testServices) Record(string, string, string, string, time.Time, any) {}
func testOptions(m *Maintainer) Options {
	return Options{DB: m.db, Dir: m.dir, Catalog: m.catalog, Router: m.router, Upstream: m.upstream, Recorder: m.recorder, Execution: m.execution, ImportOpenAPI: m.importOpenAPI, LookupEnv: m.lookupEnv}
}
func testMaintainer(t *testing.T) *Maintainer {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for adapter/process tests")
	}
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	services := testServices{registry.New(Registry{Servers: []Server{{ID: "service", Name: "Service", Kind: "http", Endpoint: "http://127.0.0.1:1", Enabled: true}}, Tools: []Tool{}}, nil)}
	fixture, err := os.ReadFile("testdata/pi-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "pi-test")
	if err := os.WriteFile(command, fixture, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_PI_COMMAND", command)
	m, err := New(Options{DB: db.DB, Dir: dir, Catalog: services, Router: services, Upstream: services, Recorder: services, Execution: sandbox.Policy{Mode: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	reference := filepath.Join(t.TempDir(), "api.json")
	putSource(t, reference, "v1", "")
	if err := m.SaveSource(MaintenanceSource{ServerID: "service", Files: []string{reference}, IntervalMinutes: 60, TimeoutSeconds: 30, AutoPublish: true}); err != nil {
		t.Fatal(err)
	}
	return m
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func putSource(t *testing.T, p, version, mode string) {
	t.Helper()
	if err := writeJSON(p, map[string]string{"version": version, "mode": mode}); err != nil {
		t.Fatal(err)
	}
}
func enqueueTest(t *testing.T, m *Maintainer, force bool) MaintenanceRun {
	t.Helper()
	r, err := m.Run("service", force)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func waitRun(t *testing.T, m *Maintainer, id string, status ...string) MaintenanceRun {
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
func objectSchema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

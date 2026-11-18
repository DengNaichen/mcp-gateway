package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"gatewaylab/internal/access"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/registry"
)

const observerTestToken = "observer-test-token-0123456789012345678"

// actorFixture is a gateway with one service and three tools: one any group
// may use, one read tool and one write tool restricted to the developer group.
// developerCalls counts requests that reached the developer-only endpoints of
// the upstream service.
type actorFixture struct {
	g              *gateway.Gateway
	svcs           gateway.Services
	handler        http.Handler
	developerCalls atomic.Int32
}

func newActorFixture(t *testing.T, shared bool) *actorFixture {
	t.Helper()
	if shared {
		t.Setenv("AUTH_ADMIN", adminTestToken)
		t.Setenv("AUTH_OBSERVER", observerTestToken)
		path := filepath.Join(t.TempDir(), "auth.json")
		// The observer may write and may reach every server and tool, so the
		// developer-only tools are kept away from it by its group alone.
		config := map[string]any{"principals": []access.PrincipalConfig{
			{Principal: access.Principal{ID: "owner", Groups: []string{"developer"}, Servers: []string{"*"}, AllowWrite: true, Admin: true}, TokenEnv: "AUTH_ADMIN"},
			{Principal: access.Principal{ID: "watcher", Groups: []string{"observer"}, Servers: []string{"*"}, Tools: []string{"*"}, AllowWrite: true}, TokenEnv: "AUTH_OBSERVER"},
		}}
		if err := writeJSON(path, config); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GATEWAY_AUTH_FILE", path)
	} else {
		t.Setenv("GATEWAY_AUTH_FILE", "")
	}
	g, svcs, err := gateway.New(t.TempDir(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	if (svcs.Access != nil) != shared {
		t.Fatalf("shared mode = %v, want %v", svcs.Access != nil, shared)
	}
	f := &actorFixture{g: g, svcs: svcs, handler: Handler(g, svcs)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/observe" {
			f.developerCalls.Add(1)
		}
		respond(w, 200, map[string]string{"status": "ok"})
	}))
	t.Cleanup(upstream.Close)
	if err := svcs.Registry.SaveServer(registry.Server{ID: "svc", Name: "Service", Kind: "http", Endpoint: upstream.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []registry.Tool{
		{ID: "svc.observe", Name: "observe", Description: "read status for anyone", Actors: []string{"observer", "developer"}, ReadOnly: true, Method: "GET", Path: "/observe"},
		{ID: "svc.develop", Name: "develop", Description: "read status for developers", Actors: []string{"developer"}, ReadOnly: true, Method: "GET", Path: "/develop"},
		{ID: "svc.deploy", Name: "deploy", Description: "change status for developers", Actors: []string{"developer"}, Method: "POST", Path: "/deploy"},
	} {
		tool.ServerID, tool.Enabled, tool.InputSchema = "svc", true, objectSchema(map[string]any{})
		if err := svcs.Registry.SaveTool(tool); err != nil {
			t.Fatal(err)
		}
	}
	useKeywordModel(t, g, svcs)
	return f
}

func (f *actorFixture) call(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	out := httptest.NewRecorder()
	f.handler.ServeHTTP(out, req)
	return out
}

func toolIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		Tools []struct {
			ID string `json:"id"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	ids := []string{}
	for _, tool := range out.Tools {
		ids = append(ids, tool.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestActorCannotEscalateAuthenticatedPrincipal: in shared mode an observer
// token stays an observer whatever actor it claims, in the query or the body.
func TestActorCannotEscalateAuthenticatedPrincipal(t *testing.T) {
	f := newActorFixture(t, true)
	observerOnly := []string{"svc.observe"}

	for _, path := range []string{"/api/catalog", "/api/catalog?actor=developer", "/api/catalog?actor=owner"} {
		got := f.call(t, "GET", path, observerTestToken, "")
		if ids := toolIDs(t, got.Body.Bytes()); got.Code != 200 || !slices.Equal(ids, observerOnly) {
			t.Errorf("GET %s: %d %v, want %v", path, got.Code, ids, observerOnly)
		}
	}
	for _, path := range []string{"/api/tools/svc.develop/schema?actor=developer", "/api/tools/svc.deploy/schema?actor=developer"} {
		if got := f.call(t, "GET", path, observerTestToken, ""); got.Code < 400 {
			t.Errorf("GET %s: %d %s, want a denial", path, got.Code, got.Body)
		}
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/discover", `{"intent":"status","actor":"developer"}`},
		{"/api/discover?actor=developer", `{"intent":"status"}`},
	} {
		got := f.call(t, "POST", tc.path, observerTestToken, tc.body)
		var result struct {
			Eligible int `json:"eligible"`
		}
		_ = json.Unmarshal(got.Body.Bytes(), &result)
		// eligible counts the tools the caller may use before the model chooses.
		if ids := toolIDs(t, got.Body.Bytes()); got.Code != 200 || result.Eligible != 1 || !slices.Equal(ids, observerOnly) {
			t.Errorf("POST %s %s: %d eligible=%d %v, want 1 eligible and %v", tc.path, tc.body, got.Code, result.Eligible, ids, observerOnly)
		}
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/invoke", `{"tool_id":"svc.develop","actor":"developer","arguments":{}}`},
		{"/api/invoke", `{"tool_id":"svc.deploy","actor":"developer","arguments":{}}`},
		{"/api/invoke?actor=developer", `{"tool_id":"svc.develop","arguments":{}}`},
	} {
		if got := f.call(t, "POST", tc.path, observerTestToken, tc.body); got.Code < 400 {
			t.Errorf("POST %s %s: %d %s, want a denial", tc.path, tc.body, got.Code, got.Body)
		}
	}
	if n := f.developerCalls.Load(); n != 0 {
		t.Errorf("observer reached developer-only upstream endpoints %d times", n)
	}
	// Management routes stay admin-only whatever actor is claimed.
	if got := f.call(t, "PUT", "/api/scopes/rules", observerTestToken, `{"repo_key":"*","grants":[]}`); got.Code != 403 {
		t.Errorf("scope rule update as observer: %d %s", got.Code, got.Body)
	}

	// The observer can still do what its group allows, and the audit log
	// records the authenticated principal, not the claimed actor.
	if got := f.call(t, "POST", "/api/invoke", observerTestToken, `{"tool_id":"svc.observe","actor":"developer","arguments":{}}`); got.Code != 200 {
		t.Fatalf("observer invoke of its own tool: %d %s", got.Code, got.Body)
	}
	for _, event := range f.svcs.Activity.Recent() {
		if event.Actor != "watcher" {
			t.Errorf("activity %s %q recorded actor %q, want the principal watcher", event.Kind, event.Title, event.Actor)
		}
	}
	// The admin's developer group does reach the developer tools, so the
	// denials above come from the observer's identity, not from the fixture.
	got := f.call(t, "GET", "/api/catalog", adminTestToken, "")
	if ids := toolIDs(t, got.Body.Bytes()); !slices.Equal(ids, []string{"svc.deploy", "svc.develop", "svc.observe"}) {
		t.Errorf("admin catalog: %d %v", got.Code, ids)
	}
}

// TestActorSimulatesIdentityInLocalMode: without authentication the actor
// parameter is the only identity, so it does select what is visible. This is
// the simulation the shared-mode test above proves cannot leak.
func TestActorSimulatesIdentityInLocalMode(t *testing.T) {
	f := newActorFixture(t, false)
	for actor, want := range map[string][]string{
		"observer":  {"svc.observe"},
		"developer": {"svc.deploy", "svc.develop", "svc.observe"},
	} {
		got := f.call(t, "GET", "/api/catalog?actor="+actor, "", "")
		if ids := toolIDs(t, got.Body.Bytes()); got.Code != 200 || !slices.Equal(ids, want) {
			t.Errorf("local catalog as %s: %d %v, want %v", actor, got.Code, ids, want)
		}
	}
}

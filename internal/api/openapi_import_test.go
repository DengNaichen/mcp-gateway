package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
)

func TestImportOpenAPIOverHTTP(t *testing.T) {
	g, err := gateway.New(t.TempDir(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	handler := Handler(g)
	specHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(openAPIFixture))
	}))
	defer specHost.Close()

	importSpec := func(req apitypes.ImportOpenAPIRequest) *httptest.ResponseRecorder {
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest("POST", "http://localhost/api/servers/openapi", strings.NewReader(string(body))))
		return out
	}
	server := func(id string) apitypes.Server {
		return apitypes.Server{ID: id, Name: id, Endpoint: "https://ci.example.com", Enabled: true}
	}
	registered := func(serverID string) (kind string, tools []string) {
		snapshot := g.Snapshot()
		for _, s := range snapshot.Servers {
			if s.ID == serverID {
				kind = s.Kind
			}
		}
		for _, tool := range snapshot.Tools {
			if tool.ServerID == serverID {
				tools = append(tools, tool.ID)
			}
		}
		return kind, tools
	}

	t.Run("inline spec", func(t *testing.T) {
		got := importSpec(apitypes.ImportOpenAPIRequest{Server: server("inline"), Spec: openAPIFixture})
		var result apitypes.ImportOpenAPIResult
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &result) != nil || result.Imported != 2 {
			t.Fatalf("import: %d %s", got.Code, got.Body)
		}
		if kind, tools := registered("inline"); kind != "http" || len(tools) != 2 {
			t.Fatalf("registry after import: kind=%q tools=%v", kind, tools)
		}
	})
	t.Run("spec url with operation filter", func(t *testing.T) {
		got := importSpec(apitypes.ImportOpenAPIRequest{Server: server("remote"), SpecURL: specHost.URL + "/openapi.yaml", OperationIDs: []string{"logs"}})
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"imported":1`) {
			t.Fatalf("import: %d %s", got.Code, got.Body)
		}
		if _, tools := registered("remote"); len(tools) != 1 || tools[0] != "remote.logs" {
			t.Fatalf("registered tools: %v", tools)
		}
	})
	for _, tc := range []struct {
		name   string
		req    apitypes.ImportOpenAPIRequest
		status int
	}{
		{"no document", apitypes.ImportOpenAPIRequest{Server: server("empty")}, 400},
		{"invalid document", apitypes.ImportOpenAPIRequest{Server: server("broken"), Spec: "openapi: 3.0.3\npaths: nope"}, 400},
		{"invalid url", apitypes.ImportOpenAPIRequest{Server: server("badurl"), SpecURL: "file:///etc/passwd"}, 400},
		{"missing remote document", apitypes.ImportOpenAPIRequest{Server: server("gone"), SpecURL: specHost.URL + "/missing.yaml"}, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := importSpec(tc.req)
			var body apitypes.Error
			if got.Code != tc.status || json.Unmarshal(got.Body.Bytes(), &body) != nil || body.Error == "" {
				t.Fatalf("got %d %s; want %d with an error message", got.Code, got.Body, tc.status)
			}
			if kind, tools := registered(tc.req.Server.ID); kind != "" || len(tools) != 0 {
				t.Fatalf("failed import left registry state: kind=%q tools=%v", kind, tools)
			}
		})
	}
}

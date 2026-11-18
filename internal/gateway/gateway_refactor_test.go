package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaylab/internal/registry"
	"gatewaylab/internal/upstream"
)

func TestInvokePreservesUpstreamValidationError(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()

	if err := g.Registry().SaveServer(registry.Server{
		ID: "svc", Name: "Svc", Kind: "http", Endpoint: "http://127.0.0.1:1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Registry().SaveTool(registry.Tool{
		ID:          "svc.item",
		ServerID:    "svc",
		Name:        "item",
		Description: "get item",
		Enabled:     true,
		Actors:      []string{"developer"},
		Method:      "GET",
		Path:        "/items/{id}",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}},
		},
		HTTP: &registry.HTTPMapping{
			Parameters: []registry.HTTPParameter{{Name: "id", In: "path", Style: "simple"}},
			Responses:  map[string]registry.HTTPResponse{"200": {MediaType: "application/json"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = g.Invoke(context.Background(), InvokeRequest{
		ToolID:    "svc.item",
		Arguments: map[string]any{"path": map[string]any{"id": ".."}},
	})
	if !errors.Is(err, upstream.ErrInvalid) {
		t.Fatalf("expected upstream.ErrInvalid, got %v", err)
	}
}

func TestImportOpenAPIAtomicOnServerOrToolValidationFailure(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()

	const spec = `openapi: 3.0.3
info: {title: Atomic, version: v1}
paths:
  /items:
    get:
      operationId: list_items
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: {type: object}
`
	_, err = g.ImportOpenAPI(context.Background(), registry.Server{
		ID:       "atomic",
		Name:     "Atomic",
		Endpoint: "https://example.com",
		Headers:  map[string]string{"Authorization": "literal-not-env"},
		Enabled:  true,
	}, []byte(spec), "", nil)
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("expected registry.ErrInvalid, got %v", err)
	}
	snap := g.Snapshot()
	for _, s := range snap.Servers {
		if s.ID == "atomic" {
			t.Fatalf("failed ImportOpenAPI persisted server: %+v", s)
		}
	}
	for _, tool := range snap.Tools {
		if tool.ServerID == "atomic" {
			t.Fatalf("failed ImportOpenAPI persisted tool: %+v", tool)
		}
	}
}

func TestMaintenanceAdapterHonorsRuntimeSecrets(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()

	const secretKey = "GATEWAY_TEST_RUNTIME_SECRET_XYZ"

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	server := registry.Server{
		ID:       "rt",
		Name:     "RT",
		Kind:     "http",
		Endpoint: srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer ${" + secretKey + "}"},
	}
	if err := g.saveServerCredentials(server, map[string]string{secretKey: "runtime-token-value"}); err != nil {
		t.Fatal(err)
	}

	tool := registry.Tool{
		ID:     "rt.ping",
		Method: "GET",
		Path:   "/ping",
	}
	if _, err := (maintenanceUpstream{Service: g.upstream, g: g}).CallHTTP(context.Background(), server, tool, nil); err != nil {
		t.Fatalf("maintenance CallHTTP failed: %v", err)
	}
	if gotAuth != "Bearer runtime-token-value" {
		t.Fatalf("maintenance CallHTTP did not expand runtime secret: got %q", gotAuth)
	}

	_, err = g.callAdapter(context.Background(), server, registry.Tool{ID: "rt.ad", ServerID: "rt", Adapter: "bad"}, nil)
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("callAdapter should have passed runtime secret CheckEnvironment and failed on adapter path, got: %v", err)
	}
}

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"
)

const desktopOpenAPIFixture = `openapi: 3.0.3
info: {title: Status, version: v1}
paths:
  /status:
    get:
      operationId: get_status
      summary: Read service status
      responses:
        '200': {description: OK, content: {application/json: {schema: {type: object}}}}
  /restart:
    post:
      operationId: restart
      summary: Restart the service
      responses:
        '204': {description: Restarted}
`

// The desktop no longer parses OpenAPI itself: an attached UI asks whichever
// backend it is connected to, so import works in attached mode too.
func TestOpenAPIImportThroughAttachedGateway(t *testing.T) {
	ctx := context.Background()
	owned, conflict, err := startOrAttachGateway(ctx, "127.0.0.1:0", t.TempDir())
	if err != nil || conflict != nil {
		t.Fatalf("start owned gateway: conflict=%v err=%v", conflict, err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owned.Shutdown(stopCtx)
	})
	attached, conflict, err := startOrAttachGateway(ctx, owned.ListenAddr, owned.DataDir)
	if err != nil || conflict != nil || attached.Mode != modeAttached {
		t.Fatalf("attach: mode=%v conflict=%v err=%v", attached, conflict, err)
	}
	specHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(desktopOpenAPIFixture))
	}))
	defer specHost.Close()

	client := NewAPIClient(attached.UIURL)
	server := func(id string) apitypes.Server {
		return apitypes.Server{ID: id, Name: id, Kind: "http", Transport: "http", Endpoint: "https://status.example.com", Enabled: true}
	}
	if n, err := client.ImportOpenAPI(ctx, server("pasted"), []byte(desktopOpenAPIFixture), "", []string{"get_status"}); err != nil || n != 1 {
		t.Fatalf("inline import: n=%d err=%v", n, err)
	}
	if n, err := client.ImportOpenAPI(ctx, server("fetched"), nil, specHost.URL+"/openapi.yaml", nil); err != nil || n != 2 {
		t.Fatalf("url import: n=%d err=%v", n, err)
	}
	if _, err := client.ImportOpenAPI(ctx, server("empty"), nil, "", nil); err == nil {
		t.Fatal("import without a document succeeded")
	}

	boot, err := NewAPIClient(owned.UIURL).Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, tool := range boot.Registry.Tools {
		if tool.ServerID == "pasted" || tool.ServerID == "fetched" {
			tools = append(tools, tool.ID)
		}
	}
	slices.Sort(tools)
	if want := []string{"fetched.get_status", "fetched.restart", "pasted.get_status"}; !slices.Equal(tools, want) {
		t.Fatalf("owned backend tools = %v, want %v", tools, want)
	}
}

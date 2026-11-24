package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"gatewaylab/internal/apiclient"
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

func TestOpenAPIImportThroughOwnedDesktopClient(t *testing.T) {
	t.Setenv("GATEWAY_AUTH_FILE", "")
	t.Setenv("GATEWAY_CREDENTIAL_STORE", "memory")
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
	if len(owned.AdminToken) != 64 || attached.AdminToken != "" {
		t.Fatal("only the owner should hold a 32-byte hex admin token")
	}
	specHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(desktopOpenAPIFixture))
	}))
	defer specHost.Close()

	attachedClient := NewDesktopUI(attached).Client
	if auth, err := attachedClient.AuthMe(ctx); err != nil || auth.Authenticated {
		t.Fatalf("attached identity: authenticated=%v err=%v", auth.Authenticated, err)
	}
	_, err = attachedClient.Bootstrap(ctx)
	var denied *apiclient.APIError
	if !errors.As(err, &denied) || denied.Status != http.StatusForbidden {
		t.Fatalf("attached client without credentials got management access: %v", err)
	}

	client := NewDesktopUI(owned).Client
	if auth, err := client.AuthMe(ctx); err != nil || !auth.Authenticated {
		t.Fatalf("owned desktop identity: authenticated=%v err=%v", auth.Authenticated, err)
	}
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

	boot, err := client.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, tool := range boot.Registry.Tools {
		if tool.ServerID == "pasted" || tool.ServerID == "fetched" {
			tools = append(tools, tool.ID)
		}
		if tool.ID == "pasted.get_status" {
			tool.Enabled = true
			if _, err := client.SaveTool(ctx, tool); err != nil {
				t.Fatal(err)
			}
		}
	}
	slices.Sort(tools)
	if want := []string{"fetched.get_status", "fetched.restart", "pasted.get_status"}; !slices.Equal(tools, want) {
		t.Fatalf("owned backend tools = %v, want %v", tools, want)
	}
	if err := client.SetScopeMode(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ToolSchema(ctx, "pasted.get_status", ""); !errors.As(err, &denied) || denied.Code != "scope_required" {
		t.Fatalf("schema request without an explicit scope: %v", err)
	}
	fullView := activePlaygroundSimScope(&playgroundState{})
	tool, err := client.ToolSchemaScoped(ctx, "pasted.get_status", "", fullView)
	if err != nil || tool.ID != "pasted.get_status" {
		t.Fatalf("Playground full-view schema request: id=%q err=%v", tool.ID, err)
	}
}

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
)

func TestModelVerificationPersistenceAndReset(t *testing.T) {
	t.Setenv("GATEWAY_CREDENTIAL_STORE", "")
	t.Setenv("GATEWAY_MODEL_API_KEY", "")

	var pingCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pingCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer key-a" && r.Header.Get("Authorization") != "Bearer key-b" {
			respond(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "invalid key"}})
			return
		}
		respond(w, http.StatusOK, map[string]any{
			"answers": map[string]any{
				"route_status": map[string]any{"type": "choice", "choice": "no_match"},
				"tool_0":       map[string]any{"type": "noul", "noul": 0.1},
			},
		})
	}))
	defer upstream.Close()

	dir := t.TempDir()
	creds := &credentialBackend{}
	openGateway := func() *Gateway {
		g, _, err := NewWithOptions(dir, "http://127.0.0.1:0", Options{CredentialBackend: creds})
		if err != nil {
			t.Fatalf("open gateway: %v", err)
		}
		return g
	}

	g := openGateway()
	if err := g.SaveServer(registry.Server{ID: "svc1", Name: "Service 1", Kind: "http", Endpoint: "https://api.example.com", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.registry.SaveTool(registry.Tool{ID: "svc1.read", ServerID: "svc1", Name: "read", Description: "Read item", Method: "GET", Path: "/items", ReadOnly: true, Enabled: true, Actors: []string{"developer"}, InputSchema: objectSchema(map[string]any{})}); err != nil {
		t.Fatal(err)
	}

	// 1. Ping and save Profile A -> ModelReady becomes true immediately.
	profA := model.ModelProfile{
		ID:             "prof-a",
		BaseURL:        upstream.URL + "/v1",
		Model:          "jev-a",
		MaxTools:       4,
		TimeoutSeconds: 30,
	}
	if _, err := g.PingModel(context.Background(), model.Settings{
		BaseURL:        profA.BaseURL,
		Model:          profA.Model,
		MaxTools:       profA.MaxTools,
		TimeoutSeconds: profA.TimeoutSeconds,
	}, "", "key-a", false); err != nil {
		t.Fatalf("PingModel A: %v", err)
	}
	if err := g.VerifyModelConnection(context.Background(), profA, "key-a", false); err != nil {
		t.Fatalf("VerifyModelConnection A after PingModel: %v", err)
	}
	if pingCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream ping after VerifyModelConnection reuse, got %d", pingCalls.Load())
	}
	if _, err := g.SaveModelProfile(profA, "key-a", false); err != nil {
		t.Fatalf("SaveModelProfile A: %v", err)
	}

	setup, err := g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Stale {
		t.Fatalf("expected ModelReady=true after ping+save, got %+v", setup)
	}

	// 2. Adding or toggling real tools does NOT invalidate model verification.
	if err := g.registry.SaveTool(registry.Tool{ID: "svc1.write", ServerID: "svc1", Name: "write", Description: "Write item", Method: "POST", Path: "/items", ReadOnly: false, Enabled: true, Actors: []string{"developer"}, InputSchema: objectSchema(map[string]any{})}); err != nil {
		t.Fatal(err)
	}
	if err := g.EnableTools("svc1", []string{"svc1.read"}); err != nil {
		t.Fatal(err)
	}
	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Stale {
		t.Fatalf("tool changes must not reset model verification: %+v", setup)
	}

	// 3. Changing MaxTools alone preserves ModelReady and skips re-pinging on save.
	profA.MaxTools = 8
	if err := g.VerifyModelConnection(context.Background(), profA, "", false); err != nil {
		t.Fatalf("VerifyModelConnection A with new MaxTools: %v", err)
	}
	if pingCalls.Load() != 1 {
		t.Fatalf("MaxTools change should not trigger upstream ping, got %d", pingCalls.Load())
	}
	if _, err := g.SaveModelProfile(profA, "", false); err != nil {
		t.Fatalf("SaveModelProfile A MaxTools: %v", err)
	}
	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Stale {
		t.Fatalf("MaxTools change must keep ModelReady=true: %+v", setup)
	}

	// 4. Verify & save Profile B, then switch back to Profile A -> both stay verified.
	profB := model.ModelProfile{
		ID:             "prof-b",
		BaseURL:        upstream.URL + "/v1",
		Model:          "jev-b",
		MaxTools:       4,
		TimeoutSeconds: 30,
	}
	if err := g.VerifyModelConnection(context.Background(), profB, "key-b", false); err != nil {
		t.Fatalf("VerifyModelConnection B: %v", err)
	}
	if pingCalls.Load() != 2 {
		t.Fatalf("expected 2 upstream pings for Profile B, got %d", pingCalls.Load())
	}
	if _, err := g.SaveModelProfile(profB, "key-b", false); err != nil {
		t.Fatalf("SaveModelProfile B: %v", err)
	}
	if _, err := g.ActivateModelProfile(profA.ID); err != nil {
		t.Fatalf("ActivateModelProfile A: %v", err)
	}
	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Stale {
		t.Fatalf("switching back to verified Profile A must stay ModelReady=true: %+v", setup)
	}

	// 5. Restart gateway (clears in-memory verifiedPings) -> persisted check still keeps ModelReady=true and avoids re-ping.
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g = openGateway()
	defer func() { _ = g.Close() }()

	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Stale {
		t.Fatalf("restarting gateway must preserve ModelReady=true: %+v", setup)
	}
	if err := g.VerifyModelConnection(context.Background(), profA, "", false); err != nil {
		t.Fatalf("VerifyModelConnection after restart: %v", err)
	}
	if pingCalls.Load() != 2 {
		t.Fatalf("expected persisted verification to skip upstream ping after restart, got %d", pingCalls.Load())
	}

	// 6. Changing connection identity (Model / TimeoutSeconds / APIKey) resets verification and triggers a fresh ping.
	profAEdited := profA
	profAEdited.Model = "jev-a-v2"
	if _, err := g.SaveModelProfile(profAEdited, "", false); err != nil {
		t.Fatal(err)
	}
	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if setup.ModelReady || setup.ModelCheck != nil {
		t.Fatalf("changing Model ID must reset ModelCheck and ModelReady, got %+v", setup)
	}
	if err := g.VerifyModelConnection(context.Background(), profAEdited, "", false); err != nil {
		t.Fatalf("VerifyModelConnection edited model: %v", err)
	}
	if pingCalls.Load() != 3 {
		t.Fatalf("expected fresh ping after Model ID change, got %d", pingCalls.Load())
	}
}

func TestLegacySingletonModelCheckCompatibility(t *testing.T) {
	t.Setenv("GATEWAY_CREDENTIAL_STORE", "")
	t.Setenv("GATEWAY_MODEL_API_KEY", "")

	g, _, err := NewWithOptions(t.TempDir(), "http://127.0.0.1:0", Options{CredentialBackend: &credentialBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()

	if err := g.SaveServer(registry.Server{ID: "svc1", Name: "Service 1", Kind: "http", Endpoint: "https://api.example.com", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.registry.SaveTool(registry.Tool{ID: "svc1.read", ServerID: "svc1", Name: "read", Description: "Read item", Method: "GET", Path: "/items", ReadOnly: true, Enabled: true, Actors: []string{"developer"}, InputSchema: objectSchema(map[string]any{})}); err != nil {
		t.Fatal(err)
	}

	fp, err := g.modelFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.saveCheck("model", fp, ConnectionCheck{Status: "success", Message: "legacy ok", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	setup, err := g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !setup.ModelReady || setup.ModelCheck == nil || setup.ModelCheck.Message != "legacy ok" {
		t.Fatalf("expected matching legacy check to be honored, got %+v", setup)
	}

	// Stale legacy check from another model config must not leak onto an unverified model.
	if err := store.ConnectionChecks[ConnectionCheck](g.db).Put("model", "different-fingerprint", ConnectionCheck{Status: "error", Message: "old error", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	setup, err = g.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if setup.ModelCheck != nil || setup.ModelReady {
		t.Fatalf("expected stale legacy check to be ignored, got %+v", setup)
	}
}

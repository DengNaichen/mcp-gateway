package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelProfilesLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	g, err := New(dir, "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	profiles := g.ModelProfiles()
	if len(profiles) != 1 || !profiles[0].Active {
		t.Fatalf("expected 1 active seeded profile, got %+v", profiles)
	}
	defaultID := profiles[0].ID

	// Save a second profile with its own in-memory API key.
	gemini, err := g.SaveModelProfile(ModelProfile{
		BaseURL:        "https://generativelanguage.googleapis.com/v1beta",
		Model:          "gemini-decision-1",
		MaxTools:       6,
		TimeoutSeconds: 60,
	}, "gem-secret-1", false)
	if err != nil {
		t.Fatalf("save gemini profile: %v", err)
	}
	if !gemini.Active || !gemini.KeyConfigured {
		t.Fatalf("expected saved gemini profile to be active with key configured, got %+v", gemini)
	}

	// Active settings should match the newly saved profile.
	cfg, hasKey := g.Config()
	if cfg.Model != "gemini-decision-1" || cfg.MaxTools != 6 || !hasKey {
		t.Fatalf("unexpected active config: %+v hasKey=%v", cfg, hasKey)
	}

	// Switching back to the default profile (which had no key) should clear active key.
	if _, err := g.ActivateModelProfile(defaultID); err != nil {
		t.Fatalf("activate default profile: %v", err)
	}
	cfg, hasKey = g.Config()
	if cfg.Model == "gemini-decision-1" || hasKey {
		t.Fatalf("expected default profile active without key, got %+v hasKey=%v", cfg, hasKey)
	}

	// Switching back to gemini should restore its in-memory key.
	if _, err := g.ActivateModelProfile(gemini.ID); err != nil {
		t.Fatalf("reactivate gemini profile: %v", err)
	}
	cfg, hasKey = g.Config()
	if cfg.Model != "gemini-decision-1" || !hasKey {
		t.Fatalf("expected gemini profile active with key, got %+v hasKey=%v", cfg, hasKey)
	}

	// Updating without re-entering key preserves it; clearKey removes it.
	updated, err := g.SaveModelProfile(ModelProfile{
		ID:             gemini.ID,
		BaseURL:        gemini.BaseURL,
		Model:          gemini.Model,
		MaxTools:       8,
		TimeoutSeconds: 45,
	}, "", false)
	if err != nil || !updated.KeyConfigured || updated.MaxTools != 8 {
		t.Fatalf("expected preserved key and updated max_tools=8, got %+v err=%v", updated, err)
	}

	cleared, err := g.SaveModelProfile(ModelProfile{
		ID:             gemini.ID,
		BaseURL:        gemini.BaseURL,
		Model:          gemini.Model,
		MaxTools:       8,
		TimeoutSeconds: 45,
	}, "", true)
	if err != nil || cleared.KeyConfigured {
		t.Fatalf("expected key cleared, got %+v err=%v", cleared, err)
	}

	// Delete default profile, then ensure deleting the last profile is rejected.
	if err := g.DeleteModelProfile(defaultID); err != nil {
		t.Fatalf("delete default profile: %v", err)
	}
	if err := g.DeleteModelProfile(gemini.ID); err == nil {
		t.Fatalf("expected error deleting the last remaining profile")
	}
	if err := g.Close(); err != nil {
		t.Fatalf("close gateway: %v", err)
	}

	// Reopen from SQLite: profile metadata persists, in-memory key does not.
	g2, err := New(dir, "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("reopen gateway: %v", err)
	}
	defer g2.Close()
	reloaded := g2.ModelProfiles()
	if len(reloaded) != 1 || reloaded[0].Model != "gemini-decision-1" || reloaded[0].MaxTools != 8 || reloaded[0].KeyConfigured {
		t.Fatalf("unexpected reloaded profiles: %+v", reloaded)
	}
}

func TestPingModelDirectConnectivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key-ok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": []map[string]any{
				{"type": "choice", "name": "route_status", "choice": "no_match", "probability": 0.9},
				{"type": "predicate", "name": "tool_0", "choice": "no", "probability": 0.1},
			},
		})
	}))
	defer srv.Close()

	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	res, err := g.PingModel(context.Background(), Settings{
		BaseURL:        srv.URL,
		Model:          "gpt-test-1",
		MaxTools:       4,
		TimeoutSeconds: 15,
	}, "", "test-key-ok", false)
	if err != nil || !res.OK || res.Model != "gpt-test-1" {
		t.Fatalf("expected ping ok, got %+v err=%v", res, err)
	}

	if _, err := g.PingModel(context.Background(), Settings{
		BaseURL:        srv.URL,
		Model:          "gpt-test-1",
		MaxTools:       4,
		TimeoutSeconds: 15,
	}, "", "wrong-key", false); err == nil {
		t.Fatalf("expected ping error with wrong key")
	}
}

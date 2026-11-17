package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"
)

func TestModelProfilesLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	g, err := openTestService(dir)
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
	g2, err := openTestService(dir)
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

	g, err := openTestService(t.TempDir())
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

func TestVerifyModelConnectionReusesRecentPing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer ok-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"route_status": map[string]any{"type": "choice", "choice": "no_match"},
				"tool_0":       map[string]any{"type": "noul", "noul": 0.1},
			},
		})
	}))
	defer srv.Close()

	g, err := openTestService(t.TempDir())
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	prof := ModelProfile{
		BaseURL:        srv.URL + "/v1",
		Model:          "jev",
		MaxTools:       4,
		TimeoutSeconds: 15,
	}

	// Failing ping must reject verification without caching.
	if err := g.VerifyModelConnection(context.Background(), prof, "bad-key", false); err == nil {
		t.Fatal("expected VerifyModelConnection to fail with bad key")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 upstream call, got %d", calls.Load())
	}

	// Passing ping caches the connection signature so saving with different MaxTools skips re-pinging.
	if _, err := g.PingModel(context.Background(), Settings{
		BaseURL:        prof.BaseURL,
		Model:          prof.Model,
		MaxTools:       prof.MaxTools,
		TimeoutSeconds: prof.TimeoutSeconds,
	}, "", "ok-key", false); err != nil {
		t.Fatalf("PingModel failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 upstream calls after PingModel, got %d", calls.Load())
	}

	prof.MaxTools = 8
	if err := g.VerifyModelConnection(context.Background(), prof, "ok-key", false); err != nil {
		t.Fatalf("expected cached verification to succeed, got %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected cached ping to avoid extra upstream call, got %d", calls.Load())
	}

	// Changing TimeoutSeconds invalidates the cached ping signature and triggers a fresh ping.
	prof.TimeoutSeconds = 30
	if err := g.VerifyModelConnection(context.Background(), prof, "ok-key", false); err != nil {
		t.Fatalf("expected fresh verification after timeout change, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 upstream calls after timeout change, got %d", calls.Load())
	}

	// Invalid profile fields return ErrInvalid immediately without touching upstream.
	if err := g.VerifyModelConnection(context.Background(), ModelProfile{BaseURL: srv.URL + "/v1", Model: "", MaxTools: 4, TimeoutSeconds: 15}, "ok-key", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("invalid profile should not call upstream, got %d", calls.Load())
	}
}

type testService struct {
	*Service
	storage *store.Store
}

func openTestService(dir string) (*testService, error) {
	storage, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	svc, err := New(storage.DB, &secrets.Store{})
	if err != nil {
		_ = storage.Close()
		return nil, err
	}
	return &testService{svc, storage}, nil
}
func (g *testService) Close() error { return g.storage.Close() }

func TestProfileMutationsAreAtomicAcrossTablesAndMemory(t *testing.T) {
	g, err := openTestService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	second, err := g.SaveModelProfile(ModelProfile{
		BaseURL:        "https://api.openai.com/v1",
		Model:          "gpt-test-2",
		MaxTools:       5,
		TimeoutSeconds: 30,
	}, "key-2", false)
	if err != nil {
		t.Fatal(err)
	}
	beforeCfg, beforeHasKey := g.Config()
	beforeProfiles := g.ModelProfiles()

	// Abort any write to settings after model_profiles has already been modified in the tx.
	if _, err := g.storage.Exec(`CREATE TRIGGER fail_settings_update BEFORE UPDATE ON settings BEGIN SELECT RAISE(ABORT, 'inject failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveModelProfile(ModelProfile{
		ID:             second.ID,
		BaseURL:        "https://api.openai.com/v1",
		Model:          "gpt-test-updated",
		MaxTools:       7,
		TimeoutSeconds: 40,
	}, "key-updated", false); err == nil {
		t.Fatal("expected trigger failure on SaveModelProfile")
	}
	if _, err := g.ActivateModelProfile("default"); err == nil {
		t.Fatal("expected trigger failure on ActivateModelProfile")
	}
	if err := g.DeleteModelProfile(second.ID); err == nil {
		t.Fatal("expected trigger failure on DeleteModelProfile")
	}

	// Also fail SaveSettings after settings update by rejecting model_profiles insert.
	if _, err := g.storage.Exec(`DROP TRIGGER fail_settings_update; CREATE TRIGGER fail_profiles_insert BEFORE INSERT ON model_profiles BEGIN SELECT RAISE(ABORT, 'inject profile failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveSettings(Settings{
		BaseURL:        "https://api.openai.com/v1",
		Model:          "gpt-broken",
		MaxTools:       9,
		TimeoutSeconds: 60,
	}, "key-broken", false); err == nil {
		t.Fatal("expected trigger failure on SaveSettings")
	}

	// Neither SQLite nor in-memory snapshots should reflect any partial writes.
	afterCfg, afterHasKey := g.Config()
	if afterCfg != beforeCfg || afterHasKey != beforeHasKey {
		t.Fatalf("in-memory config mutated on failed tx: %+v vs %+v", afterCfg, beforeCfg)
	}
	if got := g.ModelProfiles(); len(got) != len(beforeProfiles) || got[0].Model != beforeProfiles[0].Model {
		t.Fatalf("in-memory profiles mutated on failed tx: %+v vs %+v", got, beforeProfiles)
	}
	storedSettings, err := store.Settings[Settings](g.storage.DB).Load()
	if err != nil || storedSettings != beforeCfg {
		t.Fatalf("sqlite settings mutated on failed tx: %+v err=%v", storedSettings, err)
	}
	storedProfiles, err := store.ModelProfiles[ModelProfile](g.storage.DB).List()
	if err != nil || len(storedProfiles) != len(beforeProfiles) || storedProfiles[0].Model != beforeProfiles[0].Model {
		t.Fatalf("sqlite model_profiles mutated on failed tx: %+v err=%v", storedProfiles, err)
	}
}

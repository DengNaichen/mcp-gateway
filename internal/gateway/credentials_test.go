package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
)

type credentialBackend struct {
	data []byte
	fail bool
}

func (b *credentialBackend) Load() ([]byte, error) { return bytes.Clone(b.data), nil }
func (b *credentialBackend) Save(data []byte) error {
	if b.fail {
		return errors.New("keychain locked")
	}
	b.data = bytes.Clone(data)
	return nil
}

func TestCredentialsSurviveRestartAndRemainIsolated(t *testing.T) {
	t.Setenv("GATEWAY_MODEL_API_KEY", "")
	t.Setenv("GATEWAY_CREDENTIAL_STORE", "")
	dir := t.TempDir()
	b := &credentialBackend{}
	open := func() *Gateway {
		g, _, err := NewWithOptions(dir, "http://127.0.0.1:8789", Options{CredentialBackend: b})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	g := open()
	defaultID := g.model.ModelProfiles()[0].ID
	first, err := g.SaveModelProfile(model.ModelProfile{ID: defaultID, BaseURL: "https://model.example/v1", Model: "jev-a", MaxTools: 4, TimeoutSeconds: 30}, "model-secret-one", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.SaveModelProfile(model.ModelProfile{BaseURL: "https://model.example/v1", Model: "jev-b", MaxTools: 4, TimeoutSeconds: 30}, "model-secret-two", false)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	for _, id := range []string{"one", "two"} {
		s := registry.Server{ID: id, Name: id, Kind: "http", Enabled: true, Endpoint: upstream.URL, Headers: map[string]string{"Authorization": "Bearer ${SHARED_TOKEN}"}}
		if err := g.SaveServer(s); err != nil {
			t.Fatal(err)
		}
		if err := g.SetServerCredentials(id, map[string]string{"SHARED_TOKEN": "upstream-secret-" + id}); err != nil {
			t.Fatal(err)
		}
		if err := g.registry.SaveTool(registry.Tool{ID: id + ".read", ServerID: id, Name: "read", Description: "read test", Enabled: true, ReadOnly: true, Actors: []string{"developer"}, InputSchema: map[string]any{"type": "object"}, Method: "GET", Path: "/"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g = open()
	defer func() { _ = g.Close() }()
	_, key := g.model.RuntimeConfig()
	if key != "model-secret-two" {
		t.Fatal("active model key not restored")
	}
	if _, err := g.ActivateModelProfile(first.ID); err != nil {
		t.Fatal(err)
	}
	_, key = g.model.RuntimeConfig()
	if key != "model-secret-one" {
		t.Fatal("profile keys mixed")
	}
	for _, id := range []string{"one", "two"} {
		if _, err := g.Invoke(context.Background(), InvokeRequest{ToolID: id + ".read", Actor: "developer"}); err != nil {
			t.Fatal(err)
		}
		if seen != "Bearer upstream-secret-"+id {
			t.Fatal("server credential not applied or isolated")
		}
	}
	if err := g.SetServerCredentials("one", map[string]string{"UNREFERENCED": "bad"}); err == nil {
		t.Fatal("accepted unrelated secret")
	}
	b.fail = true
	if err := g.SetServerCredentials("one", map[string]string{"SHARED_TOKEN": "replacement"}); err == nil {
		t.Fatal("keychain failure hidden")
	}
	b.fail = false
	if g.lookupServerEnv("one", "SHARED_TOKEN") != "upstream-secret-one" {
		t.Fatal("failed update changed key")
	}
	t.Setenv("SHARED_TOKEN", "temporary-override")
	if g.lookupServerEnv("one", "SHARED_TOKEN") != "temporary-override" {
		t.Fatal("environment did not override")
	}
	t.Setenv("SHARED_TOKEN", "")
	if err := g.SetServerCredentials("one", map[string]string{"SHARED_TOKEN": ""}); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteServer("two"); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteModelProfile(second.ID); err != nil {
		t.Fatal(err)
	}
	cfg, _ := g.model.RuntimeConfig()
	if err := g.SaveSettings(cfg, "", true); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g = open()
	_, key = g.model.RuntimeConfig()
	if key != "" {
		t.Fatal("cleared model key restored")
	}
	if g.lookupServerEnv("one", "SHARED_TOKEN") != "" || len(g.credentials.Prefix("server/two/")) != 0 || g.credentials.Get("model/"+second.ID) != "" {
		t.Fatal("deleted secret restored")
	}
	// Inspect actual SQLite files, including the WAL while the database is open.
	paths, err := filepath.Glob(filepath.Join(dir, "gateway.db*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"model-secret-one", "model-secret-two", "upstream-secret-one", "upstream-secret-two"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("secret leaked into %s", path)
			}
		}
	}
}

func TestModelEnvironmentOverrideIsNotPersisted(t *testing.T) {
	t.Setenv("GATEWAY_CREDENTIAL_STORE", "")
	t.Setenv("GATEWAY_MODEL_API_KEY", "")
	dir := t.TempDir()
	backend := &credentialBackend{}
	open := func() *Gateway {
		g, _, err := NewWithOptions(dir, "http://127.0.0.1:0", Options{CredentialBackend: backend})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	g := open()
	cfg, _ := g.model.RuntimeConfig()
	if err := g.SaveSettings(cfg, "saved-model-key", false); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_MODEL_API_KEY", "temporary-model-key")
	g = open()
	cfg, key := g.model.RuntimeConfig()
	if key != "temporary-model-key" {
		t.Fatal("model env did not override")
	}
	if err := g.SaveSettings(cfg, "", false); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_MODEL_API_KEY", "")
	g = open()
	defer func() { _ = g.Close() }()
	_, key = g.model.RuntimeConfig()
	if key != "saved-model-key" {
		t.Fatal("temporary environment replaced saved key")
	}
}

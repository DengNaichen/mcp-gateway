package gateway

import (
	"errors"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
	"testing"
	"time"
)

func TestRegistryOperationsDoNotAcquirePublicationConfigMutex(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:8789")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.publicationConfigMu.Lock()
	done := make(chan error, 1)
	go func() {
		_ = g.Snapshot()
		done <- g.SaveServer(registry.Server{ID: "independent", Name: "Independent", Kind: "http", Endpoint: "https://example.com"})
	}()
	select {
	case err := <-done:
		g.publicationConfigMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		g.publicationConfigMu.Unlock()
		<-done
		t.Fatal("registry acquired the publication/config mutex")
	}
}

func TestInitializationFailureReleasesDataDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GATEWAY_MODEL_BASE_URL", "invalid")
	if g, err := New(dir, "http://127.0.0.1:8789"); err == nil {
		_ = g.Close()
		t.Fatal("expected model settings failure")
	}
	t.Setenv("GATEWAY_MODEL_BASE_URL", "")
	g, err := New(dir, "http://127.0.0.1:8789")
	if err != nil {
		t.Fatalf("initialization leaked lock: %v", err)
	}
	if other, err := New(dir, "http://127.0.0.1:8790"); !errors.Is(err, store.ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("expected locked gateway: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir, "http://127.0.0.1:8790")
	if err != nil {
		t.Fatalf("close leaked lock: %v", err)
	}
	_ = reopened.Close()
}

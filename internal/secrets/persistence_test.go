package secrets

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"testing"
)

type testBackend struct {
	data []byte
	fail bool
}

func (b *testBackend) Load() ([]byte, error) { return bytes.Clone(b.data), nil }
func (b *testBackend) Save(data []byte) error {
	if b.fail {
		return errors.New("keychain unavailable")
	}
	b.data = bytes.Clone(data)
	return nil
}
func TestPersistRestoreAndRollback(t *testing.T) {
	b := &testBackend{}
	s, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(map[string]string{"model/a": "saved-key"}, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(b)
	if err != nil || reopened.Get("model/a") != "saved-key" {
		t.Fatal("credential not restored", err)
	}
	b.fail = true
	if err = s.Update(map[string]string{"model/a": "replacement"}, nil); err == nil {
		t.Fatal("write failure hidden")
	}
	if s.Get("model/a") != "saved-key" {
		t.Fatal("failed save published")
	}
	b.fail = false
	if err = s.Update(map[string]string{"model/a": "replacement"}, func() error { return errors.New("db failed") }); err == nil {
		t.Fatal("commit failure hidden")
	}
	reopened, err = Open(b)
	if err != nil || reopened.Get("model/a") != "saved-key" {
		t.Fatal("rollback lost credential", err)
	}
	if err = s.Update(map[string]string{"model/a": ""}, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(b)
	if err != nil || reopened.Get("model/a") != "" {
		t.Fatal("deleted credential restored", err)
	}
}

// Opt in to exercise the real system Keychain with a disposable namespace.
func TestSystemKeychainRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("GATEWAY_TEST_KEYCHAIN") != "1" {
		t.Skip("set GATEWAY_TEST_KEYCHAIN=1 on macOS")
	}
	b, err := Keychain("test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := b.(*keychain)
	t.Cleanup(func() {
		if _, err := runKeychain([]string{"delete-generic-password", "-a", k.account, "-s", k.service}, ""); err != nil {
			t.Error("remove disposable keychain item", err)
		}
	})
	s, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := "test-only-key-\"-$()-多行\nvalue"
	if err = s.Update(map[string]string{"model/a": sentinel}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Get("model/a") != sentinel {
		t.Fatal("real Keychain round trip failed")
	}
	if err = restored.Update(map[string]string{"model/a": ""}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err = Open(b)
	if err != nil || restored.Get("model/a") != "" {
		t.Fatal("real Keychain clear failed", err)
	}
}

func TestKeychainNamespaceIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS")
	}
	dir := t.TempDir()
	stable, err := Keychain("stable", dir)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := Keychain("dev", dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Keychain("stable", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if stable.(*keychain).account == dev.(*keychain).account || stable.(*keychain).account == other.(*keychain).account {
		t.Fatal("credential namespace collision")
	}
}

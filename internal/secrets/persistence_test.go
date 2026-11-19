package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"runtime"
	"strings"
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
		k.deleteChunkRange(0)
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
	// Exceed security -i's 4096-byte interactive line limit to verify chunked storage.
	largeVal := strings.Repeat("token-segment-0123456789abcdef-", 320)
	if err = restored.Update(map[string]string{"service/large": largeVal}, nil); err != nil {
		t.Fatal("real Keychain large save failed:", err)
	}
	restoredLarge, err := Open(b)
	if err != nil {
		t.Fatal("real Keychain large load failed:", err)
	}
	if restoredLarge.Get("service/large") != largeVal || restoredLarge.Get("model/a") != sentinel {
		t.Fatal("real Keychain large round trip mismatch")
	}
	if err = restoredLarge.Update(map[string]string{"model/a": "", "service/large": ""}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err = Open(b)
	if err != nil || restored.Get("model/a") != "" || restored.Get("service/large") != "" {
		t.Fatal("real Keychain clear failed", err)
	}
}

func TestKeychainChunkingStaysUnderInteractiveLineLimit(t *testing.T) {
	items := map[string]string{}
	const macOSSecurityMaxLine = 4096
	k := &keychain{
		account: strings.Repeat("a", 64),
		service: keychainServiceName,
		run: func(args []string, input string) ([]byte, error) {
			if len(args) == 1 && args[0] == "-i" {
				for _, line := range strings.Split(strings.TrimSuffix(input, "\n"), "\n") {
					if len(line)+1 >= macOSSecurityMaxLine {
						// Mirror macOS /usr/bin/security -i 4096-byte line truncation.
						line = line[:macOSSecurityMaxLine-1]
					}
					fields := strings.Fields(line)
					if len(fields) == 8 && fields[0] == "add-generic-password" && fields[2] == "-a" && fields[6] == "-w" {
						items[fields[3]] = fields[7]
					}
				}
				return nil, nil
			}
			if len(args) >= 4 && args[0] == "find-generic-password" && args[1] == "-a" {
				val, ok := items[args[2]]
				if !ok {
					return nil, errKeychainItemNotFound
				}
				return []byte(val + "\n"), nil
			}
			if len(args) >= 4 && args[0] == "delete-generic-password" && args[1] == "-a" {
				if _, ok := items[args[2]]; !ok {
					return nil, errKeychainItemNotFound
				}
				delete(items, args[2])
				return nil, nil
			}
			t.Fatalf("unexpected security args: %v", args)
			return nil, nil
		},
	}

	s, err := Open(k)
	if err != nil {
		t.Fatal(err)
	}
	// Small payload stays in the single primary item for backward compatibility.
	if err := s.Update(map[string]string{"model/small": "sk-short"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 keychain item for small payload, got %d", len(items))
	}

	// Large payload (> 10 KB) must chunk across multiple items without truncation.
	largeSecret := strings.Repeat("ghp_1234567890abcdef", 600)
	if err := s.Update(map[string]string{"service/github": largeSecret}, nil); err != nil {
		t.Fatalf("large payload save failed: %v", err)
	}
	if len(items) < 4 {
		t.Fatalf("expected chunked keychain items, got %d", len(items))
	}
	reopened, err := Open(k)
	if err != nil {
		t.Fatalf("large payload reopen failed: %v", err)
	}
	if reopened.Get("service/github") != largeSecret || reopened.Get("model/small") != "sk-short" {
		t.Fatal("large payload round trip corrupted credentials")
	}

	// Shrinking back to a small payload must delete stale chunk items.
	if err := reopened.Update(map[string]string{"service/github": ""}, nil); err != nil {
		t.Fatalf("shrink save failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("stale chunk items left behind after shrink: %d", len(items))
	}
	reopened, err = Open(k)
	if err != nil || reopened.Get("model/small") != "sk-short" || reopened.Get("service/github") != "" {
		t.Fatalf("post-shrink reopen failed: err=%v", err)
	}
}

func TestOpenPreservesCorruptedKeychainErrorMessage(t *testing.T) {
	// Simulate a legacy 3970-byte truncated base64 payload (3970 % 4 == 2).
	validB64 := base64.StdEncoding.EncodeToString([]byte(`{"k":"` + strings.Repeat("x", 3000) + `"}`))
	truncated := validB64[:3970]
	k := &keychain{
		account: strings.Repeat("b", 64),
		service: keychainServiceName,
		run: func(args []string, _ string) ([]byte, error) {
			return []byte(truncated + "\n"), nil
		},
	}
	_, err := Open(k)
	if err == nil {
		t.Fatal("expected error for truncated keychain payload")
	}
	if !strings.Contains(err.Error(), "格式已损坏") {
		t.Fatalf("expected specific format-corruption error, got: %v", err)
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

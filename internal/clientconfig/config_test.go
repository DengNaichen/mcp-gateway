package clientconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycle(t *testing.T) {
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			m := New(home, filepath.Join(home, "receipts"), func(string) string { return "" })
			c, _ := m.client(id)
			original := []byte("{\n // keep this comment\n \"unrelated\": {\"value\": 42},\n}\n")
			if c.TOML {
				original = []byte("# keep this comment\nmodel = \"example\"\n[other]\nvalue = 42\n")
			}
			if e := atomicWrite(c.Path, original); e != nil {
				t.Fatal(e)
			}
			endpoint := "http://127.0.0.1:8788/mcp"
			if e := m.Write(id, endpoint); e != nil {
				t.Fatal(e)
			}
			first, _ := read(c.Path)
			if !bytes.Contains(first, []byte("keep this comment")) {
				t.Fatal("lost comment")
			}
			if e := m.Write(id, endpoint); e != nil {
				t.Fatal(e)
			}
			again, _ := read(c.Path)
			if !bytes.Equal(first, again) {
				t.Fatal("not idempotent")
			}
			s := m.status(c, endpoint)
			if !s.CanRevoke || s.CanWrite {
				t.Fatalf("bad status %+v", s)
			}
			// Simulate unrelated edits after installation. Revoke must preserve them.
			if c.TOML {
				again = append(again, []byte("\n[later]\nkeep = true\n")...)
			} else {
				again = bytes.Replace(again, []byte("42"), []byte("43"), 1)
			}
			if e := os.WriteFile(c.Path, again, 0600); e != nil {
				t.Fatal(e)
			}
			if e := m.Revoke(id); e != nil {
				t.Fatal(e)
			}
			final, _ := read(c.Path)
			got, e := entry(c, final)
			if e != nil || got != nil {
				t.Fatalf("entry still present %v %v", got, e)
			}
			marker := []byte("43")
			if c.TOML {
				marker = []byte("keep = true")
			}
			if !bytes.Contains(final, marker) {
				t.Fatal("lost later edits")
			}
			if !bytes.Contains(final, []byte("keep this comment")) {
				t.Fatal("lost comment after revoke")
			}
		})
	}
}
func TestProtectModifiedAndConflictingEntries(t *testing.T) {
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		t.Run(id, func(t *testing.T) {
			h := t.TempDir()
			m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
			c, _ := m.client(id)
			if e := m.Write(id, "http://localhost:8788/mcp"); e != nil {
				t.Fatal(e)
			}
			b, _ := read(c.Path)
			b = bytes.ReplaceAll(b, []byte("8788"), []byte("9999"))
			os.WriteFile(c.Path, b, 0600)
			if e := m.Revoke(id); e == nil {
				t.Fatal("removed modified entry")
			}
			if e := m.Write(id, "http://localhost:8788/mcp"); e == nil {
				t.Fatal("overwrote conflict")
			}
			after, _ := read(c.Path)
			if !bytes.Equal(b, after) {
				t.Fatal("modified conflict")
			}
		})
	}
}
func TestMalformedConfigUntouched(t *testing.T) {
	h := t.TempDir()
	m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
	for _, c := range m.Clients {
		b := []byte("not valid {{{")
		atomicWrite(c.Path, b)
		if e := m.Write(c.ID, "http://localhost/mcp"); e == nil {
			t.Fatal(c.ID)
		}
		after, _ := read(c.Path)
		if !bytes.Equal(after, b) {
			t.Fatal("modified malformed config")
		}
	}
}

func TestDuplicateJSONKeysRejected(t *testing.T) {
	h := t.TempDir()
	m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
	c, _ := m.client("cursor")
	original := []byte(`{"mcpServers":{},"mcpServers":{"existing":{"url":"http://localhost"}}}`)
	atomicWrite(c.Path, original)
	if err := m.Write("cursor", "http://localhost/mcp"); err == nil {
		t.Fatal("accepted duplicate keys")
	}
	actual, _ := read(c.Path)
	if !bytes.Equal(actual, original) {
		t.Fatal("changed ambiguous config")
	}
}
func TestPermissionsAndReceiptRecovery(t *testing.T) {
	h := t.TempDir()
	m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
	c, _ := m.client("codex")
	atomicWrite(c.Path, []byte("# original\n"))
	os.Chmod(c.Path, 0600)
	if e := m.Write(c.ID, "http://localhost/mcp"); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(c.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("changed permissions")
	}
	// New process can revoke, even after an interrupted previous run.
	reopened := New(h, m.StateDir, func(string) string { return "" })
	if e := reopened.Revoke(c.ID); e != nil {
		t.Fatal(e)
	}
	actual, _ := read(c.Path)
	if string(actual) != "# original\n" {
		t.Fatal("TOML did not round-trip byte-for-byte")
	}
}
func TestPathOverridesAndJSONC(t *testing.T) {
	h := t.TempDir()
	custom := filepath.Join(h, "custom")
	os.MkdirAll(filepath.Join(custom, "opencode"), 0700)
	os.WriteFile(filepath.Join(custom, "opencode", "opencode.jsonc"), []byte("{}"), 0600)
	m := New(h, filepath.Join(h, "receipts"), func(key string) string {
		switch key {
		case "CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR":
			return custom
		}
		return ""
	})
	paths := map[string]string{"codex": "config.toml", "claude": ".claude.json", "opencode": "opencode/opencode.jsonc", "pi": "mcp.json"}
	for id, suffix := range paths {
		c, _ := m.client(id)
		if c.Path != filepath.Join(custom, suffix) {
			t.Fatalf("%s: %s", id, c.Path)
		}
	}
}

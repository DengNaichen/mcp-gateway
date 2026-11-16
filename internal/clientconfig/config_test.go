package clientconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			firstEntry, err := entry(c, first)
			if err != nil || firstEntry["url"] != "http://127.0.0.1:8788/mcp?agent="+id {
				t.Fatalf("expected ?agent=%s query param, got %v (err=%v)", id, firstEntry, err)
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

func TestUpdateManagedClients(t *testing.T) {
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		t.Run(id, func(t *testing.T) {
			h := t.TempDir()
			m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
			c, _ := m.client(id)
			oldURL, newURL := "http://localhost:8788/mcp", "http://localhost:8789/mcp"
			if err := m.Update(id, newURL); err == nil {
				t.Fatal("updated absent entry")
			}
			if err := m.Write(id, oldURL); err != nil {
				t.Fatal(err)
			}
			b, _ := read(c.Path)
			if c.TOML {
				b = append(b, []byte("\n[unrelated]\nkeep = true\n")...)
			} else {
				b = bytes.Replace(b, []byte("{"), []byte("{\n// retain user comment\n\"unrelated\":true,"), 1)
			}
			os.WriteFile(c.Path, b, 0600)
			s := m.status(c, newURL)
			if !s.CanUpdate || !s.CanRevoke || s.CanWrite {
				t.Fatalf("bad pending status: %+v", s)
			}
			if err := m.Update(id, newURL); err != nil {
				t.Fatal(err)
			}
			updated, _ := read(c.Path)
			got, err := entry(c, updated)
			if err != nil || got["url"] != ClientEndpoint(newURL, id) {
				t.Fatal("wrong updated entry", got, err)
			}
			if !bytes.Contains(updated, []byte("unrelated")) {
				t.Fatal("lost unrelated config")
			}
			if s = m.status(c, newURL); s.CanUpdate || !s.CanRevoke {
				t.Fatalf("not current: %+v", s)
			}
			if err := m.Update(id, newURL); err != nil {
				t.Fatal("update not idempotent", err)
			}
			again, _ := read(c.Path)
			if !bytes.Equal(updated, again) {
				t.Fatal("repeat update rewrote file")
			}
			// Manual edits are never silently replaced by Update.
			changed := bytes.ReplaceAll(updated, []byte("8789"), []byte("9999"))
			os.WriteFile(c.Path, changed, 0600)
			if err := m.Update(id, newURL); err == nil {
				t.Fatal("overwrote user edit")
			}
			after, _ := read(c.Path)
			if !bytes.Equal(after, changed) {
				t.Fatal("changed conflict")
			}
			os.WriteFile(c.Path, updated, 0600)
			if err := m.Revoke(id); err != nil {
				t.Fatal(err)
			}
			after, _ = read(c.Path)
			if !bytes.Contains(after, []byte("unrelated")) {
				t.Fatal("revoke lost unrelated config")
			}
		})
	}
}
func TestInterruptedUpdateKeepsOwnership(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(fmt.Sprint(written), func(t *testing.T) {
			h := t.TempDir()
			m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
			c, _ := m.client("cursor")
			oldURL, newURL := "http://localhost:8788/mcp", "http://localhost:8789/mcp"
			if err := m.Write(c.ID, oldURL); err != nil {
				t.Fatal(err)
			}
			prior, err := m.load(c)
			if err != nil {
				t.Fatal(err)
			}
			pending := receipt{Path: c.Path, Entry: desired(c, newURL), Previous: prior}
			raw, _ := json.Marshal(pending)
			atomicWrite(m.receiptPath(c), raw)
			if written {
				b, _ := read(c.Path)
				next, err := editJSON(c, b, pending.Entry)
				if err != nil {
					t.Fatal(err)
				}
				atomicWrite(c.Path, next)
			}
			reopened := New(h, m.StateDir, func(string) string { return "" })
			if !reopened.status(c, newURL).CanRevoke {
				t.Fatal("lost ownership after interrupted update")
			}
			if err := reopened.Update(c.ID, newURL); err != nil {
				t.Fatal(err)
			}
			settled, err := reopened.load(c)
			if err != nil || settled.Previous != nil {
				t.Fatal("update journal was not finalized", err)
			}
			if err := reopened.Revoke(c.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpgradeLegacyURLWithoutAgentQuery(t *testing.T) {
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		t.Run(id, func(t *testing.T) {
			h := t.TempDir()
			m := New(h, filepath.Join(h, "receipts"), func(string) string { return "" })
			c, _ := m.client(id)
			legacyURL := "http://127.0.0.1:8788/mcp"
			legacyEntry := desired(c, legacyURL)
			legacyEntry["url"] = legacyURL
			var rec receipt
			rec.Path = c.Path
			rec.Entry = legacyEntry
			if c.TOML {
				rec.Block = "\n[mcp_servers." + serverName + "]\nurl = \"http://127.0.0.1:8788/mcp\"\n"
				atomicWrite(c.Path, []byte(rec.Block))
			} else {
				next, err := editJSON(c, nil, legacyEntry)
				if err != nil {
					t.Fatal(err)
				}
				atomicWrite(c.Path, next)
			}
			rawRec, _ := json.Marshal(rec)
			atomicWrite(m.receiptPath(c), rawRec)

			st := m.status(c, legacyURL)
			if !st.CanUpdate || !st.CanRevoke || st.CanWrite {
				t.Fatalf("expected legacy entry without ?agent= to offer update: %+v", st)
			}
			if err := m.Update(id, legacyURL); err != nil {
				t.Fatalf("update legacy entry: %v", err)
			}
			after, _ := read(c.Path)
			got, err := entry(c, after)
			if err != nil || got["url"] != "http://127.0.0.1:8788/mcp?agent="+id {
				t.Fatalf("expected upgraded url with ?agent=%s, got %v (%v)", id, got, err)
			}
		})
	}
}

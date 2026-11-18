// Package clientconfig manages only the desktop user's Gateway MCP registrations.
//
// It is a local-machine capability, not part of the server API: it reads and
// writes MCP client configuration files (Claude, Codex, Cursor and similar) in
// the home directory of the user running the desktop app. It therefore runs
// only on the user's own machine (cmd/gateway-desktop and local developer
// scripts). A shared or self-hosted Gateway must never expose it over /api,
// because the server's filesystem is not the user's.
package clientconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/i18n"

	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

var serverName = buildinfo.Current().Slug

type Client struct {
	ID, Name, Path string
	Keys           []string
	TOML           bool
}
type Status struct {
	ID, Name, Path, State, Detail  string
	CanWrite, CanUpdate, CanRevoke bool
}
type Manager struct {
	Clients  []Client
	StateDir string
	mu       sync.Mutex
}
type receipt struct {
	Path     string
	Entry    map[string]any
	Block    string
	Previous *receipt `json:",omitempty"`
}

// New uses explicit paths so integration tests never touch the user's real settings.
func New(home, stateDir string, getenv func(string) string) *Manager {
	specs := Specs()
	clients := make([]Client, 0, len(specs))
	for _, spec := range specs {
		clients = append(clients, Client{
			ID:   spec.ID,
			Name: spec.Name,
			Path: spec.MCPConfigPath(home, getenv),
			Keys: spec.Keys,
			TOML: spec.TOML,
		})
	}
	return &Manager{StateDir: stateDir, Clients: clients}
}
func (m *Manager) client(id string) (Client, error) {
	for _, c := range m.Clients {
		if c.ID == id {
			return c, nil
		}
	}
	return Client{}, errors.New(i18n.T("client.err-unknown-client"))
}
func desired(c Client, endpoint string) map[string]any {
	e := map[string]any{"url": ClientEndpoint(endpoint, c.ID)}
	switch c.ID {
	case "claude":
		e["type"] = "http"
	case "opencode":
		e["type"] = "remote"
		e["oauth"] = false
	case "pi":
		e["exposure"] = "direct"
	}
	return e
}
func read(path string) ([]byte, error) {
	b, e := os.ReadFile(path) //nolint:gosec // G304: path is built from the user's own local config/data directories, not from remote input
	if os.IsNotExist(e) {
		return nil, nil
	}
	return b, e
}
func decode(c Client, b []byte) (map[string]any, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return root, nil
	}
	if c.TOML {
		return root, toml.Unmarshal(b, &root)
	}
	v, e := hujson.Parse(bytes.Clone(b))
	if e != nil {
		return nil, e
	}
	if e = uniqueKeys(&v); e != nil {
		return nil, e
	}
	v.Standardize()
	e = json.Unmarshal(v.Pack(), &root)
	if e == nil && root == nil {
		e = errors.New(i18n.T("client.err-root-not-object"))
	}
	return root, e
}
func entry(c Client, b []byte) (map[string]any, error) {
	root, e := decode(c, b)
	if e != nil {
		return nil, e
	}
	for _, key := range c.Keys {
		v, ok := root[key]
		if !ok {
			return nil, nil
		}
		root, ok = v.(map[string]any)
		if !ok {
			return nil, errors.New(i18n.T("client.err-key-not-object", key))
		}
	}
	v, ok := root[serverName]
	if !ok {
		return nil, nil
	}
	out, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New(i18n.T("client.err-gateway-not-object")) //nolint:staticcheck // ST1005: "Gateway" is the product name
	}
	return out, nil
}
func (m *Manager) receiptPath(c Client) string { return filepath.Join(m.StateDir, c.ID+".json") }
func (m *Manager) load(c Client) (*receipt, error) {
	b, e := read(m.receiptPath(c))
	if e != nil || len(b) == 0 {
		return nil, e
	}
	var r receipt
	e = json.Unmarshal(b, &r)
	if e == nil && r.Path != c.Path {
		e = errors.New(i18n.T("client.err-path-changed"))
	}
	return &r, e
}

// A pending update owns either side of the atomic config replacement.
func matchingReceipt(r *receipt, got map[string]any) *receipt {
	if r != nil && !reflect.DeepEqual(got, r.Entry) && r.Previous != nil && reflect.DeepEqual(got, r.Previous.Entry) {
		return r.Previous
	}
	return r
}
func (m *Manager) status(c Client, endpoint string) Status {
	s := Status{ID: c.ID, Name: c.Name, Path: c.Path, State: i18n.T("client.state.not-configured"), CanWrite: true}
	b, e := read(c.Path)
	if e != nil {
		s.State = i18n.T("client.state.read-failed")
		s.Detail = e.Error()
		s.CanWrite = false
		return s
	}
	got, e := entry(c, b)
	if e != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = e.Error()
		s.CanWrite = false
		return s
	}
	r, e := m.load(c)
	if e != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = e.Error()
		s.CanWrite = false
		return s
	}
	r = matchingReceipt(r, got)
	if got == nil {
		return s
	}
	s.CanWrite = false
	if r != nil && reflect.DeepEqual(got, r.Entry) {
		if c.TOML && (r.Block == "" || bytes.Count(b, []byte(r.Block)) != 1) {
			s.State = i18n.T("client.state.config-changed")
			s.Detail = i18n.T("client.detail-external-modification")
			return s
		}
		s.State = i18n.T("client.state.configured")
		s.CanRevoke = true
		if !reflect.DeepEqual(got, desired(c, endpoint)) {
			s.State = i18n.T("client.state.update-required")
			s.CanUpdate = true
			s.Detail = i18n.T("client.detail-endpoint-changed")
		}
		return s
	}
	s.State = i18n.T("client.state.existing-config")
	s.Detail = i18n.T("client.detail-not-managed")
	return s
}
func (m *Manager) Statuses(endpoint string) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Status{}
	for _, c := range m.Clients {
		out = append(out, m.status(c, endpoint))
	}
	return out
}

// Write refuses conflicts; the durable receipt is saved before changing the client file.
func (m *Manager) Write(id, endpoint string) error { return m.write(id, endpoint, false) }

// Update only replaces an entry still matching our durable ownership receipt.
func (m *Manager) Update(id, endpoint string) error { return m.write(id, endpoint, true) }
func (m *Manager) write(id, endpoint string, update bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New(i18n.T("client.err-invalid-mcp-url"))
	}
	c, e := m.client(id)
	if e != nil {
		return e
	}
	lock, e := lockConfig(c.Path)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	b, e := read(c.Path)
	if e != nil {
		return e
	}
	got, e := entry(c, b)
	if e != nil {
		return e
	}
	want := desired(c, endpoint)
	r, e := m.load(c)
	if e != nil {
		return e
	}
	pending := r != nil && r.Previous != nil
	r = matchingReceipt(r, got)
	if got != nil {
		if r == nil || !reflect.DeepEqual(got, r.Entry) {
			return errors.New(i18n.T("client.err-external-modified"))
		}
		if reflect.DeepEqual(got, want) {
			if pending {
				final := *r
				final.Previous = nil
				raw, _ := json.MarshalIndent(final, "", "  ")
				return atomicWrite(m.receiptPath(c), raw)
			}
			return nil
		}
		if !update {
			return errors.New(i18n.T("client.err-existing-needs-update"))
		}
	} else if update {
		return errors.New(i18n.T("client.err-config-missing"))
	}
	rec := receipt{Path: c.Path, Entry: want}
	if got != nil {
		previous := *r
		previous.Previous = nil
		rec.Previous = &previous
	}

	var next []byte
	if c.TOML {
		encoded, _ := json.Marshal(want["url"])
		rec.Block = "\n[mcp_servers." + serverName + "]\nurl = " + string(encoded) + "\n"
		if got != nil {
			if r.Block == "" || bytes.Count(b, []byte(r.Block)) != 1 {
				return errors.New(i18n.T("client.err-text-changed-not-updated"))
			}
			next = bytes.Replace(b, []byte(r.Block), []byte(rec.Block), 1)
		} else {
			next = append(bytes.Clone(b), []byte(rec.Block)...)
		}
	} else {
		next, e = editJSON(c, b, want)
		if e != nil {
			return e
		}
	}
	if _, e = entry(c, next); e != nil {
		return fmt.Errorf(i18n.T("client.err-generate-failed"), e)
	}
	rb, _ := json.MarshalIndent(rec, "", "  ")
	if e = atomicWrite(m.receiptPath(c), rb); e != nil {
		return e
	}
	if e = replaceIfUnchanged(c.Path, b, next); e != nil {
		return e
	}
	written, e := read(c.Path)
	if e != nil {
		return e
	}
	actual, e := entry(c, written)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, want) {
		return errors.New(i18n.T("client.err-verify-after-write"))
	}
	if rec.Previous != nil {
		rec.Previous = nil
		rb, _ = json.MarshalIndent(rec, "", "  ")
		return atomicWrite(m.receiptPath(c), rb)
	}
	return nil
}
func (m *Manager) Revoke(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, e := m.client(id)
	if e != nil {
		return e
	}
	lock, e := lockConfig(c.Path)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	r, e := m.load(c)
	if e != nil {
		return e
	}
	if r == nil {
		return errors.New(i18n.T("client.err-no-write-record"))
	}
	b, e := read(c.Path)
	if e != nil {
		return e
	}
	got, e := entry(c, b)
	if e != nil {
		return e
	}
	r = matchingReceipt(r, got)
	if got == nil {
		return os.Remove(m.receiptPath(c))
	}
	if !reflect.DeepEqual(got, r.Entry) {
		return errors.New(i18n.T("client.err-not-revoked-external"))
	}
	var next []byte
	if c.TOML {
		if r.Block == "" || bytes.Count(b, []byte(r.Block)) != 1 {
			return errors.New(i18n.T("client.err-not-revoked-text-changed"))
		}
		next = bytes.Replace(b, []byte(r.Block), nil, 1)
	} else {
		next, e = editJSON(c, b, nil)
		if e != nil {
			return e
		}
	}
	if _, e = entry(c, next); e != nil {
		return e
	}
	if e = replaceIfUnchanged(c.Path, b, next); e != nil {
		return e
	}
	return os.Remove(m.receiptPath(c))
}
func editJSON(c Client, b []byte, value map[string]any) ([]byte, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}\n")
	}
	v, e := hujson.Parse(bytes.Clone(b))
	if e != nil {
		return nil, e
	}
	ptr := ""
	for _, key := range c.Keys {
		ptr += "/" + key
		if v.Find(ptr) == nil {
			p, _ := json.Marshal([]map[string]any{{"op": "add", "path": ptr, "value": map[string]any{}}})
			if e = v.Patch(p); e != nil {
				return nil, e
			}
		}
	}
	op := map[string]any{"op": "remove", "path": ptr + "/" + serverName}
	if value != nil {
		op["op"] = "add"
		op["value"] = value
	}
	p, _ := json.Marshal([]map[string]any{op})
	if e = v.Patch(p); e != nil {
		return nil, e
	}
	v.Format()
	return v.Pack(), nil
}
func replaceIfUnchanged(path string, b, next []byte) error {
	now, e := read(path)
	if e != nil {
		return e
	}
	if !bytes.Equal(now, b) {
		return errors.New(i18n.T("client.err-modified-just-now"))
	}
	return atomicWrite(path, next)
}
func atomicWrite(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	mode := os.FileMode(0600)
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() {
			return errors.New(i18n.T("client.err-path-not-regular-file"))
		}
		mode = info.Mode().Perm()
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".gateway-*")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	if e = f.Chmod(mode); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

func uniqueKeys(v *hujson.Value) error {
	for child := range v.All() {
		if obj, ok := child.Value.(*hujson.Object); ok {
			seen := map[string]bool{}
			for _, member := range obj.Members {
				var key string
				if err := json.Unmarshal([]byte(member.Name.Value.(hujson.Literal)), &key); err != nil {
					return err
				}
				if seen[key] {
					return errors.New(i18n.T("client.err-duplicate-key", key))
				}
				seen[key] = true
			}
		}
	}
	return nil
}

// Both channels coordinate on the target file, not their separate receipt dirs.
// The lock file is kept so waiting processes always refer to the same inode.
func lockConfig(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".mcp-gateway.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		_ = lock.Close()
		if err != nil {
			return nil, fmt.Errorf(i18n.T("client.err-lock-wait"), err)
		}
		return nil, errors.New(i18n.T("client.err-locked-by-other-process"))
	}
	return lock, nil
}

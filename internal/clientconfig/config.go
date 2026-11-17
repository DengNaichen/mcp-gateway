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
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"

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
	dir := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return filepath.Join(home, fallback)
	}
	oc := dir("XDG_CONFIG_HOME", ".config")
	ocPath := filepath.Join(oc, "opencode", "opencode.json")
	if _, err := os.Stat(strings.TrimSuffix(ocPath, ".json") + ".jsonc"); err == nil {
		ocPath = strings.TrimSuffix(ocPath, ".json") + ".jsonc"
	}
	claude := filepath.Join(home, ".claude.json")
	if v := getenv("CLAUDE_CONFIG_DIR"); v != "" {
		claude = filepath.Join(v, ".claude.json")
	}
	return &Manager{StateDir: stateDir, Clients: []Client{
		{"codex", "Codex CLI", filepath.Join(dir("CODEX_HOME", ".codex"), "config.toml"), []string{"mcp_servers"}, true},
		{"claude", "Claude Code", claude, []string{"mcpServers"}, false},
		{"cursor", "Cursor", filepath.Join(home, ".cursor", "mcp.json"), []string{"mcpServers"}, false},
		{"opencode", "OpenCode v2", ocPath, []string{"mcp", "servers"}, false},
		{"pi", "Pi", filepath.Join(dir("PI_CODING_AGENT_DIR", ".pi/agent"), "mcp.json"), []string{"mcpServers"}, false},
	}}
}
func (m *Manager) client(id string) (Client, error) {
	for _, c := range m.Clients {
		if c.ID == id {
			return c, nil
		}
	}
	return Client{}, errors.New("未知客户端")
}
func ClientEndpoint(endpoint, clientID string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	if clientID != "" {
		q := u.Query()
		q.Set("agent", clientID)
		u.RawQuery = q.Encode()
	}
	return u.String()
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
		e = errors.New("配置根节点必须是对象")
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
			return nil, fmt.Errorf("%s 必须是对象", key)
		}
	}
	v, ok := root[serverName]
	if !ok {
		return nil, nil
	}
	out, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("Gateway 配置不是对象") //nolint:staticcheck // ST1005: "Gateway" is the product name
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
		e = errors.New("配置路径已改变，保留原写入记录")
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
	s := Status{ID: c.ID, Name: c.Name, Path: c.Path, State: "未配置", CanWrite: true}
	b, e := read(c.Path)
	if e != nil {
		s.State = "读取失败"
		s.Detail = e.Error()
		s.CanWrite = false
		return s
	}
	got, e := entry(c, b)
	if e != nil {
		s.State = "配置异常"
		s.Detail = e.Error()
		s.CanWrite = false
		return s
	}
	r, e := m.load(c)
	if e != nil {
		s.State = "记录异常"
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
			s.State = "配置已变更"
			s.Detail = "配置文本已被外部修改，请检查配置"
			return s
		}
		s.State = "已配置"
		s.CanRevoke = true
		if !reflect.DeepEqual(got, desired(c, endpoint)) {
			s.State = "需要更新"
			s.CanUpdate = true
			s.Detail = "Gateway 地址已改变，可更新本应用管理的配置"
		}
		return s
	}
	s.State = "已有配置"
	s.Detail = "此条目不属于本次管理记录，或已被外部修改；保留原内容"
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
		return errors.New("无效的 MCP HTTP 地址")
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
			return errors.New("配置已被外部修改或不属于本应用，未覆盖")
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
			return errors.New("已有配置需要更新，请使用更新配置")
		}
	} else if update {
		return errors.New("配置已不存在，请刷新后写入配置")
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
				return errors.New("配置文本已改变，未更新")
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
		return fmt.Errorf("生成配置失败: %w", e)
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
		return errors.New("写入后校验失败，请刷新配置")
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
		return errors.New("没有可撤回的写入记录")
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
		return errors.New("配置已被外部修改，未撤回")
	}
	var next []byte
	if c.TOML {
		if r.Block == "" || bytes.Count(b, []byte(r.Block)) != 1 {
			return errors.New("配置文本已改变，未撤回")
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
		return errors.New("配置刚被其他程序修改，请刷新后重试")
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
			return errors.New("配置路径不是普通文件")
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
					return fmt.Errorf("配置包含重复键 %q", key)
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
			return nil, fmt.Errorf("等待客户端配置锁失败，请稍后重试: %w", err)
		}
		return nil, errors.New("客户端配置正被其他进程修改，请稍后重试")
	}
	return lock, nil
}

package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"
)

func TestReplayCacheConcurrentConsumeExpiryAndCapacity(t *testing.T) {
	now := time.Now()
	c := newJTICache()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if c.consume("once", now.Unix()+60, now) {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("consumed %d times", accepted.Load())
	}
	if c.consume("", now.Unix()+60, now) {
		t.Fatal("empty JTI accepted")
	}
	c = newJTICache()
	for i := 0; i < maxJTICacheEntries; i++ {
		if !c.consume(fmt.Sprint(i), now.Unix()+60, now) {
			t.Fatal("cache filled early")
		}
	}
	if c.consume("overflow", now.Unix()+60, now) {
		t.Fatal("evicted an unexpired JTI")
	}
	later := now.Add(61 * time.Second)
	if !c.consume("next", later.Unix()+60, later) {
		t.Fatal("expired entries were not reclaimed")
	}
}

func TestVerifiedScopePersistsRulesAndRejectsReplay(t *testing.T) {
	dir := t.TempDir()
	repoDir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := []byte("01234567890123456789012345678901")
	reg := registry.Registry{Servers: []Server{{ID: "svc", Enabled: true}}, Tools: []Tool{{ID: "svc.read", ServerID: "svc", ReadOnly: true, Enabled: true}, {ID: "svc.write", ServerID: "svc", Enabled: true}}}
	options := Options{DB: db.DB, Dir: dir, BaseURL: "http://localhost", Key: key, Snapshot: func() registry.Registry { return reg }, AdminCaller: func(context.Context) bool { return true }, IdentityAllowsTool: func(context.Context, Tool, string) bool { return true }}
	s, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := s.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "project", Path: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveScopeRule(ScopeRule{RepoKey: repo.ID, AgentKey: "codex", Grants: []ServiceGrant{{ServerID: "svc", Access: "ro", ToolIDs: []string{}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}
	s, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsEnforced() || len(s.ScopeOverview().Repositories) != 1 {
		t.Fatal("scope state did not persist")
	}
	preview, err := s.PreviewScope(context.Background(), repo.ID, "developer")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, agent := range preview.Agents {
		if agent.Agent == "codex" {
			found = true
			if agent.ToolCount != 1 || agent.HasWrite {
				t.Fatalf("preview: %+v", agent)
			}
		}
	}
	if !found {
		t.Fatal("missing codex preview")
	}
	args := map[string]any{"intent": "inspect"}
	hash, err := workspace.CanonicalArgsHash(args, workspace.CurrentContextField())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, err := workspace.SignToken(key, workspace.Claims{Channel: buildinfo.Current().Channel, Agent: "codex", Cwd: repoDir, Op: "discover_tools", ArgsHash: hash, JTI: "once", IAT: now.Unix(), EXP: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	args[workspace.CurrentContextField()] = token
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	ctx, clean, err := s.ResolveMCPToolCallScope(context.Background(), "codex", "discover_tools", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AllowsTool(ctx, reg.Tools[0]) || s.AllowsTool(ctx, reg.Tools[1]) {
		t.Fatal("verified read-only policy not enforced")
	}
	var stripped map[string]any
	if err := json.Unmarshal(clean, &stripped); err != nil {
		t.Fatal(err)
	}
	if _, ok := stripped[workspace.CurrentContextField()]; ok {
		t.Fatal("scope credential leaked into args")
	}
	if _, _, err := s.ResolveMCPToolCallScope(context.Background(), "codex", "discover_tools", raw); !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay error: %v", err)
	}
}

// FuzzResolveMCPToolCallScope drives the full MCP scope verification path with
// an arbitrary context token and URL agent. It must never panic, and no token
// other than the one actually signed with the scope key may be accepted.
func FuzzResolveMCPToolCallScope(f *testing.F) {
	dir := f.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { db.Close() })
	key := []byte("01234567890123456789012345678901")
	reg := registry.Registry{}
	s, err := New(Options{DB: db.DB, Dir: dir, BaseURL: "http://localhost", Key: key, Snapshot: func() registry.Registry { return reg }, AdminCaller: func(context.Context) bool { return true }, IdentityAllowsTool: func(context.Context, Tool, string) bool { return true }})
	if err != nil {
		f.Fatal(err)
	}
	baseArgs := map[string]any{"intent": "inspect"}
	field := workspace.CurrentContextField()
	hash, err := workspace.CanonicalArgsHash(baseArgs, field)
	if err != nil {
		f.Fatal(err)
	}
	// Fixed clock and cwd keep the signed seed token identical in the fuzz workers.
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	valid, err := workspace.SignToken(key, workspace.Claims{Channel: buildinfo.Current().Channel, Agent: "codex", Cwd: "/", Op: "discover_tools", ArgsHash: hash, JTI: "fuzz-valid", IAT: now.Unix(), EXP: now.Add(time.Minute).Unix()})
	if err != nil {
		f.Fatal(err)
	}
	parts := strings.Split(valid, ".")
	f.Add(valid, "codex")
	f.Add(valid, "claude")
	f.Add(valid, "")
	f.Add(parts[0]+"."+parts[1]+".", "codex")
	f.Add("v1."+parts[1]+"."+parts[2][:len(parts[2])-1]+"A", "codex")
	f.Add("", "codex")
	f.Add("not.a.token", "CODEX")
	f.Add("v1..", "unknown-agent")
	f.Fuzz(func(t *testing.T, token, agent string) {
		args := map[string]any{"intent": "inspect", field: token}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Skip()
		}
		_, clean, err := s.ResolveMCPToolCallScope(context.Background(), agent, "discover_tools", raw)
		if err == nil && token != valid {
			t.Fatalf("forged token accepted: %q (agent %q)", token, agent)
		}
		if err == nil && bytes.Contains(clean, []byte(field)) {
			t.Fatalf("context field leaked into upstream arguments: %s", clean)
		}
	})
}

package scope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

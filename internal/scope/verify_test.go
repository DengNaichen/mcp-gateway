package scope

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	options := Options{DB: db.DB, Dir: dir, BaseURL: "http://localhost", Key: key, Snapshot: func() registry.Registry { return reg }, AdminCaller: func(context.Context) bool { return true }}
	s, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := s.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "project", Path: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveScopeRule(ScopeRule{RepoKey: repo.ID, Grants: []ServiceGrant{{ServerID: "svc", Access: "ro", ToolIDs: []string{}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}
	s, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsEnforced() || len(s.ScopeOverview().Repositories) != 1 || len(s.ScopeOverview().Rules) != 2 {
		t.Fatalf("scope state did not persist: %+v", s.ScopeOverview())
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
	if resolved := s.ExecutionScope(ctx); resolved.Mode != ModeEnforced || len(resolved.Rule.Grants) != 1 || resolved.Rule.Grants[0].Access != "ro" {
		t.Fatal("verified read-only policy not enforced")
	}
	ev := EventForContext(ctx)
	if ev.Agent != "codex" || ev.MatchedRule != repo.ID {
		t.Fatalf("expected event to retain agent=codex and matched_rule=%s, got %+v", repo.ID, ev)
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

func TestLegacyAgentRulesDoNotTakeEffectAsGhostRules(t *testing.T) {
	dir := t.TempDir()
	repoRODir, _ := filepath.EvalSymlinks(t.TempDir())
	repoGhostDir, _ := filepath.EvalSymlinks(t.TempDir())
	unboundDir, _ := filepath.EvalSymlinks(t.TempDir())

	// Seed a v7 database containing legacy agent_key != "*" rules.
	rawDB, err := sql.Open("sqlite", filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	repoRODef, _ := json.Marshal(Repository{ID: "repo-ro", Name: "repo-ro", RootPaths: []string{repoRODir}})
	repoGhostDef, _ := json.Marshal(Repository{ID: "repo-ghost", Name: "repo-ghost", RootPaths: []string{repoGhostDir}})
	if _, err := rawDB.Exec(`
CREATE TABLE servers(id TEXT PRIMARY KEY,definition TEXT NOT NULL CHECK(json_valid(definition)),position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE tools(id TEXT PRIMARY KEY,server_id TEXT NOT NULL REFERENCES servers(id),definition TEXT NOT NULL CHECK(json_valid(definition)),position INTEGER NOT NULL DEFAULT 0);
CREATE TABLE scope_state(id INTEGER PRIMARY KEY CHECK(id=1),definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE repositories(id TEXT PRIMARY KEY,definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE scope_rules(repo_key TEXT NOT NULL,agent_key TEXT NOT NULL,definition TEXT NOT NULL CHECK(json_valid(definition)),PRIMARY KEY(repo_key,agent_key));
CREATE TABLE scope_observations(fingerprint TEXT PRIMARY KEY,last_seen TEXT NOT NULL,definition TEXT NOT NULL CHECK(json_valid(definition)));
INSERT INTO scope_state(id,definition) VALUES(1,'{"enforce":true}');
INSERT INTO repositories(id,definition) VALUES('repo-ro',?),('repo-ghost',?);
INSERT INTO scope_rules(repo_key,agent_key,definition) VALUES
  ('*','*','{"repo_key":"*","agent_key":"*","grants":[]}'),
  ('*','codex','{"repo_key":"*","agent_key":"codex","grants":[{"server_id":"svc","access":"rw"}]}'),
  ('repo-ro','*','{"repo_key":"repo-ro","agent_key":"*","grants":[{"server_id":"svc","access":"ro"}]}'),
  ('repo-ro','codex','{"repo_key":"repo-ro","agent_key":"codex","grants":[{"server_id":"svc","access":"rw"}]}'),
  ('repo-ghost','claude','{"repo_key":"repo-ghost","agent_key":"claude","grants":[{"server_id":"svc","access":"rw"}]}');
PRAGMA user_version=7;`, string(repoRODef), string(repoGhostDef)); err != nil {
		t.Fatal(err)
	}
	_ = rawDB.Close()

	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := []byte("01234567890123456789012345678901")
	reg := registry.Registry{
		Servers: []Server{{ID: "svc", Enabled: true}},
		Tools: []Tool{
			{ID: "svc.read", ServerID: "svc", ReadOnly: true, Enabled: true},
			{ID: "svc.write", ServerID: "svc", ReadOnly: false, Enabled: true},
		},
	}
	options := Options{DB: db.DB, Dir: dir, BaseURL: "http://localhost", Key: key, Snapshot: func() registry.Registry { return reg }, AdminCaller: func(context.Context) bool { return true }}
	s, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	ov := s.ScopeOverview()
	if len(ov.Rules) != 2 {
		t.Fatalf("expected only '*' and 'repo-ro' after legacy cleanup, got %+v", ov.Rules)
	}

	callFor := func(s *Service, agent, cwd, jti string) context.Context {
		t.Helper()
		args := map[string]any{"intent": "inspect"}
		hash, err := workspace.CanonicalArgsHash(args, workspace.CurrentContextField())
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		tok, err := workspace.SignToken(key, workspace.Claims{
			Channel: buildinfo.Current().Channel, Agent: agent, Cwd: cwd, Op: "discover_tools",
			ArgsHash: hash, JTI: jti, IAT: now.Unix(), EXP: now.Add(time.Minute).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		args[workspace.CurrentContextField()] = tok
		raw, _ := json.Marshal(args)
		ctx, _, err := s.ResolveMCPToolCallScope(context.Background(), agent, "discover_tools", raw)
		if err != nil {
			t.Fatalf("ResolveMCPToolCallScope(%s, %s): %v", agent, cwd, err)
		}
		return ctx
	}

	// 1. Legacy (repo-ro, codex)->rw must NOT override repo-ro's ro rule.
	ctxRO := callFor(s, "codex", repoRODir, "jti-1")
	if resolved := s.ExecutionScope(ctxRO); resolved.Mode != ModeEnforced || len(resolved.Rule.Grants) != 1 || resolved.Rule.Grants[0].Access != "ro" {
		t.Fatalf("legacy per-agent rule replaced repo-ro: %+v", resolved)
	}

	// 2. Legacy (*, codex)->rw must NOT grant tools to unconfigured workspaces.
	ctxUnbound := callFor(s, "codex", unboundDir, "jti-2")
	if len(s.ExecutionScope(ctxUnbound).Rule.Grants) != 0 {
		t.Fatal("legacy (*, codex) rw rule took ghost effect in unbound repo")
	}

	// 3. Legacy (repo-ghost, claude)->rw must NOT grant tools to repo-ghost.
	ctxGhost := callFor(s, "claude", repoGhostDir, "jti-3")
	if len(s.ExecutionScope(ctxGhost).Rule.Grants) != 0 {
		t.Fatal("legacy (repo-ghost, claude) rw rule took ghost effect")
	}

	// 4. Even if a stale JSON definition containing agent_key != "*" exists in scope_rules,
	// loadScopes() ignores and cleans it instead of letting it become a repo rule.
	if err := store.ScopeRules[map[string]any](db.DB).Put("repo-ghost", map[string]any{
		"repo_key":  "repo-ghost",
		"agent_key": "claude",
		"grants":    []map[string]any{{"server_id": "svc", "access": "rw"}},
	}); err != nil {
		t.Fatal(err)
	}
	s2, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.ScopeOverview().Rules) != 2 {
		t.Fatalf("expected loadScopes to ignore/clean stale agent_key != '*' rule, got %+v", s2.ScopeOverview().Rules)
	}
	ctxGhost2 := callFor(s2, "claude", repoGhostDir, "jti-4")
	if len(s2.ExecutionScope(ctxGhost2).Rule.Grants) != 0 {
		t.Fatal("stale JSON rule with agent_key != '*' took ghost effect after reload")
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
	s, err := New(Options{DB: db.DB, Dir: dir, BaseURL: "http://localhost", Key: key, Snapshot: func() registry.Registry { return reg }, AdminCaller: func(context.Context) bool { return true }})
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

package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/workspace"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func initGitRepoForScopeTest(t *testing.T, dir, remoteURL string, withCommit bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", realDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, realDir, err, out)
		}
	}
	git("init")
	if remoteURL != "" {
		git("remote", "add", "origin", remoteURL)
	}
	if withCommit {
		if err := os.WriteFile(filepath.Join(realDir, "README.md"), []byte("# repo\n"), 0644); err != nil {
			t.Fatal(err)
		}
		git("add", "README.md")
		git("commit", "-m", "initial")
	}
	return realDir
}

func signMCPArgs(t *testing.T, g *Gateway, agent, cwd string, ambiguous bool, op string, args map[string]any) map[string]any {
	t.Helper()
	ctxField := workspace.CurrentContextField()
	cloned := make(map[string]any, len(args)+1)
	for k, v := range args {
		if k != ctxField {
			cloned[k] = v
		}
	}
	hash, err := workspace.CanonicalArgsHash(cloned, ctxField)
	if err != nil {
		t.Fatal(err)
	}
	now := g.now()
	tok, err := workspace.SignToken(g.scopeKey, workspace.Claims{
		Channel:   buildinfo.Current().Channel,
		Agent:     agent,
		Cwd:       cwd,
		Ambiguous: ambiguous,
		Op:        op,
		ArgsHash:  hash,
		SID:       "test-sid",
		IAT:       now.Unix(),
		EXP:       now.Add(60 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cloned[ctxField] = tok
	return cloned
}

func registerMultiServiceUpstream(t *testing.T, g *Gateway) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"ok": true, "path": r.URL.Path, "method": r.Method})
	}))
	t.Cleanup(upstream.Close)

	for _, srv := range []struct {
		id   string
		name string
		desc string
	}{
		{"github", "GitHub", "代码仓库 PR 与 Issue 管理"},
		{"gitlab", "GitLab", "CI Pipeline 与合并请求"},
		{"roster", "Roster", "团队任务与 Spec 管理"},
	} {
		if err := g.SaveServer(Server{
			ID:          srv.id,
			Name:        srv.name,
			Description: srv.desc,
			Kind:        "http",
			Endpoint:    upstream.URL,
			Enabled:     true,
		}); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct {
			name     string
			desc     string
			readOnly bool
			method   string
			actors   []string
		}{
			{"list", "List items in " + srv.id, true, "GET", []string{"developer", "observer"}},
			{"get", "Get single item in " + srv.id, true, "GET", []string{"developer"}},
			{"mutate", "Create or update item in " + srv.id, false, "POST", []string{"developer"}},
		} {
			if err := g.SaveTool(Tool{
				ID:          srv.id + "." + spec.name,
				ServerID:    srv.id,
				Name:        spec.name,
				Description: spec.desc,
				Tags:        []string{srv.id, spec.name},
				InputSchema: objectSchema(map[string]any{}),
				Enabled:     true,
				ReadOnly:    spec.readOnly,
				Actors:      spec.actors,
				Method:      spec.method,
				Path:        "/" + srv.id + "/" + spec.name,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestV6MigrationAndWriteRegistryNoFKInterference(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gateway.db")

	// 1. Construct a legacy user_version=5 database before opening Gateway.
	u := url.URL{Scheme: "file", Path: dbPath}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	rawDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = rawDB.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE servers (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE tools (id TEXT PRIMARY KEY, server_id TEXT NOT NULL REFERENCES servers(id), definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK(id=1), definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE activity (id TEXT PRIMARY KEY, time TEXT NOT NULL, kind TEXT NOT NULL, actor TEXT NOT NULL, status TEXT NOT NULL, title TEXT NOT NULL, duration_ms INTEGER NOT NULL, detail TEXT NOT NULL CHECK(json_valid(detail)));
CREATE TABLE connection_checks (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE maintenance_sources (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE maintenance_runs (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE maintenance_acceptance (id TEXT PRIMARY KEY, definition TEXT NOT NULL CHECK(json_valid(definition)));
CREATE TABLE maintenance_deliveries (server_id TEXT NOT NULL, provider TEXT NOT NULL, delivery_id TEXT NOT NULL, received_at TEXT NOT NULL, run_id TEXT NOT NULL, definition TEXT NOT NULL CHECK(json_valid(definition)), PRIMARY KEY(server_id,provider,delivery_id));
INSERT INTO servers(id, definition) VALUES('svc', '{"id":"svc","name":"Svc","description":"legacy","kind":"http","endpoint":"http://127.0.0.1:9999","enabled":true}');
INSERT INTO settings(id, definition) VALUES(1, '{"base_url":"https://api.openai.com/v1","model":"gpt-6-luna","max_tools":4,"timeout_seconds":45}');
PRAGMA user_version=5;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	g, err := New(dir, "http://127.0.0.1:8789")
	if err != nil {
		t.Fatalf("New over v5 db: %v", err)
	}
	defer g.Close()

	var version int
	if err := g.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("expected PRAGMA user_version=6, got %d", version)
	}

	overview := g.ScopeOverview()
	if overview.Enforce {
		t.Fatal("default enforce must be false")
	}
	if len(overview.Rules) != 1 || overview.Rules[0].RepoKey != "*" || overview.Rules[0].AgentKey != "*" || len(overview.Rules[0].Grants) != 0 {
		t.Fatalf("expected out-of-the-box (*,*) -> [] rule, got %+v", overview.Rules)
	}

	// 2. Bind a repo + rule referencing 'svc', then rewrite servers/tools via SaveServer/SaveTool
	// (which runs DELETE FROM tools; DELETE FROM servers in writeRegistry) and confirm rules survive!
	repoDir := initGitRepoForScopeTest(t, filepath.Join(t.TempDir(), "app"), "", false)
	repo, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "app", Path: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveScopeRule(ScopeRule{
		RepoKey:  repo.ID,
		AgentKey: "*",
		Grants:   []ServiceGrant{{ServerID: "svc", Access: "rw"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveServer(Server{ID: "svc", Name: "Svc Updated", Description: "updated", Kind: "http", Endpoint: "http://127.0.0.1:9999", Enabled: true}); err != nil {
		t.Fatalf("SaveServer failed after scope_rules added: %v", err)
	}
	if err := g.SaveTool(Tool{ID: "svc.ping", ServerID: "svc", Name: "ping", Description: "ping", InputSchema: objectSchema(map[string]any{}), Enabled: true, ReadOnly: true, Actors: []string{"developer"}, Method: "GET", Path: "/ping"}); err != nil {
		t.Fatalf("SaveTool failed after scope_rules added: %v", err)
	}
	after := g.ScopeOverview()
	if len(after.Rules) != 2 {
		t.Fatalf("scope_rules lost after writeRegistry: %+v", after.Rules)
	}
}

func TestMCPScopeSecurityInvariantsWorktreesAndPrecedence(t *testing.T) {
	workspace.ClearCache()
	g, _ := testGateway(t)
	useKeywordModel(t, g)
	registerMultiServiceUpstream(t, g)

	host := httptest.NewServer(g.Handler())
	defer host.Close()

	// Create repos:
	// - repoMain + linked git worktree + subdir
	// - repoEvilClone: DIFFERENT local path with IDENTICAL origin RemoteHint (must NOT auto-authorize!)
	// - repoEmpty: empty `git init` without commits or remote
	// - dirPlain (/base/proj) and sibling /base/proj-old (segment boundary test!)
	base := t.TempDir()
	realBase, _ := filepath.EvalSymlinks(base)
	repoMain := initGitRepoForScopeTest(t, filepath.Join(realBase, "gateway-repo"), "https://github.com/DengNaichen/mcp-gateway.git", true)
	repoWT := filepath.Join(realBase, "gateway-wt")
	if out, err := exec.Command("git", "-C", repoMain, "worktree", "add", "-b", "wt-1", repoWT).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	repoWTSub := filepath.Join(repoWT, "internal", "sub")
	if err := os.MkdirAll(repoWTSub, 0755); err != nil {
		t.Fatal(err)
	}

	repoEvilClone := initGitRepoForScopeTest(t, filepath.Join(realBase, "evil-clone"), "https://github.com/DengNaichen/mcp-gateway.git", true)
	repoEmpty := initGitRepoForScopeTest(t, filepath.Join(realBase, "fresh-empty"), "", false)

	dirProj := filepath.Join(realBase, "proj")
	dirProjOld := filepath.Join(realBase, "proj-old")
	if err := os.MkdirAll(dirProj, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirProjOld, 0755); err != nil {
		t.Fatal(err)
	}

	connectAgent := func(agent string, extraHeaders map[string]string) *mcp.ClientSession {
		t.Helper()
		endpoint := host.URL + "/mcp"
		if agent != "" {
			endpoint += "?agent=" + agent
		}
		var hc *http.Client
		if len(extraHeaders) > 0 {
			hc = &http.Client{Transport: headerTransport{headers: extraHeaders, base: http.DefaultTransport}}
		}
		cl := mcp.NewClient(&mcp.Implementation{Name: "test-" + agent, Version: "1"}, nil)
		sess, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { closeSession(sess) })
		return sess
	}

	codexSess := connectAgent("codex", nil)
	claudeSess := connectAgent("claude", nil)
	cursorSess := connectAgent("cursor", nil)

	ctxField := workspace.CurrentContextField()

	// --- Invariant 1: Even when enforce == false, invalid/expired/replayed/tampered/agent-mismatched _gw_ctx MUST fail 403 scope_invalid! ---
	// 1a. Forged token string
	_, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "discover_tools",
		Arguments: map[string]any{"intent": "github list", ctxField: "v1.bad.sig"},
	})
	if err == nil || !strings.Contains(err.Error(), "scope_invalid") {
		t.Fatalf("expected scope_invalid when enforce==false on bad token, got: %v", err)
	}

	// 1b. Valid token used once succeeds, replayed a second time MUST fail 403 scope_invalid!
	signedOnce := signMCPArgs(t, g, "codex", repoMain, false, "discover_tools", map[string]any{"intent": "github list"})
	if _, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: signedOnce}); err != nil {
		t.Fatalf("first call with valid token failed: %v", err)
	}
	_, err = codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: signedOnce})
	if err == nil || !strings.Contains(err.Error(), "scope_invalid") {
		t.Fatalf("replayed JTI was not rejected with scope_invalid: %v", err)
	}

	// 1c. Argument tampering after signing (ArgsHash mismatch) -> 403 scope_invalid!
	tampered := signMCPArgs(t, g, "codex", repoMain, false, "invoke_tool", map[string]any{"tool_id": "github.list", "arguments": map[string]any{}})
	tampered["tool_id"] = "github.mutate"
	_, err = codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: tampered})
	if err == nil || !strings.Contains(err.Error(), "scope_invalid") {
		t.Fatalf("tampered arguments not rejected with scope_invalid: %v", err)
	}

	// 1d. Agent mismatch (?agent=claude URL receiving token signed for codex) -> 403 scope_invalid!
	mismatch := signMCPArgs(t, g, "codex", repoMain, false, "discover_tools", map[string]any{"intent": "github list"})
	_, err = claudeSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: mismatch})
	if err == nil || !strings.Contains(err.Error(), "scope_invalid") {
		t.Fatalf("agent-mismatched token not rejected with scope_invalid: %v", err)
	}

	// 1e. Expired token -> 403 scope_invalid!
	expiredHash, _ := workspace.CanonicalArgsHash(map[string]any{"intent": "github"}, ctxField)
	expiredTok, err := workspace.SignToken(g.scopeKey, workspace.Claims{
		Channel:  buildinfo.Current().Channel,
		Agent:    "codex",
		Cwd:      repoMain,
		Op:       "discover_tools",
		ArgsHash: expiredHash,
		IAT:      time.Now().Add(-120 * time.Second).Unix(),
		EXP:      time.Now().Add(-60 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = codexSess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "discover_tools",
		Arguments: map[string]any{"intent": "github", ctxField: expiredTok},
	})
	if err == nil || !strings.Contains(err.Error(), "scope_invalid") {
		t.Fatalf("expired token not rejected with scope_invalid: %v", err)
	}

	// 1f. Raw HTTP POST /mcp verifies exact HTTP 403 status code on scope_invalid.
	rawRPC, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "discover_tools",
			"arguments": map[string]any{"intent": "github", ctxField: "v1.invalid.sig"},
		},
	})
	httpResp, err := http.Post(host.URL+"/mcp?agent=codex", "application/json", bytes.NewReader(rawRPC))
	if err != nil {
		t.Fatal(err)
	}
	_ = httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 on /mcp scope_invalid, got %d", httpResp.StatusCode)
	}

	// --- Check X-Gateway-Actor consistency on get_tool_schema ---
	observerSess := connectAgent("codex", map[string]string{"X-Gateway-Actor": "observer"})
	// github.get is developer-only; even if caller passes actor="developer" in args, X-Gateway-Actor="observer" must win and deny!
	_, err = observerSess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_tool_schema",
		Arguments: map[string]any{"tool_id": "github.get", "actor": "developer"},
	})
	if err == nil {
		t.Fatal("get_tool_schema ignored X-Gateway-Actor header")
	}

	// --- Bind repositories & configure precedence rules ---
	boundMain, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "mcp-gateway", Path: repoMain})
	if err != nil {
		t.Fatal(err)
	}
	boundEmpty, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "fresh-empty", Path: repoEmpty})
	if err != nil {
		t.Fatal(err)
	}
	boundDir, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "proj", Path: dirProj})
	if err != nil {
		t.Fatal(err)
	}

	// Configure precedence rules:
	// - (*, *) remains default [] (0 tools for unconfigured repos!)
	// - (*, claude) -> gitlab RO (global default for claude in unconfigured repos)
	// - (mcp-gateway, *) -> roster RO (repo default: whole-rule replaces (*, claude), so claude in mcp-gateway gets roster RO, NOT gitlab!)
	// - (mcp-gateway, codex) -> roster RW + github subset [github.list] (agent override in mcp-gateway)
	// - (fresh-empty, *) -> github RO
	// - (proj, *) -> gitlab RW
	for _, r := range []ScopeRule{
		{RepoKey: "*", AgentKey: "claude", Grants: []ServiceGrant{{ServerID: "gitlab", Access: "ro"}}},
		{RepoKey: boundMain.ID, AgentKey: "*", Grants: []ServiceGrant{{ServerID: "roster", Access: "ro"}}},
		{RepoKey: boundMain.ID, AgentKey: "codex", Grants: []ServiceGrant{
			{ServerID: "roster", Access: "rw"},
			{ServerID: "github", Access: "rw", ToolIDs: []string{"github.list"}},
		}},
		{RepoKey: boundEmpty.ID, AgentKey: "*", Grants: []ServiceGrant{{ServerID: "github", Access: "ro"}}},
		{RepoKey: boundDir.ID, AgentKey: "*", Grants: []ServiceGrant{{ServerID: "gitlab", Access: "rw"}}},
	} {
		if _, err := g.SaveScopeRule(r); err != nil {
			t.Fatal(err)
		}
	}

	// Enable enforcement!
	if err := g.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}

	// --- Invariant 2: Missing _gw_ctx when enforce == true MUST return 403 scope_required (NEVER fall back to global pool!) ---
	_, err = codexSess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "discover_tools",
		Arguments: map[string]any{"intent": "roster list"},
	})
	if err == nil || !strings.Contains(err.Error(), "scope_required") {
		t.Fatalf("missing _gw_ctx did not return scope_required: %v", err)
	}

	// --- Invariant 3: Ambiguous workspace when enforce == true MUST return 403 ambiguous_workspace ---
	ambigArgs := signMCPArgs(t, g, "cursor", "", true, "discover_tools", map[string]any{"intent": "roster list"})
	_, err = cursorSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: ambigArgs})
	if err == nil || !strings.Contains(err.Error(), "ambiguous_workspace") {
		t.Fatalf("ambiguous workspace did not return ambiguous_workspace: %v", err)
	}

	// --- Invariant 4: Worktree subdir automatically hits boundMain via shared CommonDir! ---
	// Codex in repoWTSub: gets roster RW (roster.mutate allowed!) + github.list ONLY (github.get & gitlab.list denied!).
	wtInvokeOK := signMCPArgs(t, g, "codex", repoWTSub, false, "invoke_tool", map[string]any{"tool_id": "roster.mutate", "arguments": map[string]any{}})
	if res, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: wtInvokeOK}); err != nil || res.IsError {
		t.Fatalf("codex in linked worktree subdir failed authorized roster.mutate: %v %+v", err, res)
	}
	wtSubsetDenied := signMCPArgs(t, g, "codex", repoWTSub, false, "get_tool_schema", map[string]any{"tool_id": "github.get"})
	if _, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_tool_schema", Arguments: wtSubsetDenied}); err == nil {
		t.Fatal("codex accessed github.get excluded by ToolIDs subset")
	}
	wtSubsetAllowed := signMCPArgs(t, g, "codex", repoWTSub, false, "get_tool_schema", map[string]any{"tool_id": "github.list"})
	if _, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_tool_schema", Arguments: wtSubsetAllowed}); err != nil {
		t.Fatalf("codex denied github.list included in ToolIDs subset: %v", err)
	}

	// Claude in repoMain: matches (mcp-gateway, *) -> roster RO (whole-rule replacement over (*, claude)!).
	// So gitlab.list is DENIED, roster.mutate (write) is DENIED, and roster.list (read-only) is ALLOWED!
	claudeGitlab := signMCPArgs(t, g, "claude", repoMain, false, "invoke_tool", map[string]any{"tool_id": "gitlab.list", "arguments": map[string]any{}})
	if _, err := claudeSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: claudeGitlab}); err == nil {
		t.Fatal("whole-rule replacement failed: claude inherited (*, claude) gitlab inside mcp-gateway")
	}
	claudeWrite := signMCPArgs(t, g, "claude", repoMain, false, "invoke_tool", map[string]any{"tool_id": "roster.mutate", "arguments": map[string]any{}})
	if _, err := claudeSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: claudeWrite}); err == nil {
		t.Fatal("RO grant allowed write tool roster.mutate")
	}
	claudeRead := signMCPArgs(t, g, "claude", repoMain, false, "invoke_tool", map[string]any{"tool_id": "roster.list", "arguments": map[string]any{}})
	if _, err := claudeSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: claudeRead}); err != nil {
		t.Fatalf("claude denied authorized RO tool roster.list: %v", err)
	}

	// --- Invariant 5: Evil clone with identical RemoteHint at different path MUST NOT match mcp-gateway! ---
	// For codex, unconfigured repoEvilClone falls through to (*, *) = [] -> 0 tools, and records in Observed!
	evilDiscover := signMCPArgs(t, g, "codex", repoEvilClone, false, "discover_tools", map[string]any{"intent": "roster list"})
	evilRes, err := codexSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: evilDiscover})
	if err != nil {
		t.Fatalf("discover in unconfigured repo returned error instead of 0-tool no_match: %v", err)
	}
	evilText := evilRes.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(evilText, `"eligible":0`) {
		t.Fatalf("evil clone with spoofed RemoteHint got tools! %s", evilText)
	}
	// Verify repoEvilClone was recorded in scope_observations!
	ov := g.ScopeOverview()
	if !slices.ContainsFunc(ov.Observed, func(o ObservedRepo) bool { return o.Root == repoEvilClone && o.LastAgent == "codex" }) {
		t.Fatalf("expected evil-clone in Observed, got %+v", ov.Observed)
	}

	// Meanwhile for claude in unconfigured repoEvilClone, (*, claude) -> gitlab RO applies!
	claudeUnconf := signMCPArgs(t, g, "claude", repoEvilClone, false, "invoke_tool", map[string]any{"tool_id": "gitlab.list", "arguments": map[string]any{}})
	if _, err := claudeSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: claudeUnconf}); err != nil {
		t.Fatalf("claude in unconfigured repo should match (*, claude) gitlab.list: %v", err)
	}

	// --- Invariant 6: Empty `git init` repo without origin & Dir segment-boundary (/proj != /proj-old) ---
	emptyRead := signMCPArgs(t, g, "cursor", repoEmpty, false, "invoke_tool", map[string]any{"tool_id": "github.list", "arguments": map[string]any{}})
	if _, err := cursorSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: emptyRead}); err != nil {
		t.Fatalf("empty git init repo failed to match CommonDir: %v", err)
	}
	projOK := signMCPArgs(t, g, "cursor", dirProj, false, "invoke_tool", map[string]any{"tool_id": "gitlab.mutate", "arguments": map[string]any{}})
	if _, err := cursorSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: projOK}); err != nil {
		t.Fatalf("/proj dir anchor failed: %v", err)
	}
	projOldDenied := signMCPArgs(t, g, "cursor", dirProjOld, false, "invoke_tool", map[string]any{"tool_id": "gitlab.mutate", "arguments": map[string]any{}})
	if _, err := cursorSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: projOldDenied}); err == nil {
		t.Fatal("/proj-old erroneously matched /proj dir anchor!")
	}

	// Regression: an unbound Git repo nested inside bound KindDir `dirProj` MUST NOT fall through
	// to inherit `dirProj`'s RootPaths grants!
	nestedUntrustedGit := initGitRepoForScopeTest(t, filepath.Join(dirProj, "nested-untrusted-git"), "", false)
	nestedDenied := signMCPArgs(t, g, "cursor", nestedUntrustedGit, false, "invoke_tool", map[string]any{"tool_id": "gitlab.mutate", "arguments": map[string]any{}})
	if _, err := cursorSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: nestedDenied}); err == nil {
		t.Fatal("unbound Git repo nested inside bound KindDir RootPaths inherited parent dir grants!")
	}

	// --- Verify Activity Logs contain detail.scope ---
	g.mu.RLock()
	events := slices.Clone(g.events)
	g.mu.RUnlock()
	foundDiscoverScope := false
	foundInvokeScope := false
	for _, ev := range events {
		var detail map[string]any
		if json.Unmarshal(ev.Detail, &detail) != nil {
			continue
		}
		sc, ok := detail["scope"].(map[string]any)
		if !ok {
			continue
		}
		if ev.Kind == "discover" && sc["status"] != "" {
			foundDiscoverScope = true
		}
		if ev.Kind == "invoke" && sc["repo_id"] == boundMain.ID && sc["agent"] == "codex" && sc["matched_rule"] == boundMain.ID+":codex" {
			foundInvokeScope = true
		}
	}
	if !foundDiscoverScope || !foundInvokeScope {
		t.Fatalf("missing detail.scope in activity events (discover=%v invoke=%v)", foundDiscoverScope, foundInvokeScope)
	}
}

func TestSingleServiceScopeSkipsHierarchicalSelectServices(t *testing.T) {
	g, _ := testGateway(t)
	g.routing = routingOptions{hierarchical: true, refine: false}
	registerMultiServiceUpstream(t, g)

	var serviceStageCalls atomic.Int32
	var toolStageCalls atomic.Int32
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input     string             `json:"input"`
			Questions []decisionQuestion `json:"questions"`
		}
		var input struct {
			Candidates []Capability `json:"candidates"`
			Services   []any        `json:"services"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_ = json.Unmarshal([]byte(payload.Input), &input)
		if len(input.Services) > 0 {
			serviceStageCalls.Add(1)
			probs := make([]float64, len(input.Services))
			for i := range probs {
				probs[i] = 0.9
			}
			respond(w, 200, decisionFixture("selected", probs...))
			return
		}
		toolStageCalls.Add(1)
		probs := make([]float64, len(input.Candidates))
		probs[0] = 0.95
		respond(w, 200, decisionFixture("selected", probs...))
	}))
	t.Cleanup(modelSrv.Close)

	settings, _ := g.Config()
	settings.BaseURL, settings.Model = modelSrv.URL, "gpt-6-luna"
	if err := g.SaveSettings(settings, "", false); err != nil {
		t.Fatal(err)
	}

	// 1. Unscoped (3 services: github, gitlab, roster) -> must invoke both selectServices (1) and selectTools (1).
	if _, err := g.Discover(context.Background(), DiscoverRequest{Intent: "list items"}); err != nil {
		t.Fatal(err)
	}
	if serviceStageCalls.Load() != 1 || toolStageCalls.Load() != 1 {
		t.Fatalf("expected 1 service call + 1 tool call unscoped, got service=%d tool=%d", serviceStageCalls.Load(), toolStageCalls.Load())
	}

	// 2. Scoped to single service (roster only) -> selectServices MUST be skipped (0 new calls)!
	repoDir := initGitRepoForScopeTest(t, filepath.Join(t.TempDir(), "single-svc"), "", false)
	repo, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "single-svc", Path: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveScopeRule(ScopeRule{
		RepoKey:  repo.ID,
		AgentKey: "*",
		Grants:   []ServiceGrant{{ServerID: "roster", Access: "ro"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}

	serviceStageCalls.Store(0)
	toolStageCalls.Store(0)

	simCtx, err := g.evaluateSimulatedScope(SimScope{RepoKey: repo.ID, AgentKey: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Discover(withRequestScope(context.Background(), simCtx), DiscoverRequest{Intent: "list items"})
	if err != nil {
		t.Fatal(err)
	}
	if serviceStageCalls.Load() != 0 || toolStageCalls.Load() != 1 {
		t.Fatalf("expected single-service scope to skip selectServices (0 calls), got service=%d tool=%d", serviceStageCalls.Load(), toolStageCalls.Load())
	}
	if res.Eligible != 2 { // roster.list + roster.get (RO excludes roster.mutate)
		t.Fatalf("expected 2 RO eligible tools in roster, got %d", res.Eligible)
	}

	// 3. Internal TestModel and Maintenance Acceptance MUST bypass repo scope even when enforce == true!
	useKeywordModel(t, g)
	if _, err := g.TestModel(context.Background(), "list github items"); err != nil {
		t.Fatalf("TestModel blocked by repo scope enforce: %v", err)
	}
	report := g.maintenance.evaluateAcceptance(context.Background(), MaintenanceRun{
		ServerID: "github",
		Server:   Server{ID: "github", Name: "GitHub", Kind: "http", Endpoint: "http://127.0.0.1:9", Enabled: true},
		Tools: []Tool{
			{ID: "github.list", ServerID: "github", Name: "list", Description: "List items in github", Tags: []string{"github"}, InputSchema: objectSchema(map[string]any{}), Enabled: true, ReadOnly: true, Actors: []string{"developer"}, Method: "GET", Path: "/list"},
		},
		AcceptanceInput: &AcceptanceSuite{
			ServerID: "github",
			Contracts: []ContractCase{
				{Name: "c1", ToolID: "github.list", Arguments: map[string]any{}, Exchanges: []HTTPExchange{{Method: "GET", Path: "/list", Status: 200, Response: json.RawMessage(`{"ok":true}`)}}, Expected: json.RawMessage(`{"ok":true}`)},
			},
			Routing: []RoutingCase{
				{Name: "r1", Intent: "github", Expected: []string{"github.list"}},
			},
		},
	}, t.TempDir())
	if !report.Passed {
		t.Fatalf("evaluateAcceptance failed under enforce==true: %+v", report)
	}
}

func TestSharedModeRESTNonAdminGuardSimScopeAndHTTPAPIs(t *testing.T) {
	g := sharedGateway(t)
	useKeywordModel(t, g)
	handler := g.Handler()

	doReq := func(method, path, bearer, scopeHdr string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, "http://localhost"+path, reader)
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if scopeHdr != "" {
			req.Header.Set("X-Gateway-Scope", scopeHdr)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Non-admin cannot access /api/scopes management endpoints.
	if rec := doReq("GET", "/api/scopes", writerTestToken, "", nil); rec.Code != 403 {
		t.Fatalf("non-admin accessed /api/scopes: %d", rec.Code)
	}

	// 2. Shared mode rejects server-side local `path` probing in PUT /api/scopes/repositories!
	repoDir := initGitRepoForScopeTest(t, filepath.Join(t.TempDir(), "shared-repo"), "", false)
	if rec := doReq("PUT", "/api/scopes/repositories", adminTestToken, "", map[string]any{"name": "bad", "path": repoDir}); rec.Code != 403 {
		t.Fatalf("shared mode accepted server-side path probe: %d %s", rec.Code, rec.Body.String())
	}
	// Rejects broad anchors /, $HOME, /tmp.
	if rec := doReq("PUT", "/api/scopes/repositories", adminTestToken, "", map[string]any{"name": "tmp", "root_paths": []string{"/tmp"}}); rec.Code != 400 {
		t.Fatalf("accepted /tmp root anchor: %d %s", rec.Code, rec.Body.String())
	}

	// 3. Admin binds repo via explicit git_common_dirs, sets RO rule, enables enforce.
	info, err := workspace.Identify(context.Background(), repoDir)
	if err != nil {
		t.Fatal(err)
	}
	recRepo := doReq("PUT", "/api/scopes/repositories", adminTestToken, "", map[string]any{
		"name":            "shared-repo",
		"git_common_dirs": []string{info.CommonDir},
		"remote_hint":     "github.com/org/shared-repo",
	})
	if recRepo.Code != 200 {
		t.Fatalf("PUT /api/scopes/repositories failed: %d %s", recRepo.Code, recRepo.Body.String())
	}
	var createdRepo Repository
	_ = json.Unmarshal(recRepo.Body.Bytes(), &createdRepo)

	recRule := doReq("PUT", "/api/scopes/rules", adminTestToken, "", ScopeRule{
		RepoKey:  createdRepo.ID,
		AgentKey: "*",
		Grants: []ServiceGrant{
			{ServerID: "service", Access: "ro"},
			{ServerID: "catalog", Access: "ro"}, // built-in demo server must be excluded from preview counts
		},
	})
	if recRule.Code != 200 {
		t.Fatalf("PUT /api/scopes/rules failed: %d %s", recRule.Code, recRule.Body.String())
	}

	// Verify POST /api/scopes/preview (excludes demo servers!) & GET /api/scopes/snippets & GET /api/bootstrap.
	recPreview := doReq("POST", "/api/scopes/preview", adminTestToken, "", map[string]any{"repo_key": createdRepo.ID, "actor": "owner"})
	if recPreview.Code != 200 {
		t.Fatalf("preview failed: %d %s", recPreview.Code, recPreview.Body.String())
	}
	var preview ScopePreviewResponse
	_ = json.Unmarshal(recPreview.Body.Bytes(), &preview)
	if len(preview.Agents) != 5 || preview.Agents[0].Agent != "codex" || preview.Agents[0].ServerCount != 1 || preview.Agents[0].ToolCount != 1 || preview.Agents[0].HasWrite {
		t.Fatalf("unexpected preview response (demo server should be excluded): %+v", preview)
	}

	recSnip := doReq("GET", "/api/scopes/snippets?agent=opencode", adminTestToken, "", nil)
	if recSnip.Code != 200 || !strings.Contains(recSnip.Body.String(), "ctx.session.get") {
		t.Fatalf("snippet endpoint failed: %d %s", recSnip.Code, recSnip.Body.String())
	}

	if rec := doReq("PUT", "/api/scopes/mode", adminTestToken, "", map[string]bool{"enforce": true}); rec.Code != 200 {
		t.Fatal(rec.Code)
	}

	recBoot := doReq("GET", "/api/bootstrap", adminTestToken, "", nil)
	if recBoot.Code != 200 || !strings.Contains(recBoot.Body.String(), `"scopes"`) || !strings.Contains(recBoot.Body.String(), `"enforce":true`) {
		t.Fatalf("bootstrap missing scopes overview: %d %s", recBoot.Code, recBoot.Body.String())
	}

	// 4. Non-admin writer WITHOUT X-Gateway-Scope MUST get 403 scope_required on ALL 4 invocation REST endpoints!
	for _, ep := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/discover", map[string]any{"intent": "read status"}},
		{"POST", "/api/invoke", map[string]any{"tool_id": "service.read", "arguments": map[string]any{}}},
		{"GET", "/api/catalog", nil},
		{"GET", "/api/tools/service.read/schema", nil},
	} {
		rec := doReq(ep.method, ep.path, writerTestToken, "", ep.body)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "scope_required") {
			t.Fatalf("non-admin %s %s bypassed enforce without X-Gateway-Scope: %d %s", ep.method, ep.path, rec.Code, rec.Body.String())
		}
	}

	// 5. Non-admin writer attempting to spoof sim_scope -> 403 scope_invalid!
	recSpoof := doReq("POST", "/api/invoke", writerTestToken, "", map[string]any{
		"tool_id":   "service.write",
		"arguments": map[string]any{},
		"sim_scope": map[string]string{"repo_key": ""},
	})
	if recSpoof.Code != 403 || !strings.Contains(recSpoof.Body.String(), "scope_invalid") {
		t.Fatalf("non-admin spoofed sim_scope: %d %s", recSpoof.Code, recSpoof.Body.String())
	}

	// 6. Non-admin writer WITH valid X-Gateway-Scope token for shared-repo (RO):
	//    - service.read succeeds (200)
	//    - service.write is blocked by RO scope (403), even though writer Principal has AllowWrite=true!
	signREST := func(op string, body map[string]any) string {
		h, err := workspace.CanonicalArgsHash(body, workspace.CurrentContextField())
		if err != nil {
			t.Fatal(err)
		}
		tok, err := workspace.SignToken(g.scopeKey, workspace.Claims{
			Channel:  buildinfo.Current().Channel,
			Agent:    "codex",
			Cwd:      repoDir,
			Op:       op,
			ArgsHash: h,
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	readBody := map[string]any{"tool_id": "service.read", "arguments": map[string]any{}}
	if rec := doReq("POST", "/api/invoke", writerTestToken, signREST("invoke_tool", readBody), readBody); rec.Code != 200 {
		t.Fatalf("non-admin with valid X-Gateway-Scope denied RO tool: %d %s", rec.Code, rec.Body.String())
	}
	writeBody := map[string]any{"tool_id": "service.write", "arguments": map[string]any{}}
	if rec := doReq("POST", "/api/invoke", writerTestToken, signREST("invoke_tool", writeBody), writeBody); rec.Code != 403 {
		t.Fatalf("non-admin writer bypassed RO repo scope on service.write: %d %s", rec.Code, rec.Body.String())
	}

	// 7. Admin sim_scope:
	//    - repo_key == "*" (unconfigured) -> 403 on service.read
	//    - repo_key == createdRepo.ID (RO) -> 200 on service.read, 403 on service.write
	//    - repo_key == "" (admin full view) -> 200 on service.write
	if rec := doReq("POST", "/api/invoke", adminTestToken, "", map[string]any{"tool_id": "service.read", "arguments": map[string]any{}, "sim_scope": map[string]string{"repo_key": "*", "agent_key": "codex"}}); rec.Code != 403 {
		t.Fatalf("admin sim_scope * should have 0 tools: %d", rec.Code)
	}
	if rec := doReq("POST", "/api/invoke", adminTestToken, "", map[string]any{"tool_id": "service.write", "arguments": map[string]any{}, "sim_scope": map[string]string{"repo_key": createdRepo.ID, "agent_key": "codex"}}); rec.Code != 403 {
		t.Fatalf("admin sim_scope RO should block write: %d", rec.Code)
	}
	if rec := doReq("POST", "/api/invoke", adminTestToken, "", map[string]any{"tool_id": "service.write", "arguments": map[string]any{}, "sim_scope": map[string]string{"repo_key": "", "agent_key": ""}}); rec.Code != 200 {
		t.Fatalf("admin sim_scope full view failed: %d %s", rec.Code, rec.Body.String())
	}

	// 8. Delete rule and repository via HTTP API.
	if rec := doReq("DELETE", fmt.Sprintf("/api/scopes/rules?repo_key=%s&agent_key=*", url.QueryEscape(createdRepo.ID)), adminTestToken, "", nil); rec.Code != 200 {
		t.Fatalf("DELETE rule failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doReq("DELETE", "/api/scopes/repositories/"+url.PathEscape(createdRepo.ID), adminTestToken, "", nil); rec.Code != 200 {
		t.Fatalf("DELETE repository failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEndToEndGatewayHookBinaryToGatewayMCP(t *testing.T) {
	workspace.ClearCache()
	g, _ := testGateway(t)
	useKeywordModel(t, g)
	registerMultiServiceUpstream(t, g)

	repoDir := initGitRepoForScopeTest(t, filepath.Join(t.TempDir(), "e2e-repo"), "git@github.com:DengNaichen/mcp-gateway.git", true)
	repo, err := g.SaveRepository(context.Background(), SaveRepositoryRequest{Name: "e2e-repo", Path: repoDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveScopeRule(ScopeRule{
		RepoKey:  repo.ID,
		AgentKey: "claude",
		Grants:   []ServiceGrant{{ServerID: "github", Access: "ro"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}

	hookBin := filepath.Join(t.TempDir(), "gateway-hook")
	buildCmd := exec.Command("go", "build", "-ldflags=-X gatewaylab/internal/buildinfo.channel="+buildinfo.Current().Channel, "-o", hookBin, "../../cmd/gateway-hook")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build gateway-hook: %v\n%s", err, out)
	}

	slug := buildinfo.Current().Slug
	hookStdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "mcp__" + slug + "__invoke_tool",
		"cwd":             repoDir,
		"session_id":      "e2e-sess-1",
		"tool_input": map[string]any{
			"tool_id":   "github.list",
			"arguments": map[string]any{},
		},
	})
	hookCmd := exec.Command(hookBin, "claude", "--data", g.dir)
	hookCmd.Stdin = bytes.NewReader(hookStdin)
	hookOut, err := hookCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run gateway-hook claude: %v\n%s", err, hookOut)
	}

	var hookEnvelope struct {
		HookSpecificOutput struct {
			UpdatedInput map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(hookOut, &hookEnvelope); err != nil {
		t.Fatalf("decode hook stdout: %v\n%s", err, hookOut)
	}

	host := httptest.NewServer(g.Handler())
	defer host.Close()

	cl := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "1"}, nil)
	sess, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp?agent=claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(sess)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_tool",
		Arguments: hookEnvelope.HookSpecificOutput.UpdatedInput,
	})
	if err != nil || res.IsError {
		t.Fatalf("end-to-end hook -> MCP call failed: %v %+v", err, res)
	}
}

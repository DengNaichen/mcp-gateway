package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanGitEnv(os.Environ())
	cmd.Env = append(cmd.Env,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
}

func TestIdentifyGitWorktreesSubdirsEmptyAndBare(t *testing.T) {
	ClearCache()
	base := t.TempDir()
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Empty git init repo (zero commits, no origin).
	emptyRepo := filepath.Join(realBase, "empty-repo")
	if err := os.MkdirAll(filepath.Join(emptyRepo, "nested", "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, emptyRepo, "init")

	infoEmpty, err := Identify(context.Background(), emptyRepo)
	if err != nil {
		t.Fatalf("Identify empty repo: %v", err)
	}
	if infoEmpty.Kind != KindGit || infoEmpty.CommonDir != filepath.Join(emptyRepo, ".git") || infoEmpty.Root != emptyRepo || infoEmpty.RemoteHint != "" || infoEmpty.Name != "empty-repo" {
		t.Fatalf("unexpected empty repo info: %+v", infoEmpty)
	}
	infoSub, err := Identify(context.Background(), filepath.Join(emptyRepo, "nested", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if infoSub.CommonDir != infoEmpty.CommonDir || infoSub.Root != emptyRepo {
		t.Fatalf("subdir did not resolve to same CommonDir/Root: %+v vs %+v", infoSub, infoEmpty)
	}

	// 2. Repo with commit, remote, symlinked path, and linked worktree.
	mainRepo := filepath.Join(realBase, "main-repo")
	if err := os.MkdirAll(mainRepo, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainRepo, "init")
	runGit(t, mainRepo, "remote", "add", "origin", "ssh://git@GitHub.COM:2222/DengNaichen/MCP-Gateway.git")
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "init")

	wtDir := filepath.Join(realBase, "wt-feature")
	runGit(t, mainRepo, "worktree", "add", "-b", "feat/test", wtDir)
	wtSub := filepath.Join(wtDir, "pkg", "sub")
	if err := os.MkdirAll(wtSub, 0755); err != nil {
		t.Fatal(err)
	}

	symlinkWT := filepath.Join(realBase, "wt-link")
	if err := os.Symlink(wtSub, symlinkWT); err != nil {
		t.Fatal(err)
	}

	// Poison GIT_DIR / GIT_WORK_TREE in process env to ensure Identify scrubs them.
	t.Setenv("GIT_DIR", filepath.Join(emptyRepo, ".git"))
	t.Setenv("GIT_WORK_TREE", emptyRepo)

	infoMain, err := Identify(context.Background(), mainRepo)
	if err != nil {
		t.Fatal(err)
	}
	infoWT, err := Identify(context.Background(), symlinkWT)
	if err != nil {
		t.Fatal(err)
	}
	if infoMain.CommonDir != filepath.Join(mainRepo, ".git") {
		t.Fatalf("unexpected main CommonDir: %s", infoMain.CommonDir)
	}
	if infoWT.CommonDir != infoMain.CommonDir || infoWT.Fingerprint != infoMain.Fingerprint {
		t.Fatalf("worktree did not share CommonDir/Fingerprint: main=%+v wt=%+v", infoMain, infoWT)
	}
	if infoWT.Root != wtDir {
		t.Fatalf("expected worktree root %s, got %s", wtDir, infoWT.Root)
	}
	if infoMain.RemoteHint != "github.com:2222/DengNaichen/MCP-Gateway" {
		t.Fatalf("unexpected RemoteHint: %q", infoMain.RemoteHint)
	}
	if infoWT.Name != "main-repo" {
		t.Fatalf("expected worktree repo name main-repo, got %q", infoWT.Name)
	}

	// 3. Bare repository.
	bareRepo := filepath.Join(realBase, "service-bare.git")
	runGit(t, realBase, "init", "--bare", bareRepo)
	infoBare, err := Identify(context.Background(), bareRepo)
	if err != nil {
		t.Fatalf("Identify bare repo: %v", err)
	}
	if infoBare.Kind != KindGit || infoBare.CommonDir != bareRepo || infoBare.Root != bareRepo || infoBare.Name != "service-bare" {
		t.Fatalf("unexpected bare repo info: %+v", infoBare)
	}

	// 4. Plain non-git directory.
	plainDir := filepath.Join(realBase, "plain-notes")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}
	infoPlain, err := Identify(context.Background(), plainDir)
	if err != nil {
		t.Fatal(err)
	}
	if infoPlain.Kind != KindDir || infoPlain.CommonDir != "" || infoPlain.Root != plainDir || infoPlain.Name != "plain-notes" {
		t.Fatalf("unexpected plain dir info: %+v", infoPlain)
	}
}

func TestIdentifyCacheTTL(t *testing.T) {
	ClearCache()
	now := time.Unix(1700000000, 0)
	identifyCacheMu.Lock()
	oldNow := identifyNow
	identifyNow = func() time.Time { return now }
	identifyCacheMu.Unlock()
	t.Cleanup(func() {
		identifyCacheMu.Lock()
		identifyNow = oldNow
		identifyCacheMu.Unlock()
		ClearCache()
	})

	dir := t.TempDir()
	realDir, _ := filepath.EvalSymlinks(dir)

	first, err := Identify(context.Background(), realDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != KindDir {
		t.Fatalf("expected dir before git init, got %+v", first)
	}

	// Convert dir into a git repo; within 30s TTL cache should still return cached KindDir.
	runGit(t, realDir, "init")
	now = now.Add(10 * time.Second)
	cached, err := Identify(context.Background(), realDir)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Kind != KindDir {
		t.Fatalf("expected cached KindDir within TTL, got %+v", cached)
	}

	// Advance past 30s TTL: must re-probe and detect KindGit.
	now = now.Add(25 * time.Second)
	refreshed, err := Identify(context.Background(), realDir)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Kind != KindGit {
		t.Fatalf("expected KindGit after TTL expiry, got %+v", refreshed)
	}
}

func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"git@GitHub.COM:DengNaichen/MCP-Gateway.git", "github.com/DengNaichen/MCP-Gateway"},
		{"https://GitHub.com/DengNaichen/mcp-gateway.git/", "github.com/DengNaichen/mcp-gateway"},
		{"ssh://git@GitLab.Company.COM:2222/Platform/CoreService.git", "gitlab.company.com:2222/Platform/CoreService"},
		{"https://token@git.internal:8443/Group/SubGroup/Repo", "git.internal:8443/Group/SubGroup/Repo"},
		{"file:///tmp/local-repo", ""},
		{"/Users/alice/local-repo", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeRemote(tc.raw); got != tc.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestHasPathPrefixAndValidateDirAnchor(t *testing.T) {
	if !HasPathPrefix("/repo", "/repo") {
		t.Fatal("exact path should match")
	}
	if !HasPathPrefix("/repo/sub/file", "/repo") {
		t.Fatal("child path should match")
	}
	if HasPathPrefix("/repo-old", "/repo") {
		t.Fatal("/repo-old must NOT match /repo")
	}
	if HasPathPrefix("/repo-old/sub", "/repo") {
		t.Fatal("/repo-old/sub must NOT match /repo")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, bad := range []string{"", "relative/path", "/", home, "/tmp", "/private/tmp", "/Users"} {
		if err := ValidateDirAnchor(bad); err == nil {
			t.Errorf("ValidateDirAnchor(%q) succeeded, expected rejection", bad)
		}
	}
	good := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(good, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDirAnchor(good); err != nil {
		t.Fatalf("ValidateDirAnchor(%q) unexpected error: %v", good, err)
	}
}

func TestScopeKeyTokensAndChannelContextFields(t *testing.T) {
	if got := ContextFieldForSlug("mcp-gateway"); got != "_gw_ctx" {
		t.Fatalf("stable field = %q", got)
	}
	if got := ContextFieldForSlug("mcp-gateway-dev"); got != "_gw_ctx_mcp_gateway_dev" {
		t.Fatalf("dev field = %q", got)
	}

	dataDir := filepath.Join(t.TempDir(), "data")
	if _, err := LoadScopeKey(dataDir); !errors.Is(err, ErrScopeKeyMissing) {
		t.Fatalf("expected ErrScopeKeyMissing, got %v", err)
	}

	key, err := EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ScopeKeyLength {
		t.Fatalf("unexpected key len %d", len(key))
	}
	st, err := os.Stat(ScopeKeyPath(dataDir))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("scope.key permissions = %v, err=%v", st.Mode().Perm(), err)
	}
	keyAgain, err := LoadScopeKey(dataDir)
	if err != nil || string(keyAgain) != string(key) {
		t.Fatal("LoadScopeKey did not return same key")
	}

	// Verify 2-channel hook-order independence (e.g. Stable hook runs 1st, Dev hook runs 2nd on Cursor preToolUse).
	stableKey, err := EnsureScopeKey(filepath.Join(t.TempDir(), "stable-data"))
	if err != nil {
		t.Fatal(err)
	}
	baseArgs := map[string]any{"intent": "check logs", "services": []string{"github"}}

	// Step 1: Stable hook signs baseArgs (before Dev hook has run) and injects _gw_ctx.
	stableSignHash, err := CanonicalArgsHash(baseArgs, "_gw_ctx")
	if err != nil {
		t.Fatal(err)
	}
	stableTok, err := SignToken(stableKey, Claims{
		Channel:  "stable",
		Agent:    "cursor",
		Cwd:      dataDir,
		Op:       "discover_tools",
		ArgsHash: stableSignHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterHook1 := map[string]any{
		"intent":   "check logs",
		"services": []string{"github"},
		"_gw_ctx":  stableTok,
	}

	// Step 2: Dev hook runs second, preserves _gw_ctx, signs, and injects _gw_ctx_mcp_gateway_dev.
	devSignHash, err := CanonicalArgsHash(afterHook1, "_gw_ctx_mcp_gateway_dev")
	if err != nil {
		t.Fatal(err)
	}
	devTok, err := SignToken(key, Claims{
		Channel:  "dev",
		Agent:    "cursor",
		Cwd:      dataDir,
		Op:       "discover_tools",
		ArgsHash: devSignHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	combinedJSON, _ := json.Marshal(map[string]any{
		"intent":                  "check logs",
		"services":                []string{"github"},
		"_gw_ctx":                 stableTok,
		"_gw_ctx_mcp_gateway_dev": devTok,
	})

	// Both Stable Gateway and Dev Gateway must verify their respective tokens from combinedJSON!
	gotStableHash, extractedStableTok, hasStable, err := CanonicalArgsHashJSON(combinedJSON, "_gw_ctx")
	if err != nil || !hasStable || extractedStableTok != stableTok {
		t.Fatalf("extract stable token from combined payload: %v", err)
	}
	if _, err := VerifyToken(stableKey, extractedStableTok, VerifyParams{
		Channel:    "stable",
		URLAgent:   "cursor",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   gotStableHash,
	}, time.Now()); err != nil {
		t.Fatalf("Stable Gateway rejected token after Dev hook ran second: %v", err)
	}

	hashDev, tokDev, hasDev, err := CanonicalArgsHashJSON(combinedJSON, "_gw_ctx_mcp_gateway_dev")
	if err != nil || !hasDev || tokDev != devTok {
		t.Fatalf("CanonicalArgsHashJSON dev failed: %v has=%v tok=%q", err, hasDev, tokDev)
	}
	if _, err := VerifyToken(key, tokDev, VerifyParams{
		Channel:    "dev",
		URLAgent:   "cursor",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   hashDev,
	}, time.Now()); err != nil {
		t.Fatalf("Dev Gateway rejected token from combined payload: %v", err)
	}

	stripped, err := StripContextFieldsJSON(combinedJSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stripped), "_gw_ctx") {
		t.Fatalf("StripContextFieldsJSON left context field: %s", stripped)
	}

	now := time.Now()
	tok, err := SignToken(key, Claims{
		Channel:  "dev",
		Agent:    "codex",
		Cwd:      dataDir,
		Op:       "discover_tools",
		ArgsHash: hashDev,
		SID:      "sess-1",
		IAT:      now.Unix(),
		EXP:      now.Add(60 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "v1.") || strings.ContainsAny(tok, " \t\n\"") {
		t.Fatalf("unexpected token format: %q", tok)
	}

	claims, err := VerifyToken(key, tok, VerifyParams{
		Channel:    "dev",
		URLAgent:   "codex",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   hashDev,
	}, now)
	if err != nil || claims.Agent != "codex" || claims.Cwd != dataDir {
		t.Fatalf("VerifyToken failed: %v %+v", err, claims)
	}

	// Tampered signature.
	if _, err := VerifyToken(key, tok+"x", VerifyParams{Channel: "dev", AllowedOps: []string{"discover_tools"}, ArgsHash: hashDev}, now); err == nil {
		t.Fatal("accepted tampered signature")
	}
	// Agent mismatch.
	if _, err := VerifyToken(key, tok, VerifyParams{Channel: "dev", URLAgent: "claude", AllowedOps: []string{"discover_tools"}, ArgsHash: hashDev}, now); err == nil {
		t.Fatal("accepted mismatched URL agent")
	}
	// ArgsHash mismatch.
	if _, err := VerifyToken(key, tok, VerifyParams{Channel: "dev", URLAgent: "codex", AllowedOps: []string{"discover_tools"}, ArgsHash: "deadbeef"}, now); err == nil {
		t.Fatal("accepted mismatched ArgsHash")
	}
	// Expired token.
	if _, err := VerifyToken(key, tok, VerifyParams{Channel: "dev", URLAgent: "codex", AllowedOps: []string{"discover_tools"}, ArgsHash: hashDev}, now.Add(61*time.Second)); err == nil {
		t.Fatal("accepted expired token")
	}
}

func TestBuildSnippetAllAgents(t *testing.T) {
	home := t.TempDir()
	for _, agent := range KnownAgents {
		spec, err := BuildSnippet(SnippetOptions{
			Agent:    agent,
			Endpoint: "http://127.0.0.1:8789/mcp",
			BinPath:  "/opt/gw/mcp-gateway-dev-hook",
			DataDir:  filepath.Join(home, "custom-data"),
			HomeDir:  home,
		})
		if err != nil {
			t.Fatalf("BuildSnippet(%s): %v", agent, err)
		}
		if !strings.Contains(spec.MCPURL, "agent="+agent) {
			t.Fatalf("%s MCPURL missing ?agent=%s: %s", agent, agent, spec.MCPURL)
		}
		if spec.ConfigPath == "" || spec.Snippet == "" {
			t.Fatalf("%s empty config_path or snippet: %+v", agent, spec)
		}
		switch agent {
		case "claude", "codex", "cursor":
			var parsed map[string]any
			if err := json.Unmarshal([]byte(spec.Snippet), &parsed); err != nil {
				t.Fatalf("%s snippet is not valid JSON: %v\n%s", agent, err, spec.Snippet)
			}
		case "opencode":
			if !strings.Contains(spec.Snippet, "ctx.session.get({ sessionID: event.sessionID }") || !strings.Contains(spec.Snippet, "session?.location?.directory") {
				t.Fatalf("opencode snippet missing session.location.directory lookup:\n%s", spec.Snippet)
			}
		case "pi":
			if !strings.Contains(spec.Snippet, `pi.on("tool_call"`) {
				t.Fatalf("pi snippet missing tool_call listener:\n%s", spec.Snippet)
			}
		}
	}
}

func TestNativePreToolMatcherPluginNamespaces(t *testing.T) {
	re := regexp.MustCompile(NativePreToolMatcher("mcp-gateway-dev"))
	for _, match := range []string{
		"mcp__mcp-gateway-dev__discover_tools",
		"mcp__mcp_gateway_dev__invoke_tool",
		"mcp__plugin_mcp-gateway-dev_mcp-gateway-dev__discover_tools",
		"mcp__plugin_mcp_gateway_dev_mcp_gateway_dev__get_tool_schema",
	} {
		if !re.MatchString(match) {
			t.Fatalf("expected matcher to match %q", match)
		}
	}
	for _, miss := range []string{
		"mcp__other__discover_tools",
		"mcp__plugin_other_other__discover_tools",
		"mcp__mcp-gateway__discover_tools",
	} {
		if re.MatchString(miss) {
			t.Fatalf("expected matcher NOT to match %q", miss)
		}
	}
}

// fuzzTokenKey is a fixed key so the corpus stays valid across runs.
var fuzzTokenKey = []byte("01234567890123456789012345678901")

func fuzzTokenNow() time.Time { return time.Unix(1_700_000_000, 0) }

func fuzzValidToken(tb testing.TB) string {
	tb.Helper()
	token, err := SignToken(fuzzTokenKey, Claims{Channel: "stable", Agent: "codex", Cwd: "/work/repo", Op: "discover_tools", ArgsHash: "abc", JTI: "jti-1", IAT: fuzzTokenNow().Unix(), EXP: fuzzTokenNow().Unix() + 60})
	if err != nil {
		tb.Fatal(err)
	}
	return token
}

// FuzzVerifyToken checks that VerifyToken never panics and never accepts any
// string other than the one token that was actually signed with the key.
func FuzzVerifyToken(f *testing.F) {
	valid := fuzzValidToken(f)
	f.Add(valid)
	parts := strings.Split(valid, ".")
	f.Add("")
	f.Add("v1..")
	f.Add("v1.e30.AAAA")
	f.Add(parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-1])
	f.Add(parts[0] + "." + parts[1] + "A." + parts[2])
	f.Add(parts[0] + "." + parts[1] + ".")
	f.Add("v2." + parts[1] + "." + parts[2])
	f.Add(valid + ".extra")
	f.Add(strings.Repeat("v1.", 100))
	expected := VerifyParams{Channel: "stable", URLAgent: "codex", AllowedOps: []string{"discover_tools"}, ArgsHash: "abc"}
	f.Fuzz(func(t *testing.T, token string) {
		claims, err := VerifyToken(fuzzTokenKey, token, expected, fuzzTokenNow())
		if err == nil && token != valid {
			t.Fatalf("forged token verified: %q -> %+v", token, claims)
		}
		if err != nil && !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("unexpected error class: %v", err)
		}
		if _, err := VerifyToken([]byte("short"), token, expected, fuzzTokenNow()); err == nil {
			t.Fatal("token verified with an invalid key")
		}
	})
}

// FuzzVerifyTokenMutation flips one byte of a valid token; the result must be
// rejected unless the mutation is a no-op.
func FuzzVerifyTokenMutation(f *testing.F) {
	valid := fuzzValidToken(f)
	f.Add(0, byte(1))
	f.Add(len(valid)-1, byte(1))
	f.Add(len(valid)/2, byte(0x80))
	f.Fuzz(func(t *testing.T, pos int, mask byte) {
		if pos < 0 || pos >= len(valid) || mask == 0 {
			return
		}
		b := []byte(valid)
		b[pos] ^= mask
		if _, err := VerifyToken(fuzzTokenKey, string(b), VerifyParams{Channel: "stable"}, fuzzTokenNow()); err == nil {
			t.Fatalf("mutated token verified (pos=%d mask=%#x): %q", pos, mask, b)
		}
	})
}

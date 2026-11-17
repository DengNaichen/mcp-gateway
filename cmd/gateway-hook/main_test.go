package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/workspace"
)

func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestClaudeAndCodexPreToolUse(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "gwdata")
	cwd := t.TempDir()
	ctxField := workspace.CurrentContextField()
	slug := buildinfo.Current().Slug

	// 1. Before scope.key exists: must fail open with exit 0 and empty output.
	eventJSON := `{"hook_event_name":"PreToolUse","tool_name":"mcp__` + slug + `__discover_tools","cwd":` + mustStrJSON(cwd) + `,"session_id":"s-123","tool_input":{"intent":"search logs"}}`
	var out, errBuf bytes.Buffer
	if code := runCLI([]string{"claude", "--data", dataDir}, strings.NewReader(eventJSON), &out, &errBuf); code != 0 || out.Len() != 0 {
		t.Fatalf("uninitialized key should exit 0 noop, got code=%d out=%q", code, out.String())
	}

	// 2. Initialize scope.key and test both hyphen and underscore tool names across claude & codex.
	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	underscoreSlug := strings.ReplaceAll(slug, "-", "_")
	for _, tc := range []struct {
		platform string
		toolName string
	}{
		{"claude", "mcp__" + slug + "__discover_tools"},
		{"claude", "mcp__plugin_" + slug + "_" + slug + "__discover_tools"},
		{"claude", "mcp__plugin_" + underscoreSlug + "_" + underscoreSlug + "__invoke_tool"},
		{"codex", "mcp__" + underscoreSlug + "__invoke_tool"},
	} {
		inputMap := map[string]any{
			"intent":  "find errors",
			ctxField:  "forged-by-caller",
			"_gw_ctx": "preserve-if-other-channel",
		}
		if strings.HasSuffix(tc.toolName, "invoke_tool") {
			inputMap = map[string]any{
				"tool_id":   "github.list_issues",
				"arguments": map[string]any{"limit": 10},
				ctxField:    "forged-by-caller",
				"_gw_ctx":   "preserve-if-other-channel",
			}
		}
		evBytes, _ := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       tc.toolName,
			"cwd":             cwd,
			"session_id":      "sess-abc",
			"tool_input":      inputMap,
		})
		out.Reset()
		errBuf.Reset()
		if code := runCLI([]string{tc.platform, "--data", dataDir}, bytes.NewReader(evBytes), &out, &errBuf); code != 0 {
			t.Fatalf("%s failed: code=%d stderr=%s", tc.platform, code, errBuf.String())
		}
		var resp struct {
			HookSpecificOutput struct {
				HookEventName      string         `json:"hookEventName"`
				PermissionDecision string         `json:"permissionDecision"`
				UpdatedInput       map[string]any `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("%s invalid output JSON: %v\n%s", tc.platform, err, out.String())
		}
		if resp.HookSpecificOutput.HookEventName != "PreToolUse" || resp.HookSpecificOutput.PermissionDecision != "allow" {
			t.Fatalf("%s unexpected envelope: %+v", tc.platform, resp)
		}
		tok, _ := resp.HookSpecificOutput.UpdatedInput[ctxField].(string)
		if tok == "" || tok == "forged-by-caller" {
			t.Fatalf("%s did not overwrite %s: %v", tc.platform, ctxField, resp.HookSpecificOutput.UpdatedInput)
		}
		if ctxField != "_gw_ctx" && resp.HookSpecificOutput.UpdatedInput["_gw_ctx"] != "preserve-if-other-channel" {
			t.Fatalf("stripped other channel's _gw_ctx field: %v", resp.HookSpecificOutput.UpdatedInput)
		}

		updatedBytes, _ := json.Marshal(resp.HookSpecificOutput.UpdatedInput)
		argsHash, extractedTok, has, err := workspace.CanonicalArgsHashJSON(updatedBytes, ctxField)
		if err != nil || !has || extractedTok != tok {
			t.Fatalf("CanonicalArgsHashJSON failed: %v", err)
		}
		wantOp := "discover_tools"
		if strings.HasSuffix(tc.toolName, "invoke_tool") {
			wantOp = "invoke_tool"
		}
		claims, err := workspace.VerifyToken(key, tok, workspace.VerifyParams{
			Channel:    buildinfo.Current().Channel,
			URLAgent:   tc.platform,
			AllowedOps: []string{wantOp},
			ArgsHash:   argsHash,
		}, time.Now())
		if err != nil || claims.Cwd != cwd || claims.SID != "sess-abc" || claims.Ambiguous {
			t.Fatalf("VerifyToken failed: %v %+v", err, claims)
		}
	}

	// Unrelated plugin MCP tools must noop even when sharing meta-tool suffix.
	unrelatedEv, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "mcp__plugin_other_other__discover_tools",
		"cwd":             cwd,
		"session_id":      "sess-abc",
		"tool_input":      map[string]any{"intent": "find errors"},
	})
	out.Reset()
	errBuf.Reset()
	if code := runCLI([]string{"claude", "--data", dataDir}, bytes.NewReader(unrelatedEv), &out, &errBuf); code != 0 || out.Len() != 0 {
		t.Fatalf("unrelated plugin tool should noop, got code=%d out=%q", code, out.String())
	}
}

func TestCursorShapeGateAndWorkspaceResolution(t *testing.T) {
	workspace.ClearCache()
	dataDir := filepath.Join(t.TempDir(), "gwdata")
	ctxField := workspace.CurrentContextField()

	// Uninitialized key -> Cursor must receive {}\n and exit 0.
	var out, errBuf bytes.Buffer
	code := runCLI([]string{"cursor", "--data", dataDir}, strings.NewReader(`{"hook_event_name":"preToolUse","tool_name":"MCP:discover_tools","workspace_roots":["/tmp"],"tool_input":{"intent":"hi"}}`), &out, &errBuf)
	if code != 0 || out.String() != "{}\n" {
		t.Fatalf("cursor noop without key: code=%d out=%q", code, out.String())
	}

	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Third-party identically-named MCP:discover_tools with foreign keys -> {}\n noop!
	out.Reset()
	thirdParty := `{"hook_event_name":"preToolUse","tool_name":"MCP:discover_tools","workspace_roots":["/tmp"],"tool_input":{"query":"search third party"}}`
	if code := runCLI([]string{"cursor", "--data", dataDir}, strings.NewReader(thirdParty), &out, &errBuf); code != 0 || out.String() != "{}\n" {
		t.Fatalf("cursor shape gate failed to noop third-party tool: code=%d out=%q", code, out.String())
	}

	// 2. Multi-root resolving to same Git CommonDir (main repo + linked worktree) -> Ambiguous:false!
	base := t.TempDir()
	realBase, _ := filepath.EvalSymlinks(base)
	repoA := filepath.Join(realBase, "repo-a")
	initTestRepo(t, repoA)
	wtA := filepath.Join(realBase, "repo-a-wt")
	cmd := exec.Command("git", "-C", repoA, "worktree", "add", "-b", "wt", wtA)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, b)
	}

	sameRepoEvent, _ := json.Marshal(map[string]any{
		"hook_event_name": "preToolUse",
		"tool_name":       "MCP:get_tool_schema",
		"conversation_id": "conv-1",
		"workspace_roots": []string{repoA, wtA},
		"tool_input":      map[string]any{"tool_id": "roster.get_task"},
	})
	out.Reset()
	if code := runCLI([]string{"cursor", "--data", dataDir}, bytes.NewReader(sameRepoEvent), &out, &errBuf); code != 0 {
		t.Fatal(code)
	}
	var cursorResp struct {
		Permission   string         `json:"permission"`
		UpdatedInput map[string]any `json:"updated_input"`
	}
	if err := json.Unmarshal(out.Bytes(), &cursorResp); err != nil {
		t.Fatalf("decode cursor output: %v\n%s", err, out.String())
	}
	tok, _ := cursorResp.UpdatedInput[ctxField].(string)
	updatedRaw, _ := json.Marshal(cursorResp.UpdatedInput)
	argsHash, _, _, _ := workspace.CanonicalArgsHashJSON(updatedRaw, ctxField)
	claims, err := workspace.VerifyToken(key, tok, workspace.VerifyParams{
		URLAgent:   "cursor",
		AllowedOps: []string{"get_tool_schema"},
		ArgsHash:   argsHash,
	}, time.Now())
	if err != nil || claims.Ambiguous || claims.Cwd != repoA || claims.SID != "conv-1" {
		t.Fatalf("expected non-ambiguous same-CommonDir claims: err=%v claims=%+v", err, claims)
	}

	// 3. Multi-root across DIFFERENT repos without disambiguating cwd -> Ambiguous:true!
	repoB := filepath.Join(realBase, "repo-b")
	initTestRepo(t, repoB)
	ambigEvent, _ := json.Marshal(map[string]any{
		"hook_event_name": "preToolUse",
		"tool_name":       "MCP:discover_tools",
		"workspace_roots": []string{repoA, repoB},
		"tool_input":      map[string]any{"intent": "list tasks"},
	})
	out.Reset()
	if code := runCLI([]string{"cursor", "--data", dataDir}, bytes.NewReader(ambigEvent), &out, &errBuf); code != 0 {
		t.Fatal(code)
	}
	cursorResp.UpdatedInput = nil
	if err := json.Unmarshal(out.Bytes(), &cursorResp); err != nil {
		t.Fatal(err)
	}
	tokAmbig, _ := cursorResp.UpdatedInput[ctxField].(string)
	ambigRaw, _ := json.Marshal(cursorResp.UpdatedInput)
	ambigHash, _, _, _ := workspace.CanonicalArgsHashJSON(ambigRaw, ctxField)
	claimsAmbig, err := workspace.VerifyToken(key, tokAmbig, workspace.VerifyParams{
		URLAgent:   "cursor",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   ambigHash,
	}, time.Now())
	if err != nil || !claimsAmbig.Ambiguous || claimsAmbig.Cwd != "" {
		t.Fatalf("expected Ambiguous:true for multi-repo roots: err=%v claims=%+v", err, claimsAmbig)
	}
}

func TestSignAndSnippetSubcommands(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "gwdata")
	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	ctxField := workspace.CurrentContextField()

	var out, errBuf bytes.Buffer
	code := runCLI([]string{"sign", "--agent", "opencode", "--cwd", cwd, "--op", "discover_tools", "--sid", "oc-9", "--data", dataDir}, strings.NewReader(`{"intent":"deploy check"}`), &out, &errBuf)
	if code != 0 {
		t.Fatalf("sign failed: code=%d stderr=%s", code, errBuf.String())
	}
	hash, tok, has, err := workspace.CanonicalArgsHashJSON(out.Bytes(), ctxField)
	if err != nil || !has {
		t.Fatalf("parse signed output: %v", err)
	}
	claims, err := workspace.VerifyToken(key, tok, workspace.VerifyParams{
		URLAgent:   "opencode",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   hash,
	}, time.Now())
	if err != nil || claims.Cwd != cwd || claims.SID != "oc-9" {
		t.Fatalf("VerifyToken sign output: %v %+v", err, claims)
	}

	for _, agent := range []string{"claude", "codex", "cursor", "opencode", "pi"} {
		out.Reset()
		errBuf.Reset()
		code := runCLI([]string{"snippet", agent, "--endpoint", "http://127.0.0.1:8788/mcp", "--bin", "/usr/local/bin/gateway-hook", "--data", dataDir}, strings.NewReader(""), &out, &errBuf)
		if code != 0 || out.Len() == 0 {
			t.Fatalf("snippet %s failed: code=%d err=%s", agent, code, errBuf.String())
		}
	}
}

func mustStrJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/workspace"
)

const hookStdinLimit = 1 << 20

func main() {
	code := runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	sub := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(args[0])), "--")
	rest := args[1:]

	switch sub {
	case "claude", "codex", "cursor":
		fs := flag.NewFlagSet("gateway-hook "+sub, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dataDir := fs.String("data", "", "Gateway data directory")
		if err := fs.Parse(rest); err != nil {
			_ = emitHostNoop(sub, stdout)
			return 0
		}
		// Host hooks fail open on internal/missing-key errors so the host never hangs;
		// when Gateway enforce==true, missing _gw_ctx is deterministically rejected with 403 scope_required.
		if err := handleHostHook(context.Background(), sub, *dataDir, stdin, stdout); err != nil {
			_ = emitHostNoop(sub, stdout)
		}
		return 0

	case "sign":
		fs := flag.NewFlagSet("gateway-hook sign", flag.ContinueOnError)
		fs.SetOutput(stderr)
		agent := fs.String("agent", "", "Coding agent id (opencode|pi)")
		cwd := fs.String("cwd", "", "Session working directory")
		op := fs.String("op", "", "Gateway meta-tool name")
		sid := fs.String("sid", "", "Optional session identifier")
		dataDir := fs.String("data", "", "Gateway data directory")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if err := handleSign(*agent, *cwd, *op, *sid, *dataDir, stdin, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0

	case "snippet":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			_, _ = fmt.Fprintln(stderr, "usage: gateway-hook snippet <claude|codex|cursor|opencode|pi> [--endpoint <url>] [--bin <path>] [--data <dir>]")
			return 2
		}
		targetAgent := strings.ToLower(strings.TrimSpace(rest[0]))
		fs := flag.NewFlagSet("gateway-hook snippet", flag.ContinueOnError)
		fs.SetOutput(stderr)
		endpoint := fs.String("endpoint", "", "Gateway MCP URL")
		binPath := fs.String("bin", "", "Hook binary path")
		dataDir := fs.String("data", "", "Gateway data directory")
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		spec, err := workspace.BuildSnippet(workspace.SnippetOptions{
			Agent:    targetAgent,
			Endpoint: *endpoint,
			BinPath:  *binPath,
			DataDir:  *dataDir,
		})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		out := spec.Snippet
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		_, _ = io.WriteString(stdout, out)
		return 0

	case "help", "-h", "version":
		if sub == "version" {
			_, _ = fmt.Fprintf(stdout, "%s %s (%s)\n", buildinfo.Current().Slug+"-hook", buildinfo.Current().Version, buildinfo.Current().Channel)
			return 0
		}
		printUsage(stdout)
		return 0

	default:
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage:")
	_, _ = fmt.Fprintln(w, "  gateway-hook <claude|codex|cursor> [--data <dir>]")
	_, _ = fmt.Fprintln(w, "  gateway-hook sign --agent <opencode|pi> --cwd <dir> --op <tool> [--sid <id>] [--data <dir>]")
	_, _ = fmt.Fprintln(w, "  gateway-hook snippet <claude|codex|cursor|opencode|pi> [--endpoint <url>] [--bin <path>] [--data <dir>]")
}

func emitHostNoop(platform string, stdout io.Writer) error {
	if platform == "cursor" {
		_, err := io.WriteString(stdout, "{}\n")
		return err
	}
	return nil
}

func readBoundedJSONObject(r io.Reader) (map[string]any, []byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, hookStdinLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > hookStdinLimit {
		return nil, nil, errors.New("hook input exceeds 1 MiB limit")
	}
	if !utf8.Valid(raw) {
		return nil, nil, errors.New("hook input is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil, raw, err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, raw, errors.New("trailing JSON data in hook input")
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return nil, raw, errors.New("hook input must be a JSON object")
	}
	return obj, raw, nil
}

func handleHostHook(ctx context.Context, platform, explicitDataDir string, stdin io.Reader, stdout io.Writer) error {
	event, _, err := readBoundedJSONObject(stdin)
	if err != nil {
		return emitHostNoop(platform, stdout)
	}

	eventName := firstString(event, "hook_event_name", "hookEventName")
	toolName := firstString(event, "tool_name", "toolName")
	inputObj, hasInput := firstObject(event, "tool_input", "toolInput")
	if !hasInput {
		return emitHostNoop(platform, stdout)
	}

	var (
		op        string
		cwd       string
		ambiguous bool
		sid       string
	)

	switch platform {
	case "claude", "codex":
		if eventName != "" && eventName != "PreToolUse" {
			return emitHostNoop(platform, stdout)
		}
		matchedOp, ok := matchNativeMetaTool(toolName)
		if !ok {
			return emitHostNoop(platform, stdout)
		}
		op = matchedOp
		cwd = strings.TrimSpace(firstString(event, "cwd"))
		if cwd == "" || !filepath.IsAbs(cwd) {
			return emitHostNoop(platform, stdout)
		}
		sid = sanitizeSID(firstString(event, "session_id", "sessionId"))

	case "cursor":
		if eventName != "" && eventName != "preToolUse" {
			return emitHostNoop(platform, stdout)
		}
		matchedOp, ok := matchCursorMetaTool(toolName)
		if !ok {
			return emitHostNoop(platform, stdout)
		}
		if !passesCursorInputShapeGate(matchedOp, inputObj) {
			return emitHostNoop(platform, stdout)
		}
		op = matchedOp
		cwd, ambiguous = resolveCursorWorkspace(ctx, event)
		sid = sanitizeSID(firstString(event, "conversation_id", "session_id"))

	default:
		return emitHostNoop(platform, stdout)
	}

	key, err := workspace.LoadScopeKey(explicitDataDir)
	if err != nil {
		// Key not yet initialized: exit 0 with noop output so host is never blocked.
		return emitHostNoop(platform, stdout)
	}

	updated, err := injectSignedToken(key, platform, cwd, ambiguous, op, sid, inputObj)
	if err != nil {
		return emitHostNoop(platform, stdout)
	}

	if platform == "cursor" {
		return writeCompactJSONLine(stdout, map[string]any{
			"permission":    "allow",
			"updated_input": updated,
		})
	}
	return writeCompactJSONLine(stdout, map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"updatedInput":       updated,
		},
	})
}

func handleSign(agent, cwd, opFlag, sid, explicitDataDir string, stdin io.Reader, stdout io.Writer) error {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if !workspace.IsKnownAgent(agent) {
		return fmt.Errorf("invalid --agent %q (expected opencode or pi)", agent)
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return errors.New("--cwd must be a non-empty absolute path")
	}
	op, ok := normalizeMetaToolOp(opFlag)
	inputObj, rawBytes, err := readBoundedJSONObject(stdin)
	if err != nil {
		return fmt.Errorf("invalid tool_input JSON on stdin: %w", err)
	}
	if !ok {
		// Unrelated tool: pass input through untouched.
		return writeCompactJSONLine(stdout, inputObj)
	}

	key, err := workspace.LoadScopeKey(explicitDataDir)
	if err != nil {
		// Key not yet initialized: fail open without mutating input so host never breaks.
		trimmed := bytes.TrimSpace(rawBytes)
		if len(trimmed) > 0 {
			_, werr := stdout.Write(append(trimmed, '\n'))
			return werr
		}
		return writeCompactJSONLine(stdout, inputObj)
	}

	updated, err := injectSignedToken(key, agent, cwd, false, op, sanitizeSID(sid), inputObj)
	if err != nil {
		return err
	}
	return writeCompactJSONLine(stdout, updated)
}

func injectSignedToken(key []byte, agent, cwd string, ambiguous bool, op, sid string, input map[string]any) (map[string]any, error) {
	ctxField := workspace.CurrentContextField()
	updated := make(map[string]any, len(input)+1)
	for k, v := range input {
		if k == ctxField {
			continue // Strip ONLY the current channel's _gw_ctx* field.
		}
		updated[k] = v
	}

	argsHash, err := workspace.CanonicalArgsHash(updated, ctxField)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	tok, err := workspace.SignToken(key, workspace.Claims{
		V:         workspace.TokenVersion,
		Channel:   buildinfo.Current().Channel,
		Agent:     agent,
		Cwd:       cwd,
		Ambiguous: ambiguous,
		Op:        op,
		ArgsHash:  argsHash,
		SID:       sid,
		IAT:       now,
		EXP:       now + int64(workspace.TokenTTL/time.Second),
	})
	if err != nil {
		return nil, err
	}
	updated[ctxField] = tok
	return updated, nil
}

func matchNativeMetaTool(toolName string) (string, bool) {
	re, err := regexp.Compile(workspace.NativePreToolMatcher(buildinfo.Current().Slug))
	if err != nil {
		return "", false
	}
	m := re.FindStringSubmatch(strings.TrimSpace(toolName))
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

var cursorMetaToolRe = regexp.MustCompile(workspace.CursorPreToolMatcher())

func matchCursorMetaTool(toolName string) (string, bool) {
	m := cursorMetaToolRe.FindStringSubmatch(strings.TrimSpace(toolName))
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

func normalizeMetaToolOp(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if workspace.IsMetaTool(s) {
		return s, true
	}
	if op, ok := matchNativeMetaTool(s); ok {
		return op, true
	}
	if op, ok := matchCursorMetaTool(s); ok {
		return op, true
	}
	re, err := regexp.Compile("^(?:mcp__)?" + workspace.SlugRegex(buildinfo.Current().Slug) + "[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$")
	if err == nil {
		if m := re.FindStringSubmatch(s); len(m) == 2 {
			return m[1], true
		}
	}
	return "", false
}

// passesCursorInputShapeGate ensures Cursor's prefix-less MCP:<tool> hook only
// injects _gw_ctx* when the input strictly conforms to Gateway's allowed & required
// top-level keys for that meta-tool, leaving identically-named third-party tools untouched.
func passesCursorInputShapeGate(op string, input map[string]any) bool {
	if input == nil {
		return false
	}
	var allowed map[string]bool
	switch op {
	case "discover_tools":
		intent, ok := input["intent"].(string)
		if !ok || intent == "" {
			return false
		}
		allowed = map[string]bool{
			"intent":       true,
			"context":      true,
			"actor":        true,
			"services":     true,
			"summary_only": true,
			"include_demo": true,
		}
	case "get_tool_schema":
		toolID, ok := input["tool_id"].(string)
		if !ok || toolID == "" {
			return false
		}
		allowed = map[string]bool{
			"tool_id": true,
			"actor":   true,
		}
	case "invoke_tool":
		toolID, ok := input["tool_id"].(string)
		if !ok || toolID == "" {
			return false
		}
		if _, argsOK := input["arguments"].(map[string]any); !argsOK {
			return false
		}
		allowed = map[string]bool{
			"tool_id":   true,
			"arguments": true,
			"fields":    true,
			"actor":     true,
		}
	default:
		return false
	}

	for key := range input {
		if workspace.IsReservedContextField(key) {
			continue
		}
		if !allowed[key] {
			return false
		}
	}
	return true
}

func resolveCursorWorkspace(ctx context.Context, event map[string]any) (cwd string, ambiguous bool) {
	rawCwd := strings.TrimSpace(firstString(event, "cwd"))
	if rawCwd != "" && !filepath.IsAbs(rawCwd) {
		return "", true
	}

	roots, rootsValid := extractWorkspaceRoots(event)
	if !rootsValid {
		return "", true
	}

	if len(roots) == 0 {
		if rawCwd != "" {
			return rawCwd, false
		}
		return "", true
	}

	if len(roots) == 1 {
		r0 := roots[0]
		if rawCwd == "" {
			return r0, false
		}
		if pathWithinOrSameRepo(ctx, rawCwd, r0) {
			return rawCwd, false
		}
		return "", true
	}

	// Multi-root workspace:
	// 1. If explicit cwd falls within one of workspace_roots, use cwd.
	if rawCwd != "" {
		evalCwd := evalOrClean(rawCwd)
		matchedRoots := 0
		for _, r := range roots {
			if workspace.HasPathPrefix(evalCwd, evalOrClean(r)) {
				matchedRoots++
			}
		}
		if matchedRoots == 1 {
			return rawCwd, false
		}
		if matchedRoots > 1 {
			// Nested workspace roots: cwd itself resolves to the innermost repo/dir.
			return rawCwd, false
		}
	}

	// 2. Check if all workspace_roots resolve to the exact same Git CommonDir
	// (e.g. main checkout + linked git worktree opened together).
	var sharedCommon string
	for i, r := range roots {
		info, err := workspace.Identify(ctx, r)
		if err != nil || info.Kind != workspace.KindGit || info.CommonDir == "" {
			return "", true
		}
		if i == 0 {
			sharedCommon = info.CommonDir
		} else if info.CommonDir != sharedCommon {
			return "", true
		}
	}
	if sharedCommon != "" {
		return roots[0], false
	}
	return "", true
}

func pathWithinOrSameRepo(ctx context.Context, cwd, root string) bool {
	if workspace.HasPathPrefix(evalOrClean(cwd), evalOrClean(root)) {
		return true
	}
	cInfo, err1 := workspace.Identify(ctx, cwd)
	rInfo, err2 := workspace.Identify(ctx, root)
	if err1 == nil && err2 == nil && cInfo.Kind == workspace.KindGit && rInfo.Kind == workspace.KindGit && cInfo.CommonDir != "" && cInfo.CommonDir == rInfo.CommonDir {
		return true
	}
	return false
}

func extractWorkspaceRoots(event map[string]any) ([]string, bool) {
	rawVal, exists := event["workspace_roots"]
	if !exists || rawVal == nil {
		return nil, true
	}
	arr, ok := rawVal.([]any)
	if !ok {
		return nil, false
	}
	var out []string
	for _, elem := range arr {
		s, ok := elem.(string)
		s = strings.TrimSpace(s)
		if !ok || s == "" || !filepath.IsAbs(s) {
			return nil, false
		}
		evalS := evalOrClean(s)
		dup := false
		for _, existing := range out {
			if evalOrClean(existing) == evalS {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out, true
}

func evalOrClean(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

func firstString(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k].(string); ok {
			return v
		}
	}
	return ""
}

func firstObject(obj map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		if v, ok := obj[k].(map[string]any); ok {
			return v, true
		}
	}
	return nil, false
}

func sanitizeSID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 256 {
		return s[:256]
	}
	return s
}

func writeCompactJSONLine(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

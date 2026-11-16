package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gatewaylab/internal/buildinfo"
)

var MetaToolNames = []string{"discover_tools", "get_tool_schema", "invoke_tool"}

func IsMetaTool(name string) bool {
	return slices.Contains(MetaToolNames, name)
}

// SlugRegex returns a regular-expression fragment matching both hyphen and
// underscore normalizations of slug (e.g. "mcp[-_]gateway[-_]dev").
func SlugRegex(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = buildinfo.Current().Slug
	}
	parts := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return regexp.QuoteMeta(slug)
	}
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = regexp.QuoteMeta(p)
	}
	return strings.Join(escaped, "[-_]")
}

// NativePreToolMatcher returns the PreToolUse regex for Claude Code and Codex CLI.
func NativePreToolMatcher(slug string) string {
	return "^mcp__" + SlugRegex(slug) + "__(discover_tools|get_tool_schema|invoke_tool)$"
}

// CursorPreToolMatcher returns the preToolUse regex for Cursor.
func CursorPreToolMatcher() string {
	return "^MCP:(discover_tools|get_tool_schema|invoke_tool)$"
}

// DefaultHookBinPath returns ~/.<slug>/bin/<slug>-hook.
func DefaultHookBinPath(homeDir string) string {
	slug := buildinfo.Current().Slug
	if strings.TrimSpace(homeDir) == "" {
		if h, err := resolveHomeDir(); err == nil {
			homeDir = h
		}
	}
	if homeDir == "" {
		return filepath.Join("bin", "gateway-hook")
	}
	return filepath.Join(homeDir, "."+slug, "bin", slug+"-hook")
}

// FindBuiltHookBinary locates a compiled gateway-hook binary next to the current
// executable or in local workspace ./bin directories.
func FindBuiltHookBinary() string {
	slug := buildinfo.Current().Slug
	ch := buildinfo.Current().Channel
	var candidates []string

	if exe, err := os.Executable(); err == nil && exe != "" {
		exeDir := filepath.Dir(exe)
		// If this process IS gateway-hook itself, prefer it directly.
		base := filepath.Base(exe)
		if base == "gateway-hook" || base == slug+"-hook" {
			candidates = append(candidates, exe)
		}
		candidates = append(candidates,
			filepath.Join(exeDir, slug+"-hook"),
			filepath.Join(exeDir, "gateway-hook"),
		)
	}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		candidates = append(candidates,
			filepath.Join(wd, "bin", "gateway-hook"),
			filepath.Join(wd, "bin", ch, "gateway-hook"),
			filepath.Join(wd, "bin", slug+"-hook"),
		)
	}

	for _, c := range candidates {
		if isExecutableFile(c) {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

// SyncHookBinary copies the compiled gateway-hook binary to ~/.<slug>/bin/<slug>-hook
// (0755) if a built binary is available and differs from the installed target.
func SyncHookBinary(homeDir string) (string, error) {
	if strings.TrimSpace(homeDir) == "" {
		var err error
		homeDir, err = resolveHomeDir()
		if err != nil {
			return "", err
		}
	}
	target := DefaultHookBinPath(homeDir)
	src := FindBuiltHookBinary()
	if src == "" {
		return target, nil
	}
	if sameFilePath(src, target) || filesHaveSameSHA256(src, target) {
		_ = os.Chmod(target, 0755)
		return target, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return target, fmt.Errorf("read hook binary %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return target, fmt.Errorf("create hook bin dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".gateway-hook-*")
	if err != nil {
		return target, fmt.Errorf("create temp hook binary: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return target, err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return target, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return target, err
	}
	if err := tmp.Close(); err != nil {
		return target, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return target, fmt.Errorf("install hook binary: %w", err)
	}
	return target, nil
}

// ResolveHookBinary returns the best available hook binary path. When dataDir is
// within homeDir, it opportunistically syncs a built binary into ~/.<slug>/bin/<slug>-hook;
// otherwise (e.g. isolated unit tests using temp dataDir) it avoids mutating $HOME.
func ResolveHookBinary(homeDir, dataDir string) string {
	if strings.TrimSpace(homeDir) == "" {
		homeDir, _ = resolveHomeDir()
	}
	if homeDir != "" && dataDir != "" {
		if absData, err1 := filepath.Abs(dataDir); err1 == nil {
			if absHome, err2 := filepath.Abs(homeDir); err2 == nil && HasPathPrefix(absData, absHome) {
				if synced, err := SyncHookBinary(homeDir); err == nil && synced != "" {
					return synced
				}
			}
		}
	}
	installed := DefaultHookBinPath(homeDir)
	if isExecutableFile(installed) {
		return installed
	}
	if built := FindBuiltHookBinary(); built != "" {
		return built
	}
	return installed
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return info.Mode()&0111 != 0
}

func sameFilePath(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func filesHaveSameSHA256(a, b string) bool {
	da, ea := os.ReadFile(a)
	if ea != nil {
		return false
	}
	db, eb := os.ReadFile(b)
	if eb != nil || len(da) != len(db) {
		return false
	}
	return sha256.Sum256(da) == sha256.Sum256(db)
}

// ShellQuote quotes s as a POSIX single-quoted literal safe for shell command lines.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("/._-:", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type SnippetOptions struct {
	Agent    string
	Endpoint string
	BinPath  string
	DataDir  string
	HomeDir  string
	Getenv   func(string) string
}

type SnippetSpec struct {
	Agent        string `json:"agent"`
	ConfigPath   string `json:"config_path"`
	MCPURL       string `json:"mcp_url"`
	HookBinPath  string `json:"hook_bin_path"`
	Instructions string `json:"instructions"`
	Snippet      string `json:"snippet"`
}

func AgentMCPURL(baseEndpoint, agent string) string {
	raw := strings.TrimSpace(baseEndpoint)
	if raw == "" {
		raw = "http://" + buildinfo.DefaultAddr() + "/mcp"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/mcp"
	}
	if agent != "" {
		q := u.Query()
		q.Set("agent", agent)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// BuildSnippet constructs the ready-to-paste manual Hook configuration or plugin
// code for Claude Code, Codex CLI, Cursor, OpenCode v2, or Pi.
func BuildSnippet(opts SnippetOptions) (SnippetSpec, error) {
	agent := strings.ToLower(strings.TrimSpace(opts.Agent))
	if !IsKnownAgent(agent) {
		return SnippetSpec{}, fmt.Errorf("不支持的客户端类型 %q（支持 codex, claude, cursor, opencode, pi）", opts.Agent)
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		home, _ = resolveHomeDir()
	}
	slug := buildinfo.Current().Slug
	slugRe := SlugRegex(slug)

	binPath := strings.TrimSpace(opts.BinPath)
	if binPath == "" {
		binPath = ResolveHookBinary(home, opts.DataDir)
	}

	var extraArgs []string
	if trimmedData := strings.TrimSpace(opts.DataDir); trimmedData != "" {
		if absData, err := filepath.Abs(trimmedData); err == nil {
			defData, defErr := DefaultDataDir()
			if defErr != nil || !sameFilePath(absData, defData) {
				extraArgs = []string{"--data", absData}
			}
		}
	}

	mcpURL := AgentMCPURL(opts.Endpoint, agent)

	buildCommand := func(sub string) string {
		parts := []string{ShellQuote(binPath), sub}
		for _, a := range extraArgs {
			parts = append(parts, ShellQuote(a))
		}
		return strings.Join(parts, " ")
	}

	switch agent {
	case "claude":
		cfgDir := filepath.Join(home, ".claude")
		if v := strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR")); v != "" {
			cfgDir = v
		}
		cfgPath := filepath.Join(cfgDir, "settings.json")
		doc := map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{
					map[string]any{
						"matcher": NativePreToolMatcher(slug),
						"hooks": []any{
							map[string]any{
								"type":    "command",
								"command": buildCommand("claude"),
							},
						},
					},
				},
			},
		}
		return SnippetSpec{
			Agent:        agent,
			ConfigPath:   cfgPath,
			MCPURL:       mcpURL,
			HookBinPath:  binPath,
			Instructions: fmt.Sprintf("确认 Claude Code 的 MCP 地址为 %s，并将以下 PreToolUse Hook 合并到 %s 后重启会话。", mcpURL, cfgPath),
			Snippet:      marshalPrettyJSON(doc),
		}, nil

	case "codex":
		cfgDir := filepath.Join(home, ".codex")
		if v := strings.TrimSpace(getenv("CODEX_HOME")); v != "" {
			cfgDir = v
		}
		cfgPath := filepath.Join(cfgDir, "hooks.json")
		doc := map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{
					map[string]any{
						"matcher": NativePreToolMatcher(slug),
						"hooks": []any{
							map[string]any{
								"type":    "command",
								"command": buildCommand("codex"),
							},
						},
					},
				},
			},
		}
		return SnippetSpec{
			Agent:        agent,
			ConfigPath:   cfgPath,
			MCPURL:       mcpURL,
			HookBinPath:  binPath,
			Instructions: fmt.Sprintf("确认 Codex CLI 的 MCP 地址为 %s，并将以下 PreToolUse Hook 合并到 %s 后重启会话。", mcpURL, cfgPath),
			Snippet:      marshalPrettyJSON(doc),
		}, nil

	case "cursor":
		cfgPath := filepath.Join(home, ".cursor", "hooks.json")
		doc := map[string]any{
			"version": 1,
			"hooks": map[string]any{
				"preToolUse": []any{
					map[string]any{
						"matcher": CursorPreToolMatcher(),
						"command": buildCommand("cursor"),
					},
				},
			},
		}
		return SnippetSpec{
			Agent:        agent,
			ConfigPath:   cfgPath,
			MCPURL:       mcpURL,
			HookBinPath:  binPath,
			Instructions: fmt.Sprintf("确认 Cursor 的 MCP 地址为 %s，并将以下 preToolUse 配置合并到 %s（仅本机 Cursor 会话支持动态目录签名）。", mcpURL, cfgPath),
			Snippet:      marshalPrettyJSON(doc),
		}, nil

	case "opencode":
		xdg := filepath.Join(home, ".config")
		if v := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); v != "" {
			xdg = v
		}
		cfgPath := filepath.Join(xdg, "opencode", "plugins", slug+".js")
		binJSON := mustJSONLiteral(binPath)
		extraJSON := mustJSONLiteral(extraArgs)
		toolRegex := fmt.Sprintf("/^(?:mcp__)?%s[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/", slugRe)
		js := fmt.Sprintf(`import { spawn } from "node:child_process";

const BIN = %s;
const EXTRA_ARGS = %s;
const TOOL_RE = %s;

function signToolInput(cwd, op, sessionID, input) {
  return new Promise((resolve) => {
    const args = ["sign", "--agent", "opencode", "--cwd", cwd, "--op", op, ...(sessionID ? ["--sid", sessionID] : []), ...EXTRA_ARGS];
    const child = spawn(BIN, args, { cwd, stdio: ["pipe", "pipe", "ignore"] });
    let stdout = "";
    const timer = setTimeout(() => { child.kill(); resolve(null); }, 5000);
    child.on("error", () => { clearTimeout(timer); resolve(null); });
    child.stdin.on("error", () => {});
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (Buffer.byteLength(stdout) > 1024 * 1024) { child.kill(); resolve(null); }
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code !== 0 || !stdout.trim()) { resolve(null); return; }
      try { resolve(JSON.parse(stdout)); } catch { resolve(null); }
    });
    child.stdin.end(JSON.stringify(input));
  });
}

function replaceInPlace(target, updated) {
  if (!updated || typeof updated !== "object") return;
  for (const key of Object.keys(target)) delete target[key];
  for (const [key, value] of Object.entries(updated)) {
    Object.defineProperty(target, key, { value, enumerable: true, writable: true, configurable: true });
  }
}

export default {
  id: %s,
  async setup(ctx) {
    await ctx.tool.hook("execute.before", async (event) => {
      const match = TOOL_RE.exec(event.tool ?? "");
      if (!match || !event.input || typeof event.input !== "object") return;
      let cwd = "";
      try {
        const session = await ctx.session.get({ sessionID: event.sessionID }, { signal: AbortSignal.timeout(1500) });
        cwd = session?.location?.directory ?? "";
      } catch {
        return;
      }
      if (!cwd) return;
      const updated = await signToolInput(cwd, match[1], event.sessionID ?? "", event.input);
      replaceInPlace(event.input, updated);
    });
  },
};
`, binJSON, extraJSON, toolRegex, mustJSONLiteral(slug+"-scope"))

		return SnippetSpec{
			Agent:        agent,
			ConfigPath:   cfgPath,
			MCPURL:       mcpURL,
			HookBinPath:  binPath,
			Instructions: fmt.Sprintf("确认 OpenCode v2 的 MCP 地址为 %s，并将以下插件保存至 %s 后重启 OpenCode。", mcpURL, cfgPath),
			Snippet:      js,
		}, nil

	case "pi":
		piDir := filepath.Join(home, ".pi", "agent")
		if v := strings.TrimSpace(getenv("PI_CODING_AGENT_DIR")); v != "" {
			piDir = v
		}
		cfgPath := filepath.Join(piDir, "extensions", slug+".ts")
		binJSON := mustJSONLiteral(binPath)
		extraJSON := mustJSONLiteral(extraArgs)
		toolRegex := fmt.Sprintf("/^(?:mcp__)?%s[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/", slugRe)
		ts := fmt.Sprintf(`import { spawnSync } from "node:child_process";

const BIN = %s;
const EXTRA_ARGS: string[] = %s;
const TOOL_RE = %s;

export default function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    const toolName = String(event?.toolName ?? event?.name ?? "");
    const match = TOOL_RE.exec(toolName);
    if (!match || !event?.input || typeof event.input !== "object") return;
    const cwd = String(ctx?.cwd ?? process.cwd());
    if (!cwd) return;
    const sid = String(ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId ?? "");
    const args = ["sign", "--agent", "pi", "--cwd", cwd, "--op", match[1], ...(sid ? ["--sid", sid] : []), ...EXTRA_ARGS];
    const res = spawnSync(BIN, args, {
      input: JSON.stringify(event.input),
      encoding: "utf8",
      timeout: 5000,
      maxBuffer: 1024 * 1024,
    });
    if (res.status !== 0 || !res.stdout?.trim()) return;
    try {
      const updated = JSON.parse(res.stdout);
      if (!updated || typeof updated !== "object") return;
      for (const key of Object.keys(event.input)) delete event.input[key];
      Object.assign(event.input, updated);
    } catch {
      return;
    }
  });
}
`, binJSON, extraJSON, toolRegex)

		return SnippetSpec{
			Agent:        agent,
			ConfigPath:   cfgPath,
			MCPURL:       mcpURL,
			HookBinPath:  binPath,
			Instructions: fmt.Sprintf("确认 Pi 的 MCP 地址为 %s，并将以下扩展保存至 %s 后执行 /reload。", mcpURL, cfgPath),
			Snippet:      ts,
		}, nil
	}

	return SnippetSpec{}, errors.New("unsupported agent")
}

func marshalPrettyJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.String()
}

func mustJSONLiteral(v any) string {
	if v == nil {
		return "[]"
	}
	if s, ok := v.([]string); ok && s == nil {
		v = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

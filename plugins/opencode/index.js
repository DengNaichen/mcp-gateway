import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const DEFAULT_SLUG = "mcp-gateway";
const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=opencode";
const DEFAULT_HOOK_BIN = "";
const DEFAULT_EXTRA_ARGS = [];
const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;

function resolveHookBinary() {
  const fromEnv = (process.env.GATEWAY_HOOK_BIN ?? "").trim();
  if (fromEnv && fs.existsSync(fromEnv)) return fromEnv;
  if (DEFAULT_HOOK_BIN && fs.existsSync(DEFAULT_HOOK_BIN)) return DEFAULT_HOOK_BIN;
  const homeInstalled = path.join(os.homedir(), `.${DEFAULT_SLUG}`, "bin", `${DEFAULT_SLUG}-hook`);
  if (fs.existsSync(homeInstalled)) return homeInstalled;
  return `${DEFAULT_SLUG}-hook`;
}

function signToolInput(cwd, op, sessionID, input) {
  const bin = resolveHookBinary();
  return new Promise((resolve) => {
    const args = [
      "sign",
      "--agent",
      "opencode",
      "--cwd",
      cwd,
      "--op",
      op,
      ...(sessionID ? ["--sid", sessionID] : []),
      ...DEFAULT_EXTRA_ARGS,
    ];
    const child = spawn(bin, args, { cwd, stdio: ["pipe", "pipe", "ignore"] });
    let stdout = "";
    const timer = setTimeout(() => {
      child.kill();
      resolve(null);
    }, 5000);
    child.on("error", () => {
      clearTimeout(timer);
      resolve(null);
    });
    child.stdin.on("error", () => {});
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (Buffer.byteLength(stdout) > 1024 * 1024) {
        child.kill();
        resolve(null);
      }
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code !== 0 || !stdout.trim()) {
        resolve(null);
        return;
      }
      try {
        resolve(JSON.parse(stdout));
      } catch {
        resolve(null);
      }
    });
    child.stdin.end(JSON.stringify(input));
  });
}

function replaceInPlace(target, updated) {
  if (!updated || typeof updated !== "object") return;
  for (const key of Object.keys(target)) delete target[key];
  for (const [key, value] of Object.entries(updated)) {
    Object.defineProperty(target, key, {
      value,
      enumerable: true,
      writable: true,
      configurable: true,
    });
  }
}

export default {
  id: `${DEFAULT_SLUG}-scope`,
  async setup(ctx) {
    const serverSlug = DEFAULT_SLUG;
    const mcpURL = (process.env.GATEWAY_MCP_URL ?? "").trim() || DEFAULT_MCP_URL;
    if (ctx.mcp?.transform) {
      await ctx.mcp.transform((editor) => {
        if (editor.get(serverSlug) !== undefined) return;
        editor.set(serverSlug, {
          type: "remote",
          url: mcpURL,
          oauth: false,
        });
      });
    }

    if (ctx.tool?.hook) {
      await ctx.tool.hook("execute.before", async (event) => {
        const match = TOOL_RE.exec(String(event?.tool ?? ""));
        if (!match || !event?.input || typeof event.input !== "object") return;
        let cwd = "";
        try {
          const session = event.sessionID
            ? await ctx.session?.get?.({ sessionID: event.sessionID }, { signal: AbortSignal.timeout(1500) })
            : null;
          cwd = session?.location?.directory || ctx.location?.directory || "";
        } catch {
          cwd = ctx.location?.directory || "";
        }
        if (!cwd) return;
        const updated = await signToolInput(cwd, match[1], String(event.sessionID ?? ""), event.input);
        replaceInPlace(event.input, updated);
      });
    }
  },
};

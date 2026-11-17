import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const DEFAULT_SLUG = "mcp-gateway";
const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=pi";
const DEFAULT_HOOK_BIN = "";
const DEFAULT_EXTRA_ARGS: string[] = [];
const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;

function resolveHookBinary(): string {
  const fromEnv = (process.env.GATEWAY_HOOK_BIN ?? "").trim();
  if (fromEnv && fs.existsSync(fromEnv)) return fromEnv;
  if (DEFAULT_HOOK_BIN && fs.existsSync(DEFAULT_HOOK_BIN)) return DEFAULT_HOOK_BIN;
  const homeInstalled = path.join(os.homedir(), `.${DEFAULT_SLUG}`, "bin", `${DEFAULT_SLUG}-hook`);
  if (fs.existsSync(homeInstalled)) return homeInstalled;
  return `${DEFAULT_SLUG}-hook`;
}

export default function (pi: any) {
  const mcpURL = (process.env.GATEWAY_MCP_URL ?? "").trim() || DEFAULT_MCP_URL;

  if (typeof pi.registerMcpServer === "function") {
    try {
      pi.registerMcpServer(DEFAULT_SLUG, {
        url: mcpURL,
        exposure: "direct",
        description: "Semantic tool discovery and scoped execution via MCP Gateway",
      });
    } catch {
      // Ignore if an extension or explicit configuration already owns this server.
    }
  }

  pi.on("tool_call", async (event: any, ctx: any) => {
    const toolName = String(event?.toolName ?? event?.name ?? "");
    const match = TOOL_RE.exec(toolName);
    if (!match || !event?.input || typeof event.input !== "object") return;

    const cwd = String(ctx?.cwd ?? process.cwd());
    if (!cwd) return;
    const sid = String(ctx?.sessionManager?.getSessionId?.() ?? ctx?.sessionId ?? "");
    const args = [
      "sign",
      "--agent",
      "pi",
      "--cwd",
      cwd,
      "--op",
      match[1],
      ...(sid ? ["--sid", sid] : []),
      ...DEFAULT_EXTRA_ARGS,
    ];
    const res = spawnSync(resolveHookBinary(), args, {
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

  if (typeof pi.registerCommand === "function") {
    pi.registerCommand(DEFAULT_SLUG, {
      description: `Show ${DEFAULT_SLUG} endpoint and hook status`,
      handler: async (_args: string, ctx: any) => {
        const bin = resolveHookBinary();
        ctx?.ui?.notify?.(`${DEFAULT_SLUG}: ${mcpURL} (hook: ${bin})`, "info");
      },
    });
  }
}

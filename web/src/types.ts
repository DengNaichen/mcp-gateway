export type Server = {
  id: string;
  name: string;
  description: string;
  kind: "http" | "mcp";
  endpoint: string;
  enabled: boolean;
  demo?: boolean;
  headers?: Record<string, string>;
  transport?: "http" | "stdio";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  work_dir?: string;
  redact_fields?: string[];
  redact_emails?: boolean;
};

export type Tool = {
  id: string;
  server_id: string;
  name: string;
  description: string;
  tags: string[];
  input_schema: Record<string, any>;
  enabled: boolean;
  read_only: boolean;
  actors: string[];
  method?: string;
  path?: string;
  example?: Record<string, any>;
  adapter?: string;
  revision?: string;
  http?: Record<string, any>;
  output_schema?: Record<string, any>;
};

export type Config = {
  base_url: string;
  model: string;
  max_tools: number;
  timeout_seconds: number;
};

export type EventScopeTrace = {
  agent?: string;
  repo_id?: string;
  repo_name?: string;
  kind?: string;
  root?: string;
  git_common_dir?: string;
  remote_hint?: string;
  matched_rule?: string;
  status?: string;
};

export type Event = {
  id: string;
  time: string;
  kind: string;
  actor: string;
  status: string;
  title: string;
  duration_ms: number;
  detail?: {
    scope?: EventScopeTrace;
    [key: string]: any;
  };
};

export type ScopeRepository = {
  id: string;
  name: string;
  git_common_dirs?: string[];
  root_paths?: string[];
  remote_hint?: string;
  created_at?: string;
};

export type ScopeServiceGrant = {
  server_id: string;
  access: "rw" | "ro";
  tool_ids?: string[];
};

export type ScopeRule = {
  repo_key: string;
  agent_key: string;
  grants: ScopeServiceGrant[];
  revision?: number;
  updated_at?: string;
};

export type ObservedRepo = {
  fingerprint: string;
  name: string;
  kind: string;
  root: string;
  git_common_dir?: string;
  remote_hint?: string;
  last_agent?: string;
  last_seen?: string;
  request_count?: number;
};

export type ScopeOverview = {
  enforce: boolean;
  hook_bin_path: string;
  repositories: ScopeRepository[];
  rules: ScopeRule[];
  observed: ObservedRepo[];
};

export type ScopeServerSummary = {
  server_id: string;
  access: "rw" | "ro";
  tool_count: number;
};

export type ScopeAgentPreview = {
  agent: string;
  matched_repo_key: string;
  matched_agent_key: string;
  source_label: string;
  server_count: number;
  tool_count: number;
  has_write: boolean;
  servers: ScopeServerSummary[];
};

export type ScopePreviewResponse = {
  repo_key: string;
  agents: ScopeAgentPreview[];
};

export type ScopeSnippetResponse = {
  agent: string;
  config_path: string;
  mcp_url: string;
  hook_bin_path: string;
  instructions: string;
  snippet: string;
};

export type SimScope = {
  repo_key: string;
  agent_key: string;
};

export type Bootstrap = {
  access: { shared: boolean; actor: string; admin?: boolean };
  registry: { servers: Server[]; tools: Tool[] };
  settings: Config;
  key_configured: boolean;
  events: Event[];
  mcp_url: string;
  setup: SetupStatus;
  scopes?: ScopeOverview;
};

export type ConnectionCheck = {
  status: string;
  message: string;
  detail?: string;
  checked_at: string;
  tool_count?: number;
  stale: boolean;
};

export type SetupStatus = {
  sources: number;
  enabled_tools: number;
  tools_ready: boolean;
  model_configured: boolean;
  model_ready: boolean;
  model_check: ConnectionCheck | null;
  source_checks: Record<string, ConnectionCheck>;
  agent_last_request: Event | null;
};

export type Selection = Tool & { reason: string };

export type Discovery = {
  id: string;
  status: string;
  model?: string;
  summary: string;
  question?: string;
  tools: Selection[];
  eligible: number;
  duration_ms: number;
  prompt_tokens: number;
  output_tokens: number;
};

export type Invocation = {
  id: string;
  tool_id: string;
  protocol: string;
  data: any;
  duration_ms: number;
};

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  const data = await response.json();
  if (response.status === 401 && path !== "/auth/login") {
    window.dispatchEvent(new Event("gateway:unauthorized"));
  }
  if (!response.ok) {
    throw Object.assign(new Error(data.error || `HTTP ${response.status}`), {
      detail: data.detail,
      status: response.status,
    });
  }
  return data;
}

export const pretty = (value: unknown) => JSON.stringify(value, null, 2);

export const SCOPE_AGENTS = [
  { id: "codex", label: "Codex CLI" },
  { id: "claude", label: "Claude Code" },
  { id: "cursor", label: "Cursor" },
  { id: "opencode", label: "OpenCode v2" },
  { id: "pi", label: "Pi" },
] as const;

export type ScopeAgentId = (typeof SCOPE_AGENTS)[number]["id"];

export function scopeAgentLabel(agentKey?: string): string {
  if (!agentKey || agentKey === "*") return "默认 (*)";
  const found = SCOPE_AGENTS.find((a) => a.id === agentKey);
  return found ? found.label : agentKey;
}

export function normalizeScopeOverview(raw?: Partial<ScopeOverview> | null): ScopeOverview {
  return {
    enforce: Boolean(raw?.enforce),
    hook_bin_path: raw?.hook_bin_path || "",
    repositories: (raw?.repositories || []).map((r) => ({
      ...r,
      git_common_dirs: r.git_common_dirs || [],
      root_paths: r.root_paths || [],
    })),
    rules: (raw?.rules || []).map((rule) => ({
      ...rule,
      grants: (rule.grants || []).map((g) => ({
        ...g,
        tool_ids: g.tool_ids || [],
      })),
    })),
    observed: raw?.observed || [],
  };
}

export function getScopeOverview(state: Bootstrap): ScopeOverview {
  return normalizeScopeOverview(state.scopes);
}

export async function fetchScopes(): Promise<ScopeOverview> {
  const res = await api<ScopeOverview>("/scopes");
  return normalizeScopeOverview(res);
}

export function setScopeMode(enforce: boolean): Promise<{ enforce: boolean }> {
  return api<{ enforce: boolean }>("/scopes/mode", {
    method: "PUT",
    body: pretty({ enforce }),
  });
}

export function saveScopeRepository(input: {
  id?: string;
  name: string;
  path?: string;
  git_common_dirs?: string[];
  root_paths?: string[];
  remote_hint?: string;
}): Promise<ScopeRepository> {
  return api<ScopeRepository>("/scopes/repositories", {
    method: "PUT",
    body: pretty(input),
  });
}

export function deleteScopeRepository(id: string): Promise<{ deleted: boolean }> {
  return api<{ deleted: boolean }>(
    `/scopes/repositories/${encodeURIComponent(id)}`,
    { method: "DELETE" },
  );
}

export function saveScopeRule(input: {
  repo_key: string;
  agent_key: string;
  grants: ScopeServiceGrant[];
}): Promise<ScopeRule> {
  return api<ScopeRule>("/scopes/rules", {
    method: "PUT",
    body: pretty(input),
  });
}

export function deleteScopeRule(
  repoKey: string,
  agentKey: string,
): Promise<{ deleted: boolean }> {
  return api<{ deleted: boolean }>(
    `/scopes/rules?repo_key=${encodeURIComponent(repoKey)}&agent_key=${encodeURIComponent(agentKey)}`,
    { method: "DELETE" },
  );
}

export async function previewScope(
  repoKey: string,
  actor: string,
): Promise<ScopePreviewResponse> {
  const res = await api<ScopePreviewResponse>("/scopes/preview", {
    method: "POST",
    body: pretty({ repo_key: repoKey, actor }),
  });
  return {
    repo_key: res.repo_key,
    agents: (res.agents || []).map((a) => ({
      ...a,
      servers: a.servers || [],
    })),
  };
}

export function fetchHookSnippet(agent: string): Promise<ScopeSnippetResponse> {
  return api<ScopeSnippetResponse>(
    `/scopes/snippets?agent=${encodeURIComponent(agent)}`,
  );
}

export function cleanDescriptionSummary(raw?: string, fallback = "—"): string {
  const text = (raw || "").replace(/\r\n/g, "\n").trim();
  if (!text) return fallback;
  const lines = text.split("\n");
  const leadParts: string[] = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      if (leadParts.length > 0) break;
      continue;
    }
    if (
      /^(args|arguments|parameters|params|inputs|returns?|outputs?|examples?|notes?|参数|参数说明|返回值|示例|注意)\s*[:：]?$/i.test(
        trimmed,
      )
    ) {
      break;
    }
    const cleaned = trimmed
      .replace(/^#{1,6}\s+/, "")
      .replace(/^(?:[-*•]|\d+[.)])\s+/, "")
      .replace(/\*\*(.*?)\*\*/g, "$1")
      .replace(/__(.*?)__/g, "$1")
      .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
      .replace(/`([^`]+)`/g, "$1")
      .trim();
    if (cleaned) leadParts.push(cleaned);
  }
  let summary = leadParts.join(" ").replace(/\s+/g, " ").trim();
  if (!summary) return fallback;
  const m = summary.match(/^(.+?[。！？!?]|.+?\.\s)/);
  if (m) summary = m[1].trim();
  if (summary.length > 100) summary = summary.slice(0, 99) + "…";
  return summary;
}

export function formatTime(value?: string): string {
  if (!value) return "—";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function serverProtocolLabel(server: Server): string {
  if (server.kind === "http") return "HTTP API";
  if (server.transport === "stdio") return "MCP · stdio";
  return "MCP · HTTP";
}

export function serverTargetText(server: Server): string {
  if (server.kind === "mcp" && server.transport === "stdio") {
    return [server.command, ...(server.args || [])].filter(Boolean).join(" ");
  }
  return server.endpoint || "—";
}

export function exampleFromSchema(schema?: Record<string, any>): Record<string, any> {
  if (!schema) return {};
  const out: Record<string, any> = {};
  for (const key of schema.required || []) {
    const p = schema.properties?.[key] || {};
    out[key] =
      p.default ??
      p.examples?.[0] ??
      p.enum?.[0] ??
      (p.type === "integer" || p.type === "number"
        ? (p.minimum ?? 1)
        : p.type === "boolean"
          ? false
          : p.type === "object"
            ? {}
            : p.type === "array"
              ? []
              : "");
  }
  return out;
}

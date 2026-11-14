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

export type Event = {
  id: string;
  time: string;
  kind: string;
  actor: string;
  status: string;
  title: string;
  duration_ms: number;
  detail?: any;
};

export type Bootstrap = {
  access: { shared: boolean; actor: string };
  registry: { servers: Server[]; tools: Tool[] };
  settings: Config;
  key_configured: boolean;
  events: Event[];
  mcp_url: string;
  setup: SetupStatus;
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

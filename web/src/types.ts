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
  mode: string;
  candidate_limit: number;
  max_tools: number;
  timeout_seconds: number;
  json_mode: boolean;
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
  mode: string;
  model?: string;
  summary: string;
  question?: string;
  tools: Selection[];
  candidates: (Tool & { score: number })[];
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
  if (response.status === 401 && path !== "/auth/login") window.dispatchEvent(new Event("gateway:unauthorized"));
 if (!response.ok)
    throw Object.assign(new Error(data.error || `HTTP ${response.status}`), {
      detail: data.detail,
 status: response.status,
    });
  return data;
}
export const pretty = (value: unknown) => JSON.stringify(value, null, 2);

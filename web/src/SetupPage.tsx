import React, { useEffect, useState } from "react";
import {
  ArrowRight,
  ArrowUpRight,
  Check,
  ChevronRight,
  Copy,
  Code2,
  FileJson,
  Loader2,
  Network,
  Plus,
  RefreshCw,
  Settings2,
  Sparkles,
  Terminal,
} from "lucide-react";
import {
  api,
  pretty,
  type Bootstrap,
  type Config,
  type ConnectionCheck,
  type Discovery,
  type Server,
  type SetupStatus,
  type Tool,
} from "./types";
import { GatewayDiagram, SetupSidebar } from "./SetupVisual";

type Props = {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  initialStep?: number;
  openRegistry: () => void;
  openDebug: () => void;
  notify: (text: string) => void;
};

export function SetupPage({
  state,
  refresh,
  initialStep,
  openRegistry,
  openDebug,
  notify,
}: Props) {
  const [chosenStep, setChosenStep] = useState<number | null>(
    initialStep ?? null,
  );
  const [pollError, setPollError] = useState("");
  const status = state.setup;
  const done = [
    status.tools_ready,
    status.model_ready,
    Boolean(
      status.tools_ready && status.model_ready && status.agent_last_request,
    ),
  ];
  const step = chosenStep ?? (done[0] ? (done[1] ? 3 : 2) : 1);
  useEffect(() => {
    if (initialStep) setChosenStep(initialStep);
  }, [initialStep]);
  useEffect(() => {
    if (step !== 3) return;
    const timer = setInterval(
      () =>
        refresh()
          .then(() => setPollError(""))
          .catch((e) => setPollError(e.message)),
      4000,
    );
    return () => clearInterval(timer);
  }, [step]);
  const labels = ["接入工具", "配置模型", "连接 Agent"];

  return (
    <div className="setup-page">
      <section className="setup-heading">
        <div className="setup-intro">
          <h1>
            把工具接好，
            <br />让 Agent 去行动<span>。</span>
          </h1>
          <p>为本地 Agent 按需提供工具。</p>
        </div>
        <GatewayDiagram tools={status.enabled_tools} />
      </section>
      <div className="setup-section-label">
        <span>开始接入</span>
        <span>
          <strong>
            {String(done.filter(Boolean).length).padStart(2, "0")}
          </strong>{" "}
          / 03 已完成
        </span>
      </div>
      <nav className="setup-steps" aria-label="接入步骤">
        {labels.map((label, index) => (
          <button
            key={label}
            aria-current={step === index + 1 ? "step" : undefined}
            className={`setup-step ${step === index + 1 ? "active" : ""} ${done[index] ? "done" : ""}`}
            onClick={() => setChosenStep(index + 1)}
          >
            <span className="setup-step-number">
              {done[index] ? <Check size={17} /> : `0${index + 1}`}
            </span>
            <span>
              <strong>
                {label}
                <ArrowUpRight size={15} />
              </strong>
            </span>
          </button>
        ))}
      </nav>
      <div className="setup-workspace">
        <div className="setup-stage" key={step}>
          {step === 1 && (
            <SourceStep
              state={state}
              refresh={refresh}
              next={() => setChosenStep(2)}
              openRegistry={openRegistry}
              notify={notify}
            />
          )}
          {step === 2 && (
            <ModelStep
              state={state}
              refresh={refresh}
              next={() => setChosenStep(3)}
              back={() => setChosenStep(1)}
              notify={notify}
            />
          )}
          {step === 3 && (
            <AgentStep
              state={state}
              refresh={refresh}
              back={() => setChosenStep(2)}
              pollError={pollError}
              notify={notify}
            />
          )}
        </div>
        <SetupSidebar
          state={state}
          step={step}
          goTo={setChosenStep}
          openDebug={openDebug}
          openRegistry={openRegistry}
        />
      </div>
    </div>
  );
}

function Failure({ error }: { error: any }) {
  if (!error) return null;
  return (
    <div className="setup-error" role="alert">
      <strong>{typeof error === "string" ? error : error.message}</strong>
      {error.detail && (
        <details open>
          <summary>错误详情</summary>
          <pre>{error.detail}</pre>
        </details>
      )}
    </div>
  );
}
function CheckNote({ check }: { check?: ConnectionCheck | null }) {
  if (!check) return null;
  return (
    <div
      className={`check-note ${check.status === "error" || check.stale ? "warning" : ""}`}
    >
      <strong>
        {check.stale ? "配置或可用工具有变化，需要重新测试" : check.message}
      </strong>
      <small>
        上次检查：{new Date(check.checked_at).toLocaleString("zh-CN")}
      </small>
      {check.detail && (
        <details>
          <summary>查看错误详情</summary>
          <pre>{check.detail}</pre>
        </details>
      )}
    </div>
  );
}

function SourceStep({
  state,
  refresh,
  next,
  openRegistry,
  notify,
}: {
  state: Bootstrap;
  refresh: Props["refresh"];
  next: () => void;
  openRegistry: () => void;
  notify: Props["notify"];
}) {
  const sources = state.registry.servers.filter((s) => !s.demo);
  const [form, setForm] = useState<Server>(
    sources[0] || {
      id: "",
      name: "",
      kind: "mcp",
      transport: "http",
      description: "",
      endpoint: "",
      enabled: true,
    },
  );
  const [selectedID, setSelectedID] = useState(sources[0]?.id || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<any>(null);
  const [argsText, setArgsText] = useState(pretty(sources[0]?.args || []));
  const [envText, setEnvText] = useState(pretty(sources[0]?.env || {}));
  const [headersText, setHeadersText] = useState(
    pretty(sources[0]?.headers || {}),
  );
  const [pasteText, setPasteText] = useState("");
  const [selectedTools, setSelectedTools] = useState<string[]>([]);
  const [applying, setApplying] = useState(false);
  const [toolName, setToolName] = useState("");
  const [toolDescription, setToolDescription] = useState("");
  const [toolPath, setToolPath] = useState("");
  const [toolMethod, setToolMethod] = useState("GET");
  const [toolReadOnly, setToolReadOnly] = useState(true);
  const [toolSchema, setToolSchema] = useState(
    pretty({
      type: "object",
      properties: {},
      required: [],
      additionalProperties: false,
    }),
  );
  const [toolError, setToolError] = useState<any>(null);
  const current = sources.find((s) => s.id === selectedID);
  const sourceTools = state.registry.tools.filter(
    (t) => t.server_id === selectedID,
  );
  const toolVersion = sourceTools.map((t) => t.id).join("|");
  useEffect(() => {
    setSelectedTools(
      sourceTools
        .filter((t) =>
          sourceTools.some((tool) => tool.enabled) ? t.enabled : t.read_only,
        )
        .map((t) => t.id),
    );
  }, [selectedID, toolVersion]);
  function update(patch: Partial<Server>) {
    setForm((f) => ({ ...f, ...patch }));
    setError(null);
  }
  function edit(source: Server) {
    setSelectedID(source.id);
    setForm(source);
    setArgsText(pretty(source.args || []));
    setHeadersText(pretty(source.headers || {}));
    setEnvText(pretty(source.env || {}));
    setError(null);
  }
  function loadPasted() {
    try {
      const raw = JSON.parse(pasteText);
      if (raw.kind && raw.id) {
        edit(raw);
        return;
      }
      const entries = raw.mcpServers
        ? Object.entries(raw.mcpServers)
        : [["my-server", raw]];
      if (entries.length !== 1)
        throw new Error(
          "请一次粘贴一个 mcpServers 条目，便于确认要接入的服务。",
        );
      const [name, entry] = entries[0] as [string, any];
      if (
        entry.type &&
        !["http", "streamable-http", "stdio"].includes(entry.type)
      )
        throw new Error(
          "当前支持 Streamable HTTP 和 stdio。这个配置的传输类型暂不支持。",
        );
      edit({
        id: name.replace(/[^a-zA-Z0-9._-]/g, "-"),
        name,
        kind: "mcp",
        description: "",
        enabled: true,
        endpoint: entry.url || "",
        transport: entry.command ? "stdio" : "http",
        command: entry.command,
        args: entry.args || [],
        env: entry.env || {},
        headers: entry.headers || {},
        work_dir: entry.cwd || "",
      });
      notify("已填入表单，检查后点击连接。");
    } catch (e) {
      setError(e);
    }
  }
  async function connect(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const id =
        form.id ||
        form.name
          .toLowerCase()
          .replace(/[^a-z0-9._-]+/g, "-")
          .replace(/^-|-$/g, "") ||
        `source-${Date.now().toString(36)}`;
      const source = {
        ...form,
        id,
        transport: form.kind === "http" ? "http" : form.transport,
        args: form.transport === "stdio" ? JSON.parse(argsText) : [],
        headers: form.transport === "stdio" ? {} : JSON.parse(headersText),
        env: form.transport === "stdio" ? JSON.parse(envText) : {},
      };
      if (
        !Array.isArray(source.args) ||
        source.args.some((a: unknown) => typeof a !== "string")
      )
        throw new Error('启动参数需要是字符串数组，例如 ["--port", "3000"]。');
      setForm(source);
      setSelectedID(id);
      const check = await api<ConnectionCheck>("/setup/source", {
        method: "POST",
        body: pretty(source),
      });
      notify(check.message);
    } catch (e) {
      setError(e);
    } finally {
      await refresh().catch((e) => setError(e));
      setBusy(false);
    }
  }
  async function applyTools() {
    setApplying(true);
    setToolError(null);
    try {
      await api(`/servers/${encodeURIComponent(selectedID)}/tools`, {
        method: "POST",
        body: pretty({ ids: selectedTools }),
      });
      await refresh();
      notify("工具选择已保存；选中的工具可以被 Agent 发现。");
    } catch (e) {
      setToolError(e);
    } finally {
      setApplying(false);
    }
  }
  async function addHTTPTool(e: React.FormEvent) {
    e.preventDefault();
    setApplying(true);
    setToolError(null);
    try {
      await api("/tools", {
        method: "PUT",
        body: pretty({
          id: `${selectedID}.${toolName}`,
          server_id: selectedID,
          name: toolName,
          description: toolDescription,
          method: toolMethod,
          path: toolPath,
          read_only: toolReadOnly,
          input_schema: JSON.parse(toolSchema),
          actors: ["developer"],
          tags: [],
          enabled: true,
        }),
      });
      await refresh();
      setToolName("");
      setToolPath("");
      setToolDescription("");
      notify("HTTP 工具已保存并启用。");
    } catch (e) {
      setToolError(e);
    } finally {
      setApplying(false);
    }
  }
  return (
    <div className="setup-content">
      <section className="panel setup-card">
        <div className="setup-card-heading">
          <span className="setup-card-icon">
            <Network size={21} />
          </span>
          <div>
            <h2>添加工具来源</h2>
          </div>
        </div>
        {sources.length > 0 && (
          <div className="source-pills">
            {sources.map((s) => (
              <button
                key={s.id}
                className={s.id === selectedID ? "selected" : ""}
                onClick={() => edit(s)}
              >
                {s.name}
                <small>
                  {
                    state.registry.tools.filter(
                      (t) => t.server_id === s.id && t.enabled && s.enabled,
                    ).length
                  }{" "}
                  个启用
                </small>
              </button>
            ))}
            <button
              onClick={() => {
                setSelectedID("");
                setForm({
                  id: "",
                  name: "",
                  kind: "mcp",
                  transport: "http",
                  description: "",
                  endpoint: "",
                  enabled: true,
                });
                setHeadersText("{}");
                setEnvText("{}");
                setArgsText("[]");
              }}
            >
              <Plus size={13} />
              添加来源
            </button>
          </div>
        )}
        <form onSubmit={connect} className="source-form">
          <div className="transport-options" role="group" aria-label="接入方式">
            {[
              {
                id: "http",
                icon: Network,
                title: "MCP 地址",
                description: "Streamable HTTP",
              },
              {
                id: "stdio",
                icon: Terminal,
                title: "本地进程",
                description: "stdio",
              },
              {
                id: "api",
                icon: Code2,
                title: "HTTP API",
                description: "自定义接口",
              },
            ].map((option) => (
              <button
                type="button"
                key={option.id}
                aria-pressed={
                  (form.kind === "http" ? "api" : form.transport || "http") ===
                  option.id
                }
                className={`transport-option ${(form.kind === "http" ? "api" : form.transport || "http") === option.id ? "selected" : ""}`}
                onClick={() =>
                  update({
                    kind: option.id === "api" ? "http" : "mcp",
                    transport: option.id === "stdio" ? "stdio" : "http",
                  })
                }
              >
                <option.icon size={18} />
                <span>
                  <strong>{option.title}</strong>
                  <small>{option.description}</small>
                </span>
                <span className="transport-radio">
                  {(form.kind === "http" ? "api" : form.transport || "http") ===
                    option.id && <span />}
                </span>
              </button>
            ))}
          </div>
          <div className="setup-form-row">
            <label className="form-field">
              <span>来源名称</span>
              <input
                required
                className="text-input"
                placeholder="例如：团队知识库"
                value={form.name}
                onChange={(e) => update({ name: e.target.value })}
              />
            </label>
          </div>
          {form.kind === "mcp" && form.transport === "stdio" ? (
            <>
              <label className="form-field">
                <span>启动命令</span>
                <input
                  required
                  className="text-input mono"
                  placeholder="例如 npx、uvx 或可执行文件绝对路径"
                  value={form.command || ""}
                  onChange={(e) => update({ command: e.target.value })}
                />
              </label>
              <label className="form-field">
                <span>
                  启动参数 <em>JSON 字符串数组</em>
                </span>
                <textarea
                  className="setup-json-input"
                  value={argsText}
                  onChange={(e) => setArgsText(e.target.value)}
                  placeholder='["--port", "3000"]'
                />
              </label>
            </>
          ) : (
            <label className="form-field">
              <span>{form.kind === "mcp" ? "MCP 服务地址" : "API 基地址"}</span>
              <input
                required
                type="url"
                className="text-input mono"
                placeholder={
                  form.kind === "mcp"
                    ? "http://127.0.0.1:3000/mcp"
                    : "https://api.internal.example/v1"
                }
                value={form.endpoint}
                onChange={(e) => update({ endpoint: e.target.value })}
              />
            </label>
          )}
          <div className="source-advanced-grid">
            <details className="setup-advanced">
              <summary>
                <Settings2 size={13} />
                认证与连接选项
              </summary>
              <p>
                {form.kind === "http"
                  ? "工具路径会拼接到 API 基地址。连接检查仅发送 HEAD 请求，工具需手动定义。"
                  : form.transport === "stdio"
                    ? "命令需已安装在本机，由网关直接启动，不经过 shell。"
                    : "填写完整的 Streamable HTTP endpoint。暂不支持旧版 SSE 和交互式 OAuth。"}
              </p>
              <label className="form-field">
                <span>
                  来源 ID <em>留空自动生成</em>
                </span>
                <input
                  className="text-input mono"
                  value={form.id}
                  onChange={(e) => update({ id: e.target.value })}
                />
              </label>
              {form.transport === "stdio" ? (
                <>
                  <label className="form-field">
                    <span>
                      工作目录 <em>可选，建议绝对路径</em>
                    </span>
                    <input
                      className="text-input mono"
                      value={form.work_dir || ""}
                      onChange={(e) => update({ work_dir: e.target.value })}
                    />
                  </label>
                  <label className="form-field">
                    <span>环境变量映射</span>
                    <textarea
                      className="setup-json-input"
                      value={envText}
                      onChange={(e) => setEnvText(e.target.value)}
                    />
                    <small>
                      {
                        '例如 {"API_KEY":"${MY_API_KEY}"}。变量由启动网关的 shell 提供，修改后需重启网关。'
                      }
                    </small>
                  </label>
                </>
              ) : (
                <label className="form-field">
                  <span>
                    请求头 <em>JSON 对象</em>
                  </span>
                  <textarea
                    className="setup-json-input"
                    value={headersText}
                    onChange={(e) => setHeadersText(e.target.value)}
                  />
                  <small>
                    {
                      '例如 {"Authorization":"Bearer ${SERVICE_TOKEN}"}。请先在启动网关的环境里 export SERVICE_TOKEN，再重启。'
                    }
                  </small>
                </label>
              )}
            </details>
            <details className="setup-advanced">
              <summary>
                <FileJson size={13} />
                导入 MCP 配置
              </summary>
              <p>粘贴 mcpServers 条目或 Gateway 服务 JSON，填入后连接。</p>
              <textarea
                aria-label="现有 MCP 配置"
                className="setup-json-input"
                placeholder='{"mcpServers":{"my-server":{"type":"http","url":"http://127.0.0.1:3000/mcp"}}}'
                value={pasteText}
                onChange={(e) => setPasteText(e.target.value)}
              />
              <button
                className="button secondary"
                type="button"
                onClick={loadPasted}
              >
                填入表单
              </button>
            </details>
          </div>
          <Failure error={error} />
          <div className="setup-action-row">
            <button type="submit" className="button primary" disabled={busy}>
              {busy ? (
                <Loader2 className="spin" size={15} />
              ) : (
                <Network size={15} />
              )}
              {busy
                ? "正在连接…"
                : form.kind === "mcp"
                  ? "保存并读取工具"
                  : "保存并检查地址"}
              {!busy && <ArrowRight size={15} />}
            </button>
          </div>
        </form>
      </section>
      {current && (
        <section className="panel setup-card tool-review">
          <div className="setup-card-heading">
            <span className="setup-card-icon">
              <Check size={20} />
            </span>
            <div>
              <h2>选择向 Agent 开放的工具</h2>
              <p>
                {current.name} · {sourceTools.length} 个工具
              </p>
            </div>
          </div>
          <CheckNote check={state.setup.source_checks[current.id]} />
          {sourceTools.length > 0 && (
            <>
              <div className="tool-selection-actions">
                <button
                  className="text-button"
                  onClick={() => setSelectedTools(sourceTools.map((t) => t.id))}
                >
                  全选
                </button>
                <button
                  className="text-button"
                  onClick={() =>
                    setSelectedTools(
                      sourceTools.filter((t) => t.read_only).map((t) => t.id),
                    )
                  }
                >
                  只选只读
                </button>
                <button
                  className="text-button"
                  onClick={() => setSelectedTools([])}
                >
                  清空
                </button>
              </div>
              <div className="setup-tool-list">
                {sourceTools.map((t) => (
                  <label key={t.id} className="setup-tool-row">
                    <input
                      type="checkbox"
                      checked={selectedTools.includes(t.id)}
                      onChange={(e) =>
                        setSelectedTools((ids) =>
                          e.target.checked
                            ? [...ids, t.id]
                            : ids.filter((id) => id !== t.id),
                        )
                      }
                    />
                    <span>
                      <strong>{t.name}</strong>
                      <small>
                        {t.description || "下游未提供描述，可在注册表中补充。"}
                      </small>
                    </span>
                    <span
                      className={`tool-access ${t.read_only ? "" : "write"}`}
                    >
                      {t.read_only ? "声明只读" : "写操作 / 未声明"}
                    </span>
                  </label>
                ))}
              </div>
              <button
                className="button secondary"
                disabled={applying}
                onClick={applyTools}
              >
                {applying ? (
                  <Loader2 size={14} className="spin" />
                ) : (
                  <Check size={14} />
                )}
                保存工具选择（{selectedTools.length}）
              </button>
            </>
          )}
          {current.kind === "mcp" && !sourceTools.length && (
            <p className="setup-help">未读取到工具，请检查连接。</p>
          )}
          {current.kind === "http" && (
            <form onSubmit={addHTTPTool} className="http-tool-form">
              <h3>定义一个 HTTP 工具</h3>
              <div className="setup-form-row">
                <label className="form-field">
                  <span>工具名称</span>
                  <input
                    required
                    className="text-input mono"
                    placeholder="search_documents"
                    value={toolName}
                    onChange={(e) => setToolName(e.target.value)}
                  />
                </label>
                <label className="form-field">
                  <span>请求方法</span>
                  <select
                    className="text-input"
                    value={toolMethod}
                    onChange={(e) => {
                      setToolMethod(e.target.value);
                      setToolReadOnly(e.target.value === "GET");
                    }}
                  >
                    {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
                      <option key={m}>{m}</option>
                    ))}
                  </select>
                </label>
              </div>
              <label className="form-field">
                <span>工具路径</span>
                <input
                  required
                  className="text-input mono"
                  placeholder="/documents/search"
                  value={toolPath}
                  onChange={(e) => setToolPath(e.target.value)}
                />
              </label>
              <label className="form-field">
                <span>工具描述</span>
                <input
                  required
                  className="text-input"
                  placeholder="这个工具能做什么，什么情况下适合使用？"
                  value={toolDescription}
                  onChange={(e) => setToolDescription(e.target.value)}
                />
              </label>
              <label className="form-field">
                <span>
                  参数定义 <em>JSON Schema</em>
                </span>
                <textarea
                  className="setup-json-input schema-input"
                  value={toolSchema}
                  onChange={(e) => setToolSchema(e.target.value)}
                />
              </label>
              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={toolReadOnly}
                  onChange={(e) => setToolReadOnly(e.target.checked)}
                />
                这是一个只读工具
              </label>
              <button
                type="submit"
                className="button secondary"
                disabled={applying}
              >
                <Plus size={14} />
                保存并启用这个工具
              </button>
            </form>
          )}
          <Failure error={toolError} />
          <button className="text-button registry-link" onClick={openRegistry}>
            编辑完整 JSON <ArrowUpRight size={12} />
          </button>
        </section>
      )}
      <div className="step-next-row">
        <p>
          {state.setup.tools_ready
            ? `已启用 ${state.setup.enabled_tools} 个工具`
            : "先接入并启用至少一个工具"}
        </p>
        <button
          className="button primary"
          disabled={!state.setup.tools_ready}
          onClick={next}
        >
          配置模型 <ArrowRight size={14} />
        </button>
      </div>
    </div>
  );
}

function ModelStep({
  state,
  refresh,
  next,
  back,
  notify,
}: {
  state: Bootstrap;
  refresh: Props["refresh"];
  next: () => void;
  back: () => void;
  notify: Props["notify"];
}) {
  const [config, setConfig] = useState<Config>({
    ...state.settings,
    mode: "model",
  });
  const [key, setKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [intent, setIntent] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<any>(null);
  const [result, setResult] = useState<Discovery | null>(null);
  const dirty =
    pretty(config) !== pretty({ ...state.settings, mode: "model" }) ||
    !!key ||
    clearKey;
  const update = (patch: Partial<Config>) => {
    setConfig((c) => ({ ...c, ...patch }));
    setResult(null);
  };
  async function save(test: boolean) {
    setBusy(test ? "test" : "save");
    setError(null);
    setResult(null);
    try {
      const payload = {
        ...config,
        mode: "model",
        api_key: key,
        clear_key: clearKey,
        intent,
      };
      if (!config.model.trim())
        throw new Error("请填写服务实际支持的模型名称。");
      if (test) {
        const response = await api<Discovery>("/setup/model", {
          method: "POST",
          body: pretty(payload),
        });
        setResult(response);
        notify("模型已响应，路由决策校验通过。");
      } else {
        await api("/settings", { method: "PUT", body: pretty(payload) });
        notify("配置已保存；完成路由测试后才会显示已试通。");
      }
      setKey("");
      setClearKey(false);
    } catch (e) {
      setError(e);
    } finally {
      await refresh().catch((e) => setError(e));
      setBusy("");
    }
  }
  return (
    <div className="setup-content">
      <section className="panel setup-card">
        <div className="setup-card-heading">
          <span className="setup-card-icon">
            <Sparkles size={21} />
          </span>
          <div>
            <h2>配置路由模型</h2>
            <p>兼容 Chat Completions 的模型服务。</p>
          </div>
        </div>
        {!state.setup.tools_ready && (
          <div className="setup-notice">
            先启用一个工具，再测试路由。
            <button onClick={back}>
              接入工具 <ArrowRight size={12} />
            </button>
          </div>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            save(true);
          }}
        >
          <div className="setup-form-row">
            <label className="form-field">
              <span>Base URL</span>
              <input
                required
                type="url"
                className="text-input mono"
                value={config.base_url}
                onChange={(e) => update({ base_url: e.target.value })}
              />
            </label>
            <label className="form-field">
              <span>模型名称</span>
              <input
                required
                className="text-input mono"
                placeholder="填写服务实际支持的模型 ID"
                value={config.model}
                onChange={(e) => update({ model: e.target.value })}
              />
            </label>
          </div>
          <label className="form-field">
            <span>
              API Key{" "}
              <em>{state.key_configured ? "已载入" : "无认证服务可留空"}</em>
            </span>
            <input
              type="password"
              autoComplete="new-password"
              className="text-input mono"
              placeholder={
                state.key_configured ? "留空保留当前 Key" : "输入 API Key"
              }
              value={key}
              onChange={(e) => {
                setKey(e.target.value);
                setClearKey(false);
                setResult(null);
              }}
            />
            <small>Key 仅本次运行有效。</small>
          </label>
          {state.key_configured && (
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={clearKey}
                onChange={(e) => {
                  setClearKey(e.target.checked);
                  setKey("");
                }}
              />
              清除当前 Key
            </label>
          )}
          <details className="setup-advanced">
            <summary>
              <Settings2 size={13} />
              高级路由配置
            </summary>
            <p>
              Base URL 自动补全 /chat/completions，也可填完整 endpoint。
              长期使用可在启动环境中设置 GATEWAY_MODEL_API_KEY；切换服务时请更新
              Key。
            </p>
            <label className="form-field">
              <span>
                测试需求 <em>可选</em>
              </span>
              <input
                className="text-input"
                placeholder="留空时根据已启用工具生成"
                value={intent}
                onChange={(e) => setIntent(e.target.value)}
              />
            </label>
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={config.json_mode}
                onChange={(e) => update({ json_mode: e.target.checked })}
              />
              发送 response_format: json_object（服务不支持时取消）
            </label>
            <div className="setup-form-row three">
              <label className="form-field">
                <span>候选工具上限</span>
                <input
                  className="text-input"
                  type="number"
                  min="1"
                  max="100"
                  value={config.candidate_limit}
                  onChange={(e) => update({ candidate_limit: +e.target.value })}
                />
              </label>
              <label className="form-field">
                <span>返回工具上限</span>
                <input
                  className="text-input"
                  type="number"
                  min="1"
                  max="12"
                  value={config.max_tools}
                  onChange={(e) => update({ max_tools: +e.target.value })}
                />
              </label>
              <label className="form-field">
                <span>超时（秒）</span>
                <input
                  className="text-input"
                  type="number"
                  min="5"
                  max="180"
                  value={config.timeout_seconds}
                  onChange={(e) => update({ timeout_seconds: +e.target.value })}
                />
              </label>
            </div>
          </details>
          <Failure error={error} />
          <div className="setup-action-row">
            <button
              className="button primary"
              type="submit"
              disabled={!!busy || !state.setup.tools_ready}
            >
              {busy === "test" ? (
                <Loader2 className="spin" size={15} />
              ) : (
                <Sparkles size={15} />
              )}
              {busy === "test" ? "正在测试…" : "保存并测试"}
            </button>
            <button
              type="button"
              className="button secondary"
              disabled={!!busy}
              onClick={() => save(false)}
            >
              {busy === "save" && <Loader2 size={14} className="spin" />}
              仅保存
            </button>
          </div>
          <p className="setup-help">仅测试工具选择，不执行工具。</p>
        </form>
        {dirty && state.setup.model_check ? (
          <div className="setup-notice">配置已修改，请重新测试。</div>
        ) : (
          <CheckNote check={state.setup.model_check} />
        )}
        {result && (
          <div className="model-test-result">
            <strong>
              {result.status === "selected"
                ? `已返回 ${result.tools.length} 个工具`
                : result.status === "needs_context"
                  ? "模型请求补充信息"
                  : "模型已响应，当前需求未匹配工具"}
            </strong>
            <p>{result.summary}</p>
            {result.question && <p>{result.question}</p>}
            {result.tools.map((t) => (
              <div key={t.id}>
                <code>{t.id}</code>
                <small>{t.reason}</small>
              </div>
            ))}
            <details>
              <summary>查看原始路由结果</summary>
              <pre>{pretty(result)}</pre>
            </details>
          </div>
        )}
      </section>
      <div className="step-next-row">
        <button className="button secondary" onClick={back}>
          上一步
        </button>
        <button
          className="button primary"
          disabled={!state.setup.model_ready || !!busy || dirty}
          onClick={next}
        >
          连接 Agent <ArrowRight size={14} />
        </button>
      </div>
    </div>
  );
}

function AgentStep({
  state,
  refresh,
  back,
  pollError,
  notify,
}: {
  state: Bootstrap;
  refresh: Props["refresh"];
  back: () => void;
  pollError: string;
  notify: Props["notify"];
}) {
  const [client, setClient] = useState("codex");
  const [copied, setCopied] = useState("");
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(""), 2200);
    return () => clearTimeout(timer);
  }, [copied]);
  const last = state.setup.agent_last_request;
  const firstTool = state.registry.tools.find(
    (t) =>
      t.enabled &&
      state.registry.servers.some(
        (s) => s.id === t.server_id && s.enabled && !s.demo,
      ),
  );
  const command =
    client === "codex"
      ? `codex mcp add gateway-lab --url ${state.mcp_url}`
      : client === "claude"
        ? `claude mcp add --transport http gateway-lab ${state.mcp_url}`
        : pretty({
            mcpServers: { "gateway-lab": { type: "http", url: state.mcp_url } },
          });
  const prompt = firstTool
    ? `请通过 gateway-lab 帮我完成这个需求：${firstTool.description}。先用 discover_tools 找工具，再按返回的 schema 用 invoke_tool 调用；如果缺少必要参数，请先问我。`
    : "请使用 gateway-lab 的 discover_tools 查找适合当前任务的工具，再按返回的 schema 用 invoke_tool 调用。";
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      notify("已复制，回到本地 Agent 粘贴使用。");
    } catch {
      notify("复制失败，请手动选择文本复制。");
    }
  }
  return (
    <div className="setup-content">
      <section className="panel setup-card">
        <div className="setup-card-heading">
          <span className="setup-card-icon">
            <Terminal size={21} />
          </span>
          <div>
            <h2>连接本地 Agent</h2>
          </div>
        </div>
        {(!state.setup.tools_ready || !state.setup.model_ready) && (
          <div className="setup-notice">工具和模型尚未就绪，可先复制配置。</div>
        )}
        <div className="connection-instruction">
          <span className="instruction-number">1</span>
          <div>
            <h3>添加网关</h3>
            <label className="form-field client-choice">
              <span>本地 Agent</span>
              <select
                className="text-input"
                value={client}
                onChange={(e) => setClient(e.target.value)}
              >
                <option value="codex">Codex CLI / IDE</option>
                <option value="claude">Claude Code</option>
                <option value="json">通用 MCP JSON 配置</option>
              </select>
            </label>
            <div className="setup-code">
              <button aria-label="复制接入配置" onClick={() => copy(command)}>
                {copied === command ? <Check size={15} /> : <Copy size={15} />}
                {copied === command ? "已复制" : "复制"}
              </button>
              <pre>{command}</pre>
            </div>
            <p className="setup-help">
              {client === "codex"
                ? "在终端执行，然后重新打开 Codex 会话。"
                : client === "claude"
                  ? "在终端执行，再用 /mcp 查看连接。"
                  : "加入客户端 MCP 配置后重新连接。"}
            </p>
          </div>
        </div>
        <div className="connection-instruction">
          <span className="instruction-number">2</span>
          <div>
            <h3>回到 Agent，发起任务</h3>
            <div className="agent-task">
              <p>{prompt}</p>
              <button className="text-button" onClick={() => copy(prompt)}>
                {copied === prompt ? <Check size={13} /> : <Copy size={13} />}
                {copied === prompt ? "已复制任务" : "复制示例任务"}
              </button>
            </div>
            <details className="setup-advanced">
              <summary>Agent 长期指令</summary>
              <p>
                需要外部能力时，先向 discover_tools
                描述当前任务和必要上下文，再根据真实工具定义用 invoke_tool
                执行。任务变化时重新发现。
              </p>
              <button
                className="text-button"
                onClick={() =>
                  copy(
                    "需要外部能力时，先向 gateway-lab 的 discover_tools 描述当前任务和必要上下文，再按返回的真实工具 ID 和 input_schema 用 invoke_tool 执行。任务变化时重新发现。",
                  )
                }
              >
                复制指引
              </button>
            </details>
          </div>
        </div>
        <div className="connection-instruction">
          <span className="instruction-number">3</span>
          <div>
            <h3>连接状态</h3>
            <div className={`agent-detection ${last ? "seen" : ""}`}>
              <span className="detection-dot" />
              <div>
                <strong>
                  {last
                    ? last.title === "invoke_tool" && last.status === "success"
                      ? "工具调用成功"
                      : "已收到 MCP 请求"
                    : "等待 Agent 请求"}
                </strong>
                <p>
                  {last
                    ? `${last.detail?.client || "MCP client"} · ${last.title} · ${new Date(last.time).toLocaleString("zh-CN")}`
                    : "每 4 秒更新"}
                </p>
                {last?.status === "error" && (
                  <p className="agent-call-error">
                    最近一次调用失败：{last.detail?.error}
                  </p>
                )}
              </div>
            </div>
            <button
              className="text-button"
              onClick={() => refresh().catch((e) => notify(e.message))}
            >
              <RefreshCw size={13} />
              刷新
            </button>
            <Failure error={pollError} />
          </div>
        </div>
        <details className="setup-advanced">
          <summary>连接排查</summary>
          <ol>
            <li>
              网关进程是否运行，客户端访问的端口是否是{" "}
              {new URL(state.mcp_url).port}。
            </li>
            <li>
              客户端是否与网关在同一台机器；云端客户端访问不到你的 127.0.0.1。
            </li>
            <li>
              确认客户端支持 Streamable HTTP，连接后能看到 discover_tools 和
              invoke_tool。
            </li>
            <li>
              发现工具失败时，检查第二步的模型结果；调用失败时，在调用记录和调试页查看下游详情。
            </li>
          </ol>
        </details>
      </section>
      <div className="step-next-row">
        <button className="button secondary" onClick={back}>
          上一步
        </button>
      </div>
    </div>
  );
}

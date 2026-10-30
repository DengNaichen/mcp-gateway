import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  ArrowDown,
  ArrowRight,
  ArrowUpRight,
  Box,
  Check,
  ChevronRight,
  CircleDot,
  Code2,
  Copy,
  FileJson,
  FlaskConical,
  Layers,
  Loader2,
  Network,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  ShieldCheck,
  Sparkles,
  Terminal,
  Waypoints,
  X,
  Zap,
} from "lucide-react";
import {
  api,
  pretty,
  type Bootstrap,
  type Config,
  type Discovery,
  type Event,
  type Invocation,
  type Server,
  type Tool,
} from "./types";
import "./style.css";
import "./readability.css";
import "./setup.css";
import "./design.css";
import { LoginPage } from "./LoginPage";
import { MaintenancePage } from "./MaintenancePage";
import { SetupPage } from "./SetupPage";
import { GatewayLogo } from "./SetupVisual";

type Page = "maintenance" | "setup" | "playground" | "registry" | "activity" | "settings";
const nav = [
  { id: "setup", label: "开始接入", icon: Network },
  { id: "maintenance", label: "自动维护", icon: Sparkles },
  { id: "registry", label: "工具注册表", icon: Layers },
  { id: "playground", label: "调试", icon: Waypoints },
  { id: "activity", label: "调用记录", icon: Activity },
  { id: "settings", label: "高级设置", icon: Settings2 },
] as const;
const examples = [
  "排查 billing 服务最近频繁报错的原因",
  "查一下 checkout 服务由哪个团队负责",
  "查看 billing 最近部署了什么版本",
];

function App() {
  const [page, setPage] = useState<Page>("setup");
  const [setupStep, setSetupStep] = useState<number | undefined>();
  const [includeDemo, setIncludeDemo] = useState(false);
  function openSetup(step?: number) {
    setSetupStep(step);
    setPage("setup");
  }
  const [state, setState] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState("");
 const [needsLogin, setNeedsLogin] = useState(false);
  const [toast, setToast] = useState("");
  const [actor, setActor] = useState("developer");
  const [mode, setMode] = useState("retrieval");
  const [intent, setIntent] = useState("");
  const [context, setContext] = useState("");
  const [showContext, setShowContext] = useState(false);
  const [routing, setRouting] = useState(false);
  const [routeError, setRouteError] = useState("");
  const [result, setResult] = useState<Discovery | null>(null);
  const [selected, setSelected] = useState<Tool | null>(null);
  const [tab, setTab] = useState<"schema" | "invoke">("schema");
  const [argumentsText, setArgumentsText] = useState("{}");
  const [fields, setFields] = useState("");
  const [invoking, setInvoking] = useState(false);
  const [invokeError, setInvokeError] = useState("");
  const [invocation, setInvocation] = useState<Invocation | null>(null);
  const [editor, setEditor] = useState<{
    kind: "servers" | "tools";
    text: string;
  } | null>(null);
  const [saving, setSaving] = useState(false);
  const [editorError, setEditorError] = useState("");
  const [filter, setFilter] = useState("");
  const [serverFilter, setServerFilter] = useState("all");
  const [syncing, setSyncing] = useState("");
  const [eventDetail, setEventDetail] = useState<Event | null>(null);

  async function refresh() {
    const data = await api<Bootstrap>("/bootstrap");
    setState(data);
 setNeedsLogin(false); setBootError("");
 if (data.access?.shared) setActor(data.access.actor);
 return data;
  }
  useEffect(() => {
 const unauthorized = () => { setState(null); setNeedsLogin(true); };
 window.addEventListener("gateway:unauthorized", unauthorized);
 return () => window.removeEventListener("gateway:unauthorized", unauthorized);
 }, []);
 useEffect(() => {
    refresh()
      .then((data) => setMode(data.settings.mode))
      .catch((e) => setBootError(e.message));
  }, []);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(""), 3500);
    return () => clearTimeout(timer);
  }, [toast]);
  useEffect(() => {
    if (page !== "activity") return;
    const timer = setInterval(() => {
      api<Event[]>("/events")
        .then((events) => setState((s) => (s ? { ...s, events } : s)))
        .catch(() => {});
    }, 4000);
    return () => clearInterval(timer);
  }, [page]);

  function inspect(tool: Tool, nextTab: "schema" | "invoke" = "schema") {
    setSelected(tool);
    setTab(nextTab);
    setArgumentsText(
      pretty(tool.example || exampleFromSchema(tool.input_schema)),
    );
    setInvocation(null);
    setInvokeError("");
    setFields("");
  }
  async function discover() {
    if (!intent.trim() || routing) return;
    setRouting(true);
    setRouteError("");
    setResult(null);
    setSelected(null);
    setInvocation(null);
    try {
      const data = await api<Discovery>("/discover?debug=1", {
        method: "POST",
        body: pretty({
          intent,
          context,
          actor,
          mode,
          include_demo: includeDemo,
        }),
      });
      setResult({ ...data, candidates: data.candidates || [] });
      if (data.tools.length) inspect(data.tools[0]);
      await refresh();
    } catch (e: any) {
      setRouteError(e.message);
      refresh().catch(() => {});
    } finally {
      setRouting(false);
    }
  }
  async function invoke() {
    if (!selected) return;
    setInvoking(true);
    setInvokeError("");
    setInvocation(null);
    try {
      const args = JSON.parse(argumentsText);
      const data = await api<Invocation>("/invoke", {
        method: "POST",
        body: pretty({
          tool_id: selected.id,
          arguments: args,
          actor,
          fields: fields
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
        }),
      });
      setInvocation(data);
      await refresh();
    } catch (e: any) {
      setInvokeError(e.message);
      refresh().catch(() => {});
    } finally {
      setInvoking(false);
    }
  }
  async function toggleTool(tool: Tool) {
    try {
      await api("/tools", {
        method: "PUT",
        body: pretty({ ...tool, enabled: !tool.enabled }),
      });
      await refresh();
      setToast(tool.enabled ? "工具已禁用" : "工具已启用");
    } catch (e: any) {
      setToast(e.message);
    }
  }
  async function toggleServer(server: Server) {
    try {
      await api("/servers", {
        method: "PUT",
        body: pretty({ ...server, enabled: !server.enabled }),
      });
      await refresh();
    } catch (e: any) {
      setToast(e.message);
    }
  }
  async function syncServer(server: Server) {
    setSyncing(server.id);
    try {
      const data = await api<{ imported: number }>(
        `/servers/${encodeURIComponent(server.id)}/sync`,
        { method: "POST" },
      );
      await refresh();
      setToast(
        `已同步 ${data.imported} 个工具；新增或描述发生变化的工具默认禁用`,
      );
    } catch (e: any) {
      setToast(e.message);
    } finally {
      setSyncing("");
    }
  }
  async function saveEditor() {
    if (!editor) return;
    setSaving(true);
    setEditorError("");
    try {
      await api(`/${editor.kind}`, {
        method: "PUT",
        body: pretty(JSON.parse(editor.text)),
      });
      await refresh();
      setEditor(null);
      setToast("已保存到本地 Registry");
    } catch (e: any) {
      setEditorError(e.message);
    } finally {
      setSaving(false);
    }
  }
  function openEditor(kind: "servers" | "tools", data?: unknown) {
    setEditorError("");
    setEditor({
      kind,
      text: pretty(
        data ||
          (kind === "servers"
            ? {
                id: "my-server",
                name: "My MCP Server",
                kind: "mcp",
                description: "",
                endpoint: "http://127.0.0.1:3000/mcp",
                enabled: true,
                headers: {},
              }
            : {
                id: "my-server.my_tool",
                server_id: state?.registry.servers[0]?.id || "my-server",
                name: "my_tool",
                description: "描述这个工具适用的任务",
                tags: [],
                enabled: false,
                read_only: true,
                actors: ["developer"],
                method: "GET",
                path: "/items",
                input_schema: {
                  type: "object",
                  properties: {},
                  required: [],
                  additionalProperties: false,
                },
                example: {},
              }),
      ),
    });
  }
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setToast("已复制");
    } catch {
      setToast("复制失败，请手动选择文本复制");
    }
  }

  if (needsLogin) return <LoginPage onLogin={refresh} />;
 if (!state)
    return (
      <div className="loading-screen">
        <div className="brand-mark">
          <Waypoints size={25} />
        </div>
        {bootError ? (
          <>
            <h2>无法连接 Go 后端</h2>
            <p>{bootError}</p>
            <button
              className="button primary"
              onClick={() => window.location.reload()}
            >
              重试连接
            </button>
          </>
        ) : (
          <>
            <Loader2 className="spin" />
            <p>正在连接 Gateway…</p>
          </>
        )}
      </div>
    );
  const tools = state.registry.tools;
  const servers = state.registry.servers;
  const debugServers = servers.filter((s) => includeDemo || !s.demo);
  const enabled = tools.filter(
    (t) =>
      t.enabled && debugServers.some((s) => s.id === t.server_id && s.enabled),
  ).length;
  const filtered = tools.filter(
    (t) =>
      (serverFilter === "all" || t.server_id === serverFilter) &&
      `${t.name} ${t.description} ${t.id}`
        .toLowerCase()
        .includes(filter.toLowerCase()),
  );
  const modelLabel =
    mode === "model" ? state.settings.model || "模型未配置" : "本地关键词检索";

  return (
    <div className={`app-shell page-${page}`}>
      <a className="skip-link" href="#workspace-main">
        跳到工作区
      </a>
      <aside className="sidebar">
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            openSetup();
          }}
        >
          <span className="brand-mark">
            <GatewayLogo size={27} />
          </span>
          <span>
            Gateway<span className="brand-light"> Lab</span>
            <small>THE CAPABILITY LAYER</small>
          </span>
        </a>
        <div className="workspace-switch">
          <span className="workspace-avatar">
            L<span />
          </span>
          <span>
            <strong>{state.access?.shared ? "共享工作区" : "本地工作区"}</strong>
          </span>
          <span className="workspace-key">{state.access?.shared ? "SHARED" : "LOCAL"}</span>
        </div>
        <div className="workspace-label">WORKSPACE</div>
        <nav aria-label="主导航">
          {nav.map((item) => (
            <button
              key={item.id}
              aria-label={item.label}
              title={item.label}
              aria-current={page === item.id ? "page" : undefined}
              className={`nav-item ${page === item.id ? "active" : ""}`}
              onClick={() => {
                if (item.id === "setup") setSetupStep(undefined);
                setPage(item.id);
                if (item.id === "activity")
                  refresh().catch((e) => setToast(e.message));
              }}
            >
              <item.icon size={18} />
              <span>{item.label}</span>
              {item.id === "registry" && (
                <span className="nav-count">
                  {
                    tools.filter((t) =>
                      servers.some((s) => s.id === t.server_id && !s.demo),
                    ).length
                  }
                </span>
              )}
              {page === item.id && <span className="nav-active-dot" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-note">
          <div className="sidebar-note-header">
            <span>GETTING STARTED</span>
            <strong>
              {
                [
                  state.setup.tools_ready,
                  state.setup.model_ready,
                  !!(
                    state.setup.tools_ready &&
                    state.setup.model_ready &&
                    state.setup.agent_last_request
                  ),
                ].filter(Boolean).length
              }
              <em>/ 3</em>
            </strong>
          </div>
          <div className="sidebar-progress" aria-hidden="true">
            {[
              state.setup.tools_ready,
              state.setup.model_ready,
              !!(
                state.setup.tools_ready &&
                state.setup.model_ready &&
                state.setup.agent_last_request
              ),
            ].map((done, index) => (
              <span key={index} className={done ? "done" : ""} />
            ))}
          </div>
          <button onClick={() => openSetup()}>
            继续接入 <ArrowUpRight size={14} />
          </button>
        </div>
        <div className="sidebar-bottom">
          <span className="online-dot" />
          <div>
            <strong>Gateway is running</strong>
            <small>{new URL(state.mcp_url).host}</small>
          </div>
          <span className="local-badge">v0.1</span>
        </div>
      </aside>

      <div className="main-shell">
        <header className="topbar">
          <div className="breadcrumb">
            <span>{state.access?.shared ? "共享工作区" : "本地工作区"}</span>
            <ChevronRight size={13} />
            <strong>{nav.find((n) => n.id === page)?.label}</strong>
          </div>
          <div className="topbar-right">
            <span className="prototype-badge">
              <FlaskConical size={13} /> {state.access?.shared ? state.access.actor : "LOCAL PREVIEW"}
            </span>
            {state.access?.shared && <button className="button" onClick={async () => { try { await api("/auth/logout", {method: "POST", body: "{}"}); setState(null); setNeedsLogin(true); setResult(null); setSelected(null); setInvocation(null); } catch(e: any) { setToast(e.message); } }}>退出登录</button>}
 <span className="top-divider" />
            <span className="online-text">
              <span className="online-dot" />
              网关运行中
            </span>
          </div>
        </header>
        <main id="workspace-main" tabIndex={-1}>
          {page === "setup" && (
            <SetupPage
              state={state}
              refresh={refresh}
              initialStep={setupStep}
              openRegistry={() => setPage("registry")}
              openDebug={() => setPage("playground")}
              notify={setToast}
            />
          )}
          {page === "maintenance" && <MaintenancePage servers={state.registry.servers} refreshRegistry={refresh} />}
          {page === "playground" && (
            <>
              <div className="page-heading">
                <div>
                  <div className="eyebrow">
                    <span /> INTENT-DRIVEN TOOL DISCOVERY
                  </div>
                  <h1>
                    路由调试<span>。</span>
                  </h1>
                  <p>
                    手动检查工具发现和执行过程。日常任务请直接交给已接入的本地
                    Agent。
                  </p>
                </div>
                <button
                  className="button secondary"
                  onClick={() => openSetup(3)}
                >
                  <Terminal size={15} /> 连接 Agent <ArrowUpRight size={14} />
                </button>
              </div>
              <div className="overview-strip">
                <div>
                  <Layers size={16} />
                  <strong>{enabled}</strong>
                  <span>可用工具</span>
                </div>
                <span className="strip-divider" />
                <div>
                  <Network size={16} />
                  <strong>{debugServers.length}</strong>
                  <span>已注册服务</span>
                </div>
                <span className="strip-divider" />
                <div>
                  <ShieldCheck size={16} />
                  <span>按身份筛选</span>
                </div>
                <div className={`mode-indicator ${mode}`}>
                  <span className="small-dot" />
                  {mode === "model" ? "模型决策模式" : "检索预览模式"}
                </div>
              </div>
              <div className="playground-grid">
                <div className="work-column">
                  <section className="panel composer">
                    <div className="panel-label">
                      <span className="step-number">01</span>
                      <h2>告诉网关，你要做什么</h2>
                      <span className="muted-label">DISCOVER</span>
                    </div>
                    <div className="prompt-box">
                      <textarea
                        aria-label="任务描述"
                        placeholder="例如：billing 服务最近频繁报错，帮我找排查工具…"
                        value={intent}
                        onChange={(e) => setIntent(e.target.value)}
                        onKeyDown={(e) => {
                          if ((e.metaKey || e.ctrlKey) && e.key === "Enter")
                            discover();
                        }}
                      />
                      <div className="prompt-bottom">
                        <button
                          className={`text-button context-toggle ${showContext ? "selected" : ""}`}
                          onClick={() => setShowContext(!showContext)}
                        >
                          <Plus size={13} />
                          补充任务上下文
                        </button>
                        <span>⌘ / Ctrl + Enter</span>
                      </div>
                    </div>
                    {showContext && (
                      <textarea
                        className="context-input"
                        aria-label="任务上下文"
                        placeholder="已知信息、已尝试的操作、需要的能力…"
                        value={context}
                        onChange={(e) => setContext(e.target.value)}
                      />
                    )}
                    <div className="example-row">
                      <span>内置演示 · 仅调试</span>
                      {examples.map((ex, i) => (
                        <button
                          key={ex}
                          title={ex}
                          onClick={() => {
                            setIntent(ex);
                            setIncludeDemo(true);
                            setMode("retrieval");
                            setResult(null);
                            setSelected(null);
                          }}
                        >
                          {
                            ["排查服务错误", "查找服务负责人", "查询近期发布"][
                              i
                            ]
                          }
                          <ArrowUpRight size={12} />
                        </button>
                      ))}
                    </div>
                    <div className="composer-footer">
                      <div className="input-controls">
                        <label>
                          <span>{state.access?.shared ? "当前身份" : "模拟身份"}</span>
                          <select
                            value={actor}
 disabled={state.access?.shared}
                            onChange={(e) => {
                              setActor(e.target.value);
                              setResult(null);
                              setSelected(null);
                            }}
                          >
                            {state.access?.shared && <option value={state.access.actor}>{state.access.actor}</option>}
 <option value="developer">Developer</option>
                            <option value="observer">Observer</option>
                          </select>
                        </label>
                        <label>
                          <span>选择方式</span>
                          <select
                            value={mode}
                            onChange={(e) => {
                              setMode(e.target.value);
                              setResult(null);
                              setSelected(null);
                            }}
                          >
                            <option value="retrieval">检索预览</option>
                            <option value="model">模型决策</option>
                          </select>
                        </label>
                        <label className="checkbox-row debug-demo-toggle">
                          <input
                            type="checkbox"
                            checked={includeDemo}
                            onChange={(e) => {
                              setIncludeDemo(e.target.checked);
                              setResult(null);
                              setSelected(null);
                            }}
                          />
                          包含演示工具
                        </label>
                      </div>
                      <button
                        className="button primary"
                        disabled={!intent.trim() || routing}
                        onClick={discover}
                      >
                        {routing ? (
                          <Loader2 size={16} className="spin" />
                        ) : (
                          <Sparkles size={16} />
                        )}{" "}
                        {routing ? "正在发现…" : "发现工具"}
                        {!routing && <ArrowRight size={15} />}
                      </button>
                    </div>
                    {mode === "model" && !state.settings.model && (
                      <div className="inline-note">
                        模型尚未配置。
                        <button onClick={() => openSetup(2)}>
                          前往设置 <ArrowUpRight size={12} />
                        </button>
                      </div>
                    )}
                  </section>

                  <section className="panel results-panel">
                    <div className="panel-label">
                      <span className="step-number">02</span>
                      <h2>为当前任务推荐的工具</h2>
                      {result && (
                        <span className="result-count">
                          {result.tools.length} TOOLS
                        </span>
                      )}
                    </div>
                    {routeError && (
                      <div className="error-box" role="alert">
                        {routeError}
                      </div>
                    )}
                    {routing ? (
                      <div className="routing-state">
                        <div className="routing-orbit">
                          <Sparkles size={25} />
                        </div>
                        <h3>
                          {mode === "model"
                            ? "模型正在选择合适的工具"
                            : "正在检索工具注册表"}
                        </h3>
                        <p>筛选可用工具 · 检索候选 · 返回真实定义</p>
                        <div className="loading-bar" />
                      </div>
                    ) : result ? (
                      <>
                        <div className={`decision-summary ${result.status}`}>
                          <span className="decision-icon">
                            {result.status === "selected" ? (
                              <Check size={15} />
                            ) : (
                              <Search size={15} />
                            )}
                          </span>
                          <div>
                            <strong>
                              {result.status === "selected"
                                ? "已找到相关能力"
                                : result.status === "needs_context"
                                  ? "需要补充信息"
                                  : "没有找到合适的工具"}
                            </strong>
                            <p>{result.summary}</p>
                            {result.question && (
                              <p className="clarification">{result.question}</p>
                            )}
                          </div>
                        </div>
                        <div className="tool-cards">
                          {result.tools.map((t, i) => (
                            <button
                              key={t.id}
                              className={`tool-card ${selected?.id === t.id ? "selected" : ""}`}
                              onClick={() => inspect(t)}
                            >
                              <div className="tool-icon">
                                {t.server_id === "catalog" ? (
                                  <Box size={18} />
                                ) : (
                                  <Code2 size={18} />
                                )}
                              </div>
                              <div className="tool-card-body">
                                <div className="tool-card-title">
                                  <strong>{t.name}</strong>
                                  <span className="protocol-tag">
                                    {servers
                                      .find((s) => s.id === t.server_id)
                                      ?.kind.toUpperCase()}
                                  </span>
                                </div>
                                <p>{t.reason}</p>
                                <div className="tool-card-meta">
                                  <span>
                                    {servers.find((s) => s.id === t.server_id)
                                      ?.name || t.server_id}
                                  </span>
                                  <span>·</span>
                                  <span>{t.read_only ? "只读" : "写操作"}</span>
                                </div>
                              </div>
                              <span className="tool-index">
                                {String(i + 1).padStart(2, "0")}
                              </span>
                              <ChevronRight
                                size={15}
                                className="card-chevron"
                              />
                            </button>
                          ))}
                        </div>
                        <details className="candidate-details">
                          <summary>
                            <Search size={13} />
                            查看候选召回{" "}
                            <span>
                              {result.candidates.length} 个候选 /{" "}
                              {result.eligible} 个可用工具
                            </span>
                          </summary>
                          <div className="candidate-list">
                            {result.candidates.map((c) => (
                              <div key={c.id}>
                                <code>{c.id}</code>
                                <span>{c.score.toFixed(3)}</span>
                              </div>
                            ))}
                          </div>
                        </details>
                      </>
                    ) : (
                      !routeError && (
                        <div className="empty-state">
                          <div className="empty-graphic">
                            <div />
                            <Waypoints size={30} strokeWidth={1.5} />
                            <div />
                          </div>
                          <h3>先描述任务，工具会在这里出现</h3>
                          <p>按需发现能力，让 Agent 的上下文保持精简。</p>
                          <div className="empty-flow">
                            <span>任务意图</span>
                            <ArrowRight size={13} />
                            <span>工具发现</span>
                            <ArrowRight size={13} />
                            <span>按需调用</span>
                          </div>
                        </div>
                      )
                    )}
                  </section>
                </div>

                <div className="inspector-column">
                  <section className="panel inspector">
                    <div className="panel-label">
                      <FileJson size={17} />
                      <h2>工具检查器</h2>
                      {selected && (
                        <span className="protocol-tag">
                          {selected.read_only ? "READ ONLY" : "WRITE"}
                        </span>
                      )}
                    </div>
                    {selected ? (
                      <>
                        <div className="inspector-tool">
                          <span>{selected.server_id}</span>
                          <h3>{selected.name}</h3>
                          <p>{selected.description}</p>
                        </div>
                        <div className="tabs">
                          <button
                            className={tab === "schema" ? "active" : ""}
                            onClick={() => setTab("schema")}
                          >
                            Schema
                          </button>
                          <button
                            className={tab === "invoke" ? "active" : ""}
                            onClick={() => setTab("invoke")}
                          >
                            试运行 <Play size={11} />
                          </button>
                        </div>
                        {tab === "schema" ? (
                          <>
                            <div className="code-toolbar">
                              <span>INPUT SCHEMA</span>
                              <button
                                aria-label="复制 Schema"
                                onClick={() =>
                                  copy(pretty(selected.input_schema))
                                }
                              >
                                <Copy size={13} />
                              </button>
                            </div>
                            <pre className="code-block schema-code">
                              {pretty(selected.input_schema)}
                            </pre>
                            <div className="inspector-action">
                              <button
                                className="button secondary full-width"
                                onClick={() => setTab("invoke")}
                              >
                                <Play size={14} />
                                使用这个工具
                              </button>
                            </div>
                          </>
                        ) : (
                          <div className="invoke-form">
                            <label className="field-label">
                              ARGUMENTS <span>JSON</span>
                            </label>
                            <textarea
                              className="code-input"
                              aria-label="调用参数 JSON"
                              value={argumentsText}
                              onChange={(e) => setArgumentsText(e.target.value)}
                              spellCheck={false}
                            />
                            <label className="field-label">
                              返回字段 <span>可选</span>
                            </label>
                            <input
                              className="text-input mono"
                              placeholder="logs.timestamp, logs.message"
                              value={fields}
                              onChange={(e) => setFields(e.target.value)}
                            />
                            <p className="field-help">
                              逗号分隔的字段路径，留空返回完整结果。
                            </p>
                            <button
                              className="button primary full-width"
                              disabled={invoking}
                              onClick={invoke}
                            >
                              {invoking ? (
                                <Loader2 className="spin" size={14} />
                              ) : (
                                <Play size={14} />
                              )}
                              {invoking ? "调用中…" : "执行调用"}
                            </button>
                            {invokeError && (
                              <div className="error-box" role="alert">
                                {invokeError}
                              </div>
                            )}
                            {invocation && (
                              <div className="invoke-result">
                                <div className="code-toolbar">
                                  <span>
                                    <span className="online-dot" />
                                    成功 · {invocation.duration_ms} ms
                                  </span>
                                  <button
                                    aria-label="复制调用结果"
                                    onClick={() =>
                                      copy(pretty(invocation.data))
                                    }
                                  >
                                    <Copy size={13} />
                                  </button>
                                </div>
                                <pre className="code-block">
                                  {pretty(invocation.data)}
                                </pre>
                              </div>
                            )}
                          </div>
                        )}
                      </>
                    ) : (
                      <div className="inspector-empty">
                        <div className="schema-illustration">
                          <span />
                          <span />
                          <span />
                          <span />
                          <FileJson size={24} />
                        </div>
                        <strong>查看工具的完整定义</strong>
                        <p>
                          选择一个推荐工具，检查参数
                          <br />
                          schema，或直接发起调用。
                        </p>
                      </div>
                    )}
                  </section>
                  <section className="routing-info">
                    <div className="routing-info-title">
                      <CircleDot size={15} />
                      <strong>当前路由配置</strong>
                    </div>
                    <dl>
                      <div>
                        <dt>Selector</dt>
                        <dd title={modelLabel}>{modelLabel}</dd>
                      </div>
                      <div>
                        <dt>候选上限</dt>
                        <dd>{state.settings.candidate_limit} 个工具</dd>
                      </div>
                      <div>
                        <dt>返回上限</dt>
                        <dd>{state.settings.max_tools} 个工具</dd>
                      </div>
                      {result && (
                        <>
                          <div>
                            <dt>发现耗时</dt>
                            <dd>{result.duration_ms} ms</dd>
                          </div>
                          <div>
                            <dt>模型 tokens</dt>
                            <dd>
                              {result.prompt_tokens + result.output_tokens ||
                                "—"}
                            </dd>
                          </div>
                        </>
                      )}
                    </dl>
                    <button
                      className="text-button"
                      onClick={() => setPage("settings")}
                    >
                      调整配置 <ArrowUpRight size={12} />
                    </button>
                  </section>
                </div>
              </div>
              <div className="bottom-caption">
                <ShieldCheck size={13} />
                发现前筛选权限，执行时再次校验。内置服务返回演示数据；模拟身份用于原型实验。
              </div>
            </>
          )}

          {page === "registry" && (
            <>
              <PageHeading
                eyebrow="CAPABILITY REGISTRY"
                title="工具注册表"
                subtitle="统一管理服务和工具，让它们随时可以被发现。"
              >
                <button
                  className="button secondary"
                  onClick={() => openEditor("servers")}
                >
                  <Plus size={15} />
                  添加服务
                </button>
                <button
                  className="button primary"
                  onClick={() => openEditor("tools")}
                >
                  <Plus size={15} />
                  添加 HTTP 工具
                </button>
              </PageHeading>
              <div className="server-grid">
                {servers.map((s) => (
                  <section key={s.id} className="panel server-card">
                    <div className="server-top">
                      <div className="server-symbol">
                        <Network size={21} />
                      </div>
                      <span className="protocol-tag">
                        {s.kind.toUpperCase()}
                      </span>
                      <Toggle
                        checked={s.enabled}
                        onChange={() => toggleServer(s)}
                        label={`${s.name} 启用状态`}
                      />
                    </div>
                    <h3>
                      {s.name}
                      {s.demo && <span className="demo-label">DEMO</span>}
                    </h3>
                    <p>{s.description}</p>
                    <code
                      title={
                        s.transport === "stdio"
                          ? `${s.command} ${(s.args || []).join(" ")}`
                          : s.endpoint
                      }
                    >
                      {s.transport === "stdio"
                        ? `${s.command} ${(s.args || []).join(" ")}`
                        : s.endpoint}
                    </code>
                    <div className="server-card-bottom">
                      <span>
                        {tools.filter((t) => t.server_id === s.id).length}{" "}
                        个工具
                      </span>
                      <button
                        className="text-button"
                        onClick={() => openEditor("servers", s)}
                      >
                        编辑
                      </button>
                      {s.kind === "mcp" && (
                        <button
                          className="text-button"
                          disabled={!!syncing}
                          onClick={() => syncServer(s)}
                        >
                          <RefreshCw
                            size={12}
                            className={syncing === s.id ? "spin" : ""}
                          />
                          同步工具
                        </button>
                      )}
                    </div>
                  </section>
                ))}
              </div>
              <section className="panel registry-table">
                <div className="table-toolbar">
                  <h2>
                    全部工具 <span>{tools.length}</span>
                  </h2>
                  <div className="table-filters">
                    <div className="search-field">
                      <Search size={14} />
                      <input
                        aria-label="搜索工具"
                        placeholder="搜索名称、描述…"
                        value={filter}
                        onChange={(e) => setFilter(e.target.value)}
                      />
                    </div>
                    <select
                      aria-label="筛选服务"
                      value={serverFilter}
                      onChange={(e) => setServerFilter(e.target.value)}
                    >
                      <option value="all">所有服务</option>
                      {servers.map((s) => (
                        <option value={s.id} key={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>工具</th>
                        <th>服务</th>
                        <th>访问身份</th>
                        <th>操作</th>
                        <th>状态</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((t) => (
                        <tr key={t.id}>
                          <td>
                            <strong>{t.name}</strong>
                            <p>{t.description}</p>
                          </td>
                          <td>
                            <span className="server-cell">{t.server_id}</span>
                          </td>
                          <td>
                            <div className="actor-tags">
                              {t.actors.map((a) => (
                                <span key={a}>{a}</span>
                              ))}
                            </div>
                          </td>
                          <td>
                            <span
                              className={`access-tag ${t.read_only ? "" : "write"}`}
                            >
                              {t.read_only ? "只读" : "写操作"}
                            </span>
                          </td>
                          <td>
                            <Toggle
                              checked={t.enabled}
                              label={`${t.name} 启用状态`}
                              onChange={() => toggleTool(t)}
                            />
                          </td>
                          <td>
                            <button
                              className="text-button"
                              onClick={() => openEditor("tools", t)}
                            >
                              编辑 <ArrowUpRight size={12} />
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {!filtered.length && (
                    <div className="table-empty">没有匹配的工具</div>
                  )}
                </div>
              </section>
              <div className="bottom-caption">
                <ShieldCheck size={13} />
                新导入的 MCP 工具默认禁用；编辑后可手动启用。请求头支持
                $&#123;ENV_VAR&#125; 引用。
              </div>
            </>
          )}

          {page === "activity" && (
            <>
              <PageHeading
                eyebrow="REQUEST ACTIVITY"
                title="调用记录"
                subtitle="查看最近 100 次工具发现和调用过程。记录保存在 SQLite data/gateway.db，重启后继续保留。"
              >
                <button
                  className="button secondary"
                  onClick={() => refresh().catch((e) => setToast(e.message))}
                >
                  <RefreshCw size={14} />
                  刷新
                </button>
              </PageHeading>
              <section className="panel activity-list">
                {!state.events.length ? (
                  <div className="empty-state">
                    <Activity size={28} />
                    <h3>还没有请求记录</h3>
                    <p>在工作台发现工具，或连接本地 Agent 开始使用。</p>
                    <button
                      className="button secondary"
                      onClick={() => setPage("playground")}
                    >
                      前往工作台 <ArrowRight size={13} />
                    </button>
                  </div>
                ) : (
                  state.events.map((ev) => (
                    <button
                      className="activity-row"
                      key={ev.id}
                      onClick={() => setEventDetail(ev)}
                    >
                      <span
                        className={`event-icon ${ev.status === "error" ? "error" : ""}`}
                      >
                        {ev.kind === "discover" ? (
                          <Sparkles size={17} />
                        ) : (
                          <Play size={17} />
                        )}
                      </span>
                      <div className="event-body">
                        <div>
                          <span className="event-kind">
                            {ev.kind === "agent"
                              ? "Agent 请求"
                              : ev.kind === "discover"
                                ? "工具发现"
                                : "工具调用"}
                          </span>
                          <span
                            className={`status-badge ${ev.status === "error" ? "error" : ""}`}
                          >
                            {ev.status}
                          </span>
                        </div>
                        <strong>{ev.title}</strong>
                        <small>
                          {ev.actor} ·{" "}
                          {new Date(ev.time).toLocaleString("zh-CN")}
                        </small>
                      </div>
                      <span className="event-duration">
                        {ev.duration_ms} ms
                      </span>
                      <ChevronRight size={15} />
                    </button>
                  ))
                )}
              </section>
            </>
          )}

          {page === "settings" && (
            <SettingsPage
              initial={state.settings}
              keyConfigured={state.key_configured}
              onSave={async (config, key, clear) => {
                await api("/settings", {
                  method: "PUT",
                  body: pretty({ ...config, api_key: key, clear_key: clear }),
                });
                await refresh();
                setMode(config.mode);
                setToast("配置已保存");
              }}
            />
          )}
        </main>
        <footer className="app-footer">
          <span>
            <GatewayLogo size={15} /> GATEWAY LAB
          </span>
        </footer>
      </div>

      {toast && (
        <div className="toast" role="status">
          <Check size={15} />
          {toast}
          <button aria-label="关闭提示" onClick={() => setToast("")}>
            <X size={13} />
          </button>
        </div>
      )}
      {editor && (
        <div
          className="modal-backdrop"
          onClick={() => !saving && setEditor(null)}
        >
          <section
            role="dialog"
            aria-modal="true"
            aria-label="编辑注册定义"
            className="modal"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-heading">
              <div>
                <span className="eyebrow">REGISTRY DEFINITION</span>
                <h2>{editor.kind === "servers" ? "服务定义" : "工具定义"}</h2>
              </div>
              <button aria-label="关闭编辑器" onClick={() => setEditor(null)}>
                <X size={19} />
              </button>
            </div>
            <p className="modal-description">
              编辑 JSON 后保存。服务的 headers 仅接受环境变量引用，例如{" "}
              {'"Authorization": "Bearer ${MY_TOKEN}"'}。
            </p>
            <textarea
              autoFocus
              spellCheck={false}
              className="json-editor"
              value={editor.text}
              onChange={(e) => setEditor({ ...editor, text: e.target.value })}
            />
            {editorError && (
              <div className="error-box" role="alert">
                {editorError}
              </div>
            )}
            <div className="modal-footer">
              <button
                className="button secondary"
                onClick={() => setEditor(null)}
              >
                取消
              </button>
              <button
                className="button primary"
                disabled={saving}
                onClick={saveEditor}
              >
                {saving && <Loader2 className="spin" size={14} />}保存定义
              </button>
            </div>
          </section>
        </div>
      )}
      {eventDetail && (
        <div className="modal-backdrop" onClick={() => setEventDetail(null)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="请求详情"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-heading">
              <h2>请求详情</h2>
              <button
                aria-label="关闭请求详情"
                onClick={() => setEventDetail(null)}
              >
                <X size={19} />
              </button>
            </div>
            <pre className="code-block event-json">{pretty(eventDetail)}</pre>
            <div className="modal-footer">
              <button
                className="button secondary"
                onClick={() => copy(pretty(eventDetail))}
              >
                <Copy size={14} />
                复制 JSON
              </button>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

function PageHeading({
  eyebrow,
  title,
  subtitle,
  children,
}: {
  eyebrow: string;
  title: string;
  subtitle: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <div className="eyebrow">
          <span />
          {eyebrow}
        </div>
        <h1>
          {title}
          <span>。</span>
        </h1>
        <p>{subtitle}</p>
      </div>
      {children && <div className="heading-actions">{children}</div>}
    </div>
  );
}
function Toggle({
  checked,
  label,
  onChange,
}: {
  checked: boolean;
  label: string;
  onChange: () => void;
}) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`toggle ${checked ? "on" : ""}`}
      onClick={onChange}
    >
      <span />
    </button>
  );
}
function exampleFromSchema(schema: Record<string, any>): Record<string, any> {
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

function SettingsPage({
  initial,
  keyConfigured,
  onSave,
}: {
  initial: Config;
  keyConfigured: boolean;
  onSave: (c: Config, key: string, clear: boolean) => Promise<void>;
}) {
  const [config, setConfig] = useState(initial);
  const [key, setKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const update = (patch: Partial<Config>) => {
    setConfig((c) => ({ ...c, ...patch }));
    setSaved(false);
  };
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave(config, key, clearKey);
      setKey("");
      setClearKey(false);
      setSaved(true);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeading
        eyebrow="MODEL & ROUTING"
        title="模型设置"
        subtitle="为工具选择配置一个模型，地址和模型 ID 都可以自由替换。"
      />
      <form onSubmit={submit} className="settings-grid">
        <section className="panel settings-panel">
          <div className="settings-section-title">
            <span className="settings-icon">
              <Sparkles size={18} />
            </span>
            <div>
              <h2>Decision model</h2>
              <p>通过兼容 Chat Completions 的 HTTP 接口调用。</p>
            </div>
          </div>
          <label className="form-field">
            <span>Base URL</span>
            <input
              required
              type="url"
              className="text-input mono"
              value={config.base_url}
              onChange={(e) => update({ base_url: e.target.value })}
            />
            <small>
              例如 https://api.openai.com/v1 或本地兼容服务地址。自动补全
              /chat/completions。
            </small>
          </label>
          <label className="form-field">
            <span>模型名称</span>
            <input
              className="text-input mono"
              placeholder="填写服务实际支持的模型 ID"
              value={config.model}
              onChange={(e) => update({ model: e.target.value })}
            />
          </label>
          <label className="form-field">
            <span>
              API Key{" "}
              <em>{keyConfigured ? "已配置" : "可选，本地模型可留空"}</em>
            </span>
            <input
              type="password"
              autoComplete="new-password"
              className="text-input mono"
              placeholder={
                keyConfigured
                  ? "留空以保留当前 Key"
                  : "输入后仅保留在后端内存中"
              }
              value={key}
              onChange={(e) => {
                setKey(e.target.value);
                setClearKey(false);
                setSaved(false);
              }}
            />
            <small>
              Key 不写入配置文件。重启后可重新填写，或使用 GATEWAY_MODEL_API_KEY
              环境变量。
            </small>
          </label>
          {keyConfigured && (
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
          <div className="setting-toggle">
            <div>
              <strong>JSON Mode</strong>
              <p>发送 response_format: json_object。不支持时可以关闭。</p>
            </div>
            <Toggle
              checked={config.json_mode}
              onChange={() => update({ json_mode: !config.json_mode })}
              label="JSON Mode"
            />
          </div>
        </section>
        <section className="panel settings-panel">
          <div className="settings-section-title">
            <span className="settings-icon">
              <Waypoints size={18} />
            </span>
            <div>
              <h2>路由行为</h2>
              <p>调整召回范围和返回给 Agent 的工具数量。</p>
            </div>
          </div>
          <label className="form-field">
            <span>默认选择方式</span>
            <select
              className="text-input"
              value={config.mode}
              onChange={(e) => update({ mode: e.target.value })}
            >
              <option value="retrieval">检索预览 · 不调用模型</option>
              <option value="model">模型决策 · 检索后由模型选择</option>
            </select>
          </label>
          <div className="form-columns">
            <label className="form-field">
              <span>候选上限</span>
              <input
                className="text-input"
                type="number"
                min="1"
                max="100"
                value={config.candidate_limit}
                onChange={(e) =>
                  update({ candidate_limit: Number(e.target.value) })
                }
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
                onChange={(e) => update({ max_tools: Number(e.target.value) })}
              />
            </label>
          </div>
          <label className="form-field">
            <span>
              模型超时 <em>秒</em>
            </span>
            <input
              className="text-input"
              type="number"
              min="5"
              max="180"
              value={config.timeout_seconds}
              onChange={(e) =>
                update({ timeout_seconds: Number(e.target.value) })
              }
            />
          </label>
          <div className="config-flow">
            <span>权限筛选</span>
            <ArrowDown size={13} />
            <span>候选检索</span>
            <ArrowDown size={13} />
            <strong>模型选择</strong>
            <ArrowDown size={13} />
            <span>返回真实工具定义</span>
          </div>
          <p className="settings-footnote">
            <ShieldCheck size={14} />
            模型只能选择候选集内的 ID。参数校验和权限判断由 Go 后端执行。
          </p>
        </section>
        <div className="settings-save">
          {error && (
            <div className="error-box" role="alert">
              {error}
            </div>
          )}
          <button className="button primary" type="submit" disabled={busy}>
            {busy ? (
              <Loader2 className="spin" size={15} />
            ) : saved ? (
              <Check size={15} />
            ) : (
              <Check size={15} />
            )}
            {saved ? "已保存" : "保存配置"}
          </button>
        </div>
      </form>
    </>
  );
}

createRoot(document.getElementById("root")!).render(<App />);

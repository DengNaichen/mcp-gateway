import { useState } from "react";
import {
  Activity,
  ArrowRight,
  Check,
  Loader2,
  Layers,
  Network,
  Play,
  Plus,
  Sparkles,
  Terminal,
} from "lucide-react";
import {
  api,
  formatTime,
  pretty,
  type Bootstrap,
  type Discovery,
  type Event,
} from "./types";
import { AlertBox, Badge, CopyButton } from "./ui";

type Props = {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  onOpenServices: (opts?: { add?: boolean; serverId?: string }) => void;
  onOpenPlayground: () => void;
  onOpenLogs: (eventId?: string) => void;
  onOpenSettings: () => void;
  notify: (msg: string) => void;
};

export function OverviewPage({
  state,
  refresh,
  onOpenServices,
  onOpenPlayground,
  onOpenLogs,
  onOpenSettings,
  notify,
}: Props) {
  const [clientTab, setClientTab] = useState<
    "codex" | "claude" | "json" | "guide"
  >("codex");
  const [testingModel, setTestingModel] = useState(false);
  const [testError, setTestError] = useState<any>(null);
  const [testResult, setTestResult] = useState<Discovery | null>(null);

  const realServers = state.registry.servers.filter((s) => !s.demo);
  const demoServers = state.registry.servers.filter((s) => s.demo);
  const activeRealServers = realServers.filter((s) => s.enabled);
  const realTools = state.registry.tools.filter((t) =>
    realServers.some((s) => s.id === t.server_id),
  );
  const enabledRealTools = realTools.filter(
    (t) => t.enabled && activeRealServers.some((s) => s.id === t.server_id),
  );
  const readOnlyEnabled = enabledRealTools.filter((t) => t.read_only).length;
  const writeEnabled = enabledRealTools.length - readOnlyEnabled;

  const { setup, settings } = state;
  const step1Done = setup.tools_ready;
  const step2Done = setup.model_ready && !setup.model_check?.stale;
  const step3Done = Boolean(
    step1Done && step2Done && setup.agent_last_request,
  );
  const doneCount = [step1Done, step2Done, step3Done].filter(Boolean).length;

  const firstEnabledTool = enabledRealTools[0];
  const samplePrompt = firstEnabledTool
    ? `请通过 gateway-lab 帮我完成这个任务：${firstEnabledTool.description || firstEnabledTool.name}。先调用 discover_tools 查找合适工具，再根据返回的 input_schema 调用 invoke_tool。`
    : "请先调用 gateway-lab 的 discover_tools 描述当前任务查找工具，再按返回的 input_schema 调用 invoke_tool 执行。";

  const snippet =
    clientTab === "codex"
      ? `codex mcp add gateway-lab --url ${state.mcp_url}`
      : clientTab === "claude"
        ? `claude mcp add --transport http gateway-lab ${state.mcp_url}`
        : clientTab === "json"
          ? pretty({
              mcpServers: {
                "gateway-lab": {
                  type: "http",
                  url: state.mcp_url,
                },
              },
            })
          : "需要外部能力时，先向 gateway-lab 的 discover_tools 描述当前任务与上下文；拿到候选工具的真实 ID 和 input_schema 后，再用 invoke_tool 执行调用。任务目标变化时重新调用 discover_tools。";

  async function quickTestModel() {
    setTestingModel(true);
    setTestError(null);
    setTestResult(null);
    try {
      const res = await api<Discovery>("/setup/model", {
        method: "POST",
        body: pretty({
          ...settings,
          api_key: "",
          clear_key: false,
          intent: "",
        }),
      });
      setTestResult(res);
      await refresh();
      notify("路由模型校验通过");
    } catch (e: any) {
      setTestError(e);
    } finally {
      setTestingModel(false);
    }
  }

  const recentEvents = state.events.slice(0, 8);

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>
            概览
            {step1Done && step2Done ? (
              <Badge tone="success" dot>
                网关就绪
              </Badge>
            ) : (
              <Badge tone="warning" dot>
                待完成配置
              </Badge>
            )}
          </h1>
          <p>
            管理上游工具来源与 Decisions 路由模型，向本地 Agent 按需暴露最小工具集。
          </p>
        </div>
        <div className="page-header-actions">
          <button
            type="button"
            className="btn"
            onClick={onOpenPlayground}
          >
            <Play size={14} />
            路由调试
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => onOpenServices({ add: true })}
          >
            <Plus size={15} />
            添加服务
          </button>
        </div>
      </div>

      {/* Top Grid: Left Setup Checklist + Right Navy Connection Card */}
      <div className="overview-hero-grid">
        <section className="card">
          <div className="card-header">
            <div className="card-header-title">
              <h2>接入检查清单</h2>
              <p>完成以下配置后，本地 Agent 即可通过单一端点按需发现并执行工具。</p>
            </div>
            <Badge tone={doneCount === 3 ? "success" : "accent"}>
              {doneCount} / 3 已完成
            </Badge>
          </div>

          <div className="setup-step-list">
            {/* Step 1 */}
            <div className="setup-step-row">
              <span
                className={`setup-step-indicator ${
                  step1Done ? "done" : "current"
                }`}
              >
                {step1Done ? <Check size={13} /> : "1"}
              </span>
              <div className="setup-step-body">
                <div className="setup-step-head">
                  <strong>接入上游服务并启用工具</strong>
                  {step1Done ? (
                    <Badge tone="success">
                      已启用 {setup.enabled_tools} 个工具
                    </Badge>
                  ) : realServers.length > 0 ? (
                    <Badge tone="warning">待启用工具</Badge>
                  ) : (
                    <Badge>未接入</Badge>
                  )}
                </div>
                <p>
                  {step1Done
                    ? `当前共有 ${activeRealServers.length} 个上游服务在线，向路由模型开放 ${setup.enabled_tools} 个工具。`
                    : realServers.length > 0
                      ? `已添加 ${realServers.length} 个服务（如 ${realServers[0].name}），请勾选要向 Agent 开放的工具。`
                      : "支持远程 Streamable HTTP MCP、本地 stdio 进程以及自定义 HTTP API。"}
                </p>
                <div className="setup-step-actions">
                  {realServers.length === 0 ? (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      onClick={() => onOpenServices({ add: true })}
                    >
                      <Plus size={13} />
                      添加第一个服务
                    </button>
                  ) : (
                    <button
                      type="button"
                      className={`btn btn-sm ${!step1Done ? "btn-primary" : ""}`}
                      onClick={() =>
                        onOpenServices({ serverId: realServers[0].id })
                      }
                    >
                      管理服务与工具
                      <ArrowRight size={13} />
                    </button>
                  )}
                </div>
              </div>
            </div>

            {/* Step 2 */}
            <div className="setup-step-row">
              <span
                className={`setup-step-indicator ${
                  step2Done
                    ? "done"
                    : step1Done
                      ? "current"
                      : ""
                }`}
              >
                {step2Done ? <Check size={13} /> : "2"}
              </span>
              <div className="setup-step-body">
                <div className="setup-step-head">
                  <strong>配置并验证 Decisions 路由模型</strong>
                  {step2Done ? (
                    <Badge tone="success">{settings.model}</Badge>
                  ) : setup.model_configured ? (
                    <Badge tone="warning">
                      {setup.model_check?.stale ? "需重新校验" : "待测试路由"}
                    </Badge>
                  ) : (
                    <Badge>未配置</Badge>
                  )}
                </div>
                <p>
                  {step2Done
                    ? `路由模型 ${settings.model} 已验证通过，仅评估工具相关性，不直接执行下游操作。`
                    : setup.model_configured
                      ? `已保存模型 ${settings.model}${state.key_configured ? "（API Key 已载入）" : "（未检测到 API Key）"}，运行一次真实路由校验即可就绪。`
                      : "支持 OpenAI Decisions API（如 gpt-6-luna）与 Gemini Decisions API（gemini-*）。"}
                </p>
                {testError && (
                  <div style={{ marginTop: 8 }}>
                    <AlertBox
                      tone="error"
                      title={testError.message || String(testError)}
                      detail={testError.detail}
                    />
                  </div>
                )}
                {testResult && (
                  <div style={{ marginTop: 8 }}>
                    <AlertBox
                      tone="success"
                      title={`校验通过（${testResult.duration_ms} ms）：${testResult.summary}`}
                    />
                  </div>
                )}
                <div className="setup-step-actions">
                  {setup.model_configured &&
                    state.key_configured &&
                    step1Done &&
                    !step2Done && (
                      <button
                        type="button"
                        className="btn btn-primary btn-sm"
                        disabled={testingModel}
                        onClick={quickTestModel}
                      >
                        {testingModel ? (
                          <Loader2 size={13} className="spin" />
                        ) : (
                          <Sparkles size={13} />
                        )}
                        {testingModel ? "校验中…" : "立即校验路由"}
                      </button>
                    )}
                  <button
                    type="button"
                    className={`btn btn-sm ${
                      !step2Done &&
                      !(setup.model_configured && state.key_configured && step1Done)
                        ? "btn-primary"
                        : ""
                    }`}
                    onClick={onOpenSettings}
                  >
                    {setup.model_configured ? "调整路由设置" : "配置路由模型"}
                    <ArrowRight size={13} />
                  </button>
                </div>
              </div>
            </div>

            {/* Step 3 */}
            <div className="setup-step-row">
              <span
                className={`setup-step-indicator ${
                  step3Done
                    ? "done"
                    : step1Done && step2Done
                      ? "current"
                      : ""
                }`}
              >
                {step3Done ? <Check size={13} /> : "3"}
              </span>
              <div className="setup-step-body">
                <div className="setup-step-head">
                  <strong>在本地 Agent 中连接并调用</strong>
                  {setup.agent_last_request ? (
                    <Badge tone="success">已连接</Badge>
                  ) : (
                    <Badge>等待请求</Badge>
                  )}
                </div>
                <p>
                  {setup.agent_last_request
                    ? `最近收到请求：${setup.agent_last_request.title}（${formatTime(setup.agent_last_request.time)}）`
                    : "复制右侧客户端命令完成挂载；重启会话后向 Agent 提出任务，网关会自动记录握手与调用。"}
                </p>
                <div className="setup-step-actions">
                  <button
                    type="button"
                    className="btn btn-sm"
                    onClick={onOpenPlayground}
                  >
                    <Terminal size={13} />
                    在调试台手动试跑
                  </button>
                  {setup.agent_last_request && (
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      onClick={() => onOpenLogs(setup.agent_last_request?.id)}
                    >
                      查看最近请求
                    </button>
                  )}
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Right: Stripe Deep Navy Developer Connection Card */}
        <section className="navy-card" aria-label="客户端连接配置">
          <div className="navy-card-header">
            <span className="navy-card-label">
              <span className="sail-status-dot" />
              MCP ENDPOINT
            </span>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <code style={{ fontSize: 12, color: "#fff" }}>
                {state.mcp_url}
              </code>
              <CopyButton
                value={state.mcp_url}
                label="复制地址"
                variant="navy"
                onCopied={() => notify("已复制 MCP 地址")}
              />
            </div>
          </div>

          <div
            className="navy-card-header"
            style={{ background: "rgba(255,255,255,0.03)" }}
          >
            <div className="navy-tabs" role="tablist" aria-label="客户端类型">
              {(
                [
                  ["codex", "Codex"],
                  ["claude", "Claude Code"],
                  ["json", "MCP JSON"],
                  ["guide", "Agent 指引"],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  role="tab"
                  aria-selected={clientTab === id}
                  className={clientTab === id ? "active" : ""}
                  onClick={() => setClientTab(id)}
                >
                  {label}
                </button>
              ))}
            </div>
            <CopyButton
              value={snippet}
              label="复制配置"
              variant="navy"
              onCopied={() => notify("已复制到剪贴板")}
            />
          </div>

          <pre className="navy-pre">{snippet}</pre>

          <div className="navy-footer">
            <span>
              暴露固定工具：<code>discover_tools</code> ·{" "}
              <code>get_tool_schema</code> · <code>invoke_tool</code>
            </span>
            <CopyButton
              value={samplePrompt}
              label="复制测试任务"
              variant="navy"
              onCopied={() => notify("已复制示例任务 Prompt")}
            />
          </div>
        </section>
      </div>

      {/* 3-Column Summary Cards */}
      <div className="metric-cards">
        <div className="card metric-card">
          <div className="metric-card-top">
            <span>上游服务</span>
            <Network size={15} />
          </div>
          <div className="metric-number">
            {activeRealServers.length}
            <small>/ {realServers.length} 个已启用</small>
          </div>
          <div className="metric-foot">
            <span>另含 {demoServers.length} 个调试演示服务</span>
            <button
              type="button"
              className="btn-link"
              onClick={() => onOpenServices()}
            >
              管理服务 →
            </button>
          </div>
        </div>

        <div className="card metric-card">
          <div className="metric-card-top">
            <span>向 Agent 开放的工具</span>
            <Layers size={15} />
          </div>
          <div className="metric-number">
            {setup.enabled_tools}
            <small>/ {realTools.length} 个已开放</small>
          </div>
          <div className="metric-foot">
            <span>
              {readOnlyEnabled} 只读 · {writeEnabled} 写操作
            </span>
            <button
              type="button"
              className="btn-link"
              onClick={() => onOpenServices()}
            >
              检索工具 →
            </button>
          </div>
        </div>

        <div className="card metric-card">
          <div className="metric-card-top">
            <span>Decisions 路由策略</span>
            <Sparkles size={15} />
          </div>
          <div
            className="metric-number mono"
            style={{
              fontSize: 17,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              paddingTop: 3,
              paddingBottom: 2,
            }}
            title={settings.model || "未配置模型"}
          >
            {settings.model || "未配置模型"}
          </div>
          <div className="metric-foot">
            <span className="tnum">
              返回上限 {settings.max_tools} · 超时 {settings.timeout_seconds}s
            </span>
            <button type="button" className="btn-link" onClick={onOpenSettings}>
              路由设置 →
            </button>
          </div>
        </div>
      </div>

      {/* Recent Traffic Table */}
      <section className="card">
        <div className="card-header">
          <div className="card-header-title">
            <h2>最近请求记录</h2>
            <p>实时记录工具发现、下游执行与 MCP 客户端会话请求。</p>
          </div>
          {state.events.length > 0 && (
            <button
              type="button"
              className="btn btn-sm"
              onClick={() => onOpenLogs()}
            >
              查看全部 ({state.events.length})
              <ArrowRight size={13} />
            </button>
          )}
        </div>

        {recentEvents.length === 0 ? (
          <div className="empty-box">
            <Activity size={24} />
            <h3>暂无请求记录</h3>
            <p>连接本地 Agent 发起任务，或在「路由调试」手动运行一次工具发现。</p>
            <button
              type="button"
              className="btn btn-sm"
              style={{ marginTop: 6 }}
              onClick={onOpenPlayground}
            >
              前往路由调试
            </button>
          </div>
        ) : (
          <div className="table-wrap">
            <table className="sail-table">
              <thead>
                <tr>
                  <th>状态</th>
                  <th>类型</th>
                  <th>请求内容 / 目标</th>
                  <th>身份</th>
                  <th style={{ textAlign: "right" }}>耗时</th>
                  <th style={{ textAlign: "right" }}>时间</th>
                </tr>
              </thead>
              <tbody>
                {recentEvents.map((ev: Event) => (
                  <tr
                    key={ev.id}
                    className="row-clickable"
                    onClick={() => onOpenLogs(ev.id)}
                  >
                    <td style={{ width: 110 }}>
                      <EventStatusBadge status={ev.status} />
                    </td>
                    <td style={{ width: 110 }}>
                      <span style={{ color: "var(--ink-muted)", fontSize: 12.5 }}>
                        {ev.kind === "agent"
                          ? "Agent 请求"
                          : ev.kind === "discover"
                            ? "工具发现"
                            : "工具调用"}
                      </span>
                    </td>
                    <td>
                      <span className="cell-primary">{ev.title}</span>
                    </td>
                    <td style={{ width: 110 }}>
                      <code style={{ fontSize: 12, color: "var(--ink-muted)" }}>
                        {ev.actor}
                      </code>
                    </td>
                    <td
                      className="mono tnum"
                      style={{
                        width: 90,
                        textAlign: "right",
                        fontSize: 12,
                        color: "var(--ink-muted)",
                      }}
                    >
                      {ev.duration_ms} ms
                    </td>
                    <td
                      className="tnum"
                      style={{
                        width: 150,
                        textAlign: "right",
                        fontSize: 12,
                        color: "var(--ink-muted)",
                      }}
                    >
                      {formatTime(ev.time)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

export function EventStatusBadge({ status }: { status: string }) {
  if (status === "error" || status === "failed") {
    return <Badge tone="danger">{status}</Badge>;
  }
  if (status === "needs_context" || status === "no_match") {
    return <Badge tone="warning">{status}</Badge>;
  }
  return <Badge tone="success">{status}</Badge>;
}

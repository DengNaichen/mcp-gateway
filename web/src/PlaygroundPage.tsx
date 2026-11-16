import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import {
  SCOPE_AGENTS,
  api,
  exampleFromSchema,
  getScopeOverview,
  pretty,
  previewScope,
  type Bootstrap,
  type Discovery,
  type Invocation,
  type SimScope,
  type Tool,
} from "./types";
import { EventStatusBadge } from "./OverviewPage";
import { AlertBox, CopyButton } from "./ui";

const demoExamples = [
  {
    short: "排查服务错误日志",
    intent: "排查 billing 服务最近频繁报错的原因",
  },
  {
    short: "查询服务归属团队",
    intent: "查一下 checkout 服务由哪个团队负责",
  },
  {
    short: "查看近期部署版本",
    intent: "查看 billing 最近部署了什么版本",
  },
];

export type ReplayDraft = {
  intent?: string;
  actor?: string;
  simScope?: SimScope | null;
  seq: number;
};

export function PlaygroundPage({
  state,
  refresh,
  replayDraft,
  onOpenSettings,
  notify,
}: {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  replayDraft?: ReplayDraft | null;
  onOpenSettings: () => void;
  notify: (msg: string) => void;
}) {
  const scopes = getScopeOverview(state);
  const canSimulateScope =
    !state.access?.shared || state.access?.admin !== false;

  const [actor, setActor] = useState(
    state.access?.shared
      ? state.access.actor
      : replayDraft?.actor || "developer",
  );
  const [simRepoKey, setSimRepoKey] = useState<string>(
    replayDraft?.simScope?.repo_key || "",
  );
  const [simAgentKey, setSimAgentKey] = useState<string>(
    replayDraft?.simScope?.agent_key || "*",
  );
  const [scopedCandidateCount, setScopedCandidateCount] = useState<
    number | null
  >(null);
  const [includeDemo, setIncludeDemo] = useState(false);
  const [intent, setIntent] = useState(replayDraft?.intent || "");
  const [showContext, setShowContext] = useState(false);
  const [context, setContext] = useState("");
  const [routing, setRouting] = useState(false);
  const [routeError, setRouteError] = useState("");
  const [result, setResult] = useState<Discovery | null>(null);

  const [selected, setSelected] = useState<Tool | null>(null);
  const [inspectorTab, setInspectorTab] = useState<"schema" | "invoke">(
    "schema",
  );
  const [argsText, setArgsText] = useState("{}");
  const [fields, setFields] = useState("");
  const [invoking, setInvoking] = useState(false);
  const [invokeError, setInvokeError] = useState("");
  const [invocation, setInvocation] = useState<Invocation | null>(null);

  useEffect(() => {
    if (!replayDraft) return;
    if (typeof replayDraft.intent === "string") {
      setIntent(replayDraft.intent);
    }
    if (!state.access?.shared && replayDraft.actor) {
      setActor(replayDraft.actor);
    }
    if (replayDraft.simScope !== undefined) {
      setSimRepoKey(replayDraft.simScope?.repo_key || "");
      setSimAgentKey(replayDraft.simScope?.agent_key || "*");
    }
    setIncludeDemo(false);
    setResult(null);
    setSelected(null);
    setInvocation(null);
    setRouteError("");
  }, [replayDraft?.seq]);

  const servers = state.registry.servers;
  const eligiblePool = state.registry.tools.filter((t) => {
    if (!t.enabled) return false;
    const srv = servers.find((s) => s.id === t.server_id);
    if (!srv || !srv.enabled) return false;
    if (
      t.actors?.length &&
      !t.actors.includes("*") &&
      !t.actors.includes(actor)
    ) {
      return false;
    }
    return includeDemo || !srv.demo;
  });

  useEffect(() => {
    if (!simRepoKey) {
      setScopedCandidateCount(null);
      return;
    }
    let cancelled = false;
    previewScope(simRepoKey, actor)
      .then((res) => {
        if (cancelled) return;
        const targetAgent =
          simAgentKey && simAgentKey !== "*" ? simAgentKey : "codex";
        const found = res.agents.find((a) => a.agent === targetAgent);
        if (found && simAgentKey && simAgentKey !== "*") {
          setScopedCandidateCount(found.tool_count);
          return;
        }
        // When agent_key === "*", compute directly against (repo, *) > (*, *)
        const rule =
          scopes.rules.find(
            (r) => r.repo_key === simRepoKey && r.agent_key === "*",
          ) ||
          scopes.rules.find((r) => r.repo_key === "*" && r.agent_key === "*");
        if (!rule) {
          setScopedCandidateCount(0);
          return;
        }
        const count = eligiblePool.filter((t) => {
          const g = rule.grants.find((gr) => gr.server_id === t.server_id);
          if (!g) return false;
          if (g.tool_ids?.length && !g.tool_ids.includes(t.id)) return false;
          if (g.access === "ro" && !t.read_only) return false;
          return true;
        }).length;
        setScopedCandidateCount(count);
      })
      .catch(() => {
        if (!cancelled) setScopedCandidateCount(null);
      });
    return () => {
      cancelled = true;
    };
  }, [simRepoKey, simAgentKey, actor, JSON.stringify(scopes.rules), includeDemo]);

  const activeSimScope: SimScope | undefined =
    canSimulateScope && simRepoKey
      ? { repo_key: simRepoKey, agent_key: simAgentKey || "*" }
      : undefined;

  const displayedPoolCount =
    simRepoKey && scopedCandidateCount !== null
      ? scopedCandidateCount
      : eligiblePool.length;

  function selectTool(tool: Tool, nextTab: "schema" | "invoke" = "schema") {
    setSelected(tool);
    setInspectorTab(nextTab);
    setArgsText(pretty(tool.example || exampleFromSchema(tool.input_schema)));
    setInvocation(null);
    setInvokeError("");
    setFields("");
  }

  async function runDiscover() {
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
          include_demo: includeDemo,
          sim_scope: activeSimScope,
        }),
      });
      setResult(data);
      if (data.tools.length > 0) {
        selectTool(data.tools[0], "schema");
      }
      await refresh();
    } catch (e: any) {
      setRouteError(e.message);
      refresh().catch(() => {});
    } finally {
      setRouting(false);
    }
  }

  async function runInvoke() {
    if (!selected || invoking) return;
    setInvoking(true);
    setInvokeError("");
    setInvocation(null);
    try {
      const parsedArgs = JSON.parse(argsText);
      const res = await api<Invocation>("/invoke", {
        method: "POST",
        body: pretty({
          tool_id: selected.id,
          arguments: parsedArgs,
          actor,
          fields: fields
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
          sim_scope: activeSimScope,
        }),
      });
      setInvocation(res);
      await refresh();
    } catch (e: any) {
      setInvokeError(e.message);
      refresh().catch(() => {});
    } finally {
      setInvoking(false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>路由沙盒</h1>
          <p>
            模拟下游 Agent 发起意图发现（discover_tools），检验语义召回结果并直接试调候选工具（invoke_tool）。
          </p>
        </div>
        <div className="page-header-actions">
          <span
            className="tnum"
            style={{ fontSize: 12.5, color: "var(--ink-muted)", marginRight: 4 }}
          >
            模型{" "}
            <code style={{ color: "var(--ink-body)" }}>
              {state.settings.model || "未配置"}
            </code>{" "}
            · 候选池 {displayedPoolCount} 个工具
          </span>
          <button type="button" className="btn btn-sm" onClick={onOpenSettings}>
            路由参数
          </button>
        </div>
      </div>

      {!state.settings.model && (
        <div style={{ marginBottom: 16 }}>
          <AlertBox
            tone="warning"
            title="尚未配置 Decisions 路由模型，请先前往「路由设置」填写模型与 API Key。"
          />
        </div>
      )}

      <div className="workbench-split">
        {/* Left Pane: Intent Composer + Ranked Tool Results */}
        <div className="form-stack">
          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>1. 输入任务意图（discover_tools）</h2>
              </div>
              <span className="field-hint">快捷键 ⌘ / Ctrl + Enter</span>
            </div>

            <div className="card-body form-stack">
              <label className="field">
                <textarea
                  aria-label="任务描述"
                  className="textarea"
                  rows={3}
                  placeholder="描述当前要完成的开发或运维任务，例如：查看仓库最近有哪些未关闭的 PR 需要 review…"
                  value={intent}
                  onChange={(e) => setIntent(e.target.value)}
                  onKeyDown={(e) => {
                    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                      void runDiscover();
                    }
                  }}
                />
              </label>

              {showContext && (
                <label className="field">
                  <span className="field-label">补充上下文（context）</span>
                  <textarea
                    className="textarea"
                    rows={2}
                    placeholder="补充已知服务名、仓库名或前序排查线索…"
                    value={context}
                    onChange={(e) => setContext(e.target.value)}
                  />
                </label>
              )}

              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 8,
                  flexWrap: "wrap",
                }}
              >
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  onClick={() => setShowContext(!showContext)}
                >
                  {showContext ? "收起上下文" : "+ 添加上下文"}
                </button>

                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 6,
                    flexWrap: "wrap",
                  }}
                >
                  <span className="field-hint">内置演示用例：</span>
                  {demoExamples.map((ex) => (
                    <button
                      key={ex.short}
                      type="button"
                      className="btn btn-sm"
                      onClick={() => {
                        setIntent(ex.intent);
                        setIncludeDemo(true);
                        setResult(null);
                        setSelected(null);
                      }}
                    >
                      {ex.short}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className="card-footer">
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 14,
                  flexWrap: "wrap",
                }}
              >
                <label
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 6,
                    fontSize: 12.5,
                  }}
                >
                  <span style={{ color: "var(--ink-muted)" }}>
                    {state.access?.shared ? "当前身份" : "模拟身份"}
                  </span>
                  <select
                    className="select"
                    style={{ height: 28, width: 125, fontSize: 12.5 }}
                    disabled={state.access?.shared}
                    value={actor}
                    onChange={(e) => {
                      setActor(e.target.value);
                      setResult(null);
                      setSelected(null);
                    }}
                  >
                    {state.access?.shared && (
                      <option value={state.access.actor}>
                        {state.access.actor}
                      </option>
                    )}
                    <option value="developer">developer</option>
                    <option value="observer">observer</option>
                  </select>
                </label>

                {canSimulateScope && (
                  <>
                    <label
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 6,
                        fontSize: 12.5,
                      }}
                    >
                      <span style={{ color: "var(--ink-muted)" }}>仓库</span>
                      <select
                        aria-label="模拟仓库作用域"
                        className="select"
                        style={{ height: 28, width: 155, fontSize: 12.5 }}
                        value={simRepoKey}
                        onChange={(e) => {
                          const nextRepo = e.target.value;
                          setSimRepoKey(nextRepo);
                          if (!nextRepo) {
                            setSimAgentKey("*");
                          }
                          setResult(null);
                          setSelected(null);
                        }}
                      >
                        <option value="">管理员全量</option>
                        <option value="*">未绑定新仓库 (*)</option>
                        {scopes.repositories.map((repo) => (
                          <option key={repo.id} value={repo.id}>
                            {repo.name}
                          </option>
                        ))}
                      </select>
                    </label>

                    <label
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 6,
                        fontSize: 12.5,
                      }}
                    >
                      <span style={{ color: "var(--ink-muted)" }}>客户端</span>
                      <select
                        aria-label="模拟客户端"
                        className="select"
                        style={{ height: 28, width: 130, fontSize: 12.5 }}
                        value={simAgentKey}
                        onChange={(e) => {
                          const nextAgent = e.target.value;
                          setSimAgentKey(nextAgent);
                          if (!simRepoKey && nextAgent !== "*") {
                            setSimRepoKey("*");
                          }
                          setResult(null);
                          setSelected(null);
                        }}
                      >
                        <option value="*">默认 (*)</option>
                        {SCOPE_AGENTS.map((a) => (
                          <option key={a.id} value={a.id}>
                            {a.label}
                          </option>
                        ))}
                      </select>
                    </label>
                  </>
                )}

                <label className="checkbox-field" style={{ fontSize: 12.5 }}>
                  <input
                    type="checkbox"
                    checked={includeDemo}
                    onChange={(e) => {
                      setIncludeDemo(e.target.checked);
                      setResult(null);
                      setSelected(null);
                    }}
                  />
                  包含内置演示工具
                </label>
              </div>

              <button
                type="button"
                className="btn btn-primary"
                disabled={!intent.trim() || routing}
                onClick={() => void runDiscover()}
              >
                {routing && <Loader2 size={14} className="spin" />}
                {routing ? "模型选择中…" : "运行工具发现"}
              </button>
            </div>
          </section>

          {/* Discovery Results */}
          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>2. 路由决策与命中工具</h2>
              </div>
              {result && (
                <div
                  className="tnum"
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 10,
                    fontSize: 12,
                    color: "var(--ink-muted)",
                  }}
                >
                  <EventStatusBadge status={result.status} />
                  <span>{result.duration_ms} ms</span>
                  {result.prompt_tokens + result.output_tokens > 0 && (
                    <span>
                      {(
                        result.prompt_tokens + result.output_tokens
                      ).toLocaleString()}{" "}
                      tok
                    </span>
                  )}
                </div>
              )}
            </div>

            {routeError && (
              <div className="card-body">
                <AlertBox tone="error" title={routeError} />
              </div>
            )}

            {routing ? (
              <div className="empty-box">
                <Loader2 size={20} className="spin" />
                <h3>正在请求 Decisions 路由模型…</h3>
                <p>按身份筛选候选工具 → 逐项评估相关性概率 → 返回匹配 Schema</p>
              </div>
            ) : result ? (
              <div>
                <div
                  style={{
                    padding: "12px 16px",
                    background: "var(--bg-subtle)",
                    borderBottom:
                      result.tools.length > 0
                        ? "1px solid var(--border)"
                        : "none",
                    fontSize: 13,
                  }}
                >
                  <strong style={{ color: "var(--ink-strong)" }}>
                    {result.summary}
                  </strong>
                  {result.question && (
                    <p style={{ marginTop: 4, color: "var(--ink-muted)" }}>
                      补充信息建议：{result.question}
                    </p>
                  )}
                </div>

                {result.tools.map((t, idx) => {
                  const srv = servers.find((s) => s.id === t.server_id);
                  const isSelected = selected?.id === t.id;
                  return (
                    <button
                      key={t.id}
                      type="button"
                      className={`log-row ${isSelected ? "selected" : ""}`}
                      onClick={() => selectTool(t, "schema")}
                    >
                      <div className="log-row-main">
                        <div className="log-row-top">
                          <span
                            className="mono"
                            style={{
                              fontWeight: 600,
                              color: "var(--ink-strong)",
                              fontSize: 13,
                            }}
                          >
                            {t.name}
                          </span>
                          <span
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {srv?.name || t.server_id} ·{" "}
                            {t.read_only ? "只读" : "写操作"}
                          </span>
                        </div>
                        <span className="log-row-meta">{t.reason}</span>
                      </div>
                      <div className="log-row-right">#{idx + 1}</div>
                    </button>
                  );
                })}
              </div>
            ) : (
              !routeError && (
                <div className="empty-box">
                  <h3>等待运行工具发现</h3>
                  <p>输入任务描述后运行，命中的候选工具与相关性概率将在此展示。</p>
                </div>
              )
            )}
          </section>
        </div>

        {/* Right Pane: Sticky Tool Schema & Invoke Inspector */}
        <section className="card">
          <div className="card-header">
            <div className="card-header-title">
              <h2>3. 工具检查与试调用（invoke_tool）</h2>
            </div>
            {selected && (
              <span style={{ fontSize: 12, color: "var(--ink-muted)" }}>
                {selected.read_only ? "只读" : "写操作"}
              </span>
            )}
          </div>

          {!selected ? (
            <div className="empty-box">
              <h3>未选中工具</h3>
              <p>从左侧命中结果中选择任一工具，检查真实 JSON Schema 或直接发起试调用。</p>
            </div>
          ) : (
            <div className="card-body form-stack">
              <div>
                <div
                  className="mono"
                  style={{
                    fontSize: 14,
                    fontWeight: 650,
                    color: "var(--ink-strong)",
                  }}
                >
                  {selected.id}
                </div>
                <p
                  style={{
                    marginTop: 4,
                    fontSize: 12.5,
                    color: "var(--ink-muted)",
                  }}
                >
                  {selected.description}
                </p>
              </div>

              <div className="segmented" style={{ alignSelf: "flex-start" }}>
                <button
                  type="button"
                  className={inspectorTab === "schema" ? "active" : ""}
                  onClick={() => setInspectorTab("schema")}
                >
                  Input Schema
                </button>
                <button
                  type="button"
                  className={inspectorTab === "invoke" ? "active" : ""}
                  onClick={() => setInspectorTab("invoke")}
                >
                  发起试调用
                </button>
              </div>

              {inspectorTab === "schema" ? (
                <>
                  <div className="navy-card">
                    <div className="navy-card-header">
                      <span className="navy-card-label">INPUT SCHEMA</span>
                      <CopyButton
                        value={pretty(selected.input_schema)}
                        label="复制"
                        variant="navy"
                        onCopied={() => notify("已复制 Schema")}
                      />
                    </div>
                    <pre className="navy-pre">
                      {pretty(selected.input_schema)}
                    </pre>
                  </div>
                  <button
                    type="button"
                    className="btn btn-primary"
                    style={{ alignSelf: "flex-start" }}
                    onClick={() => setInspectorTab("invoke")}
                  >
                    使用此工具试运行
                  </button>
                </>
              ) : (
                <div className="form-stack">
                  <label className="field">
                    <span className="field-label">
                      调用参数（arguments） <em>JSON</em>
                    </span>
                    <textarea
                      aria-label="调用参数 JSON"
                      className="textarea mono"
                      rows={6}
                      value={argsText}
                      onChange={(e) => setArgsText(e.target.value)}
                      spellCheck={false}
                    />
                  </label>

                  <label className="field">
                    <span className="field-label">
                      返回字段裁剪（fields） <em>可选，逗号分隔路径</em>
                    </span>
                    <input
                      className="input mono"
                      placeholder="例如：logs.timestamp, logs.message"
                      value={fields}
                      onChange={(e) => setFields(e.target.value)}
                    />
                  </label>

                  <button
                    type="button"
                    className="btn btn-primary"
                    style={{ alignSelf: "flex-start" }}
                    disabled={invoking}
                    onClick={() => void runInvoke()}
                  >
                    {invoking && <Loader2 size={14} className="spin" />}
                    {invoking ? "执行中…" : "执行调用"}
                  </button>

                  {invokeError && <AlertBox tone="error" title={invokeError} />}

                  {invocation && (
                    <div className="navy-card">
                      <div className="navy-card-header">
                        <span className="navy-card-label">
                          <span className="sail-status-dot" />
                          RESPONSE · {invocation.duration_ms} ms
                        </span>
                        <CopyButton
                          value={pretty(invocation.data)}
                          label="复制结果"
                          variant="navy"
                          onCopied={() => notify("已复制调用结果")}
                        />
                      </div>
                      <pre className="navy-pre">{pretty(invocation.data)}</pre>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

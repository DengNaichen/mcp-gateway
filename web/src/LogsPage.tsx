import { useEffect, useState } from "react";
import { Loader2, Search } from "lucide-react";
import {
  formatTime,
  pretty,
  saveScopeRepository,
  scopeAgentLabel,
  type Event,
  type ScopeOverview,
  type SimScope,
} from "./types";
import { EventStatusBadge } from "./OverviewPage";
import { AlertBox, Badge, CopyButton } from "./ui";

export function LogsPage({
  events,
  scopes,
  allowLocalPath = true,
  initialEventId,
  onReplay,
  onOpenRepo,
  onRefresh,
  notify,
}: {
  events: Event[];
  scopes?: ScopeOverview;
  allowLocalPath?: boolean;
  initialEventId?: string | null;
  onReplay?: (payload: {
    intent: string;
    actor?: string;
    simScope?: SimScope | null;
  }) => void;
  onOpenRepo?: (repoId: string) => void;
  onRefresh: () => Promise<any> | void;
  notify: (msg: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [kindFilter, setKindFilter] = useState<
    "all" | "discover" | "invoke" | "agent"
  >("all");
  const [statusFilter, setStatusFilter] = useState<"all" | "ok" | "problem">(
    "all",
  );
  const [selectedId, setSelectedId] = useState<string | null>(
    initialEventId || events[0]?.id || null,
  );
  const [bindingRoot, setBindingRoot] = useState(false);

  useEffect(() => {
    if (initialEventId) {
      setSelectedId(initialEventId);
    } else if (!selectedId && events[0]) {
      setSelectedId(events[0].id);
    }
  }, [initialEventId, events]);

  const filtered = events.filter((ev) => {
    if (kindFilter !== "all" && ev.kind !== kindFilter) return false;
    const isProblem =
      ev.status === "error" ||
      ev.status === "failed" ||
      ev.status === "no_match" ||
      ev.status === "needs_context";
    if (statusFilter === "ok" && isProblem) return false;
    if (statusFilter === "problem" && !isProblem) return false;
    if (!query.trim()) return true;
    const q = query.toLowerCase();
    return `${ev.title} ${ev.actor} ${ev.kind} ${ev.status} ${JSON.stringify(ev.detail || {})}`
      .toLowerCase()
      .includes(q);
  });

  const activeEvent =
    filtered.find((e) => e.id === selectedId) ||
    events.find((e) => e.id === selectedId) ||
    filtered[0] ||
    null;

  const activeScope = activeEvent?.detail?.scope || null;
  const matchedObs =
    activeScope &&
    (scopes?.observed.find(
      (o) =>
        (Boolean(activeScope.git_common_dir) &&
          o.git_common_dir === activeScope.git_common_dir) ||
        (Boolean(activeScope.root) && o.root === activeScope.root),
    ) ||
      null);
  const effectiveCommonDir =
    activeScope?.git_common_dir || matchedObs?.git_common_dir || "";

  const alreadyBoundRepo = (() => {
    if (!activeScope || !scopes?.repositories.length) return null;
    if (activeScope.repo_id && activeScope.repo_id !== "*") {
      const byId = scopes.repositories.find(
        (r) => r.id === activeScope.repo_id,
      );
      if (byId) return byId;
    }
    if (effectiveCommonDir) {
      return (
        scopes.repositories.find((r) =>
          (r.git_common_dirs || []).includes(effectiveCommonDir),
        ) || null
      );
    }
    if (activeScope.kind === "git") {
      if (!activeScope.root) return null;
      const inferredGitDir = `${activeScope.root.replace(/\/+$/, "")}/.git`;
      return (
        scopes.repositories.find((r) =>
          (r.git_common_dirs || []).includes(inferredGitDir),
        ) || null
      );
    }
    if (activeScope.root) {
      const rootClean = activeScope.root.replace(/\/+$/, "");
      return (
        scopes.repositories.find(
          (r) =>
            (r.root_paths || []).some((rp) => {
              const rpClean = rp.replace(/\/+$/, "");
              return (
                Boolean(rpClean) &&
                (rootClean === rpClean || rootClean.startsWith(`${rpClean}/`))
              );
            }) ||
            (r.git_common_dirs || []).includes(`${rootClean}/.git`),
        ) || null
      );
    }
    return null;
  })();

  const boundRepoId =
    alreadyBoundRepo?.id ||
    (activeScope?.repo_id && activeScope.repo_id !== "*"
      ? activeScope.repo_id
      : "");

  async function handleBindFromScope() {
    if ((!activeScope?.root && !effectiveCommonDir) || bindingRoot) return;
    setBindingRoot(true);
    try {
      const effectiveRemoteHint =
        activeScope?.remote_hint || matchedObs?.remote_hint || "";
      const name =
        matchedObs?.name ||
        activeScope?.repo_name ||
        activeScope?.root?.split("/").filter(Boolean).pop() ||
        effectiveCommonDir
          .replace(/\/\.git\/?$/, "")
          .split("/")
          .filter(Boolean)
          .pop() ||
        "repo";
      const saved = await saveScopeRepository(
        effectiveCommonDir
          ? {
              name,
              git_common_dirs: [effectiveCommonDir],
              remote_hint: effectiveRemoteHint || undefined,
            }
          : allowLocalPath && activeScope?.root
            ? {
                name,
                path: activeScope.root,
                remote_hint: effectiveRemoteHint || undefined,
              }
            : {
                name,
                root_paths: activeScope?.root ? [activeScope.root] : undefined,
                remote_hint: effectiveRemoteHint || undefined,
              },
      );
      await onRefresh();
      notify(`已绑定仓库 ${saved.name}`);
      onOpenRepo?.(saved.id);
    } catch (e: any) {
      notify(e.message || "绑定仓库失败");
    } finally {
      setBindingRoot(false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>请求日志</h1>
          <p>
            实时追踪数据面工具发现（discover）、下游调用（invoke）与客户端会话负载，支持一键送入路由沙盒复现。
          </p>
        </div>
        <div className="page-header-actions">
          <span className="field-hint" style={{ marginRight: 4 }}>
            每 4s 自动刷新
          </span>
          <button type="button" className="btn" onClick={onRefresh}>
            刷新
          </button>
        </div>
      </div>

      {/* Filter Bar */}
      <div
        className="card"
        style={{ marginBottom: 14, borderRadius: "var(--radius-lg)" }}
      >
        <div className="toolbar" style={{ borderBottom: 0, borderRadius: 8 }}>
          <div className="toolbar-group">
            <div className="search-box">
              <Search size={14} />
              <input
                aria-label="搜索调用日志"
                className="input"
                placeholder="搜索意图、工具名、错误或身份…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>

            <div className="segmented" role="group" aria-label="事件类型">
              {(
                [
                  ["all", "全部类型"],
                  ["discover", "工具发现"],
                  ["invoke", "工具调用"],
                  ["agent", "Agent 请求"],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  className={kindFilter === id ? "active" : ""}
                  onClick={() => setKindFilter(id)}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="segmented" role="group" aria-label="状态筛选">
              {(
                [
                  ["all", "全部状态"],
                  ["ok", "成功 / 命中"],
                  ["problem", "错误 / 未命中"],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  className={statusFilter === id ? "active" : ""}
                  onClick={() => setStatusFilter(id)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>

          <span
            className="tnum"
            style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
          >
            {filtered.length} 条记录
          </span>
        </div>
      </div>

      {/* Split Master-Detail Logs Workbench */}
      {filtered.length === 0 ? (
        <div className="card empty-box">
          <h3>没有匹配的请求记录</h3>
          <p>尝试放宽过滤条件，或通过本地 Agent / 路由调试发起一次请求。</p>
        </div>
      ) : (
        <div className="logs-split">
          <div className="logs-list-pane" role="listbox" aria-label="请求列表">
            {filtered.map((ev) => (
              <button
                key={ev.id}
                type="button"
                className={`log-row ${activeEvent?.id === ev.id ? "selected" : ""}`}
                onClick={() => setSelectedId(ev.id)}
              >
                <div className="log-row-main">
                  <div className="log-row-top">
                    <EventStatusBadge status={ev.status} />
                    <span
                      style={{
                        fontSize: 12,
                        fontWeight: 500,
                        color: "var(--ink-muted)",
                      }}
                    >
                      {ev.kind === "agent"
                        ? "Agent 请求"
                        : ev.kind === "discover"
                          ? "工具发现"
                          : "工具调用"}
                    </span>
                  </div>
                  <span className="log-row-title" title={ev.title}>
                    {ev.title}
                  </span>
                  <span className="log-row-meta">
                    {ev.actor}
                    {ev.detail?.scope?.agent
                      ? ` · ${ev.detail.scope.agent}`
                      : ""}
                    {ev.detail?.scope?.repo_name
                      ? ` · ${ev.detail.scope.repo_name}`
                      : ev.detail?.scope?.root
                        ? " · 未绑定仓库"
                        : ""}{" "}
                    · {formatTime(ev.time)}
                  </span>
                </div>
                <div className="log-row-right">{ev.duration_ms} ms</div>
              </button>
            ))}
          </div>

          <div className="logs-inspector-pane">
            {activeEvent ? (
              <>
                <div
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    justifyContent: "space-between",
                    gap: 12,
                  }}
                >
                  <div>
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 8,
                        marginBottom: 6,
                      }}
                    >
                      <EventStatusBadge status={activeEvent.status} />
                      <code
                        style={{ fontSize: 12, color: "var(--ink-muted)" }}
                      >
                        {activeEvent.id}
                      </code>
                    </div>
                    <h2
                      style={{
                        fontSize: 16,
                        fontWeight: 650,
                        color: "var(--ink-strong)",
                      }}
                    >
                      {activeEvent.title}
                    </h2>
                  </div>
                  <div style={{ display: "inline-flex", gap: 6, flexShrink: 0 }}>
                    {activeEvent.kind === "discover" && onReplay && (
                      <button
                        type="button"
                        className="btn btn-sm"
                        onClick={() =>
                          onReplay({
                            intent: activeEvent.title,
                            actor: activeEvent.actor,
                            simScope: activeScope
                              ? {
                                  repo_key:
                                    boundRepoId ||
                                    (activeScope.root || effectiveCommonDir
                                      ? "*"
                                      : ""),
                                  agent_key: activeScope.agent || "*",
                                }
                              : null,
                          })
                        }
                      >
                        在沙盒中重放
                      </button>
                    )}
                    <CopyButton
                      value={pretty(activeEvent)}
                      label="复制事件 JSON"
                      onCopied={() => notify("已复制事件 JSON")}
                    />
                  </div>
                </div>

                <dl className="kv-grid">
                  <div className="kv-item">
                    <dt>事件类型</dt>
                    <dd className="mono">{activeEvent.kind}</dd>
                  </div>
                  <div className="kv-item">
                    <dt>执行耗时</dt>
                    <dd className="mono tnum">{activeEvent.duration_ms} ms</dd>
                  </div>
                  <div className="kv-item">
                    <dt>调用身份（Actor）</dt>
                    <dd className="mono">{activeEvent.actor}</dd>
                  </div>
                  <div className="kv-item">
                    <dt>发生时间</dt>
                    <dd className="tnum">{formatTime(activeEvent.time)}</dd>
                  </div>
                  {activeEvent.detail?.model && (
                    <div className="kv-item">
                      <dt>路由模型</dt>
                      <dd className="mono">{activeEvent.detail.model}</dd>
                    </div>
                  )}
                  {typeof activeEvent.detail?.eligible === "number" && (
                    <div className="kv-item">
                      <dt>候选池与消耗</dt>
                      <dd className="mono tnum">
                        {activeEvent.detail.eligible} 工具
                        {typeof activeEvent.detail?.prompt_tokens === "number"
                          ? ` · ${(
                              activeEvent.detail.prompt_tokens +
                              (activeEvent.detail.output_tokens || 0)
                            ).toLocaleString()} tok`
                          : ""}
                      </dd>
                    </div>
                  )}
                </dl>

                {activeScope && (
                  <div
                    style={{
                      padding: "12px 16px",
                      borderRadius: 6,
                      background: "#ffffff",
                      border: "1px solid var(--border)",
                      display: "flex",
                      flexDirection: "column",
                      gap: 10,
                    }}
                  >
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "space-between",
                        gap: 8,
                      }}
                    >
                      <span
                        style={{
                          fontSize: 12,
                          fontWeight: 600,
                          color: "var(--ink-muted)",
                        }}
                      >
                        仓库作用域追踪
                      </span>

                      {(activeScope.root || effectiveCommonDir) &&
                      !boundRepoId ? (
                        <button
                          type="button"
                          className="btn btn-sm"
                          disabled={bindingRoot}
                          onClick={() => void handleBindFromScope()}
                        >
                          {bindingRoot && (
                            <Loader2 size={12} className="spin" />
                          )}
                          绑定此仓库
                        </button>
                      ) : boundRepoId && onOpenRepo ? (
                        <button
                          type="button"
                          className="btn-link"
                          style={{ fontSize: 12 }}
                          onClick={() => onOpenRepo(boundRepoId)}
                        >
                          查看绑定规则
                        </button>
                      ) : null}
                    </div>

                    <dl
                      style={{
                        display: "grid",
                        gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
                        gap: "8px 16px",
                      }}
                    >
                      <div className="kv-item">
                        <dt>客户端</dt>
                        <dd className="mono">
                          {activeScope.agent
                            ? `${scopeAgentLabel(activeScope.agent)} (${activeScope.agent})`
                            : "—"}
                        </dd>
                      </div>
                      <div className="kv-item">
                        <dt>命中仓库</dt>
                        <dd>
                          {activeScope.repo_name ||
                            alreadyBoundRepo?.name ||
                            (boundRepoId
                              ? boundRepoId
                              : activeScope.root || effectiveCommonDir
                                ? "未绑定仓库"
                                : "—")}
                        </dd>
                      </div>
                      <div className="kv-item">
                        <dt>命中规则</dt>
                        <dd className="mono">
                          {activeScope.matched_rule || "—"}
                        </dd>
                      </div>
                      <div className="kv-item">
                        <dt>验证状态</dt>
                        <dd>
                          {activeScope.status === "scope_required" ||
                          activeScope.status === "scope_invalid" ||
                          activeScope.status === "ambiguous_workspace" ? (
                            <Badge tone="danger">
                              <span className="mono">{activeScope.status}</span>
                            </Badge>
                          ) : (
                            <span className="mono">
                              {activeScope.status || "—"}
                            </span>
                          )}
                        </dd>
                      </div>
                      {activeScope.root && (
                        <div
                          className="kv-item"
                          style={{ gridColumn: "1 / -1" }}
                        >
                          <dt>工作区根路径</dt>
                          <dd className="mono">{activeScope.root}</dd>
                        </div>
                      )}
                      {effectiveCommonDir && (
                        <div
                          className="kv-item"
                          style={{ gridColumn: "1 / -1" }}
                        >
                          <dt>Git Common Dir</dt>
                          <dd className="mono">{effectiveCommonDir}</dd>
                        </div>
                      )}
                    </dl>
                  </div>
                )}

                {activeEvent.detail?.summary && (
                  <AlertBox
                    tone="info"
                    title={String(activeEvent.detail.summary)}
                  />
                )}

                {activeEvent.detail?.error && (
                  <AlertBox
                    tone="error"
                    title={String(activeEvent.detail.error)}
                  />
                )}

                {Array.isArray(activeEvent.detail?.tools) &&
                  activeEvent.detail.tools.length > 0 && (
                    <div
                      style={{
                        padding: "12px 14px",
                        borderRadius: 6,
                        background: "#fff",
                        border: "1px solid var(--border)",
                      }}
                    >
                      <div
                        style={{
                          fontSize: 12,
                          fontWeight: 600,
                          color: "var(--ink-muted)",
                          marginBottom: 8,
                        }}
                      >
                        命中的工具（{activeEvent.detail.tools.length}）
                      </div>
                      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                        {activeEvent.detail.tools.map((t: any, idx: number) => (
                          <Badge key={idx} tone="accent">
                            <span className="mono">
                              {typeof t === "string" ? t : t.id || t.name}
                            </span>
                          </Badge>
                        ))}
                      </div>
                    </div>
                  )}

                <div className="navy-card">
                  <div className="navy-card-header">
                    <span className="navy-card-label">EVENT PAYLOAD</span>
                  </div>
                  <pre className="navy-pre" style={{ maxHeight: 420 }}>
                    {pretty(activeEvent)}
                  </pre>
                </div>
              </>
            ) : (
              <div className="empty-box">
                <p>在左侧选择一条记录查看完整负载。</p>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

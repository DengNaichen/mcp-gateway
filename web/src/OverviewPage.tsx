import { useState } from "react";
import { Loader2, X } from "lucide-react";
import {
  computeGatewayTelemetry,
  formatCompactTokens,
} from "./analytics";
import {
  api,
  formatTime,
  pretty,
  serverProtocolLabel,
  type Bootstrap,
  type Discovery,
} from "./types";
import { AlertBox, CopyButton } from "./ui";

type Props = {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  onOpenUpstreams: (opts?: { add?: boolean; serverId?: string }) => void;
  onOpenCatalog: (opts?: { filter?: "rw" }) => void;
  onOpenPlayground: (replay?: { intent: string; actor?: string }) => void;
  onOpenLogs: (eventId?: string) => void;
  onOpenPolicies: () => void;
  onOpenConnectModal: () => void;
  notify: (msg: string) => void;
};

export function OverviewPage({
  state,
  refresh,
  onOpenUpstreams,
  onOpenCatalog,
  onOpenPlayground,
  onOpenLogs,
  onOpenPolicies,
  onOpenConnectModal,
  notify,
}: Props) {
  const [bannerDismissed, setBannerDismissed] = useState(false);
  const [testingModel, setTestingModel] = useState(false);
  const [testError, setTestError] = useState<any>(null);

  const realServers = state.registry.servers.filter((s) => !s.demo);
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
  const modelReady = setup.model_ready && !setup.model_check?.stale;
  const telemetry = computeGatewayTelemetry(state);
  const maxTopHits = telemetry.topTools[0]?.hits || 1;

  async function quickVerifyModel() {
    setTestingModel(true);
    setTestError(null);
    try {
      await api<Discovery>("/setup/model", {
        method: "POST",
        body: pretty({
          ...settings,
          api_key: "",
          clear_key: false,
          intent: "",
        }),
      });
      await refresh();
      notify("路由模型连通性校验通过");
    } catch (e: any) {
      setTestError(e);
    } finally {
      setTestingModel(false);
    }
  }

  const showSetupBanner =
    !bannerDismissed && (!setup.tools_ready || !modelReady);

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>运行大盘</h1>
          <p>
            实时监控语义路由命中率、工具上下文收敛效率、上游连接健康度与未命中意图盲区。
          </p>
        </div>
        <div className="page-header-actions">
          <button
            type="button"
            className="btn"
            onClick={() => onOpenPlayground()}
          >
            路由沙盒
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => onOpenUpstreams({ add: true })}
          >
            添加上游服务
          </button>
        </div>
      </div>

      {/* Slim Day-0 / Unverified Banner — collapses completely once verified or dismissed */}
      {showSetupBanner && (
        <div className="setup-banner">
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <strong style={{ fontSize: 13, color: "var(--ink-strong)" }}>
              {!setup.tools_ready
                ? "网关尚未向 Agent 开放任何上游工具"
                : !setup.model_configured
                  ? "尚未配置 Decisions 语义路由模型"
                  : `路由模型 ${settings.model} 尚未完成连通性校验`}
            </strong>
            <span style={{ fontSize: 12.5, color: "var(--ink-muted)" }}>
              {!setup.tools_ready
                ? "请先接入至少一个 MCP 或 HTTP 服务，并在白名单中启用需要暴露的工具。"
                : !setup.model_configured
                  ? "前往「访问与路由」配置模型 Base URL 与密钥后即可启用按需工具发现。"
                  : state.key_configured
                    ? "API Key 已就绪，运行一次真实工具发现校验即可确认控制面就绪。"
                    : "当前进程未检测到模型 API Key，请前往「访问与路由」注入密钥并完成校验。"}
            </span>
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {!setup.tools_ready ? (
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => onOpenUpstreams({ add: realServers.length === 0 })}
              >
                {realServers.length === 0 ? "接入服务" : "启用工具"}
              </button>
            ) : setup.model_configured && state.key_configured ? (
              <button
                type="button"
                className="btn btn-sm"
                disabled={testingModel}
                onClick={quickVerifyModel}
              >
                {testingModel && <Loader2 size={12} className="spin" />}
                {testingModel ? "校验中…" : "立即校验"}
              </button>
            ) : null}
            {setup.tools_ready && (
              <button
                type="button"
                className="btn btn-sm"
                onClick={onOpenPolicies}
              >
                配置路由模型
              </button>
            )}
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              aria-label="收起提示"
              onClick={() => setBannerDismissed(true)}
            >
              <X size={14} />
            </button>
          </div>
        </div>
      )}

      {testError && (
        <div style={{ marginBottom: 16 }}>
          <AlertBox
            tone="error"
            title={testError.message || String(testError)}
            detail={testError.detail}
          />
        </div>
      )}

      {/* Row 1: 4 Enterprise Gateway Golden Signal KPI Cards */}
      <div className="kpi-grid-4">
        <div className="card metric-card">
          <div className="metric-card-top">
            <span>语义路由命中率</span>
            <span className="tnum" style={{ fontSize: 12 }}>
              近 {telemetry.discoverCount} 次发现
            </span>
          </div>
          <div className="metric-number">
            {telemetry.hitRatePct !== null
              ? `${telemetry.hitRatePct.toFixed(1)}%`
              : "—"}
            {telemetry.discoverCount > 0 && (
              <small>
                ({telemetry.selectedCount} / {telemetry.discoverCount})
              </small>
            )}
          </div>
          <div className="metric-foot tnum">
            <span>
              {telemetry.noMatchCount} 未命中 · {telemetry.needsContextCount}{" "}
              待补上下文
            </span>
            {telemetry.errorCount > 0 && (
              <span
                className="status-inline"
                style={{ color: "var(--danger-ink)" }}
              >
                <span className="status-dot err" />
                {telemetry.errorCount} 错误
              </span>
            )}
          </div>
        </div>

        <div className="card metric-card">
          <div className="metric-card-top">
            <span>工具上下文收敛率</span>
            <span style={{ fontSize: 12 }}>按需暴露 ROI</span>
          </div>
          <div className="metric-number">
            {telemetry.compressionPct !== null
              ? `${telemetry.compressionPct.toFixed(1)}%`
              : "—"}
            <small>Schema 缩减</small>
          </div>
          <div className="metric-foot tnum">
            <span>
              {telemetry.avgEligible !== null &&
              telemetry.avgSelectedOnHit !== null
                ? `平均 ${Math.round(telemetry.avgEligible)} 候选 → 下发 ${telemetry.avgSelectedOnHit.toFixed(1)} 个`
                : `单次下发上限 ${settings.max_tools} 个工具`}
            </span>
          </div>
        </div>

        <div className="card metric-card">
          <div className="metric-card-top">
            <span>路由决策延迟（P50）</span>
            <span className="mono" style={{ fontSize: 12 }}>
              {settings.model || "未配置"}
            </span>
          </div>
          <div className="metric-number">
            {telemetry.p50DiscoverMs !== null
              ? `${telemetry.p50DiscoverMs.toLocaleString()} ms`
              : "—"}
            {telemetry.p95DiscoverMs !== null && (
              <small>P95 {telemetry.p95DiscoverMs.toLocaleString()} ms</small>
            )}
          </div>
          <div className="metric-foot tnum">
            <span>
              {telemetry.avgInvokeMs !== null
                ? `下游调用均值 ${telemetry.avgInvokeMs} ms`
                : `累计路由消耗 ${formatCompactTokens(telemetry.totalTokens)} tok`}
            </span>
            <button
              type="button"
              className="btn-link"
              onClick={() => onOpenLogs()}
            >
              追踪明细
            </button>
          </div>
        </div>

        <div className="card metric-card">
          <div className="metric-card-top">
            <span>开放工具面与护栏</span>
            <span className="tnum" style={{ fontSize: 12 }}>
              {activeRealServers.length} / {realServers.length} 服务在线
            </span>
          </div>
          <div className="metric-number">
            {setup.enabled_tools}
            <small>/ {realTools.length} 已开放</small>
          </div>
          <div className="metric-foot tnum">
            <span>
              {readOnlyEnabled} 只读 · {writeEnabled} 写操作
            </span>
            <button
              type="button"
              className="btn-link"
              onClick={() =>
                onOpenCatalog(writeEnabled > 0 ? { filter: "rw" } : undefined)
              }
            >
              治理目录
            </button>
          </div>
        </div>
      </div>

      {/* Row 2: Traffic Outcome Proportion Bar */}
      {telemetry.sampleSize > 0 && (
        <section className="card dist-strip" aria-label="请求状态分布">
          <div className="dist-strip-head">
            <strong style={{ color: "var(--ink-strong)", fontWeight: 600 }}>
              最近 {telemetry.sampleSize} 次网关请求状态分布
            </strong>
            <div className="dist-legend">
              <span className="dist-legend-item">
                <span
                  className="status-dot"
                  style={{ background: "var(--ink-strong)" }}
                />
                命中下发 {telemetry.selectedCount}
              </span>
              <span className="dist-legend-item">
                <span className="status-dot" style={{ background: "#94a3b8" }} />
                需补充上下文 {telemetry.needsContextCount}
              </span>
              <span className="dist-legend-item">
                <span className="status-dot" style={{ background: "#cbd5e1" }} />
                未命中 {telemetry.noMatchCount}
              </span>
              {telemetry.errorCount > 0 && (
                <span
                  className="dist-legend-item"
                  style={{ color: "var(--danger-ink)" }}
                >
                  <span className="status-dot err" />
                  异常错误 {telemetry.errorCount}
                </span>
              )}
              <button
                type="button"
                className="btn-link"
                onClick={() => onOpenLogs()}
              >
                全部请求日志
              </button>
            </div>
          </div>

          <div className="dist-bar" role="presentation">
            {telemetry.selectedCount > 0 && (
              <div
                className="dist-seg-selected"
                style={{ flexGrow: telemetry.selectedCount }}
                title={`命中 selected: ${telemetry.selectedCount}`}
              />
            )}
            {telemetry.needsContextCount > 0 && (
              <div
                className="dist-seg-context"
                style={{ flexGrow: telemetry.needsContextCount }}
                title={`待补充上下文 needs_context: ${telemetry.needsContextCount}`}
              />
            )}
            {telemetry.noMatchCount > 0 && (
              <div
                className="dist-seg-nomatch"
                style={{ flexGrow: telemetry.noMatchCount }}
                title={`未命中 no_match: ${telemetry.noMatchCount}`}
              />
            )}
            {telemetry.errorCount > 0 && (
              <div
                className="dist-seg-error"
                style={{ flexGrow: Math.max(telemetry.errorCount, 1.5) }}
                title={`错误 error: ${telemetry.errorCount}`}
              />
            )}
          </div>
        </section>
      )}

      {/* Row 3: Control Plane 2-Column Operational Split */}
      <div className="control-split">
        {/* Left: Actionable Triage (Errors & Blind Spots) + Top Routed Tools */}
        <div className="form-stack">
          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>异常告警与未命中意图盲区</h2>
                <p>
                  聚合近期网关错误及 Agent 发起但未匹配到开放工具的任务，支持一键带入沙盒复现。
                </p>
              </div>
              <span
                className="tnum"
                style={{ fontSize: 12, color: "var(--ink-muted)" }}
              >
                {telemetry.attentionItems.length} 项待关注
              </span>
            </div>

            {telemetry.attentionItems.length === 0 ? (
              <div className="empty-box">
                <h3>当前无异常或未命中盲区</h3>
                <p>近期所有工具发现与调用请求均正常命中。</p>
              </div>
            ) : (
              <div className="table-wrap">
                <table className="sail-table">
                  <thead>
                    <tr>
                      <th>状态</th>
                      <th>任务意图 / 诊断摘要</th>
                      <th>身份</th>
                      <th style={{ textAlign: "right" }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {telemetry.attentionItems.map((item) => (
                      <tr key={`${item.status}-${item.lastEventId}`}>
                        <td style={{ width: 120 }}>
                          <EventStatusBadge status={item.status} />
                        </td>
                        <td>
                          <span className="cell-primary">
                            <span>{item.intent}</span>
                            {item.count > 1 && (
                              <span
                                className="mono tnum"
                                style={{
                                  fontSize: 12,
                                  fontWeight: 500,
                                  color: "var(--ink-muted)",
                                }}
                              >
                                ×{item.count}
                              </span>
                            )}
                          </span>
                          <span className="cell-sub">
                            {item.errorSummary
                              ? item.errorSummary
                              : `${formatTime(item.lastTime)} · 当前开放工具池未匹配到合适工具（或对应写操作已被护栏拦截）`}
                          </span>
                        </td>
                        <td style={{ width: 96 }}>
                          <code
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {item.actor}
                          </code>
                        </td>
                        <td style={{ width: 156, textAlign: "right" }}>
                          <div
                            style={{
                              display: "inline-flex",
                              gap: 6,
                              justifyContent: "flex-end",
                            }}
                          >
                            <button
                              type="button"
                              className="btn btn-sm"
                              onClick={() => onOpenLogs(item.lastEventId)}
                            >
                              日志
                            </button>
                            <button
                              type="button"
                              className="btn btn-sm"
                              onClick={() =>
                                onOpenPlayground({
                                  intent: item.intent,
                                  actor: item.actor,
                                })
                              }
                            >
                              沙盒重放
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>高频命中工具（Top Routed Tools）</h2>
                <p>按近期语义路由选中频次排序，识别团队高频依赖能力与冗余长尾工具。</p>
              </div>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => onOpenCatalog()}
              >
                全部工具目录
              </button>
            </div>

            {telemetry.topTools.length === 0 ? (
              <div className="empty-box">
                <p>暂无路由命中统计。</p>
              </div>
            ) : (
              <div className="table-wrap">
                <table className="sail-table">
                  <thead>
                    <tr>
                      <th>工具名称</th>
                      <th>所属上游</th>
                      <th>权限属性</th>
                      <th style={{ textAlign: "right" }}>命中频次</th>
                    </tr>
                  </thead>
                  <tbody>
                    {telemetry.topTools.map((t) => (
                      <tr key={t.id}>
                        <td>
                          <span className="cell-primary mono">{t.name}</span>
                        </td>
                        <td style={{ width: 120 }}>
                          <button
                            type="button"
                            className="btn-link"
                            onClick={() =>
                              onOpenUpstreams({ serverId: t.serverId })
                            }
                          >
                            {t.serverName}
                          </button>
                        </td>
                        <td style={{ width: 90 }}>
                          <span
                            style={{
                              fontSize: 12,
                              color: t.readOnly
                                ? "var(--ink-muted)"
                                : "var(--ink-body)",
                            }}
                          >
                            {t.readOnly ? "只读" : "写操作"}
                          </span>
                        </td>
                        <td style={{ width: 160 }}>
                          <div
                            style={{
                              display: "flex",
                              alignItems: "center",
                              justifyContent: "flex-end",
                              gap: 10,
                            }}
                          >
                            <div className="hit-bar-track">
                              <div
                                className="hit-bar-fill"
                                style={{
                                  width: `${Math.max(
                                    8,
                                    Math.round((t.hits / maxTopHits) * 100),
                                  )}%`,
                                }}
                              />
                            </div>
                            <span
                              className="mono tnum"
                              style={{
                                fontSize: 12,
                                color: "var(--ink-strong)",
                                minWidth: 38,
                                textAlign: "right",
                              }}
                            >
                              {t.hits} 次
                            </span>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>

        {/* Right: Upstream Services Health Topology + Compact MCP Endpoint */}
        <div className="form-stack">
          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>上游服务拓扑</h2>
                <p>已注册的下游 MCP 与 HTTP 服务实例。</p>
              </div>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => onOpenUpstreams()}
              >
                管理上游
              </button>
            </div>

            {realServers.length === 0 ? (
              <div className="empty-box">
                <h3>尚未接入上游服务</h3>
                <p>支持远程 Streamable HTTP MCP、stdio 进程或 REST/OpenAPI 服务。</p>
                <button
                  type="button"
                  className="btn btn-primary btn-sm"
                  style={{ marginTop: 6 }}
                  onClick={() => onOpenUpstreams({ add: true })}
                >
                  添加第一个上游
                </button>
              </div>
            ) : (
              <div>
                {realServers.map((s) => {
                  const srvTools = realTools.filter((t) => t.server_id === s.id);
                  const openTools = srvTools.filter((t) => t.enabled);
                  const openWrite = openTools.filter((t) => !t.read_only).length;
                  const check = state.setup.source_checks[s.id];
                  const hasErr = check?.status === "error";
                  return (
                    <div
                      key={s.id}
                      className="log-row"
                      style={{ cursor: "pointer" }}
                      onClick={() => onOpenUpstreams({ serverId: s.id })}
                    >
                      <div className="log-row-main">
                        <div className="log-row-top">
                          <span
                            className={`status-dot ${
                              hasErr ? "err" : s.enabled ? "ok" : ""
                            }`}
                          />
                          <strong
                            style={{
                              fontSize: 13.5,
                              color: "var(--ink-strong)",
                            }}
                          >
                            {s.name}
                          </strong>
                          <span
                            className="mono"
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {serverProtocolLabel(s)}
                          </span>
                        </div>
                        <span className="log-row-meta tnum">
                          开放 {openTools.length} / {srvTools.length} 工具 ·{" "}
                          {openTools.length - openWrite} 只读 / {openWrite} 写操作
                        </span>
                      </div>
                      <span
                        style={{
                          fontSize: 12,
                          color: "var(--accent)",
                          fontWeight: 500,
                          flexShrink: 0,
                        }}
                      >
                        详情
                      </span>
                    </div>
                  );
                })}
              </div>
            )}
          </section>

          <section className="navy-card" aria-label="网关数据面端点">
            <div className="navy-card-header">
              <span className="navy-card-label">
                <span className="sail-status-dot" />
                DATA PLANE ENDPOINT
              </span>
              <CopyButton
                value={state.mcp_url}
                label="复制地址"
                variant="navy"
                onCopied={() => notify("已复制 MCP 网关地址")}
              />
            </div>
            <pre className="navy-pre" style={{ padding: "12px 14px" }}>
              {state.mcp_url}
            </pre>
            <div className="navy-footer">
              <span>
                统一暴露 3 个元工具收敛上下文
              </span>
              <button
                type="button"
                className="navy-copy-btn"
                onClick={onOpenConnectModal}
              >
                客户端接入指令
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

export function EventStatusBadge({ status }: { status: string }) {
  const isError = status === "error" || status === "failed";
  const isWarn = status === "needs_context";
  const isNeutral = status === "no_match";
  const dotClass = isError
    ? "err"
    : isWarn
      ? "warn"
      : isNeutral
        ? ""
        : "ok";
  return (
    <span
      className="status-inline mono"
      style={{
        color: isError
          ? "var(--danger-ink)"
          : isNeutral
            ? "var(--ink-muted)"
            : "var(--ink-body)",
      }}
    >
      <span className={`status-dot ${dotClass}`} />
      {status}
    </span>
  );
}

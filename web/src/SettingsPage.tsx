import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { computeActorMatrix } from "./analytics";
import {
  api,
  formatTime,
  pretty,
  type Bootstrap,
  type Config,
  type Discovery,
} from "./types";
import { AlertBox } from "./ui";

export function SettingsPage({
  state,
  refresh,
  onOpenServices,
  onOpenCatalog,
  notify,
}: {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  onOpenServices: () => void;
  onOpenCatalog?: (opts?: { access?: "rw" }) => void;
  notify: (msg: string) => void;
}) {
  const [config, setConfig] = useState<Config>({ ...state.settings });
  const [apiKey, setApiKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [testIntent, setTestIntent] = useState("");
  const [busyMode, setBusyMode] = useState<"" | "save" | "test">("");
  const [guardrailBusy, setGuardrailBusy] = useState(false);
  const [error, setError] = useState<any>(null);
  const [testResult, setTestResult] = useState<Discovery | null>(null);

  useEffect(() => {
    setConfig({ ...state.settings });
  }, [
    state.settings.base_url,
    state.settings.model,
    state.settings.max_tools,
    state.settings.timeout_seconds,
  ]);

  const dirty =
    pretty(config) !== pretty(state.settings) || Boolean(apiKey) || clearKey;

  function patch(next: Partial<Config>) {
    setConfig((c) => ({ ...c, ...next }));
    setTestResult(null);
  }

  async function submit(runTest: boolean) {
    setBusyMode(runTest ? "test" : "save");
    setError(null);
    setTestResult(null);
    try {
      if (!config.model.trim()) {
        throw new Error("请填写路由模型名称（如 gpt-6-luna 或 gemini-decision-1）。");
      }
      const payload = {
        ...config,
        api_key: apiKey,
        clear_key: clearKey,
        intent: testIntent,
      };
      if (runTest) {
        const res = await api<Discovery>("/setup/model", {
          method: "POST",
          body: pretty(payload),
        });
        setTestResult(res);
        notify("路由配置已保存并通过实时校验");
      } else {
        await api("/settings", {
          method: "PUT",
          body: pretty(payload),
        });
        notify("路由配置已保存");
      }
      setApiKey("");
      setClearKey(false);
      await refresh();
    } catch (err) {
      setError(err);
    } finally {
      setBusyMode("");
    }
  }

  const modelCheck = state.setup.model_check;
  const actorRows = computeActorMatrix(
    state.registry.servers,
    state.registry.tools,
    state.access?.shared ? state.access.actor : undefined,
  );
  const realActiveServers = state.registry.servers.filter(
    (s) => !s.demo && s.enabled,
  );
  const enabledWriteTools = state.registry.tools.filter(
    (t) =>
      t.enabled &&
      !t.read_only &&
      realActiveServers.some((s) => s.id === t.server_id),
  );

  async function enforceReadOnlyGuardrail() {
    if (enabledWriteTools.length === 0 || guardrailBusy) return;
    setGuardrailBusy(true);
    try {
      const affectedServerIds = Array.from(
        new Set(enabledWriteTools.map((t) => t.server_id)),
      );
      for (const serverId of affectedServerIds) {
        const keepIds = state.registry.tools
          .filter(
            (t) => t.server_id === serverId && t.enabled && t.read_only,
          )
          .map((t) => t.id);
        await api(`/servers/${encodeURIComponent(serverId)}/tools`, {
          method: "POST",
          body: pretty({ ids: keepIds }),
        });
      }
      await refresh();
      notify(`已关闭 ${enabledWriteTools.length} 个写操作工具，全局候选池已收敛为只读`);
    } catch (e: any) {
      notify(e.message);
    } finally {
      setGuardrailBusy(false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>访问与路由</h1>
          <p>
            审计各调用方身份（Actor）的工具可见范围与写操作护栏，并配置 Decisions 语义路由模型与分流阈值。
          </p>
        </div>
      </div>

      {/* Section 1: Consumer Actor & Read/Write Guardrail Matrix */}
      <div className="settings-section">
        <div className="settings-aside">
          <h2>调用身份与写操作护栏</h2>
          <p>
            网关在路由与调用两阶段强制校验调用方 <code>actor</code>{" "}
            白名单。不在候选池内的工具既不会暴露给模型，也无法通过{" "}
            <code>invoke_tool</code> 越权调用。
          </p>
          <p style={{ marginTop: 8 }}>
            接入鉴权：{" "}
            <strong style={{ color: "var(--ink-strong)", fontWeight: 550 }}>
              {state.access?.shared
                ? `共享网关鉴权（${state.access.actor}）`
                : "本机直连（默认 developer）"}
            </strong>
          </p>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: 6,
              marginTop: 12,
            }}
          >
            {onOpenCatalog && (
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => onOpenCatalog({ access: "rw" })}
              >
                审查写操作工具（{enabledWriteTools.length}）
              </button>
            )}
            {enabledWriteTools.length > 0 && (
              <button
                type="button"
                className="btn btn-sm"
                disabled={guardrailBusy}
                onClick={() => void enforceReadOnlyGuardrail()}
              >
                {guardrailBusy && <Loader2 size={12} className="spin" />}
                全局收敛为仅只读
              </button>
            )}
          </div>
        </div>

        <section className="card">
          <div className="card-header">
            <div className="card-header-title">
              <h2>身份访问矩阵（实时生效）</h2>
              <p>基于当前在线上游服务与工具开放状态自动汇总。</p>
            </div>
            <span
              className="tnum"
              style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
            >
              {enabledWriteTools.length === 0
                ? "写操作护栏：全部只读"
                : `已开放 ${enabledWriteTools.length} 个写工具`}
            </span>
          </div>
          <div className="table-wrap">
            <table className="sail-table">
              <thead>
                <tr>
                  <th>身份（Actor）</th>
                  <th>可达上游</th>
                  <th>只读工具</th>
                  <th>写操作工具</th>
                  <th>按上游分布</th>
                </tr>
              </thead>
              <tbody>
                {actorRows.map((row) => (
                  <tr key={row.actor}>
                    <td style={{ width: 150 }}>
                      <code
                        style={{
                          fontSize: 12.5,
                          fontWeight: 600,
                          color: "var(--ink-strong)",
                        }}
                      >
                        {row.actor}
                      </code>
                    </td>
                    <td className="tnum" style={{ width: 105 }}>
                      {row.reachableServers} 个服务
                    </td>
                    <td className="tnum" style={{ width: 105 }}>
                      {row.readOnlyTools}
                    </td>
                    <td className="tnum" style={{ width: 115 }}>
                      <span
                        style={{
                          color:
                            row.writeTools > 0
                              ? "var(--ink-strong)"
                              : "var(--ink-muted)",
                          fontWeight: row.writeTools > 0 ? 600 : 400,
                        }}
                      >
                        {row.writeTools}
                      </span>
                    </td>
                    <td>
                      {row.byServer.length === 0 ? (
                        <span
                          style={{ fontSize: 12.5, color: "var(--ink-faint)" }}
                        >
                          无可见工具
                        </span>
                      ) : (
                        <span
                          className="tnum"
                          style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
                        >
                          {row.byServer
                            .map(
                              (s) =>
                                `${s.serverName} ${s.readOnly + s.write}${s.write > 0 ? `（${s.write} 写）` : ""}`,
                            )
                            .join(" · ")}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit(state.setup.tools_ready);
        }}
      >
        {/* Section 1: Decisions Model Credentials */}
        <div className="settings-section">
          <div className="settings-aside">
            <h2>Decisions 路由模型</h2>
            <p>
              模型名称以 <code>gemini-</code> 开头时自动使用 Google Gemini Decisions
              协议，其余模型使用 OpenAI Decisions 协议（自动补全{" "}
              <code>/decisions</code>）。
            </p>
            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                gap: 6,
                marginTop: 12,
              }}
            >
              <button
                type="button"
                className="btn btn-sm"
                onClick={() =>
                  patch({
                    base_url: "https://api.openai.com/v1",
                    model: "gpt-6-luna",
                  })
                }
              >
                填入 OpenAI 预设
              </button>
              <button
                type="button"
                className="btn btn-sm"
                onClick={() =>
                  patch({
                    base_url: "https://generativelanguage.googleapis.com/v1beta",
                    model: "gemini-decision-1",
                  })
                }
              >
                填入 Gemini 预设
              </button>
            </div>
          </div>

          <section className="card">
            <div className="card-body form-stack">
              <div className="form-grid-2">
                <label className="field">
                  <span className="field-label">Base URL</span>
                  <input
                    required
                    type="url"
                    className="input mono"
                    placeholder="https://api.openai.com/v1"
                    value={config.base_url}
                    onChange={(e) => patch({ base_url: e.target.value })}
                  />
                </label>

                <label className="field">
                  <span className="field-label">模型名称（Model ID）</span>
                  <input
                    required
                    className="input mono"
                    placeholder="gpt-6-luna 或 gemini-decision-1"
                    value={config.model}
                    onChange={(e) => patch({ model: e.target.value })}
                  />
                </label>
              </div>

              <label className="field">
                <span className="field-label">
                  API Key
                  <em>
                    {state.key_configured
                      ? "当前内存已加载 Key"
                      : "未检测到 Key"}
                  </em>
                </span>
                <input
                  type="password"
                  autoComplete="new-password"
                  className="input mono"
                  placeholder={
                    state.key_configured
                      ? "留空则继续使用已加载的 Key"
                      : "粘贴 API Key（仅保留在后端进程内存中）"
                  }
                  value={apiKey}
                  onChange={(e) => {
                    setApiKey(e.target.value);
                    setClearKey(false);
                    setTestResult(null);
                  }}
                />
                <span className="field-hint">
                  密钥不会写入 SQLite 或浏览器存储；长期运行可通过环境变量{" "}
                  <code>GATEWAY_MODEL_API_KEY</code> 注入。
                </span>
              </label>

              {state.key_configured && (
                <label className="checkbox-field">
                  <input
                    type="checkbox"
                    checked={clearKey}
                    onChange={(e) => {
                      setClearKey(e.target.checked);
                      if (e.target.checked) setApiKey("");
                    }}
                  />
                  保存时清除当前内存中的 API Key
                </label>
              )}
            </div>
          </section>
        </div>

        {/* Section 2: Routing Limits & Verification */}
        <div className="settings-section">
          <div className="settings-aside">
            <h2>路由策略与实时校验</h2>
            <p>
              控制每次向本地 Agent 返回的最大候选工具数（相关性概率 ≥ 0.5
              按降序截断），并可立即用真实已启用工具验证连通性。
            </p>
          </div>

          <section className="card">
            <div className="card-body form-stack">
              <div className="form-grid-2">
                <label className="field">
                  <span className="field-label">单次返回工具上限（1–12）</span>
                  <input
                    type="number"
                    min={1}
                    max={12}
                    required
                    className="input tnum"
                    value={config.max_tools}
                    onChange={(e) => patch({ max_tools: Number(e.target.value) })}
                  />
                </label>

                <label className="field">
                  <span className="field-label">模型请求超时（秒）</span>
                  <input
                    type="number"
                    min={5}
                    max={180}
                    required
                    className="input tnum"
                    value={config.timeout_seconds}
                    onChange={(e) =>
                      patch({ timeout_seconds: Number(e.target.value) })
                    }
                  />
                </label>
              </div>

              <label className="field">
                <span className="field-label">
                  自定义校验意图 <em>可选，留空自动根据已启用工具生成</em>
                </span>
                <input
                  className="input"
                  placeholder="例如：查看最近需要 review 的 pull request"
                  value={testIntent}
                  onChange={(e) => setTestIntent(e.target.value)}
                />
              </label>

              {!state.setup.tools_ready && (
                <AlertBox
                  tone="warning"
                  title="当前尚未启用任何非演示工具，可先「仅保存配置」，启用工具后再运行路由校验。"
                >
                  <div style={{ marginTop: 6 }}>
                    <button
                      type="button"
                      className="btn btn-sm"
                      onClick={onOpenServices}
                    >
                      前往启用工具 →
                    </button>
                  </div>
                </AlertBox>
              )}

              {error && (
                <AlertBox
                  tone="error"
                  title={error.message || String(error)}
                  detail={error.detail}
                />
              )}

              {dirty && modelCheck && (
                <AlertBox
                  tone="info"
                  title="表单参数已修改，点击「保存并校验路由」更新验证状态。"
                />
              )}

              {!dirty && modelCheck && (
                <AlertBox
                  tone={modelCheck.status === "error" ? "error" : "info"}
                  title={
                    modelCheck.stale
                      ? `工具池或参数发生变化，建议重新校验（上次：${formatTime(modelCheck.checked_at)}）`
                      : `${modelCheck.message}（${formatTime(modelCheck.checked_at)}）`
                  }
                  detail={modelCheck.detail}
                />
              )}

              {testResult && (
                <div
                  style={{
                    padding: 14,
                    borderRadius: 6,
                    background: "var(--bg-subtle)",
                    border: "1px solid var(--border)",
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                      marginBottom: 6,
                    }}
                  >
                    <strong style={{ color: "var(--ink-strong)" }}>
                      实时校验响应：{testResult.status}
                    </strong>
                    <span
                      className="mono tnum"
                      style={{ fontSize: 12, color: "var(--ink-muted)" }}
                    >
                      {testResult.duration_ms} ms
                    </span>
                  </div>
                  <p style={{ fontSize: 12.5, color: "var(--ink-muted)" }}>
                    {testResult.summary}
                  </p>
                  {testResult.tools.length > 0 && (
                    <div
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: 4,
                        marginTop: 8,
                        fontSize: 12,
                        color: "var(--ink-body)",
                      }}
                    >
                      {testResult.tools.map((t) => (
                        <div key={t.id}>
                          <code style={{ color: "var(--ink-strong)" }}>
                            {t.id}
                          </code>{" "}
                          <span style={{ color: "var(--ink-muted)" }}>
                            — {t.reason}
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>

            <div className="card-footer">
              <span className="field-hint">
                路由校验仅验证模型响应与工具映射，不执行下游工具。
              </span>

              <div style={{ display: "flex", gap: 8 }}>
                <button
                  type="button"
                  className="btn"
                  disabled={Boolean(busyMode)}
                  onClick={() => void submit(false)}
                >
                  {busyMode === "save" && (
                    <Loader2 size={14} className="spin" />
                  )}
                  仅保存配置
                </button>
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={Boolean(busyMode) || !state.setup.tools_ready}
                >
                  {busyMode === "test" && (
                    <Loader2 size={14} className="spin" />
                  )}
                  {busyMode === "test" ? "正在校验…" : "保存并校验路由"}
                </button>
              </div>
            </div>
          </section>
        </div>
      </form>
    </div>
  );
}

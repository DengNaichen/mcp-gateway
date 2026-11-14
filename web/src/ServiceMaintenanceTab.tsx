import { useEffect, useState } from "react";
import {
  Bot,
  Clock3,
  FileCode2,
  Loader2,
  Play,
  RefreshCw,
  RotateCcw,
  Square,
} from "lucide-react";
import { api, formatTime, pretty, type Server, type Tool } from "./types";
import { AcceptanceEditor } from "./AcceptanceEditor";
import { AlertBox, Badge } from "./ui";

type Repository = {
  url: string;
  ref: string;
  files: string[];
  project?: string;
  events_enabled: boolean;
  webhook_secret_env?: string;
};

const repositoryDefaults: Repository = {
  url: "",
  ref: "main",
  files: [],
  events_enabled: false,
  project: "",
  webhook_secret_env: "",
};

type Source = {
  server_id: string;
  files: string[];
  urls: string[];
  instructions: string;
  enabled: boolean;
  auto_publish: boolean;
  interval_minutes: number;
  timeout_seconds: number;
  repository?: Repository;
  format?: "pi" | "openapi" | "native";
  operation_ids?: string[];
  provider?: string;
  model?: string;
  next_run_at?: string;
};

type Run = {
  id: string;
  server_id: string;
  status: string;
  stage: string;
  created_at: string;
  finished_at?: string;
  published_by?: string;
  published_at?: string;
  rolled_back_by?: string;
  rolled_back_at?: string;
  summary?: string;
  error?: string;
  events: string[];
  artifacts?: Record<string, string>;
  tools?: Tool[];
  acceptance?: {
    configured: boolean;
    passed: boolean;
    results?: {
      name: string;
      kind: string;
      passed: boolean;
      error?: string;
      duration_ms: number;
    }[];
    missing_coverage?: string[];
  };
  provenance?: {
    repository?: string;
    ref?: string;
    commit?: string;
    documents?: Record<string, string>;
  };
  trigger?: {
    kind: string;
    provider?: string;
    delivery_id?: string;
    commit?: string;
  };
  review?: {
    required: boolean;
    reasons?: { tool_id: string; code: string; message: string }[];
  };
};

type MaintenanceState = {
  runtime: {
    available: boolean;
    command: string;
    started: boolean;
    model_source: string;
    execution: { mode: string; image: string };
  };
  sources: Source[];
  runs: Run[];
};

type RunDetail = {
  run: Run;
  changes: { id: string; kind: string; before?: Tool; after?: Tool }[];
};

const runStatusLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  ready: "待确认发布",
  published: "已发布",
  no_change: "无变更",
  failed: "失败",
  cancelled: "已取消",
  interrupted: "已中断",
  rolled_back: "已回退",
};

function defaultSource(server: Server): Source {
  return {
    server_id: server.id,
    format: server.kind === "mcp" ? "native" : "openapi",
    operation_ids: [],
    files: [],
    urls: [],
    instructions: "",
    enabled: false,
    auto_publish: true,
    interval_minutes: 60,
    timeout_seconds: 900,
    provider: "",
    model: "",
  };
}

const parseLines = (text: string) =>
  text
    .split("\n")
    .map((v) => v.trim())
    .filter(Boolean);

export function ServiceMaintenanceTab({
  server,
  refreshRegistry,
  notify,
}: {
  server: Server;
  refreshRegistry: () => Promise<unknown>;
  notify: (msg: string) => void;
}) {
  const [mState, setMState] = useState<MaintenanceState | null>(null);
  const [form, setForm] = useState<Source>(() => defaultSource(server));
  const [filesText, setFilesText] = useState("");
  const [urlsText, setUrlsText] = useState("");
  const [opsText, setOpsText] = useState("");
  const [repoFilesText, setRepoFilesText] = useState("");
  const [loadedServerId, setLoadedServerId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedRunId, setSelectedRunId] = useState("");
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [artifact, setArtifact] = useState<{
    name: string;
    content: string;
  } | null>(null);

  function hydrateForm(data: MaintenanceState, srv: Server) {
    const existing = data.sources.find((s) => s.server_id === srv.id);
    const base = existing || defaultSource(srv);
    const fmt = base.format || (srv.kind === "mcp" ? "native" : "openapi");
    setForm({ ...base, format: fmt });
    setRepoFilesText((base.repository?.files || []).join("\n"));
    setOpsText((base.operation_ids || []).join(", "));
    setFilesText((base.files || []).join("\n"));
    setUrlsText((base.urls || []).join("\n"));
    setLoadedServerId(srv.id);
  }

  useEffect(() => {
    let live = true;
    async function poll() {
      try {
        const data = await api<MaintenanceState>("/maintenance");
        if (!live) return;
        setMState(data);
        if (loadedServerId !== server.id) {
          hydrateForm(data, server);
          const serverRuns = data.runs.filter((r) => r.server_id === server.id);
          setSelectedRunId(serverRuns[0]?.id || "");
          setDetail(null);
          setArtifact(null);
        }
      } catch (e: any) {
        if (live) setError(e.message);
      }
    }
    void poll();
    const timer = setInterval(() => void poll(), 3500);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [server.id, loadedServerId]);

  useEffect(() => {
    if (!selectedRunId) {
      setDetail(null);
      return;
    }
    let live = true;
    api<RunDetail>(`/maintenance/runs/${selectedRunId}`)
      .then((d) => {
        if (live) setDetail(d);
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, [selectedRunId, mState]);

  function updateRepo(patch: Partial<Repository>) {
    setForm((cur) => ({
      ...cur,
      repository: { ...(cur.repository || repositoryDefaults), ...patch },
    }));
  }

  async function saveConfig() {
    await api("/maintenance/sources", {
      method: "PUT",
      body: pretty({
        ...form,
        server_id: server.id,
        repository: form.repository
          ? { ...form.repository, files: parseLines(repoFilesText) }
          : undefined,
        files: parseLines(filesText),
        urls: parseLines(urlsText),
        operation_ids: opsText
          .split(/[\n,]/)
          .map((v) => v.trim())
          .filter(Boolean),
      }),
    });
  }

  async function runAction(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      const fresh = await api<MaintenanceState>("/maintenance");
      setMState(fresh);
      await refreshRegistry();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }

  async function triggerCheck(force: boolean) {
    await runAction(async () => {
      await saveConfig();
      const r = await api<Run>(
        `/maintenance/sources/${encodeURIComponent(server.id)}/run`,
        {
          method: "POST",
          body: pretty({ force }),
        },
      );
      setSelectedRunId(r.id);
      setDetail(null);
      setArtifact(null);
      notify("已加入维护检查队列");
    });
  }

  const runs = (mState?.runs || []).filter((r) => r.server_id === server.id);
  const hasActiveRun = runs.some((r) =>
    ["queued", "running"].includes(r.status),
  );
  const savedSource = mState?.sources.find((s) => s.server_id === server.id);
  const isPi = form.format === "pi";
  const isOpenAPI = form.format === "openapi";
  const isNative = form.format === "native";

  return (
    <div className="form-stack">
      {error && <AlertBox tone="error" title={error} />}

      {isPi && mState && !mState.runtime.available && (
        <AlertBox
          tone="warning"
          title={`未检测到本机 Pi 运行时（命令：${mState.runtime.command}）。请先安装 Pi 1.4+ 或改用确定性同步模式。`}
        />
      )}

      <div className="workbench-split">
        {/* Left: Progressive Maintenance Configuration */}
        <form
          className="card"
          onSubmit={(e) => {
            e.preventDefault();
            void runAction(async () => {
              await saveConfig();
              notify("维护策略已保存");
            });
          }}
        >
          <div className="card-header">
            <div className="card-header-title">
              <h2>自动维护策略</h2>
              <p>跟踪上游接口变更并经独立验收后原子更新工具定义。</p>
            </div>
            {mState && (
              <Badge tone={mState.runtime.available ? "success" : "neutral"} dot>
                {mState.runtime.available ? "Pi 已就绪" : "无 Pi"} ·{" "}
                {mState.runtime.execution.mode === "docker"
                  ? "Docker 隔离"
                  : "本机执行"}
              </Badge>
            )}
          </div>

          <div className="card-body form-stack">
            <div className="field">
              <span className="field-label">同步与维护方式</span>
              <div className="option-cards">
                {server.kind === "mcp" ? (
                  <button
                    type="button"
                    className={`option-card ${isNative ? "selected" : ""}`}
                    onClick={() => setForm({ ...form, format: "native" })}
                  >
                    <RefreshCw size={16} />
                    <span>
                      <strong>原生 MCP 同步</strong>
                      <small>直接拉取下游 tools/list，无需模型</small>
                    </span>
                  </button>
                ) : (
                  <button
                    type="button"
                    className={`option-card ${isOpenAPI ? "selected" : ""}`}
                    onClick={() => setForm({ ...form, format: "openapi" })}
                  >
                    <FileCode2 size={16} />
                    <span>
                      <strong>OpenAPI 确定性导入</strong>
                      <small>解析 OpenAPI 3.0/3.1 JSON 或 YAML</small>
                    </span>
                  </button>
                )}
                <button
                  type="button"
                  className={`option-card ${isPi ? "selected" : ""}`}
                  onClick={() => setForm({ ...form, format: "pi" })}
                >
                  <Bot size={16} />
                  <span>
                    <strong>Pi 编程适配</strong>
                    <small>在隔离容器中生成与维护 .mjs 适配脚本</small>
                  </span>
                </button>
              </div>
            </div>

            {(isNative || isOpenAPI) && (
              <label className="field">
                <span className="field-label">
                  工具 / operationId 范围过滤 <em>可选，逗号分隔</em>
                </span>
                <input
                  className="input mono"
                  value={opsText}
                  onChange={(e) => setOpsText(e.target.value)}
                  placeholder={
                    isNative
                      ? "例如：list_issues, pull_request_read（留空导入全部）"
                      : "例如：pipelines, jobs, job_trace（留空导入全部）"
                  }
                />
              </label>
            )}

            {/* Source documents only needed for OpenAPI or Pi */}
            {(isOpenAPI || isPi) && (
              <>
                <label className="field">
                  <span className="field-label">
                    本地规格或参考文件 <em>每行一个绝对路径</em>
                  </span>
                  <textarea
                    className="textarea mono"
                    rows={2}
                    value={filesText}
                    onChange={(e) => setFilesText(e.target.value)}
                    placeholder="/absolute/path/to/openapi.yaml"
                  />
                </label>

                <label className="field">
                  <span className="field-label">
                    在线文档 URL <em>每行一个 HTTP(S) 地址，勿含密钥</em>
                  </span>
                  <textarea
                    className="textarea mono"
                    rows={2}
                    value={urlsText}
                    onChange={(e) => setUrlsText(e.target.value)}
                    placeholder="https://service.example.com/openapi.json"
                  />
                </label>

                <label className="checkbox-field">
                  <input
                    type="checkbox"
                    checked={Boolean(form.repository)}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        repository: e.target.checked
                          ? { ...repositoryDefaults }
                          : undefined,
                      })
                    }
                  />
                  从 Git 仓库读取规格文件（支持 Webhook 事件触发）
                </label>

                {form.repository && (
                  <div
                    className="form-stack"
                    style={{
                      padding: 14,
                      borderRadius: 6,
                      background: "var(--bg-subtle)",
                      border: "1px solid var(--border)",
                    }}
                  >
                    <div className="form-grid-2">
                      <label className="field">
                        <span className="field-label">仓库地址</span>
                        <input
                          className="input mono"
                          value={form.repository.url}
                          onChange={(e) => updateRepo({ url: e.target.value })}
                          placeholder="https://git.example/team/api.git"
                        />
                      </label>
                      <label className="field">
                        <span className="field-label">分支或 Commit</span>
                        <input
                          className="input mono"
                          value={form.repository.ref}
                          onChange={(e) => updateRepo({ ref: e.target.value })}
                        />
                      </label>
                    </div>
                    <label className="field">
                      <span className="field-label">
                        仓库内相对路径 <em>每行一个</em>
                      </span>
                      <textarea
                        className="textarea mono"
                        rows={2}
                        value={repoFilesText}
                        onChange={(e) => setRepoFilesText(e.target.value)}
                        placeholder="api/openapi.yaml"
                      />
                    </label>
                    <label className="checkbox-field">
                      <input
                        type="checkbox"
                        checked={form.repository.events_enabled}
                        onChange={(e) =>
                          updateRepo({ events_enabled: e.target.checked })
                        }
                      />
                      开启 GitHub / GitLab / 发布 Webhook 触发
                    </label>
                    {form.repository.events_enabled && (
                      <div className="form-grid-2">
                        <label className="field">
                          <span className="field-label">项目标识</span>
                          <input
                            className="input mono"
                            value={form.repository.project || ""}
                            onChange={(e) =>
                              updateRepo({ project: e.target.value })
                            }
                            placeholder="team/service"
                          />
                        </label>
                        <label className="field">
                          <span className="field-label">密钥环境变量名</span>
                          <input
                            className="input mono"
                            value={form.repository.webhook_secret_env || ""}
                            onChange={(e) =>
                              updateRepo({ webhook_secret_env: e.target.value })
                            }
                            placeholder="SERVICE_WEBHOOK_SECRET"
                          />
                        </label>
                      </div>
                    )}
                  </div>
                )}
              </>
            )}

            {isPi && (
              <>
                <label className="field">
                  <span className="field-label">Pi 维护指引（Prompt）</span>
                  <textarea
                    className="textarea"
                    rows={3}
                    value={form.instructions}
                    onChange={(e) =>
                      setForm({ ...form, instructions: e.target.value })
                    }
                    placeholder="例如：仅暴露 CI 构建查询与错误日志接口，保持已有工具 ID 稳定，禁止生成删除或部署写操作。"
                  />
                </label>

                <details className="sail-details">
                  <summary>覆盖 Pi 模型配置（可选）</summary>
                  <div className="form-grid-2">
                    <label className="field">
                      <span className="field-label">Provider</span>
                      <input
                        className="input mono"
                        value={form.provider || ""}
                        onChange={(e) =>
                          setForm({ ...form, provider: e.target.value })
                        }
                        placeholder="默认沿用本机 Pi 配置"
                      />
                    </label>
                    <label className="field">
                      <span className="field-label">Model</span>
                      <input
                        className="input mono"
                        value={form.model || ""}
                        onChange={(e) =>
                          setForm({ ...form, model: e.target.value })
                        }
                        placeholder="默认沿用本机 Pi 配置"
                      />
                    </label>
                  </div>
                </details>
              </>
            )}

            <div className="form-grid-2">
              <label className="field">
                <span className="field-label">检查周期（分钟）</span>
                <input
                  type="number"
                  min={1}
                  max={43200}
                  required
                  className="input tnum"
                  value={form.interval_minutes}
                  onChange={(e) =>
                    setForm({ ...form, interval_minutes: +e.target.value })
                  }
                />
              </label>
              <label className="field">
                <span className="field-label">单次任务超时（秒）</span>
                <input
                  type="number"
                  min={30}
                  max={3600}
                  required
                  className="input tnum"
                  value={form.timeout_seconds}
                  onChange={(e) =>
                    setForm({ ...form, timeout_seconds: +e.target.value })
                  }
                />
              </label>
            </div>

            <div className="form-stack" style={{ gap: 8 }}>
              <label className="checkbox-field">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) =>
                    setForm({ ...form, enabled: e.target.checked })
                  }
                />
                启用后台定期自动检查
                {savedSource?.enabled && savedSource.next_run_at && (
                  <span className="field-hint">
                    （下次：{formatTime(savedSource.next_run_at)}）
                  </span>
                )}
              </label>

              <label className="checkbox-field">
                <input
                  type="checkbox"
                  checked={form.auto_publish}
                  onChange={(e) =>
                    setForm({ ...form, auto_publish: e.target.checked })
                  }
                />
                兼容更新通过独立验收后自动发布（新增/删改权限仍需人工确认）
              </label>
            </div>

            <AcceptanceEditor
              serverID={server.id}
              native={server.kind === "mcp"}
            />
          </div>

          <div className="card-footer">
            <button
              type="button"
              className="btn-link"
              disabled={
                busy ||
                hasActiveRun ||
                (isPi && !mState?.runtime.available)
              }
              onClick={() => void triggerCheck(true)}
            >
              忽略快照缓存强制重跑
            </button>
            <div style={{ display: "flex", gap: 8 }}>
              <button type="submit" className="btn" disabled={busy}>
                保存配置
              </button>
              <button
                type="button"
                className="btn btn-primary"
                disabled={
                  busy ||
                  hasActiveRun ||
                  (isPi && !mState?.runtime.available)
                }
                onClick={() => void triggerCheck(false)}
              >
                {busy ? (
                  <Loader2 size={14} className="spin" />
                ) : (
                  <Play size={14} />
                )}
                保存并立即检查
              </button>
            </div>
          </div>
        </form>

        {/* Right: Run History & Version Review */}
        <div className="form-stack">
          <section className="card">
            <div className="card-header">
              <div className="card-header-title">
                <h2>维护运行记录</h2>
                <p>选择任一任务查看验收报告、工具 Diff 与产物。</p>
              </div>
              <Badge>{runs.length} 条记录</Badge>
            </div>

            {runs.length === 0 ? (
              <div className="empty-box">
                <Clock3 size={22} />
                <h3>尚未运行过维护检查</h3>
                <p>配置左侧同步策略与验收用例后，点击「保存并立即检查」。</p>
              </div>
            ) : (
              <div style={{ maxHeight: 320, overflowY: "auto" }}>
                {runs.map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    className={`log-row ${
                      selectedRunId === r.id ? "selected" : ""
                    }`}
                    onClick={() => {
                      setSelectedRunId(r.id);
                      setArtifact(null);
                    }}
                  >
                    <div className="log-row-main">
                      <div className="log-row-top">
                        <Badge
                          tone={
                            r.status === "published" || r.status === "no_change"
                              ? "success"
                              : r.status === "failed"
                                ? "danger"
                                : r.status === "ready"
                                  ? "warning"
                                  : "info"
                          }
                        >
                          {runStatusLabels[r.status] || r.status}
                        </Badge>
                        <span className="log-row-meta">{r.stage}</span>
                      </div>
                      <span className="log-row-title">
                        {r.error || r.summary || "检查来源快照并验证工具定义"}
                      </span>
                    </div>
                    <div className="log-row-right">
                      {formatTime(r.created_at)}
                    </div>
                  </button>
                ))}
              </div>
            )}
          </section>

          {detail && (
            <section className="card">
              <div className="card-header">
                <div className="card-header-title">
                  <h2>
                    版本详情{" "}
                    <code
                      style={{
                        fontSize: 12,
                        fontWeight: 400,
                        color: "var(--ink-muted)",
                      }}
                    >
                      {detail.run.id}
                    </code>
                  </h2>
                  <p>{detail.run.summary || detail.run.stage}</p>
                </div>
                <div style={{ display: "flex", gap: 8 }}>
                  {detail.run.status === "ready" && (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      disabled={busy || !detail.run.acceptance?.passed}
                      onClick={() =>
                        void runAction(async () => {
                          await api(
                            `/maintenance/runs/${detail.run.id}/publish`,
                            { method: "POST" },
                          );
                          notify("新版本已发布到工具注册表");
                        })
                      }
                    >
                      {detail.run.review?.required
                        ? "确认变更并发布"
                        : "发布此版本"}
                    </button>
                  )}
                  {detail.run.status === "published" && (
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={busy}
                      onClick={() =>
                        void runAction(async () => {
                          await api(
                            `/maintenance/runs/${detail.run.id}/rollback`,
                            { method: "POST" },
                          );
                          notify("已回退本次发布");
                        })
                      }
                    >
                      <RotateCcw size={13} />
                      回退
                    </button>
                  )}
                  {["queued", "running"].includes(detail.run.status) && (
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={busy}
                      onClick={() =>
                        void runAction(async () => {
                          await api(
                            `/maintenance/runs/${detail.run.id}/cancel`,
                            { method: "POST" },
                          );
                          notify("已取消任务");
                        })
                      }
                    >
                      <Square size={12} />
                      取消
                    </button>
                  )}
                </div>
              </div>

              <div className="card-body form-stack">
                {detail.run.error && (
                  <AlertBox tone="error" title={detail.run.error} />
                )}

                {detail.run.acceptance && (
                  <AlertBox
                    tone={
                      detail.run.acceptance.configured &&
                      detail.run.acceptance.passed
                        ? "success"
                        : "warning"
                    }
                    title={
                      !detail.run.acceptance.configured
                        ? "尚未配置独立验收用例，需配置并通过验收后才允许发布。"
                        : detail.run.acceptance.passed
                          ? "独立契约与路由验收全部通过"
                          : "独立验收未通过，阻止发布"
                    }
                  >
                    {!!detail.run.acceptance.missing_coverage?.length && (
                      <p style={{ marginTop: 4 }}>
                        缺少覆盖：{detail.run.acceptance.missing_coverage.join("、")}
                      </p>
                    )}
                  </AlertBox>
                )}

                {!!detail.run.review?.reasons?.length && (
                  <div className="alert warning">
                    <strong>需要人工确认的变更</strong>
                    <ul style={{ paddingLeft: 18, marginTop: 6 }}>
                      {detail.run.review.reasons.map((reason, idx) => (
                        <li key={idx}>
                          <code>{reason.tool_id}</code>：{reason.message}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {detail.changes.length > 0 && (
                  <div className="form-stack" style={{ gap: 8 }}>
                    <strong style={{ fontSize: 12.5, color: "var(--ink-strong)" }}>
                      工具变更明细（{detail.changes.length}）
                    </strong>
                    {detail.changes.map((c) => (
                      <details key={c.id} className="sail-details">
                        <summary>
                          <Badge
                            tone={
                              c.kind === "added"
                                ? "success"
                                : c.kind === "removed"
                                  ? "danger"
                                  : "info"
                            }
                          >
                            {c.kind === "added"
                              ? "新增"
                              : c.kind === "removed"
                                ? "移除"
                                : "更新"}
                          </Badge>{" "}
                          <code style={{ marginLeft: 6 }}>{c.id}</code>
                        </summary>
                        <div className="form-grid-2">
                          <div>
                            <div className="field-hint">变更前</div>
                            <pre
                              className="navy-pre"
                              style={{
                                borderRadius: 6,
                                marginTop: 4,
                                background: "var(--navy-bg)",
                              }}
                            >
                              {pretty(c.before || null)}
                            </pre>
                          </div>
                          <div>
                            <div className="field-hint">变更后</div>
                            <pre
                              className="navy-pre"
                              style={{
                                borderRadius: 6,
                                marginTop: 4,
                                background: "var(--navy-bg)",
                              }}
                            >
                              {pretty(c.after || null)}
                            </pre>
                          </div>
                        </div>
                      </details>
                    ))}
                  </div>
                )}

                {detail.run.artifacts && (
                  <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                    {Object.keys(detail.run.artifacts).map((name) => (
                      <button
                        key={name}
                        type="button"
                        className="btn btn-sm"
                        onClick={() =>
                          void runAction(async () => {
                            const art = await api<{
                              name: string;
                              content: string;
                            }>(
                              `/maintenance/runs/${detail.run.id}/artifacts/${encodeURIComponent(name)}`,
                            );
                            setArtifact(art);
                          })
                        }
                      >
                        <FileCode2 size={13} />
                        {name}
                      </button>
                    ))}
                  </div>
                )}

                {artifact && (
                  <div className="navy-card">
                    <div className="navy-card-header">
                      <span className="navy-card-label">{artifact.name}</span>
                    </div>
                    <pre className="navy-pre">{artifact.content}</pre>
                  </div>
                )}
              </div>
            </section>
          )}
        </div>
      </div>
    </div>
  );
}

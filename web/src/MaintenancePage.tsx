import { useEffect, useRef, useState } from "react";
import { Bot, Check, Clock3, FileCode2, Loader2, Play, RefreshCw, RotateCcw, Square } from "lucide-react";
import { api, pretty, type Server, type Tool } from "./types";
import "./maintenance.css";
import { AcceptanceEditor } from "./AcceptanceEditor";

type Repository = { url: string; ref: string; files: string[]; project?: string; events_enabled: boolean; webhook_secret_env?: string };
const repositoryDefaults: Repository = { url: "", ref: "main", files: [], events_enabled: false, project: "", webhook_secret_env: "" };
type Source = {
  server_id: string; files: string[]; urls: string[]; instructions: string;
  enabled: boolean; auto_publish: boolean; interval_minutes: number;
  timeout_seconds: number; repository?: Repository; format?: "pi" | "openapi" | "native"; operation_ids?: string[]; provider?: string; model?: string; next_run_at?: string;
};
type Run = {
  id: string; server_id: string; status: string; stage: string; created_at: string;
  finished_at?: string; published_by?: string; published_at?: string; rolled_back_by?: string; rolled_back_at?: string; summary?: string; error?: string; events: string[];
  artifacts?: Record<string, string>; tools?: Tool[];
  acceptance?: { configured: boolean; passed: boolean; results?: { name: string; kind: string; passed: boolean; error?: string; duration_ms: number }[]; missing_coverage?: string[] };
  provenance?: { repository?: string; ref?: string; commit?: string; documents?: Record<string, string> };
  trigger?: { kind: string; provider?: string; delivery_id?: string; commit?: string };
  review?: { required: boolean; reasons?: { tool_id: string; code: string; message: string }[] };
};
type State = { runtime: { available: boolean; command: string; started: boolean; model_source: string; execution: {mode: string; image: string} }; sources: Source[]; runs: Run[] };
type Detail = { run: Run; changes: { id: string; kind: string; before?: Tool; after?: Tool }[] };
const labels: Record<string, string> = {
  queued: "等待运行", running: "维护中", ready: "待发布", published: "已发布", no_change: "无变化",
  failed: "失败", cancelled: "已取消", interrupted: "已中断", rolled_back: "已回退",
};
const defaults = (id: string, kind = "http"): Source => ({ server_id: id, format: kind === "mcp" ? "native" : "openapi", operation_ids: [], files: [], urls: [], instructions: "", enabled: false, auto_publish: true, interval_minutes: 60, timeout_seconds: 900, provider: "", model: "" });
const lines = (text: string) => text.split("\n").map(v => v.trim()).filter(Boolean);
const timestamp = (value?: string) => value ? new Date(value).toLocaleString() : "—";

export function MaintenancePage({ servers, refreshRegistry }: { servers: Server[]; refreshRegistry: () => Promise<unknown> }) {
  const available = servers.filter(s => !s.demo);
  const [state, setState] = useState<State | null>(null);
  const [form, setForm] = useState<Source>(() => defaults(available[0]?.id || "", available[0]?.kind));
  const [files, setFiles] = useState("");
  const [urls, setURLs] = useState("");
  const [operations, setOperations] = useState("");
  const [repositoryFiles, setRepositoryFiles] = useState("");
  function updateRepository(patch: Partial<Repository>) { setForm(current => ({ ...current, repository: { ...(current.repository || repositoryDefaults), ...patch } })); }
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [artifact, setArtifact] = useState<{ name: string; content: string } | null>(null);
  const [selected, setSelected] = useState("");
  const initialized = useRef(false);
  const catalogVersion = useRef("");
  function selectSource(id: string, data = state) {
    const source = data?.sources.find(s => s.server_id === id) || defaults(id,available.find(s => s.id === id)?.kind);
    setForm({ ...source, format: source.format || "pi" }); setRepositoryFiles((source.repository?.files || []).join("\n")); setOperations((source.operation_ids || []).join(", ")); setFiles(source.files.join("\n")); setURLs(source.urls.join("\n"));
    setNotice(""); setSelected(""); setDetail(null); setArtifact(null);
  }
  async function refresh() {
    const data = await api<State>("/maintenance");
    setState(data);
    const version = data.runs.filter(r => ["published", "rolled_back"].includes(r.status)).map(r => `${r.id}:${r.status}`).join(",");
    if (version !== catalogVersion.current) { catalogVersion.current = version; await refreshRegistry(); }
    return data;
  }
  useEffect(() => {
    let live = true;
    async function poll() {
      try {
        const data = await api<State>("/maintenance");
        if (!live) return;
        setState(data);
        if (!initialized.current) { initialized.current = true; selectSource(available[0]?.id || "", data); }
        const version = data.runs.filter(r => ["published", "rolled_back"].includes(r.status)).map(r => `${r.id}:${r.status}`).join(",");
        if (version !== catalogVersion.current) { catalogVersion.current = version; await refreshRegistry(); }
      } catch (e) { if (live) setError((e as Error).message); }
    }
    void poll(); const timer = setInterval(() => void poll(), 3000);
    return () => { live = false; clearInterval(timer); };
  }, []);
  useEffect(() => {
    if (!selected) return;
    let live = true;
    api<Detail>(`/maintenance/runs/${selected}`).then(d => { if (live) setDetail(d); }).catch(e => { if (live) setError(e.message); });
    return () => { live = false; };
  }, [selected, state]);
  async function action(fn: () => Promise<unknown>) {
    setBusy(true); setError(""); setNotice("");
    try { await fn(); await refresh(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  }
  async function save() {
    await api("/maintenance/sources", { method: "PUT", body: pretty({ ...form, repository: form.repository ? { ...form.repository, files: lines(repositoryFiles) } : undefined, files: lines(files), urls: lines(urls), operation_ids: operations.split(/[\n,]/).map(v => v.trim()).filter(Boolean) }) });
  }
  async function run(force: boolean) {
    await save();
    const r = await api<Run>(`/maintenance/sources/${encodeURIComponent(form.server_id)}/run`, { method: "POST", body: pretty({ force }) });
    setSelected(r.id); setDetail(null); setArtifact(null); setNotice("维护任务已排队，可以继续使用网关。");
  }
  const selectedRuns = (state?.runs || []).filter(r => r.server_id === form.server_id);
  const active = selectedRuns.some(r => ["queued", "running"].includes(r.status));
  return <div className="maintenance-page">
    <div className="page-heading"><div><div className="eyebrow"><span /> CONTINUOUS TOOL MAINTENANCE</div><h1>自动维护</h1><p>让 Pi 跟进服务变化，维护工具定义与适配代码，保持路由候选可用；标准接口直接同步。</p></div>
      <div className={`maintenance-runtime ${state?.runtime.available ? "ready" : ""}`}><Bot size={18} /><span>{state ? state.runtime.available ? "已找到 Pi" : "未找到 Pi" : "检查运行时…"}</span>{state && <small>{state.runtime.execution.mode === "docker" ? "容器隔离" : "本机执行"}</small>}</div>
    </div>
    {error && <div className="maintenance-message error" role="alert">{error}</div>}
    {notice && <div className="maintenance-message" role="status"><Check size={16} />{notice}</div>}
    {state && !state.runtime.available && form.format === "pi" && <div className="maintenance-message error">请先安装 Pi 1.4+ 并配置模型；当前启动命令：{state.runtime.command}。也可通过 GATEWAY_PI_COMMAND 指定路径。</div>}
    {!available.length ? <section className="panel maintenance-empty"><Bot size={32} /><h2>先接入一个服务</h2><p>在「开始接入」保存 HTTP 或 MCP 来源后，就可以配置它的自动维护。</p></section> : <>
      <div className="maintenance-layout">
        <form className="panel maintenance-config" onSubmit={e => { e.preventDefault(); void action(async () => { await save(); setNotice("维护配置已保存。"); }); }}>
          <div className="maintenance-section-heading"><h2>维护来源</h2><span>每个服务独立配置</span></div>
          <label className="form-field"><span>服务</span><select className="text-input" value={form.server_id} onChange={e => selectSource(e.target.value)}>{available.map(s => <option key={s.id} value={s.id}>{s.name} · {s.id}</option>)}</select></label>
          <label className="form-field"><span>维护方式</span><select className="text-input" value={form.format || "pi"} onChange={e => setForm({ ...form, format: e.target.value as Source["format"] })}>
            {available.find(s => s.id === form.server_id)?.kind === "mcp" ? <option value="native">原生 MCP · 直接同步工具定义</option> : <option value="openapi">OpenAPI · 确定性导入</option>}
            <option value="pi">Pi · 生成或维护适配代码</option>
          </select><small>标准接口无需模型；复杂业务转换使用 Pi。</small></label>
          {(form.format === "openapi" || form.format === "native") && <label className="form-field"><span>操作范围 <em>operationId / MCP 工具名，用逗号分隔</em></span><input className="text-input mono" value={operations} onChange={e => setOperations(e.target.value)} placeholder="healthz, readyz" /><small>留空导入全部支持的操作；OpenAPI 使用第一个仓库文件、参考文件或 URL 的定义。</small></label>}
          <label className="checkbox-row"><input type="checkbox" checked={!!form.repository} onChange={e => setForm({ ...form, repository: e.target.checked ? { ...repositoryDefaults } : undefined })} />从 Git 仓库读取接口定义</label>
          {form.repository && <div className="maintenance-repository">
            <label className="form-field"><span>仓库 URL 或本地路径</span><input className="text-input mono" value={form.repository.url} onChange={e => updateRepository({ url: e.target.value })} placeholder="https://git.example/team/service.git" /><small>使用单独的 Git 缓存读取已提交文件，不修改原 checkout。</small></label>
            <label className="form-field"><span>分支或 commit</span><input className="text-input mono" value={form.repository.ref} onChange={e => updateRepository({ ref: e.target.value })} /></label>
            <label className="form-field"><span>仓库内文件 <em>每行一个相对路径</em></span><textarea className="text-input mono" rows={3} value={repositoryFiles} onChange={e => setRepositoryFiles(e.target.value)} placeholder="api/openapi.yaml" /></label>
            <label className="checkbox-row"><input type="checkbox" checked={form.repository.events_enabled} onChange={e => updateRepository({ events_enabled: e.target.checked })} />接收 Git / CI / 发布事件</label>
            {form.repository.events_enabled && <>
              <label className="form-field"><span>仓库标识</span><input className="text-input mono" value={form.repository.project || ""} onChange={e => updateRepository({ project: e.target.value })} placeholder="team/service" /></label>
              <label className="form-field"><span>签名密钥的环境变量名称</span><input className="text-input mono" value={form.repository.webhook_secret_env || ""} onChange={e => updateRepository({ webhook_secret_env: e.target.value })} placeholder="SERVICE_WEBHOOK_SECRET" /><small>只填写变量名；密钥从网关启动环境读取。</small></label>
              <p className="maintenance-help">Webhook 地址：<code>{window.location.origin}/api/maintenance/sources/{form.server_id}/webhook/&#123;github|gitlab|generic&#125;</code></p>
            </>}
          </div>}
          <label className="form-field"><span>参考文件 <em>每行一个绝对路径</em></span><textarea className="text-input mono" rows={3} value={files} onChange={e => setFiles(e.target.value)} placeholder={"/path/to/service/openapi.json\n/path/to/service/ci.yml"} /><small>选择接口定义、日志字段说明、CI 配置或相关源码文件。原生 MCP 会自动读取下游工具定义，可留空。</small></label>
          <label className="form-field"><span>文档地址 <em>每行一个 URL</em></span><textarea className="text-input mono" rows={2} value={urls} onChange={e => setURLs(e.target.value)} placeholder="https://service.example/openapi.json" /><small>直接可读取的 HTTP(S) 文档；请勿在地址中填写密钥。</small></label>
          <label className="form-field"><span>维护指引</span><textarea className="text-input" rows={4} value={form.instructions} onChange={e => setForm({ ...form, instructions: e.target.value })} placeholder="例如：暴露 CI 构建状态和错误日志查询，保持已有工具 ID；不要提供部署或删除操作。" /></label>
          <div className="form-columns"><label className="form-field"><span>检查周期 / 分钟</span><input className="text-input" type="number" min={1} max={43200} required value={form.interval_minutes} onChange={e => setForm({ ...form, interval_minutes: +e.target.value })} /></label><label className="form-field"><span>任务超时 / 秒</span><input className="text-input" type="number" min={30} max={3600} required value={form.timeout_seconds} onChange={e => setForm({ ...form, timeout_seconds: +e.target.value })} /></label></div>
          <label className="checkbox-row"><input type="checkbox" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} />定期检查来源变化</label>
          <p className="maintenance-help">{state?.sources.find(s => s.server_id === form.server_id)?.enabled ? `下次定期检查：${timestamp(state.sources.find(s => s.server_id === form.server_id)?.next_run_at)}` : "定期检查尚未开启，仍可手动运行。"}</p>
          <label className="checkbox-row"><input type="checkbox" checked={form.auto_publish} onChange={e => setForm({ ...form, auto_publish: e.target.checked })} />兼容更新通过验收后自动发布</label>
          <p className="maintenance-help">新增、删除、不兼容输入或读变写需要负责人确认。新增工具在确认前保持禁用；已发布能力的兼容更新可自动发布。</p>
          {form.format === "pi" && <details className="maintenance-model"><summary>Pi 模型配置</summary><p className="maintenance-help">默认使用本机 Pi 的模型与登录配置，与路由模型独立。</p><div className="form-columns"><label className="form-field"><span>Provider</span><input className="text-input" value={form.provider || ""} onChange={e => setForm({ ...form, provider: e.target.value })} placeholder="沿用 Pi 配置" /></label><label className="form-field"><span>Model</span><input className="text-input" value={form.model || ""} onChange={e => setForm({ ...form, model: e.target.value })} placeholder="沿用 Pi 配置" /></label></div></details>}
          <div className="maintenance-actions"><button className="button secondary" disabled={busy} type="submit">保存配置</button><button type="button" className="button primary" disabled={busy || active || (form.format === "pi" && !state?.runtime.available)} onClick={() => void action(() => run(false))}>{busy ? <Loader2 size={15} className="spin" /> : <Play size={15} />}保存并检查</button></div>
          <button type="button" className="maintenance-link" disabled={busy || active || (form.format === "pi" && !state?.runtime.available)} onClick={() => void action(() => run(true))}>忽略内容缓存，重新生成</button>
          <AcceptanceEditor serverID={form.server_id} native={available.find(s => s.id === form.server_id)?.kind === "mcp"} />
        </form>
        <div className="maintenance-history">
          <section className="panel maintenance-pipeline"><h2>从变化到可用工具</h2><ol><li><RefreshCw size={16} /><span>对比来源快照<small>无变化时跳过模型调用</small></span></li><li><Bot size={16} /><span>同步接口或由 Pi 维护适配代码<small>标准定义直接解析，复杂逻辑由 Pi 处理</small></span></li><li><Check size={16} /><span>独立验收与变更审核<small>兼容更新自动发布，新能力需确认</small></span></li></ol></section>
          <section className="panel maintenance-runs"><div className="maintenance-section-heading"><h2>运行记录</h2><span>最近 50 次任务</span></div>
            {!selectedRuns.length ? <div className="maintenance-empty"><Clock3 size={24} /><p>尚未运行维护任务</p><small>保存来源后点击「保存并检查」。</small></div> : selectedRuns.map(r => <button type="button" key={r.id} className={`maintenance-run ${selected === r.id ? "selected" : ""}`} onClick={() => { setSelected(r.id); setDetail(null); setArtifact(null); }}><div><span className={`maintenance-status ${r.status}`}>{["running", "queued"].includes(r.status) && <Loader2 size={12} className="spin" />}{labels[r.status] || r.status}</span><time>{timestamp(r.created_at)}</time></div><strong>{r.stage}</strong><p>{r.error || r.summary || "读取资料并维护此服务的工具集合"}</p></button>)}
          </section>
        </div>
      </div>
      {detail && <section className="panel maintenance-detail"><div className="maintenance-section-heading"><div><h2>版本详情</h2><span className="mono">{detail.run.id}</span></div><div className="maintenance-actions">
        {detail.run.status === "ready" && <button className="button primary" disabled={busy || !detail.run.acceptance?.passed} onClick={() => void action(() => api(`/maintenance/runs/${detail.run.id}/publish`, { method: "POST" }))}>{detail.run.review?.required ? "确认变更并发布" : "发布此版本"}</button>}
        {detail.run.status === "published" && <button className="button secondary" disabled={busy} onClick={() => void action(() => api(`/maintenance/runs/${detail.run.id}/rollback`, { method: "POST" }))}><RotateCcw size={14} />回退本次发布</button>}
        {["queued", "running"].includes(detail.run.status) && <button className="button secondary" disabled={busy} onClick={() => void action(() => api(`/maintenance/runs/${detail.run.id}/cancel`, { method: "POST" }))}><Square size={14} />取消任务</button>}
      </div></div><p>{detail.run.summary || detail.run.stage}</p>{detail.run.error && <div className="maintenance-message error">{detail.run.error}</div>}
        {detail.run.published_by && <p className="maintenance-help">发布：{detail.run.published_by} · {timestamp(detail.run.published_at)}{detail.run.rolled_back_by && `；回退：${detail.run.rolled_back_by} · ${timestamp(detail.run.rolled_back_at)}`}</p>}
        {detail.run.provenance?.commit && <div className="maintenance-provenance"><strong>来源版本</strong><code>{detail.run.provenance.commit}</code><span>{detail.run.provenance.repository} · {detail.run.provenance.ref}</span></div>}
        {detail.run.trigger && <p className="maintenance-help">触发方式：{({ manual: "手动", schedule: "定期检查", push: "Git 推送", ci_completed: "CI 完成", release: "版本发布", deploy: "部署完成" } as Record<string, string>)[detail.run.trigger.kind] || detail.run.trigger.kind}{detail.run.trigger.delivery_id && ` · ${detail.run.trigger.delivery_id}`}</p>}
        <div className="maintenance-acceptance-report"><h3>独立验收</h3>
          {!detail.run.acceptance?.configured ? <p className="maintenance-message error">尚未配置独立验收。保存用例并重新检查后才能发布。</p> : <>
            <p className={detail.run.acceptance.passed ? "maintenance-message" : "maintenance-message error"}>{detail.run.acceptance.passed ? "契约与路由验收通过" : "验收未通过，版本不会发布"}</p>
            {detail.run.acceptance.results?.map((result, i) => <div className="maintenance-check" key={i}><span className={`maintenance-status ${result.passed ? "published" : "failed"}`}>{result.passed ? "通过" : "失败"}</span><span>{result.name}<small>{result.kind === "routing" ? "路由" : "请求与结果"} · {result.duration_ms} ms</small>{result.error && <p>{result.error}</p>}</span></div>)}
            {!!detail.run.acceptance.missing_coverage?.length && <p className="maintenance-message error">缺少覆盖：{detail.run.acceptance.missing_coverage.join("、")}</p>}
          </>}
          {!!detail.run.review?.reasons?.length && <><h3>{detail.run.status === "published" ? "已确认的变更" : "需要确认的变更"}</h3><ul>{detail.run.review.reasons.map((reason, i) => <li key={i}><code>{reason.tool_id}</code>：{reason.message}</li>)}</ul></>}
        </div>
        <div className="maintenance-diffs">{detail.changes.map(c => <details key={c.id}><summary><span className={`maintenance-change ${c.kind}`}>{({ added: "新增", changed: "修改", removed: "移除" })[c.kind]}</span><code>{c.id}</code></summary><div className="maintenance-diff-columns"><div><h4>之前</h4><pre>{pretty(c.before || null)}</pre></div><div><h4>之后</h4><pre>{pretty(c.after || null)}</pre></div></div></details>)}{detail.run.tools && !detail.changes.length && <p className="maintenance-help">工具定义没有变化。</p>}</div>
        {!!detail.run.events.length && <details className="maintenance-events"><summary>执行过程</summary><pre>{detail.run.events.join("\n")}</pre></details>}
        {detail.run.artifacts && <div className="maintenance-artifacts">{Object.keys(detail.run.artifacts).map(name => <button type="button" className="button secondary" key={name} onClick={() => void action(async () => setArtifact(await api(`/maintenance/runs/${detail.run.id}/artifacts/${encodeURIComponent(name)}`)))}><FileCode2 size={14} />{name}</button>)}</div>}
        {artifact && <div className="maintenance-artifact"><h3>{artifact.name}</h3><pre>{artifact.content}</pre></div>}
      </section>}
    </>}
  </div>;
}

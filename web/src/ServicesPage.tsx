import React, { useEffect, useState } from "react";
import { Loader2, Search } from "lucide-react";
import {
  api,
  formatTime,
  pretty,
  serverProtocolLabel,
  serverTargetText,
  type Bootstrap,
  type ConnectionCheck,
  type Server,
  type Tool,
} from "./types";
import { ServiceMaintenanceTab } from "./ServiceMaintenanceTab";
import { AlertBox, Badge, CopyButton, Drawer, Modal, Switch } from "./ui";

type Props = {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  selectedServerId: string | null;
  onSelectServer: (id: string | null) => void;
  onOpenCatalog?: (serverId: string) => void;
  openAddOnMount?: boolean;
  onConsumedAdd?: () => void;
  notify: (msg: string) => void;
};

export function ServicesPage({
  state,
  refresh,
  selectedServerId,
  onSelectServer,
  onOpenCatalog,
  openAddOnMount,
  onConsumedAdd,
  notify,
}: Props) {
  const [addModalMode, setAddModalMode] = useState<"form" | "paste" | null>(
    null,
  );
  const [editingTool, setEditingTool] = useState<Tool | null>(null);
  const [addingHttpServerId, setAddingHttpServerId] = useState<string | null>(
    null,
  );
  const [syncingId, setSyncingId] = useState("");

  useEffect(() => {
    if (openAddOnMount) {
      setAddModalMode("form");
      onConsumedAdd?.();
    }
  }, [openAddOnMount]);

  const servers = state.registry.servers;
  const tools = state.registry.tools;
  const realServers = servers.filter((s) => !s.demo);
  const demoServers = servers.filter((s) => s.demo);
  const selectedServer =
    servers.find((s) => s.id === selectedServerId) || null;

  async function toggleServer(server: Server) {
    try {
      await api("/servers", {
        method: "PUT",
        body: pretty({ ...server, enabled: !server.enabled }),
      });
      await refresh();
      notify(server.enabled ? `已停用 ${server.name}` : `已启用 ${server.name}`);
    } catch (e: any) {
      notify(e.message);
    }
  }

  async function toggleTool(tool: Tool) {
    try {
      await api("/tools", {
        method: "PUT",
        body: pretty({ ...tool, enabled: !tool.enabled }),
      });
      await refresh();
    } catch (e: any) {
      notify(e.message);
    }
  }

  async function syncServer(server: Server) {
    setSyncingId(server.id);
    try {
      const res = await api<{ imported: number }>(
        `/servers/${encodeURIComponent(server.id)}/sync`,
        { method: "POST" },
      );
      await refresh();
      notify(`已同步 ${res.imported} 个工具定义（新增或描述变更的工具默认关闭）`);
    } catch (e: any) {
      notify(e.message);
    } finally {
      setSyncingId("");
    }
  }

  return (
    <div>
      {selectedServer ? (
        <ServiceDetailView
          state={state}
          server={selectedServer}
          tools={tools.filter((t) => t.server_id === selectedServer.id)}
          check={state.setup.source_checks[selectedServer.id]}
          syncing={syncingId === selectedServer.id}
          onBack={() => onSelectServer(null)}
          onToggleServer={() => toggleServer(selectedServer)}
          onSync={() => syncServer(selectedServer)}
          onToggleTool={toggleTool}
          onEditTool={setEditingTool}
          onAddHttpTool={() => setAddingHttpServerId(selectedServer.id)}
          refresh={refresh}
          notify={notify}
        />
      ) : (
        <>
          <div className="page-header">
            <div className="page-header-main">
              <h1>上游服务</h1>
              <p>
                管理下游 MCP 与 HTTP 服务连接、工具开放白名单、契约自动维护与连接认证凭据。
              </p>
            </div>
            <div className="page-header-actions">
              <button
                type="button"
                className="btn"
                onClick={() => setAddModalMode("paste")}
              >
                粘贴 MCP JSON
              </button>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => setAddModalMode("form")}
              >
                添加上游服务
              </button>
            </div>
          </div>

          {/* Real Upstream Services Card */}
          <section className="card" style={{ marginBottom: 20 }}>
            <div className="card-header">
              <div className="card-header-title">
                <h2>已接入上游服务</h2>
                <p>点击任一服务管理其下游契约、自动维护策略与鉴权配置；跨服务治理请前往「工具目录」。</p>
              </div>
              <span
                className="tnum"
                style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
              >
                {realServers.filter((s) => s.enabled).length} / {realServers.length} 在线启用
              </span>
            </div>

            {realServers.length === 0 ? (
              <div className="empty-box">
                <h3>尚未接入任何上游服务</h3>
                <p>
                  你可以接入远程 Streamable HTTP MCP、本地 stdio 进程，或者已有内部 HTTP API；也可以直接粘贴一段{" "}
                  <code>mcpServers</code> JSON。
                </p>
                <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
                  <button
                    type="button"
                    className="btn"
                    onClick={() => setAddModalMode("paste")}
                  >
                    粘贴配置导入
                  </button>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => setAddModalMode("form")}
                  >
                    添加第一个服务
                  </button>
                </div>
              </div>
            ) : (
              <div className="table-wrap">
                <table className="sail-table">
                  <thead>
                    <tr>
                      <th>服务名称</th>
                      <th>协议</th>
                      <th>连接目标</th>
                      <th>开放工具</th>
                      <th>读写构成</th>
                      <th>状态</th>
                      <th style={{ textAlign: "right" }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {realServers.map((s) => {
                      const srvTools = tools.filter((t) => t.server_id === s.id);
                      const enabledSrvTools = srvTools.filter((t) => t.enabled);
                      const onCount = enabledSrvTools.length;
                      const roCount = enabledSrvTools.filter((t) => t.read_only).length;
                      const rwCount = onCount - roCount;
                      const check = state.setup.source_checks[s.id];
                      return (
                        <tr
                          key={s.id}
                          className="row-clickable"
                          onClick={() => onSelectServer(s.id)}
                        >
                          <td>
                            <span className="cell-primary">
                              {s.name}
                              <code
                                style={{
                                  fontSize: 12,
                                  fontWeight: 400,
                                  color: "var(--ink-faint)",
                                }}
                              >
                                {s.id}
                              </code>
                            </span>
                            {s.description && (
                              <span className="cell-sub">{s.description}</span>
                            )}
                          </td>
                          <td style={{ width: 120 }}>
                            <span
                              className="mono"
                              style={{ fontSize: 12, color: "var(--ink-muted)" }}
                            >
                              {serverProtocolLabel(s)}
                            </span>
                          </td>
                          <td style={{ maxWidth: 280 }}>
                            <code
                              style={{
                                fontSize: 12,
                                color: "var(--ink-muted)",
                                display: "block",
                                overflow: "hidden",
                                textOverflow: "ellipsis",
                                whiteSpace: "nowrap",
                              }}
                              title={serverTargetText(s)}
                            >
                              {serverTargetText(s)}
                            </code>
                          </td>
                          <td className="tnum" style={{ width: 115 }}>
                            <strong style={{ color: "var(--ink-strong)" }}>
                              {onCount}
                            </strong>{" "}
                            <span style={{ color: "var(--ink-muted)" }}>
                              / {srvTools.length}
                            </span>
                          </td>
                          <td className="tnum" style={{ width: 145 }}>
                            <span style={{ fontSize: 12.5, color: "var(--ink-muted)" }}>
                              {roCount} 只读 ·{" "}
                              <span
                                style={{
                                  color: rwCount > 0 ? "var(--ink-strong)" : "var(--ink-muted)",
                                  fontWeight: rwCount > 0 ? 500 : 400,
                                }}
                              >
                                {rwCount} 写
                              </span>
                            </span>
                          </td>
                          <td style={{ width: 115 }}>
                            <div
                              style={{
                                display: "inline-flex",
                                alignItems: "center",
                                gap: 8,
                              }}
                              onClick={(e) => e.stopPropagation()}
                            >
                              <Switch
                                checked={s.enabled}
                                label={`${s.name} 启用状态`}
                                onChange={() => toggleServer(s)}
                              />
                              {check?.status === "error" && (
                                <span
                                  className="status-inline"
                                  style={{ color: "var(--danger-ink)" }}
                                >
                                  <span className="status-dot err" />
                                  异常
                                </span>
                              )}
                            </div>
                          </td>
                          <td
                            style={{ width: 210, textAlign: "right" }}
                            onClick={(e) => e.stopPropagation()}
                          >
                            <div
                              style={{
                                display: "inline-flex",
                                gap: 6,
                                justifyContent: "flex-end",
                              }}
                            >
                              {onOpenCatalog && (
                                <button
                                  type="button"
                                  className="btn btn-sm"
                                  onClick={() => onOpenCatalog(s.id)}
                                >
                                  工具目录
                                </button>
                              )}
                              {s.kind === "mcp" && (
                                <button
                                  type="button"
                                  className="btn btn-sm"
                                  disabled={!!syncingId}
                                  onClick={() => syncServer(s)}
                                >
                                  {syncingId === s.id && (
                                    <Loader2 size={12} className="spin" />
                                  )}
                                  同步
                                </button>
                              )}
                              <button
                                type="button"
                                className="btn btn-sm"
                                onClick={() => onSelectServer(s.id)}
                              >
                                配置
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {/* Built-in Demo Services tucked cleanly into a collapsible */}
          {demoServers.length > 0 && (
            <details className="sail-details">
              <summary>
                内置演示服务（{demoServers.length} 个 · 仅在「路由沙盒」勾选包含演示工具时参与路由）
              </summary>
              <div className="table-wrap" style={{ background: "#fff", borderRadius: 6, border: "1px solid var(--border)" }}>
                <table className="sail-table">
                  <thead>
                    <tr>
                      <th>演示服务</th>
                      <th>协议</th>
                      <th>连接地址</th>
                      <th>工具数量</th>
                      <th style={{ textAlign: "right" }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {demoServers.map((s) => (
                      <tr key={s.id}>
                        <td>
                          <span className="cell-primary">
                            {s.name} <Badge>DEMO</Badge>
                          </span>
                          <span className="cell-sub">{s.description}</span>
                        </td>
                        <td>
                          <span
                            className="mono"
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {serverProtocolLabel(s)}
                          </span>
                        </td>
                        <td>
                          <code style={{ fontSize: 12 }}>{s.endpoint}</code>
                        </td>
                        <td className="tnum">
                          {tools.filter((t) => t.server_id === s.id).length} 个工具
                        </td>
                        <td style={{ textAlign: "right" }}>
                          <button
                            type="button"
                            className="btn btn-sm"
                            onClick={() => onSelectServer(s.id)}
                          >
                            查看工具
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </details>
          )}
        </>
      )}

      {/* Add / Import Service Modal */}
      {addModalMode && (
        <AddServiceModal
          initialMode={addModalMode}
          onClose={() => setAddModalMode(null)}
          onCreated={async (id, msg) => {
            await refresh();
            setAddModalMode(null);
            onSelectServer(id);
            notify(msg);
          }}
        />
      )}

      {/* Add HTTP Tool Modal */}
      {addingHttpServerId && (
        <AddHttpToolModal
          serverId={addingHttpServerId}
          onClose={() => setAddingHttpServerId(null)}
          onCreated={async () => {
            await refresh();
            setAddingHttpServerId(null);
            notify("HTTP 工具已创建并启用");
          }}
        />
      )}

      {/* Tool Inspector & Structured Edit Drawer */}
      {editingTool && (
        <ToolEditDrawer
          tool={editingTool}
          onClose={() => setEditingTool(null)}
          onSaved={async () => {
            await refresh();
            setEditingTool(null);
            notify("工具定义已保存");
          }}
        />
      )}
    </div>
  );
}

/* ==========================================================================
   Stripe Resource Detail View for a Single Service
   ========================================================================== */
function ServiceDetailView({
  state,
  server,
  tools,
  check,
  syncing,
  onBack,
  onToggleServer,
  onSync,
  onToggleTool,
  onEditTool,
  onAddHttpTool,
  refresh,
  notify,
}: {
  state: Bootstrap;
  server: Server;
  tools: Tool[];
  check?: ConnectionCheck;
  syncing: boolean;
  onBack: () => void;
  onToggleServer: () => void;
  onSync: () => void;
  onToggleTool: (t: Tool) => void;
  onEditTool: (t: Tool) => void;
  onAddHttpTool: () => void;
  refresh: () => Promise<Bootstrap>;
  notify: (msg: string) => void;
}) {
  const [subtab, setSubtab] = useState<"tools" | "maintenance" | "connection">(
    "tools",
  );
  const [query, setQuery] = useState("");
  const [filterMode, setFilterMode] = useState<"all" | "on" | "off" | "ro">(
    "all",
  );
  const [batchBusy, setBatchBusy] = useState(false);
  const [detailPage, setDetailPage] = useState(1);
  const DETAIL_PAGE_SIZE = 40;

  useEffect(() => {
    setDetailPage(1);
  }, [server.id, query, filterMode]);

  const enabledCount = tools.filter((t) => t.enabled).length;
  const target = serverTargetText(server);

  const visibleTools = tools.filter((t) => {
    if (filterMode === "on" && !t.enabled) return false;
    if (filterMode === "off" && t.enabled) return false;
    if (filterMode === "ro" && !t.read_only) return false;
    if (!query.trim()) return true;
    return `${t.name} ${t.description} ${t.id}`
      .toLowerCase()
      .includes(query.toLowerCase());
  });

  const detailTotalPages = Math.max(
    1,
    Math.ceil(visibleTools.length / DETAIL_PAGE_SIZE),
  );
  const safeDetailPage = Math.min(detailPage, detailTotalPages);
  const detailStart = (safeDetailPage - 1) * DETAIL_PAGE_SIZE;
  const pagedVisibleTools = visibleTools.slice(
    detailStart,
    detailStart + DETAIL_PAGE_SIZE,
  );

  async function applyBatchIds(ids: string[], label: string) {
    setBatchBusy(true);
    try {
      await api(`/servers/${encodeURIComponent(server.id)}/tools`, {
        method: "POST",
        body: pretty({ ids }),
      });
      await refresh();
      notify(`${label}（当前启用 ${ids.length} 个工具）`);
    } catch (e: any) {
      notify(e.message);
    } finally {
      setBatchBusy(false);
    }
  }

  return (
    <div>
      <button type="button" className="back-link" onClick={onBack}>
        ← 返回上游服务
      </button>

      <div className="page-header" style={{ marginBottom: 16 }}>
        <div className="page-header-main">
          <h1>
            {server.name}
            <Badge>{serverProtocolLabel(server)}</Badge>
            {server.demo && <Badge>DEMO</Badge>}
          </h1>
          {server.description && <p>{server.description}</p>}
        </div>

        <div className="page-header-actions">
          {!server.demo && (
            <label
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 8,
                fontSize: 13,
                marginRight: 6,
              }}
            >
              <span style={{ color: "var(--ink-muted)" }}>启用服务</span>
              <Switch
                checked={server.enabled}
                label={`${server.name} 启用状态`}
                onChange={onToggleServer}
              />
            </label>
          )}
          {server.kind === "mcp" && !server.demo && (
            <button
              type="button"
              className="btn"
              disabled={syncing}
              onClick={onSync}
            >
              {syncing && <Loader2 size={14} className="spin" />}
              同步下游工具
            </button>
          )}
          {server.kind === "http" && !server.demo && (
            <button
              type="button"
              className="btn btn-primary"
              onClick={onAddHttpTool}
            >
              添加 HTTP 工具
            </button>
          )}
        </div>
      </div>

      {/* Stripe Property Metadata Strip */}
      <div className="property-strip">
        <div className="property-cell">
          <span className="property-label">服务 ID</span>
          <div className="property-value mono">{server.id}</div>
        </div>
        <div className="property-cell" style={{ gridColumn: "span 2" }}>
          <span className="property-label">
            {server.transport === "stdio" ? "启动命令" : "连接地址"}
          </span>
          <div className="property-value">
            <code className="truncate" style={{ fontSize: 12.5 }} title={target}>
              {target}
            </code>
            <CopyButton
              value={target}
              label="复制"
              variant="ghost"
              onCopied={() => notify("已复制连接目标")}
            />
          </div>
        </div>
        <div className="property-cell">
          <span className="property-label">开放工具数</span>
          <div className="property-value tnum">
            <span>
              {enabledCount} / {tools.length} 已启用
            </span>
          </div>
          <span className="property-sub">
            {check
              ? `${check.status === "error" ? "检查异常" : "最近检查"} · ${formatTime(check.checked_at)}`
              : "支持按需动态发现"}
          </span>
        </div>
      </div>

      {check?.status === "error" && (
        <div style={{ marginBottom: 16 }}>
          <AlertBox tone="error" title={check.message} detail={check.detail} />
        </div>
      )}

      {/* Sub-tabs */}
      <nav className="subtabs" aria-label="服务管理视图">
        <button
          type="button"
          className={`subtab ${subtab === "tools" ? "active" : ""}`}
          onClick={() => setSubtab("tools")}
        >
          工具列表
          <span className="subtab-count">{tools.length}</span>
        </button>
        {!server.demo && (
          <>
            <button
              type="button"
              className={`subtab ${subtab === "maintenance" ? "active" : ""}`}
              onClick={() => setSubtab("maintenance")}
            >
              自动维护与同步
            </button>
            <button
              type="button"
              className={`subtab ${subtab === "connection" ? "active" : ""}`}
              onClick={() => setSubtab("connection")}
            >
              连接与认证配置
            </button>
          </>
        )}
      </nav>

      {subtab === "tools" && (
        <section className="card">
          <div className="toolbar">
            <div className="toolbar-group">
              <div className="search-box">
                <Search size={14} />
                <input
                  aria-label="筛选此服务的工具"
                  className="input"
                  placeholder="搜索工具名称或描述…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
              </div>
              <div className="segmented">
                {(
                  [
                    ["all", `全部 (${tools.length})`],
                    ["on", `已启用 (${enabledCount})`],
                    ["off", `未启用 (${tools.length - enabledCount})`],
                    ["ro", "仅只读"],
                  ] as const
                ).map(([id, label]) => (
                  <button
                    key={id}
                    type="button"
                    className={filterMode === id ? "active" : ""}
                    onClick={() => setFilterMode(id)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {!server.demo && tools.length > 0 && (
              <div className="toolbar-group">
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={batchBusy}
                  onClick={() =>
                    applyBatchIds(
                      tools.filter((t) => t.read_only).map((t) => t.id),
                      "已仅启用全部只读工具",
                    )
                  }
                >
                  仅启用只读
                </button>
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={batchBusy}
                  onClick={() =>
                    applyBatchIds(
                      tools.map((t) => t.id),
                      "已启用全部工具",
                    )
                  }
                >
                  全部启用
                </button>
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={batchBusy}
                  onClick={() => applyBatchIds([], "已关闭全部工具")}
                >
                  全部停用
                </button>
              </div>
            )}
          </div>

          {visibleTools.length === 0 ? (
            <div className="empty-box">
              <h3>
                {tools.length === 0
                  ? server.kind === "http"
                    ? "尚未为此 HTTP 服务定义工具"
                    : "尚未读取到下游工具"
                  : "没有匹配筛选条件的工具"}
              </h3>
              <p>
                {tools.length === 0 && server.kind === "http"
                  ? "你可以手动添加 HTTP 工具，或切到「自动维护与同步」通过 OpenAPI 批量导入。"
                  : "尝试清空搜索词或切换状态筛选。"}
              </p>
              {tools.length === 0 && server.kind === "http" && !server.demo && (
                <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
                  <button
                    type="button"
                    className="btn"
                    onClick={() => setSubtab("maintenance")}
                  >
                    从 OpenAPI 导入
                  </button>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={onAddHttpTool}
                  >
                    手动添加工具
                  </button>
                </div>
              )}
            </div>
          ) : (
            <>
              <div className="table-wrap">
                <table className="sail-table">
                  <thead>
                    <tr>
                      <th>工具名称与说明</th>
                      <th>操作性质</th>
                      <th>访问身份</th>
                      <th>启用状态</th>
                      <th style={{ textAlign: "right" }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pagedVisibleTools.map((t) => (
                      <tr key={t.id}>
                        <td>
                          <span className="cell-primary mono">
                            {t.name}
                            {t.method && t.path && (
                              <Badge>
                                {t.method} {t.path}
                              </Badge>
                            )}
                          </span>
                          <span className="cell-sub">
                            {t.description || "下游未提供描述，建议编辑补充以提高路由准确率。"}
                          </span>
                        </td>
                        <td style={{ width: 120 }}>
                          <span
                            style={{
                              fontSize: 12.5,
                              color: t.read_only
                                ? "var(--ink-muted)"
                                : "var(--ink-body)",
                              fontWeight: t.read_only ? 400 : 500,
                            }}
                          >
                            {t.read_only ? "只读" : "写操作"}
                          </span>
                        </td>
                        <td style={{ width: 150 }}>
                          <code
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {t.actors.join(", ")}
                          </code>
                        </td>
                        <td style={{ width: 110 }}>
                          <Switch
                            checked={t.enabled}
                            label={`${t.name} 启用状态`}
                            onChange={() => onToggleTool(t)}
                          />
                        </td>
                        <td style={{ width: 100, textAlign: "right" }}>
                          <button
                            type="button"
                            className="btn btn-sm"
                            onClick={() => onEditTool(t)}
                          >
                            查看 / 编辑
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {visibleTools.length > DETAIL_PAGE_SIZE && (
                <div className="card-footer">
                  <span
                    className="tnum"
                    style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
                  >
                    显示{" "}
                    <strong style={{ color: "var(--ink-strong)" }}>
                      {detailStart + 1}–
                      {Math.min(
                        detailStart + DETAIL_PAGE_SIZE,
                        visibleTools.length,
                      )}
                    </strong>{" "}
                    / 共 {visibleTools.length} 个工具
                  </span>
                  <div style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                    <span
                      className="tnum"
                      style={{ fontSize: 12, color: "var(--ink-muted)" }}
                    >
                      第 {safeDetailPage} / {detailTotalPages} 页
                    </span>
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={safeDetailPage <= 1}
                      onClick={() =>
                        setDetailPage(Math.max(1, safeDetailPage - 1))
                      }
                    >
                      上一页
                    </button>
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={safeDetailPage >= detailTotalPages}
                      onClick={() =>
                        setDetailPage(
                          Math.min(detailTotalPages, safeDetailPage + 1),
                        )
                      }
                    >
                      下一页
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </section>
      )}

      {subtab === "maintenance" && !server.demo && (
        <ServiceMaintenanceTab
          server={server}
          refreshRegistry={refresh}
          notify={notify}
        />
      )}

      {subtab === "connection" && !server.demo && (
        <ServiceConnectionForm
          server={server}
          onSaved={async (msg) => {
            await refresh();
            notify(msg);
          }}
        />
      )}
    </div>
  );
}

/* ==========================================================================
   Structured Connection Form (Tab 3 of Service Detail)
   ========================================================================== */
function ServiceConnectionForm({
  server,
  onSaved,
}: {
  server: Server;
  onSaved: (msg: string) => Promise<void>;
}) {
  const [mode, setMode] = useState<"structured" | "json">("structured");
  const [form, setForm] = useState<Server>({ ...server });
  const [argsText, setArgsText] = useState(pretty(server.args || []));
  const [headersText, setHeadersText] = useState(pretty(server.headers || {}));
  const [envText, setEnvText] = useState(pretty(server.env || {}));
  const [rawJson, setRawJson] = useState(pretty(server));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<any>(null);

  useEffect(() => {
    setForm({ ...server });
    setArgsText(pretty(server.args || []));
    setHeadersText(pretty(server.headers || {}));
    setEnvText(pretty(server.env || {}));
    setRawJson(pretty(server));
  }, [server]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (mode === "json") {
        const parsed = JSON.parse(rawJson);
        await api("/servers", { method: "PUT", body: pretty(parsed) });
        await onSaved("服务 JSON 定义已保存");
      } else {
        const payload: Server = {
          ...form,
          transport: form.kind === "http" ? "http" : form.transport,
          args: form.transport === "stdio" ? JSON.parse(argsText) : [],
          headers: form.transport === "stdio" ? {} : JSON.parse(headersText),
          env: form.transport === "stdio" ? JSON.parse(envText) : {},
        };
        const check = await api<ConnectionCheck>("/setup/source", {
          method: "POST",
          body: pretty(payload),
        });
        await onSaved(check.message || "连接配置已保存并测试通过");
      }
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card" onSubmit={submit}>
      <div className="card-header">
        <div className="card-header-title">
          <h2>连接与认证配置</h2>
          <p>
            请求头与环境变量仅引用宿主环境变量（如{" "}
            <code>{"${SERVICE_TOKEN}"}</code>），密钥明文不落盘。
          </p>
        </div>
        <div className="segmented">
          <button
            type="button"
            className={mode === "structured" ? "active" : ""}
            onClick={() => setMode("structured")}
          >
            表单视图
          </button>
          <button
            type="button"
            className={mode === "json" ? "active" : ""}
            onClick={() => {
              setRawJson(pretty(form));
              setMode("json");
            }}
          >
            JSON 定义
          </button>
        </div>
      </div>

      <div className="card-body form-stack">
        {error && (
          <AlertBox
            tone="error"
            title={error.message || String(error)}
            detail={error.detail}
          />
        )}

        {mode === "json" ? (
          <label className="field">
            <span className="field-label">完整服务 JSON</span>
            <textarea
              className="textarea mono"
              rows={14}
              value={rawJson}
              onChange={(e) => setRawJson(e.target.value)}
              spellCheck={false}
            />
          </label>
        ) : (
          <>
            <div className="form-grid-2">
              <label className="field">
                <span className="field-label">显示名称</span>
                <input
                  required
                  className="input"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </label>
              <label className="field">
                <span className="field-label">服务描述</span>
                <input
                  className="input"
                  value={form.description}
                  onChange={(e) =>
                    setForm({ ...form, description: e.target.value })
                  }
                  placeholder="简要说明此服务提供的能力范围"
                />
              </label>
            </div>

            {form.kind === "mcp" && form.transport === "stdio" ? (
              <>
                <div className="form-grid-2">
                  <label className="field">
                    <span className="field-label">可执行命令</span>
                    <input
                      required
                      className="input mono"
                      value={form.command || ""}
                      onChange={(e) =>
                        setForm({ ...form, command: e.target.value })
                      }
                    />
                  </label>
                  <label className="field">
                    <span className="field-label">
                      工作目录 <em>可选</em>
                    </span>
                    <input
                      className="input mono"
                      value={form.work_dir || ""}
                      onChange={(e) =>
                        setForm({ ...form, work_dir: e.target.value })
                      }
                    />
                  </label>
                </div>
                <div className="form-grid-2">
                  <label className="field">
                    <span className="field-label">
                      启动参数 <em>JSON 字符串数组</em>
                    </span>
                    <textarea
                      className="textarea mono"
                      rows={4}
                      value={argsText}
                      onChange={(e) => setArgsText(e.target.value)}
                    />
                  </label>
                  <label className="field">
                    <span className="field-label">
                      环境变量映射 <em>JSON 对象</em>
                    </span>
                    <textarea
                      className="textarea mono"
                      rows={4}
                      value={envText}
                      onChange={(e) => setEnvText(e.target.value)}
                    />
                  </label>
                </div>
              </>
            ) : (
              <>
                <label className="field">
                  <span className="field-label">
                    {form.kind === "mcp" ? "MCP Endpoint" : "HTTP API 基地址"}
                  </span>
                  <input
                    required
                    type="url"
                    className="input mono"
                    value={form.endpoint}
                    onChange={(e) =>
                      setForm({ ...form, endpoint: e.target.value })
                    }
                  />
                </label>
                <label className="field">
                  <span className="field-label">
                    请求头（Headers） <em>JSON 对象，支持 {"${ENV_VAR}"}</em>
                  </span>
                  <textarea
                    className="textarea mono"
                    rows={4}
                    value={headersText}
                    onChange={(e) => setHeadersText(e.target.value)}
                  />
                </label>
              </>
            )}
          </>
        )}
      </div>

      <div className="card-footer">
        <span className="field-hint">
          保存表单时会自动检测连接并同步 MCP 工具列表。
        </span>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy && <Loader2 size={14} className="spin" />}
          {mode === "json" ? "保存 JSON" : "保存并测试连接"}
        </button>
      </div>
    </form>
  );
}

/* ==========================================================================
   Add / Import Service Modal
   ========================================================================== */
function AddServiceModal({
  initialMode,
  onClose,
  onCreated,
}: {
  initialMode: "form" | "paste";
  onClose: () => void;
  onCreated: (id: string, message: string) => Promise<void>;
}) {
  const [tab, setTab] = useState<"form" | "paste">(initialMode);
  const [protocol, setProtocol] = useState<"http" | "stdio" | "api">("http");
  const [name, setName] = useState("");
  const [customId, setCustomId] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [command, setCommand] = useState("");
  const [argsText, setArgsText] = useState("[]");
  const [headersText, setHeadersText] = useState("{}");
  const [envText, setEnvText] = useState("{}");
  const [workDir, setWorkDir] = useState("");
  const [pasteText, setPasteText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<any>(null);

  function parsePasteIntoForm() {
    setError(null);
    try {
      const raw = JSON.parse(pasteText);
      if (raw.kind && raw.id) {
        setName(raw.name || raw.id);
        setCustomId(raw.id);
        setProtocol(
          raw.kind === "http"
            ? "api"
            : raw.transport === "stdio"
              ? "stdio"
              : "http",
        );
        setEndpoint(raw.endpoint || "");
        setCommand(raw.command || "");
        setArgsText(pretty(raw.args || []));
        setHeadersText(pretty(raw.headers || {}));
        setEnvText(pretty(raw.env || {}));
        setWorkDir(raw.work_dir || "");
        setTab("form");
        return;
      }
      const entries = raw.mcpServers
        ? Object.entries(raw.mcpServers)
        : [["my-server", raw]];
      if (entries.length !== 1) {
        throw new Error("请一次粘贴单个 mcpServers 服务条目。");
      }
      const [srvName, entry] = entries[0] as [string, any];
      if (
        entry.type &&
        !["http", "streamable-http", "stdio"].includes(entry.type)
      ) {
        throw new Error("当前支持 Streamable HTTP 与 stdio 传输类型。");
      }
      setName(srvName);
      setCustomId(srvName.toLowerCase().replace(/[^a-z0-9._-]+/g, "-"));
      setProtocol(entry.command ? "stdio" : "http");
      setEndpoint(entry.url || "");
      setCommand(entry.command || "");
      setArgsText(pretty(entry.args || []));
      setHeadersText(pretty(entry.headers || {}));
      setEnvText(pretty(entry.env || {}));
      setWorkDir(entry.cwd || "");
      setTab("form");
    } catch (err) {
      setError(err);
    }
  }

  async function handleConnect(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const id =
        customId.trim() ||
        name
          .toLowerCase()
          .replace(/[^a-z0-9._-]+/g, "-")
          .replace(/^-|-$/g, "") ||
        `source-${Date.now().toString(36)}`;
      const args = protocol === "stdio" ? JSON.parse(argsText) : [];
      if (
        !Array.isArray(args) ||
        args.some((item: unknown) => typeof item !== "string")
      ) {
        throw new Error('启动参数必须是字符串数组，例如 ["--port", "3000"]');
      }
      const payload: Server = {
        id,
        name: name.trim() || id,
        description: "",
        kind: protocol === "api" ? "http" : "mcp",
        transport: protocol === "stdio" ? "stdio" : "http",
        endpoint: protocol === "stdio" ? "" : endpoint.trim(),
        command: protocol === "stdio" ? command.trim() : undefined,
        args,
        headers: protocol === "stdio" ? {} : JSON.parse(headersText),
        env: protocol === "stdio" ? JSON.parse(envText) : {},
        work_dir: protocol === "stdio" ? workDir.trim() : undefined,
        enabled: true,
      };
      const check = await api<ConnectionCheck>("/setup/source", {
        method: "POST",
        body: pretty(payload),
      });
      await onCreated(id, check.message || "服务已连接");
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="添加上游服务"
      onClose={onClose}
      maxWidth={640}
      footer={
        tab === "paste" ? (
          <>
            <button type="button" className="btn" onClick={onClose}>
              取消
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={!pasteText.trim()}
              onClick={parsePasteIntoForm}
            >
              解析并填入表单
            </button>
          </>
        ) : (
          <>
            <button type="button" className="btn" onClick={onClose}>
              取消
            </button>
            <button
              type="submit"
              form="add-service-form"
              className="btn btn-primary"
              disabled={busy}
            >
              {busy && <Loader2 size={14} className="spin" />}
              {protocol === "api" ? "保存并检查地址" : "连接并读取工具"}
            </button>
          </>
        )
      }
    >
      <div className="form-stack">
        <div className="segmented" style={{ alignSelf: "flex-start" }}>
          <button
            type="button"
            className={tab === "form" ? "active" : ""}
            onClick={() => setTab("form")}
          >
            填写连接参数
          </button>
          <button
            type="button"
            className={tab === "paste" ? "active" : ""}
            onClick={() => setTab("paste")}
          >
            粘贴 mcpServers JSON
          </button>
        </div>

        {error && (
          <AlertBox
            tone="error"
            title={error.message || String(error)}
            detail={error.detail}
          />
        )}

        {tab === "paste" ? (
          <label className="field">
            <span className="field-label">粘贴单个 MCP 配置条目</span>
            <textarea
              className="textarea mono"
              rows={9}
              value={pasteText}
              onChange={(e) => setPasteText(e.target.value)}
              placeholder={
                '{\n  "mcpServers": {\n    "github": {\n      "type": "http",\n      "url": "https://api.githubcopilot.com/mcp/"\n    }\n  }\n}'
              }
              spellCheck={false}
            />
            <span className="field-hint">
              支持标准客户端 <code>mcpServers</code> 片段或网关服务定义 JSON。
            </span>
          </label>
        ) : (
          <form
            id="add-service-form"
            className="form-stack"
            onSubmit={handleConnect}
          >
            <div className="option-cards">
              <button
                type="button"
                className={`option-card ${protocol === "http" ? "selected" : ""}`}
                onClick={() => setProtocol("http")}
              >
                <span>
                  <strong>MCP 地址</strong>
                  <small>Streamable HTTP</small>
                </span>
              </button>
              <button
                type="button"
                className={`option-card ${protocol === "stdio" ? "selected" : ""}`}
                onClick={() => setProtocol("stdio")}
              >
                <span>
                  <strong>本地进程</strong>
                  <small>stdio 标准输入输出</small>
                </span>
              </button>
              <button
                type="button"
                className={`option-card ${protocol === "api" ? "selected" : ""}`}
                onClick={() => setProtocol("api")}
              >
                <span>
                  <strong>HTTP API</strong>
                  <small>REST / OpenAPI 服务</small>
                </span>
              </button>
            </div>

            <div className="form-grid-2">
              <label className="field">
                <span className="field-label">服务名称</span>
                <input
                  required
                  className="input"
                  placeholder="例如：GitHub 或 内部发布平台"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label className="field">
                <span className="field-label">
                  服务 ID <em>留空自动生成</em>
                </span>
                <input
                  className="input mono"
                  placeholder="github"
                  value={customId}
                  onChange={(e) => setCustomId(e.target.value)}
                />
              </label>
            </div>

            {protocol === "stdio" ? (
              <>
                <label className="field">
                  <span className="field-label">启动命令</span>
                  <input
                    required
                    className="input mono"
                    placeholder="npx、uvx 或可执行程序绝对路径"
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                  />
                </label>
                <label className="field">
                  <span className="field-label">
                    启动参数 <em>JSON 字符串数组</em>
                  </span>
                  <textarea
                    className="textarea mono"
                    rows={3}
                    value={argsText}
                    onChange={(e) => setArgsText(e.target.value)}
                  />
                </label>
              </>
            ) : (
              <label className="field">
                <span className="field-label">
                  {protocol === "api" ? "API 基地址" : "MCP 服务地址"}
                </span>
                <input
                  required
                  type="url"
                  className="input mono"
                  placeholder={
                    protocol === "api"
                      ? "https://api.internal.example/v1"
                      : "http://127.0.0.1:3000/mcp"
                  }
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </label>
            )}

            <details className="sail-details">
              <summary>认证请求头 / 环境变量（可选）</summary>
              {protocol === "stdio" ? (
                <div className="form-stack">
                  <label className="field">
                    <span className="field-label">工作目录</span>
                    <input
                      className="input mono"
                      value={workDir}
                      onChange={(e) => setWorkDir(e.target.value)}
                    />
                  </label>
                  <label className="field">
                    <span className="field-label">环境变量 JSON</span>
                    <textarea
                      className="textarea mono"
                      rows={3}
                      value={envText}
                      onChange={(e) => setEnvText(e.target.value)}
                    />
                    <span className="field-hint">
                      如 <code>{'{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}'}</code>
                    </span>
                  </label>
                </div>
              ) : (
                <label className="field">
                  <span className="field-label">请求头 JSON</span>
                  <textarea
                    className="textarea mono"
                    rows={3}
                    value={headersText}
                    onChange={(e) => setHeadersText(e.target.value)}
                  />
                  <span className="field-hint">
                    如{" "}
                    <code>
                      {'{"Authorization": "Bearer ${SERVICE_TOKEN}"}'}
                    </code>
                  </span>
                </label>
              )}
            </details>
          </form>
        )}
      </div>
    </Modal>
  );
}

/* ==========================================================================
   Add HTTP Tool Modal
   ========================================================================== */
function AddHttpToolModal({
  serverId,
  onClose,
  onCreated,
}: {
  serverId: string;
  onClose: () => void;
  onCreated: () => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [method, setMethod] = useState("GET");
  const [path, setPath] = useState("");
  const [description, setDescription] = useState("");
  const [readOnly, setReadOnly] = useState(true);
  const [schemaText, setSchemaText] = useState(
    pretty({
      type: "object",
      properties: {},
      required: [],
      additionalProperties: false,
    }),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<any>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/tools", {
        method: "PUT",
        body: pretty({
          id: `${serverId}.${name.trim()}`,
          server_id: serverId,
          name: name.trim(),
          description: description.trim(),
          method,
          path: path.trim(),
          read_only: readOnly,
          input_schema: JSON.parse(schemaText),
          actors: ["developer"],
          tags: [],
          enabled: true,
        }),
      });
      await onCreated();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="添加 HTTP 工具"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button
            type="submit"
            form="add-http-tool-form"
            className="btn btn-primary"
            disabled={busy}
          >
            {busy && <Loader2 size={14} className="spin" />}
            保存并启用工具
          </button>
        </>
      }
    >
      <form
        id="add-http-tool-form"
        className="form-stack"
        onSubmit={submit}
      >
        {error && (
          <AlertBox
            tone="error"
            title={error.message || String(error)}
            detail={error.detail}
          />
        )}
        <div className="form-grid-2">
          <label className="field">
            <span className="field-label">工具名称</span>
            <input
              required
              className="input mono"
              placeholder="search_documents"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="field">
            <span className="field-label">HTTP 方法</span>
            <select
              className="select"
              value={method}
              onChange={(e) => {
                setMethod(e.target.value);
                setReadOnly(e.target.value === "GET");
              }}
            >
              {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
                <option key={m}>{m}</option>
              ))}
            </select>
          </label>
        </div>

        <label className="field">
          <span className="field-label">相对路径</span>
          <input
            required
            className="input mono"
            placeholder="/documents/search"
            value={path}
            onChange={(e) => setPath(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="field-label">工具用途描述（供路由模型判断相关性）</span>
          <input
            required
            className="input"
            placeholder="说明该工具解决什么问题、适用哪些查询场景"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="field-label">输入参数 JSON Schema</span>
          <textarea
            className="textarea mono"
            rows={6}
            value={schemaText}
            onChange={(e) => setSchemaText(e.target.value)}
            spellCheck={false}
          />
        </label>

        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={readOnly}
            onChange={(e) => setReadOnly(e.target.checked)}
          />
          声明为只读工具（不修改上游状态）
        </label>
      </form>
    </Modal>
  );
}

/* ==========================================================================
   Structured Tool Inspector & Edit Slide-Over Drawer
   ========================================================================== */
export function ToolEditDrawer({
  tool,
  onClose,
  onSaved,
}: {
  tool: Tool;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [tab, setTab] = useState<"structured" | "json">("structured");
  const [description, setDescription] = useState(tool.description);
  const [enabled, setEnabled] = useState(tool.enabled);
  const [readOnly, setReadOnly] = useState(tool.read_only);
  const [actorsText, setActorsText] = useState(tool.actors.join(", "));
  const [rawJson, setRawJson] = useState(pretty(tool));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<any>(null);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const payload =
        tab === "json"
          ? JSON.parse(rawJson)
          : {
              ...tool,
              description,
              enabled,
              read_only: readOnly,
              actors: actorsText
                .split(",")
                .map((s) => s.trim())
                .filter(Boolean),
            };
      await api("/tools", { method: "PUT", body: pretty(payload) });
      await onSaved();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer
      title={tool.name}
      subtitle={tool.id}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            取消
          </button>
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy}
            onClick={save}
          >
            {busy && <Loader2 size={14} className="spin" />}
            保存更改
          </button>
        </>
      }
    >
      <div className="form-stack">
        <div className="segmented" style={{ alignSelf: "flex-start" }}>
          <button
            type="button"
            className={tab === "structured" ? "active" : ""}
            onClick={() => setTab("structured")}
          >
            属性与 Schema
          </button>
          <button
            type="button"
            className={tab === "json" ? "active" : ""}
            onClick={() => {
              setRawJson(
                pretty({
                  ...tool,
                  description,
                  enabled,
                  read_only: readOnly,
                  actors: actorsText
                    .split(",")
                    .map((s) => s.trim())
                    .filter(Boolean),
                }),
              );
              setTab("json");
            }}
          >
            完整 JSON
          </button>
        </div>

        {error && (
          <AlertBox
            tone="error"
            title={error.message || String(error)}
            detail={error.detail}
          />
        )}

        {tab === "json" ? (
          <textarea
            className="textarea mono"
            rows={22}
            value={rawJson}
            onChange={(e) => setRawJson(e.target.value)}
            spellCheck={false}
          />
        ) : (
          <>
            <div className="kv-grid">
              <div className="kv-item">
                <dt>开放给路由模型</dt>
                <dd style={{ marginTop: 6 }}>
                  <Switch
                    checked={enabled}
                    label="启用此工具"
                    onChange={() => setEnabled(!enabled)}
                  />
                </dd>
              </div>
              <div className="kv-item">
                <dt>只读标记</dt>
                <dd style={{ marginTop: 6 }}>
                  <Switch
                    checked={readOnly}
                    label="只读工具"
                    onChange={() => setReadOnly(!readOnly)}
                  />
                </dd>
              </div>
            </div>

            <label className="field">
              <span className="field-label">工具功能描述</span>
              <textarea
                className="textarea"
                rows={4}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
              <span className="field-hint">
                Decisions 路由模型依据此描述评估与当前任务的相关性概率。
              </span>
            </label>

            <label className="field">
              <span className="field-label">
                允许访问身份（Actors） <em>逗号分隔</em>
              </span>
              <input
                className="input mono"
                value={actorsText}
                onChange={(e) => setActorsText(e.target.value)}
                placeholder="developer, observer"
              />
            </label>

            <div className="field">
              <span className="field-label">
                输入参数 Schema（只读预览）
                <CopyButton
                  value={pretty(tool.input_schema)}
                  label="复制 Schema"
                  variant="ghost"
                />
              </span>
              <div className="navy-card">
                <pre className="navy-pre">{pretty(tool.input_schema)}</pre>
              </div>
            </div>
          </>
        )}
      </div>
    </Drawer>
  );
}

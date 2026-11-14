import { useEffect, useState } from "react";
import { Search } from "lucide-react";
import { computeGatewayTelemetry } from "./analytics";
import { ToolEditDrawer } from "./ServicesPage";
import { api, pretty, type Bootstrap, type Tool } from "./types";
import { Badge, Switch } from "./ui";

type Props = {
  state: Bootstrap;
  refresh: () => Promise<Bootstrap>;
  initialServerId?: string | null;
  initialAccess?: "all" | "ro" | "rw" | null;
  onConsumedInitial?: () => void;
  onSelectServer: (id: string) => void;
  notify: (msg: string) => void;
};

export function CatalogPage({
  state,
  refresh,
  initialServerId,
  initialAccess,
  onConsumedInitial,
  onSelectServer,
  notify,
}: Props) {
  const [query, setQuery] = useState("");
  const [serverFilter, setServerFilter] = useState(initialServerId || "all");
  const [statusFilter, setStatusFilter] = useState<"all" | "on" | "off">("all");
  const [accessFilter, setAccessFilter] = useState<"all" | "ro" | "rw">(
    initialAccess || "all",
  );
  const [actorFilter, setActorFilter] = useState("all");
  const [sortMode, setSortMode] = useState<"hits" | "name">("hits");
  const [showDemo, setShowDemo] = useState(false);
  const [page, setPage] = useState(1);
  const [editingTool, setEditingTool] = useState<Tool | null>(null);

  const PAGE_SIZE = 40;

  useEffect(() => {
    let consumed = false;
    if (initialServerId) {
      setServerFilter(initialServerId);
      consumed = true;
    }
    if (initialAccess) {
      setAccessFilter(initialAccess);
      consumed = true;
    }
    if (consumed) {
      onConsumedInitial?.();
    }
  }, [initialServerId, initialAccess]);

  const servers = state.registry.servers;
  const tools = state.registry.tools;
  const realServers = servers.filter((s) => !s.demo);
  const demoServers = servers.filter((s) => s.demo);
  const realTools = tools.filter((t) =>
    realServers.some((s) => s.id === t.server_id),
  );
  const enabledRealTools = realTools.filter((t) => t.enabled);
  const writeEnabledCount = enabledRealTools.filter((t) => !t.read_only).length;

  const telemetry = computeGatewayTelemetry(state);
  const hitCounts = telemetry.toolHitCounts;
  const hitRealToolCount = realTools.filter(
    (t) => (hitCounts[t.id] || 0) > 0,
  ).length;

  const actorOptions = Array.from(
    new Set(
      realTools.flatMap((t) => t.actors || []).filter((a) => a && a !== "*"),
    ),
  );

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

  const filteredTools = tools
    .filter((t) => {
      const srv = servers.find((s) => s.id === t.server_id);
      if (!showDemo && srv?.demo) return false;
      if (serverFilter !== "all" && t.server_id !== serverFilter) return false;
      if (statusFilter === "on" && !t.enabled) return false;
      if (statusFilter === "off" && t.enabled) return false;
      if (accessFilter === "ro" && !t.read_only) return false;
      if (accessFilter === "rw" && t.read_only) return false;
      if (
        actorFilter !== "all" &&
        !t.actors.includes("*") &&
        !t.actors.includes(actorFilter)
      ) {
        return false;
      }
      if (!query.trim()) return true;
      const q = query.toLowerCase();
      return `${t.name} ${t.description} ${t.id} ${t.server_id}`
        .toLowerCase()
        .includes(q);
    })
    .sort((a, b) => {
      if (sortMode === "hits") {
        const diff = (hitCounts[b.id] || 0) - (hitCounts[a.id] || 0);
        if (diff !== 0) return diff;
        if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
      }
      return a.name.localeCompare(b.name);
    });

  useEffect(() => {
    setPage(1);
  }, [
    query,
    serverFilter,
    statusFilter,
    accessFilter,
    actorFilter,
    sortMode,
    showDemo,
  ]);

  const totalPages = Math.max(1, Math.ceil(filteredTools.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const startIdx = (safePage - 1) * PAGE_SIZE;
  const pagedTools = filteredTools.slice(startIdx, startIdx + PAGE_SIZE);

  return (
    <div>
      <div className="page-header">
        <div className="page-header-main">
          <h1>工具目录</h1>
          <p>
            跨服务治理注册表中的全部工具契约：审计语义描述、控制候选池开放范围、收敛写操作暴露面，并识别长期零命中的冗余工具。
          </p>
        </div>
        <div className="page-header-actions">
          <span
            className="tnum"
            style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
          >
            已开放 <strong style={{ color: "var(--ink-strong)" }}>{enabledRealTools.length}</strong> /{" "}
            {realTools.length} · 写操作{" "}
            <strong style={{ color: "var(--ink-strong)" }}>{writeEnabledCount}</strong> · 近期命中过{" "}
            <strong style={{ color: "var(--ink-strong)" }}>{hitRealToolCount}</strong> 个
          </span>
        </div>
      </div>

      <section className="card">
        <div className="toolbar">
          <div className="toolbar-group">
            <div className="search-box">
              <Search size={14} />
              <input
                aria-label="搜索工具目录"
                className="input"
                placeholder="搜索工具名、语义描述或 ID…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>

            <select
              aria-label="按上游服务筛选"
              className="select"
              style={{ height: 30, width: 145, fontSize: 12.5 }}
              value={serverFilter}
              onChange={(e) => setServerFilter(e.target.value)}
            >
              <option value="all">所有上游服务</option>
              {(showDemo ? servers : realServers).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>

            <div className="segmented" role="group" aria-label="开放状态筛选">
              {(
                [
                  ["all", "全部"],
                  ["on", "已开放"],
                  ["off", "未开放"],
                ] as const
              ).map(([val, label]) => (
                <button
                  key={val}
                  type="button"
                  className={statusFilter === val ? "active" : ""}
                  onClick={() => setStatusFilter(val)}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="segmented" role="group" aria-label="读写属性筛选">
              {(
                [
                  ["all", "全部属性"],
                  ["ro", "只读"],
                  ["rw", "写操作"],
                ] as const
              ).map(([val, label]) => (
                <button
                  key={val}
                  type="button"
                  className={accessFilter === val ? "active" : ""}
                  onClick={() => setAccessFilter(val)}
                >
                  {label}
                </button>
              ))}
            </div>

            {actorOptions.length > 0 && (
              <select
                aria-label="按身份筛选"
                className="select"
                style={{ height: 30, width: 125, fontSize: 12.5 }}
                value={actorFilter}
                onChange={(e) => setActorFilter(e.target.value)}
              >
                <option value="all">所有身份</option>
                {actorOptions.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
            )}
          </div>

          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 12,
              flexWrap: "wrap",
            }}
          >
            <select
              aria-label="排序方式"
              className="select"
              style={{ height: 30, width: 132, fontSize: 12.5 }}
              value={sortMode}
              onChange={(e) => setSortMode(e.target.value as "hits" | "name")}
            >
              <option value="hits">按近期命中排序</option>
              <option value="name">按名称 A–Z</option>
            </select>

            <label className="checkbox-field" style={{ fontSize: 12.5 }}>
              <input
                type="checkbox"
                checked={showDemo}
                onChange={(e) => {
                  const next = e.target.checked;
                  setShowDemo(next);
                  if (
                    !next &&
                    demoServers.some((s) => s.id === serverFilter)
                  ) {
                    setServerFilter("all");
                  }
                }}
              />
              包含演示工具
            </label>
          </div>
        </div>

        {filteredTools.length === 0 ? (
          <div className="empty-box">
            <h3>没有匹配的工具契约</h3>
            <p>尝试放宽服务、开放状态或读写筛选条件。</p>
          </div>
        ) : (
          <>
            <div className="table-wrap">
              <table className="sail-table">
                <thead>
                  <tr>
                    <th>工具标识与语义描述</th>
                    <th>上游服务</th>
                    <th>读写属性</th>
                    <th>允许身份</th>
                    <th style={{ textAlign: "right" }}>近期命中</th>
                    <th>开放路由</th>
                    <th style={{ textAlign: "right" }}>契约</th>
                  </tr>
                </thead>
                <tbody>
                  {pagedTools.map((t) => {
                    const srv = servers.find((s) => s.id === t.server_id);
                    const hits = hitCounts[t.id] || 0;
                    return (
                      <tr key={t.id}>
                        <td>
                          <span className="cell-primary mono">{t.name}</span>
                          <span className="cell-sub">
                            {t.description ||
                              "缺少语义描述，可能降低路由召回率，建议补充。"}
                          </span>
                        </td>
                        <td style={{ width: 145 }}>
                          <button
                            type="button"
                            className="btn-link"
                            onClick={() => onSelectServer(t.server_id)}
                          >
                            {srv?.name || t.server_id}
                          </button>
                          {srv?.demo && (
                            <span style={{ marginLeft: 6 }}>
                              <Badge>DEMO</Badge>
                            </span>
                          )}
                        </td>
                        <td style={{ width: 96 }}>
                          <span
                            style={{
                              fontSize: 12.5,
                              color: t.read_only
                                ? "var(--ink-muted)"
                                : "var(--ink-strong)",
                              fontWeight: t.read_only ? 400 : 500,
                            }}
                          >
                            {t.read_only ? "只读" : "写操作"}
                          </span>
                        </td>
                        <td style={{ width: 135 }}>
                          <code
                            style={{ fontSize: 12, color: "var(--ink-muted)" }}
                          >
                            {t.actors.join(", ")}
                          </code>
                        </td>
                        <td
                          className="tnum"
                          style={{ width: 96, textAlign: "right" }}
                        >
                          {hits > 0 ? (
                            <span
                              style={{
                                fontSize: 12.5,
                                fontWeight: 550,
                                color: "var(--ink-strong)",
                              }}
                            >
                              {hits} 次
                            </span>
                          ) : (
                            <span
                              style={{
                                fontSize: 12.5,
                                color: "var(--ink-faint)",
                              }}
                            >
                              —
                            </span>
                          )}
                        </td>
                        <td style={{ width: 96 }}>
                          <Switch
                            checked={t.enabled}
                            label={`${t.name} 启用状态`}
                            onChange={() => toggleTool(t)}
                          />
                        </td>
                        <td style={{ width: 96, textAlign: "right" }}>
                          <button
                            type="button"
                            className="btn btn-sm"
                            onClick={() => setEditingTool(t)}
                          >
                            查看 / 编辑
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className="card-footer">
              <span
                className="tnum"
                style={{ fontSize: 12.5, color: "var(--ink-muted)" }}
              >
                显示{" "}
                <strong style={{ color: "var(--ink-strong)" }}>
                  {startIdx + 1}–
                  {Math.min(startIdx + PAGE_SIZE, filteredTools.length)}
                </strong>{" "}
                / 共 {filteredTools.length} 个匹配工具
              </span>

              {filteredTools.length > PAGE_SIZE && (
                <div
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 8,
                  }}
                >
                  <span
                    className="tnum"
                    style={{ fontSize: 12, color: "var(--ink-muted)" }}
                  >
                    第 {safePage} / {totalPages} 页
                  </span>
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={safePage <= 1}
                    onClick={() => setPage(Math.max(1, safePage - 1))}
                  >
                    上一页
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={safePage >= totalPages}
                    onClick={() => setPage(Math.min(totalPages, safePage + 1))}
                  >
                    下一页
                  </button>
                </div>
              )}
            </div>
          </>
        )}
      </section>

      {editingTool && (
        <ToolEditDrawer
          tool={editingTool}
          onClose={() => setEditingTool(null)}
          onSaved={async () => {
            await refresh();
            setEditingTool(null);
            notify("工具契约已保存");
          }}
        />
      )}
    </div>
  );
}

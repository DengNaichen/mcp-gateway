import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  Home,
  Layers,
  Loader2,
  Server as ServerIcon,
  Shield,
  Waypoints,
  X,
} from "lucide-react";
import "./sail.css";
import { api, type Bootstrap, type Event } from "./types";
import { CopyButton, GatewayLogo } from "./ui";
import { ConnectClientModal } from "./ConnectClientModal";
import { LoginPage } from "./LoginPage";
import { OverviewPage } from "./OverviewPage";
import { ServicesPage } from "./ServicesPage";
import { CatalogPage } from "./CatalogPage";
import { PlaygroundPage, type ReplayDraft } from "./PlaygroundPage";
import { LogsPage } from "./LogsPage";
import { SettingsPage } from "./SettingsPage";

type ViewId =
  | "overview"
  | "upstreams"
  | "catalog"
  | "policies"
  | "logs"
  | "playground";

type RouteState = {
  view: ViewId;
  subId: string | null;
};

function parseHash(): RouteState {
  const raw = window.location.hash.replace(/^#\/?/, "");
  if (!raw) return { view: "overview", subId: null };
  const [first, ...rest] = raw.split("/");
  const subId = rest.length ? decodeURIComponent(rest.join("/")) : null;
  if (first === "services") return { view: "upstreams", subId };
  if (first === "settings") return { view: "policies", subId };
  if (
    first === "overview" ||
    first === "upstreams" ||
    first === "catalog" ||
    first === "policies" ||
    first === "logs" ||
    first === "playground"
  ) {
    return { view: first, subId };
  }
  return { view: "overview", subId: null };
}

function writeHash(view: ViewId, subId?: string | null) {
  const next = subId
    ? `#${view}/${encodeURIComponent(subId)}`
    : `#${view}`;
  if (window.location.hash !== next) {
    window.history.pushState(null, "", next);
  }
}

const viewLabels: Record<ViewId, string> = {
  overview: "运行大盘",
  upstreams: "上游服务",
  catalog: "工具目录",
  policies: "访问与路由",
  logs: "请求日志",
  playground: "路由沙盒",
};

function App() {
  const [route, setRoute] = useState<RouteState>(() => parseHash());
  const [openAddService, setOpenAddService] = useState(false);
  const [connectModalOpen, setConnectModalOpen] = useState(false);
  const [catalogInit, setCatalogInit] = useState<{
    serverId?: string | null;
    access?: "all" | "ro" | "rw" | null;
  } | null>(null);
  const [replayDraft, setReplayDraft] = useState<ReplayDraft | null>(null);
  const [state, setState] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState("");
  const [needsLogin, setNeedsLogin] = useState(false);
  const [toast, setToast] = useState("");

  function navigate(view: ViewId, subId: string | null = null) {
    setRoute({ view, subId });
    writeHash(view, subId);
  }

  function openSandboxReplay(payload?: { intent: string; actor?: string }) {
    if (payload && payload.intent) {
      setReplayDraft((prev) => ({
        intent: payload.intent,
        actor: payload.actor,
        seq: (prev?.seq || 0) + 1,
      }));
    }
    navigate("playground");
  }

  useEffect(() => {
    const onPop = () => setRoute(parseHash());
    window.addEventListener("popstate", onPop);
    window.addEventListener("hashchange", onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener("hashchange", onPop);
    };
  }, []);

  async function refresh() {
    const data = await api<Bootstrap>("/bootstrap");
    setState(data);
    setNeedsLogin(false);
    setBootError("");
    return data;
  }

  useEffect(() => {
    const onUnauthorized = () => {
      setState(null);
      setNeedsLogin(true);
    };
    window.addEventListener("gateway:unauthorized", onUnauthorized);
    return () =>
      window.removeEventListener("gateway:unauthorized", onUnauthorized);
  }, []);

  useEffect(() => {
    refresh().catch((e) => setBootError(e.message));
  }, []);

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(""), 3200);
    return () => clearTimeout(timer);
  }, [toast]);

  // Poll events when viewing Logs or Overview
  useEffect(() => {
    if (route.view !== "logs" && route.view !== "overview") return;
    const timer = setInterval(() => {
      if (route.view === "logs") {
        api<Event[]>("/events")
          .then((events) =>
            setState((prev) => (prev ? { ...prev, events } : prev)),
          )
          .catch(() => {});
      } else {
        refresh().catch(() => {});
      }
    }, 4000);
    return () => clearInterval(timer);
  }, [route.view]);

  if (needsLogin) return <LoginPage onLogin={refresh} />;

  if (!state) {
    return (
      <div className="center-screen">
        <div
          className="auth-card"
          style={{ alignItems: "center", textAlign: "center" }}
        >
          <span className="sail-brand-mark">
            <GatewayLogo size={18} />
          </span>
          {bootError ? (
            <>
              <h2 style={{ fontSize: 16, color: "var(--ink-strong)" }}>
                无法连接网关控制面
              </h2>
              <p className="field-hint">{bootError}</p>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => window.location.reload()}
              >
                重试连接
              </button>
            </>
          ) : (
            <>
              <Loader2 size={20} className="spin" />
              <p className="field-hint">正在加载网关控制面…</p>
            </>
          )}
        </div>
      </div>
    );
  }

  const realServers = state.registry.servers.filter((s) => !s.demo);
  const activeRealServers = realServers.filter((s) => s.enabled);
  const realTools = state.registry.tools.filter((t) =>
    realServers.some((s) => s.id === t.server_id),
  );
  const activeService =
    route.view === "upstreams" && route.subId
      ? state.registry.servers.find((s) => s.id === route.subId)
      : null;

  const modelReady =
    state.setup.model_ready && !state.setup.model_check?.stale;

  return (
    <div className="sail-shell">
      <a className="skip-link" href="#workspace-main">
        跳到主工作区
      </a>

      {/* Light Stripe Sail Enterprise Control Plane Sidebar */}
      <aside className="sail-sidebar">
        <a
          href="#overview"
          className="sail-brand"
          onClick={(e) => {
            e.preventDefault();
            navigate("overview");
          }}
        >
          <span className="sail-brand-mark">
            <GatewayLogo size={17} />
          </span>
          <span className="sail-brand-text">
            <span className="sail-brand-title">MCP Gateway</span>
            <span className="sail-brand-sub">
              {state.access?.shared ? "共享网关控制面" : "本地网关控制面"}
            </span>
          </span>
        </a>

        <div className="sail-nav-section">
          <div className="sail-nav-heading">控制面</div>
          <nav className="sail-nav-list" aria-label="控制面导航">
            <button
              type="button"
              className={`sail-nav-item ${route.view === "overview" ? "active" : ""}`}
              onClick={() => navigate("overview")}
            >
              <Home size={15} strokeWidth={1.65} />
              <span>运行大盘</span>
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "upstreams" ? "active" : ""}`}
              onClick={() => navigate("upstreams")}
            >
              <ServerIcon size={15} strokeWidth={1.65} />
              <span>上游服务</span>
              <span className="sail-nav-badge">{activeRealServers.length}</span>
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "catalog" ? "active" : ""}`}
              onClick={() => navigate("catalog")}
            >
              <Layers size={15} strokeWidth={1.65} />
              <span>工具目录</span>
              <span className="sail-nav-badge">
                {state.setup.enabled_tools}/{realTools.length}
              </span>
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "policies" ? "active" : ""}`}
              onClick={() => navigate("policies")}
            >
              <Shield size={15} strokeWidth={1.65} />
              <span>访问与路由</span>
            </button>
          </nav>
        </div>

        <div className="sail-nav-section">
          <div className="sail-nav-heading">观测与诊断</div>
          <nav className="sail-nav-list" aria-label="观测导航">
            <button
              type="button"
              className={`sail-nav-item ${route.view === "logs" ? "active" : ""}`}
              onClick={() => navigate("logs")}
            >
              <Activity size={15} strokeWidth={1.65} />
              <span>请求日志</span>
              {state.events.length > 0 && (
                <span className="sail-nav-badge">{state.events.length}</span>
              )}
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "playground" ? "active" : ""}`}
              onClick={() => navigate("playground")}
            >
              <Waypoints size={15} strokeWidth={1.65} />
              <span>路由沙盒</span>
            </button>
          </nav>
        </div>

        <div className="sail-sidebar-footer">
          <div className="sail-endpoint-mini">
            <div className="sail-endpoint-mini-top">
              <span
                style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
              >
                <span className="sail-status-dot" />
                数据面监听中
              </span>
              <CopyButton
                value={state.mcp_url}
                label="复制"
                variant="ghost"
                onCopied={() => setToast("已复制 MCP 接入端点")}
              />
            </div>
            <code title={state.mcp_url}>{state.mcp_url}</code>
          </div>
        </div>
      </aside>

      {/* Main Workspace */}
      <div className="sail-main-wrap">
        <header className="sail-topbar">
          <div className="sail-breadcrumbs">
            <button type="button" onClick={() => navigate("overview")}>
              控制面
            </button>
            <span style={{ color: "var(--ink-faint)" }}>/</span>
            {activeService ? (
              <>
                <button type="button" onClick={() => navigate("upstreams")}>
                  上游服务
                </button>
                <span style={{ color: "var(--ink-faint)" }}>/</span>
                <strong>{activeService.name}</strong>
              </>
            ) : (
              <strong>{viewLabels[route.view]}</strong>
            )}
          </div>

          <div className="sail-topbar-actions">
            <button
              type="button"
              className="sail-top-chip"
              onClick={() => navigate("policies")}
              title="查看或修改 Decisions 路由模型配置"
            >
              <span className={`status-dot ${modelReady ? "ok" : ""}`} />
              <span className="mono">
                {state.settings.model || "配置路由模型"}
              </span>
              {!modelReady && state.setup.model_configured && (
                <span style={{ color: "var(--ink-muted)" }}>· 待校验</span>
              )}
            </button>

            <button
              type="button"
              className="btn btn-sm"
              onClick={() => setConnectModalOpen(true)}
            >
              接入客户端
            </button>

            {state.access?.shared && (
              <>
                <span
                  className="mono"
                  style={{ fontSize: 12, color: "var(--ink-muted)" }}
                >
                  {state.access.actor}
                </span>
                <button
                  type="button"
                  className="btn btn-sm"
                  onClick={async () => {
                    try {
                      await api("/auth/logout", {
                        method: "POST",
                        body: "{}",
                      });
                      setState(null);
                      setNeedsLogin(true);
                    } catch (e: any) {
                      setToast(e.message);
                    }
                  }}
                >
                  退出登录
                </button>
              </>
            )}
          </div>
        </header>

        <main id="workspace-main" className="sail-main" tabIndex={-1}>
          {route.view === "overview" && (
            <OverviewPage
              state={state}
              refresh={refresh}
              onOpenUpstreams={(opts) => {
                if (opts?.add) setOpenAddService(true);
                navigate("upstreams", opts?.serverId || null);
              }}
              onOpenCatalog={(opts) => {
                if (opts?.filter) {
                  setCatalogInit({ access: opts.filter });
                }
                navigate("catalog");
              }}
              onOpenPlayground={(replay) => openSandboxReplay(replay)}
              onOpenLogs={(eventId) => navigate("logs", eventId || null)}
              onOpenPolicies={() => navigate("policies")}
              onOpenConnectModal={() => setConnectModalOpen(true)}
              notify={setToast}
            />
          )}

          {route.view === "upstreams" && (
            <ServicesPage
              state={state}
              refresh={refresh}
              selectedServerId={route.subId}
              onSelectServer={(id) => navigate("upstreams", id)}
              onOpenCatalog={(serverId) => {
                setCatalogInit({ serverId });
                navigate("catalog");
              }}
              openAddOnMount={openAddService}
              onConsumedAdd={() => setOpenAddService(false)}
              notify={setToast}
            />
          )}

          {route.view === "catalog" && (
            <CatalogPage
              state={state}
              refresh={refresh}
              initialServerId={catalogInit?.serverId}
              initialAccess={catalogInit?.access}
              onConsumedInitial={() => setCatalogInit(null)}
              onSelectServer={(serverId) => navigate("upstreams", serverId)}
              notify={setToast}
            />
          )}

          {route.view === "policies" && (
            <SettingsPage
              state={state}
              refresh={refresh}
              onOpenServices={() => navigate("upstreams")}
              onOpenCatalog={(opts) => {
                setCatalogInit({ access: opts?.access || "all" });
                navigate("catalog");
              }}
              notify={setToast}
            />
          )}

          {route.view === "logs" && (
            <LogsPage
              events={state.events}
              initialEventId={route.subId}
              onReplay={(payload) => openSandboxReplay(payload)}
              onRefresh={() =>
                refresh()
                  .then(() => setToast("日志已刷新"))
                  .catch((e) => setToast(e.message))
              }
              notify={setToast}
            />
          )}

          {route.view === "playground" && (
            <PlaygroundPage
              state={state}
              refresh={refresh}
              replayDraft={replayDraft}
              onOpenSettings={() => navigate("policies")}
              notify={setToast}
            />
          )}
        </main>
      </div>

      {connectModalOpen && (
        <ConnectClientModal
          state={state}
          onClose={() => setConnectModalOpen(false)}
          notify={setToast}
        />
      )}

      {toast && (
        <div className="sail-toast" role="status">
          <span>{toast}</span>
          <button
            type="button"
            aria-label="关闭通知"
            style={{ color: "#94a3b8", display: "inline-flex" }}
            onClick={() => setToast("")}
          >
            <X size={13} />
          </button>
        </div>
      )}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<App />);

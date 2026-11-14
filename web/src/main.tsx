import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  Check,
  ChevronRight,
  Home,
  Layers,
  Loader2,
  Settings2,
  Sparkles,
  Waypoints,
  X,
} from "lucide-react";
import "./sail.css";
import { api, type Bootstrap, type Event } from "./types";
import { Badge, CopyButton, GatewayLogo } from "./ui";
import { LoginPage } from "./LoginPage";
import { OverviewPage } from "./OverviewPage";
import { ServicesPage } from "./ServicesPage";
import { PlaygroundPage } from "./PlaygroundPage";
import { LogsPage } from "./LogsPage";
import { SettingsPage } from "./SettingsPage";

type ViewId = "overview" | "services" | "playground" | "logs" | "settings";

type RouteState = {
  view: ViewId;
  subId: string | null;
};

function parseHash(): RouteState {
  const raw = window.location.hash.replace(/^#\/?/, "");
  if (!raw) return { view: "overview", subId: null };
  const [first, ...rest] = raw.split("/");
  const subId = rest.length ? decodeURIComponent(rest.join("/")) : null;
  if (
    first === "overview" ||
    first === "services" ||
    first === "playground" ||
    first === "logs" ||
    first === "settings"
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
  overview: "概览",
  services: "服务与工具",
  playground: "路由调试",
  logs: "调用日志",
  settings: "路由设置",
};

function App() {
  const [route, setRoute] = useState<RouteState>(() => parseHash());
  const [openAddService, setOpenAddService] = useState(false);
  const [state, setState] = useState<Bootstrap | null>(null);
  const [bootError, setBootError] = useState("");
  const [needsLogin, setNeedsLogin] = useState(false);
  const [toast, setToast] = useState("");

  function navigate(view: ViewId, subId: string | null = null) {
    setRoute({ view, subId });
    writeHash(view, subId);
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

  // Poll events when viewing Logs or Overview waiting for first Agent request
  useEffect(() => {
    if (route.view !== "logs" && route.view !== "overview") return;
    const timer = setInterval(() => {
      if (route.view === "logs") {
        api<Event[]>("/events")
          .then((events) => setState((prev) => (prev ? { ...prev, events } : prev)))
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
        <div className="auth-card" style={{ alignItems: "center", textAlign: "center" }}>
          <span className="sail-brand-mark">
            <GatewayLogo size={18} />
          </span>
          {bootError ? (
            <>
              <h2 style={{ fontSize: 16, color: "var(--ink-strong)" }}>
                无法连接网关后端
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
              <p className="field-hint">正在加载本地网关配置…</p>
            </>
          )}
        </div>
      </div>
    );
  }

  const realServers = state.registry.servers.filter((s) => !s.demo);
  const realTools = state.registry.tools.filter((t) =>
    realServers.some((s) => s.id === t.server_id),
  );
  const activeService =
    route.view === "services" && route.subId
      ? state.registry.servers.find((s) => s.id === route.subId)
      : null;

  const modelReady =
    state.setup.model_ready && !state.setup.model_check?.stale;

  return (
    <div className="sail-shell">
      <a className="skip-link" href="#workspace-main">
        跳到主工作区
      </a>

      {/* Light Stripe Sail Sidebar */}
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
            <span className="sail-brand-title">Gateway Lab</span>
            <span className="sail-brand-sub">
              {state.access?.shared ? "共享 MCP 网关" : "本地 MCP 网关"}
            </span>
          </span>
        </a>

        <div className="sail-nav-section">
          <div className="sail-nav-heading">核心管理</div>
          <nav className="sail-nav-list" aria-label="核心导航">
            <button
              type="button"
              className={`sail-nav-item ${route.view === "overview" ? "active" : ""}`}
              onClick={() => navigate("overview")}
            >
              <Home size={16} />
              <span>概览</span>
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "services" ? "active" : ""}`}
              onClick={() => navigate("services")}
            >
              <Layers size={16} />
              <span>服务与工具</span>
              <span className="sail-nav-badge">
                {state.setup.enabled_tools}/{realTools.length}
              </span>
            </button>
          </nav>
        </div>

        <div className="sail-nav-section">
          <div className="sail-nav-heading">开发者工作台</div>
          <nav className="sail-nav-list" aria-label="开发者导航">
            <button
              type="button"
              className={`sail-nav-item ${route.view === "playground" ? "active" : ""}`}
              onClick={() => navigate("playground")}
            >
              <Waypoints size={16} />
              <span>路由调试</span>
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "logs" ? "active" : ""}`}
              onClick={() => navigate("logs")}
            >
              <Activity size={16} />
              <span>调用日志</span>
              {state.events.length > 0 && (
                <span className="sail-nav-badge">{state.events.length}</span>
              )}
            </button>
            <button
              type="button"
              className={`sail-nav-item ${route.view === "settings" ? "active" : ""}`}
              onClick={() => navigate("settings")}
            >
              <Settings2 size={16} />
              <span>路由设置</span>
            </button>
          </nav>
        </div>

        <div className="sail-sidebar-footer">
          <div className="sail-endpoint-mini">
            <div className="sail-endpoint-mini-top">
              <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <span className="sail-status-dot" />
                MCP 监听中
              </span>
              <CopyButton
                value={state.mcp_url}
                label="复制"
                variant="ghost"
                onCopied={() => setToast("已复制 MCP 接入地址")}
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
              Gateway
            </button>
            <ChevronRight size={13} />
            {activeService ? (
              <>
                <button type="button" onClick={() => navigate("services")}>
                  服务与工具
                </button>
                <ChevronRight size={13} />
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
              onClick={() => navigate("settings")}
              title="查看或修改 Decisions 路由模型配置"
            >
              <Sparkles size={13} style={{ color: "var(--accent)" }} />
              <span className="mono">
                {state.settings.model || "模型未配置"}
              </span>
              <Badge tone={modelReady ? "success" : "warning"}>
                {modelReady
                  ? "已就绪"
                  : state.setup.model_configured
                    ? "待校验"
                    : "去配置"}
              </Badge>
            </button>

            {state.access?.shared && (
              <>
                <Badge tone="info">{state.access.actor}</Badge>
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
              onOpenServices={(opts) => {
                if (opts?.add) setOpenAddService(true);
                navigate("services", opts?.serverId || null);
              }}
              onOpenPlayground={() => navigate("playground")}
              onOpenLogs={(eventId) => navigate("logs", eventId || null)}
              onOpenSettings={() => navigate("settings")}
              notify={setToast}
            />
          )}

          {route.view === "services" && (
            <ServicesPage
              state={state}
              refresh={refresh}
              selectedServerId={route.subId}
              onSelectServer={(id) => navigate("services", id)}
              openAddOnMount={openAddService}
              onConsumedAdd={() => setOpenAddService(false)}
              notify={setToast}
            />
          )}

          {route.view === "playground" && (
            <PlaygroundPage
              state={state}
              refresh={refresh}
              onOpenSettings={() => navigate("settings")}
              notify={setToast}
            />
          )}

          {route.view === "logs" && (
            <LogsPage
              events={state.events}
              initialEventId={route.subId}
              onRefresh={() =>
                refresh()
                  .then(() => setToast("日志已刷新"))
                  .catch((e) => setToast(e.message))
              }
              notify={setToast}
            />
          )}

          {route.view === "settings" && (
            <SettingsPage
              state={state}
              refresh={refresh}
              onOpenServices={() => navigate("services")}
              notify={setToast}
            />
          )}
        </main>
      </div>

      {toast && (
        <div className="sail-toast" role="status">
          <Check size={14} style={{ color: "#34d399" }} />
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

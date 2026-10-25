import {
  ArrowRight,
  ArrowUpRight,
  Check,
  Circle,
  Network,
  Sparkles,
  Terminal,
} from "lucide-react";
import type { Bootstrap } from "./types";

export function GatewayLogo({ size = 24 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M7 7h8v7H7V7Zm10 11h8v7h-8v-7Z"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinejoin="round"
      />
      <path
        d="M15 10.5h5.5a4 4 0 0 1 4 4V18M17 21.5h-5.5a4 4 0 0 1-4-4V14"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinecap="round"
      />
      <circle cx="24.5" cy="7" r="2.2" fill="currentColor" />
      <circle cx="7.5" cy="25" r="2.2" fill="currentColor" />
    </svg>
  );
}

export function GatewayDiagram({ tools }: { tools: number }) {
  return (
    <div className="signal-map">
      <div className="signal-map-heading">
        <span>HOW IT CONNECTS</span>
        <span className="map-local">LOCAL</span>
      </div>
      <svg
        className="signal-drawing"
        viewBox="0 0 400 174"
        role="img"
        aria-label="工作原理：本地 Agent 向网关描述需求，由网关选择合适的 MCP、HTTP 或本地工具。"
      >
        <defs>
          <pattern
            id="signal-grid"
            width="16"
            height="16"
            patternUnits="userSpaceOnUse"
          >
            <circle cx="1" cy="1" r=".7" fill="#c8ccbd" />
          </pattern>
        </defs>
        <rect width="400" height="174" fill="url(#signal-grid)" />
        <g fill="none" stroke="#c2c8b6" strokeWidth="1.5">
          <path d="M93 87h67" />
          <path d="M236 87h28q18 0 18-18V48q0-14 14-14h11" />
          <path d="M236 87h71" />
          <path d="M236 87h28q18 0 18 18v21q0 14 14 14h11" />
        </g>
        <g className="signal-flow" fill="none" stroke="#7c9e36" strokeWidth="2">
          <path d="M93 87h67" />
          <path d="M236 87h28q18 0 18-18V48q0-14 14-14h11" />
          <path d="M236 87h71" />
          <path d="M236 87h28q18 0 18 18v21q0 14 14 14h11" />
        </g>
        <g className="map-agent">
          <rect
            x="23"
            y="61"
            width="70"
            height="52"
            rx="12"
            fill="#fff"
            stroke="#d7dbcf"
          />
          <path
            d="m43 76 7 6-7 6m13 0h10"
            fill="none"
            stroke="#363b31"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <text x="58" y="103" textAnchor="middle">
            AGENT
          </text>
        </g>
        <g className="map-hub">
          <rect x="154" y="43" width="88" height="88" rx="24" fill="#e7edda" />
          <rect x="161" y="50" width="74" height="74" rx="19" fill="#252b23" />
          <g transform="translate(182 61)" color="#d5fb74">
            <path
              d="M7 7h8v7H7V7Zm10 11h8v7h-8v-7Z"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            />
            <path
              d="M15 10.5h5.5a4 4 0 0 1 4 4V18M17 21.5h-5.5a4 4 0 0 1-4-4V14"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            />
            <circle cx="24.5" cy="7" r="2" fill="currentColor" />
            <circle cx="7.5" cy="25" r="2" fill="currentColor" />
          </g>
          <text x="198" y="111" textAnchor="middle" fill="#e6e9e0">
            GATEWAY
          </text>
        </g>
        {[
          { y: 19, label: "MCP" },
          { y: 72, label: "HTTP API" },
          { y: 125, label: "STDIO" },
        ].map(({ y, label }) => (
          <g key={label} className="map-port">
            <rect
              x="307"
              y={y}
              width="79"
              height="30"
              rx="7"
              fill="#fff"
              stroke="#d7dbcf"
            />
            <circle cx="320" cy={y + 15} r="2.5" fill="#92a66d" />
            <text x="350" y={y + 18.5} textAnchor="middle">
              {label}
            </text>
          </g>
        ))}
      </svg>
      <div className="signal-map-caption">
        <span>按需路由</span>
        <code>{String(tools).padStart(2, "0")} TOOLS</code>
      </div>
    </div>
  );
}

export function SetupSidebar({
  state,
  step,
  goTo,
  openDebug,
  openRegistry,
}: {
  state: Bootstrap;
  step: number;
  goTo: (step: number) => void;
  openDebug: () => void;
  openRegistry: () => void;
}) {
  const setup = state.setup;
  const items = [
    {
      icon: Network,
      title: "工具来源",
      value: setup.tools_ready
        ? `${setup.enabled_tools} 个工具已启用`
        : setup.sources
          ? `${setup.sources} 个来源待配置`
          : "等待接入",
      done: setup.tools_ready,
    },
    {
      icon: Sparkles,
      title: "路由模型",
      value: setup.model_ready
        ? state.settings.model
        : setup.model_configured
          ? "已保存，待测试"
          : "等待配置",
      done: setup.model_ready,
    },
    {
      icon: Terminal,
      title: "本地 Agent",
      value: setup.agent_last_request ? "已收到 MCP 请求" : "等待连接",
      done: !!setup.agent_last_request,
    },
  ];
  return (
    <aside className="setup-context" aria-label="接入状态">
      <section className="context-checklist">
        <h2>接入状态</h2>
        <div className="context-items">
          {items.map((item, index) => (
            <button
              className={`context-item ${step === index + 1 ? "current" : ""}`}
              key={item.title}
              onClick={() => goTo(index + 1)}
            >
              <span className="context-item-icon">
                <item.icon size={17} />
              </span>
              <span>
                <strong>{item.title}</strong>
                <small>{item.value}</small>
              </span>
              <span
                className={`context-item-state ${item.done ? "complete" : ""}`}
              >
                {item.done ? <Check size={14} /> : <Circle size={10} />}
              </span>
            </button>
          ))}
        </div>
      </section>
      <div className="context-shortcuts">
        <button className="text-button" onClick={openRegistry}>
          工具注册表 <ArrowUpRight size={14} />
        </button>
        <button className="text-button" onClick={openDebug}>
          路由调试 <ArrowRight size={14} />
        </button>
      </div>
    </aside>
  );
}

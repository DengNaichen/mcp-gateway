import { useState } from "react";
import { pretty, type Bootstrap } from "./types";
import { CopyButton, Modal } from "./ui";

export function ConnectClientModal({
  state,
  onClose,
  notify,
}: {
  state: Bootstrap;
  onClose: () => void;
  notify: (msg: string) => void;
}) {
  const [clientTab, setClientTab] = useState<
    "codex" | "claude" | "json" | "guide"
  >("codex");

  const realServers = state.registry.servers.filter((s) => !s.demo && s.enabled);
  const firstEnabledTool = state.registry.tools.find(
    (t) => t.enabled && realServers.some((s) => s.id === t.server_id),
  );

  const samplePrompt = firstEnabledTool
    ? `请通过 mcp-gateway 完成任务：${firstEnabledTool.description || firstEnabledTool.name}。先调用 discover_tools 查找匹配工具，再按返回的 input_schema 调用 invoke_tool。`
    : "请先调用 mcp-gateway 的 discover_tools 描述当前任务查找工具，再按返回的 input_schema 调用 invoke_tool 执行。";

  const snippet =
    clientTab === "codex"
      ? `codex mcp add mcp-gateway --url ${state.mcp_url}`
      : clientTab === "claude"
        ? `claude mcp add --transport http mcp-gateway ${state.mcp_url}`
        : clientTab === "json"
          ? pretty({
              mcpServers: {
                "mcp-gateway": {
                  type: "http",
                  url: state.mcp_url,
                },
              },
            })
          : "需要外部工具时，先向 mcp-gateway 的 discover_tools 提交当前意图与上下文；网关按相关性返回最小候选工具 Schema 后，再调用 invoke_tool 执行。任务目标切换时重新调用 discover_tools。";

  return (
    <Modal
      title="接入 MCP 客户端"
      onClose={onClose}
      maxWidth={660}
      footer={
        <>
          <CopyButton
            value={samplePrompt}
            label="复制连通性验证 Prompt"
            onCopied={() => notify("已复制验证 Prompt")}
          />
          <button type="button" className="btn btn-primary" onClick={onClose}>
            完成
          </button>
        </>
      }
    >
      <div className="form-stack">
        <p style={{ fontSize: 13, color: "var(--ink-muted)" }}>
          所有 Agent 客户端只需挂载下方单一 Streamable HTTP 端点。网关仅向客户端暴露固定的 3 个元工具（
          <code>discover_tools</code>、<code>get_tool_schema</code>、
          <code>invoke_tool</code>），避免下游上百个工具撑爆上下文。
        </p>

        <section className="navy-card" aria-label="客户端连接配置">
          <div className="navy-card-header">
            <span className="navy-card-label">
              <span className="sail-status-dot" />
              MCP ENDPOINT
            </span>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <code style={{ fontSize: 12, color: "#fff" }}>
                {state.mcp_url}
              </code>
              <CopyButton
                value={state.mcp_url}
                label="复制地址"
                variant="navy"
                onCopied={() => notify("已复制 MCP 地址")}
              />
            </div>
          </div>

          <div
            className="navy-card-header"
            style={{ background: "rgba(255,255,255,0.03)" }}
          >
            <div className="navy-tabs" role="tablist" aria-label="客户端类型">
              {(
                [
                  ["codex", "Codex CLI"],
                  ["claude", "Claude Code"],
                  ["json", "mcpServers JSON"],
                  ["guide", "系统提示词规范"],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  role="tab"
                  aria-selected={clientTab === id}
                  className={clientTab === id ? "active" : ""}
                  onClick={() => setClientTab(id)}
                >
                  {label}
                </button>
              ))}
            </div>
            <CopyButton
              value={snippet}
              label="复制配置"
              variant="navy"
              onCopied={() => notify("已复制到剪贴板")}
            />
          </div>

          <pre className="navy-pre">{snippet}</pre>
        </section>

        {state.access?.shared && (
          <p className="field-hint">
            当前实例处于共享鉴权模式，客户端请求需携带管理员签发的{" "}
            <code>Authorization: Bearer &lt;token&gt;</code> 请求头。
          </p>
        )}
      </div>
    </Modal>
  );
}

# MCP Gateway Plugin

Official coding-agent integration for **MCP Gateway** supporting **Codex CLI**, **Claude Code**, **Cursor**, **OpenCode v2**, and **Pi**.

## What It Provides

- **MCP Registration**: Connects the client to your local or shared MCP Gateway endpoint (`discover_tools`, `get_tool_schema`, `invoke_tool`).
- **Workspace Scope Signing**: Automatically signs `_gw_ctx` workspace provenance tokens before Gateway meta-tool execution so repo-level access isolation works without manual copy-paste.
- **Gateway Skill & Rules**: Guides agents through `discover_tools` → `get_tool_schema` → `invoke_tool` and field projection (`fields`).

## Recommended Setup

Use the **MCP Gateway** desktop app (`连接客户端` → `安装插件`) or `gatewayctl plugin install` so your active channel (`mcp-gateway` vs `mcp-gateway-dev`), runtime port, custom `--data` directory, and `*-hook` binary are wired automatically.

## Manual Marketplace / CLI Installation

- **Claude Code**:
  ```sh
  claude plugin marketplace add DengNaichen/mcp-gateway
  claude plugin install mcp-gateway@mcp-gateway
  ```
- **Codex CLI**:
  ```sh
  codex plugin marketplace add DengNaichen/mcp-gateway
  codex plugin add mcp-gateway@mcp-gateway
  ```
- **Cursor**: Copy or symlink `plugins/mcp-gateway` into `~/.cursor/plugins/local/mcp-gateway`.
- **Pi**:
  ```sh
  pi install git:github.com/DengNaichen/mcp-gateway
  ```
- **OpenCode v2**: See `plugins/opencode/` for the `@dengnaichen/opencode-mcp-gateway` OpenCode v2 plugin (`setup(ctx)` + `ctx.mcp.transform` + `ctx.tool.hook("execute.before")`).

## Privacy

All MCP requests and workspace provenance hooks run locally against your configured MCP Gateway endpoint (`http://127.0.0.1:8788/mcp` by default) or the self-hosted gateway URL you explicitly configure. See the [repository README](https://github.com/DengNaichen/mcp-gateway#readme) for data-handling details.


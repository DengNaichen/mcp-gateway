# OpenCode v2 Plugin for MCP Gateway

OpenCode v2 plugin implementing the `setup(ctx)` definition contract (`ctx.mcp.transform` + `ctx.tool.hook("execute.before")`).

## Local Plugin Install (Recommended)

Use the **MCP Gateway** desktop app (`连接客户端` → `安装插件`) or `gatewayctl plugin install -client opencode`, which installs the runtime plugin into `$XDG_CONFIG_HOME/opencode/plugins/mcp-gateway.js` and the skill into `$XDG_CONFIG_HOME/opencode/skills/mcp-gateway/SKILL.md`.

## Package Configuration (`opencode.json`)

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugins": ["@dengnaichen/opencode-mcp-gateway"]
}
```

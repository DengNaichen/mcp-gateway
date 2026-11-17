# MCP Gateway Antigravity Plugin

Official Google Antigravity plugin (shared across `agy` CLI and Antigravity 2.0 / IDE Desktop via `~/.gemini/config/plugins/`).

## Included Components

- **MCP Config** ([mcp_config.json](mcp_config.json)): Connects to `http://127.0.0.1:8788/mcp?agent=antigravity`.
- **Lifecycle Hook** ([hooks.json](hooks.json)): Runs `PreToolUse` via [run-gateway-hook](scripts/run-gateway-hook) and injects `_gw_ctx*` with `decision: "allow"` + `overwrite`.
- **Rules** ([rules/AGENTS.md](rules/AGENTS.md)) & **Skill** ([skills/mcp-gateway/SKILL.md](skills/mcp-gateway/SKILL.md)).

## Manual Installation

```sh
agy plugin validate --strict ./plugins/antigravity
agy plugin install ./plugins/antigravity
```

---
description: Route external service and API capability lookups through MCP Gateway's discover_tools, get_tool_schema, and invoke_tool workflow.
trigger: model_decision
---

# MCP Gateway Workflow

When a task requires external services, observability logs, issue/CI APIs, or upstream MCP tools managed by MCP Gateway:

1. Call `discover_tools` with a clear `intent` (and optional `services` / `context`) before calling `invoke_tool`.
2. Call `get_tool_schema` if you used `summary_only: true` or need full parameter validation.
3. Call `invoke_tool` with arguments matching `input_schema` and optional `fields` projections for large payloads.
4. Never synthesize `_gw_ctx` / `_gw_ctx_*` scope tokens; the plugin `PreToolUse` hook signs workspace context automatically.
5. If Gateway returns `scope_required` or a read-only (`RO`) scope error, explain to the user which repository needs binding in MCP Gateway (`仓库绑定`).

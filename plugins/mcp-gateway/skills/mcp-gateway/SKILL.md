---
name: mcp-gateway
description: Discover, inspect, and invoke external tools routed through MCP Gateway (`discover_tools`, `get_tool_schema`, `invoke_tool`) or `gatewayctl`. Use whenever the task needs external services, SaaS APIs, GitLab/GitHub/Observability tools, or Gateway-managed MCP integrations.
---

# MCP Gateway

Use MCP Gateway as a three-step discovery-then-execution gateway rather than guessing tool names or invoking downstream APIs blindly.

## Standard Workflow

1. **Discover tools for the current sub-task**:
   Call `discover_tools` with a concise natural-language `intent` describing what you need right now.
   - Pass `services: ["<service-id>"]` when you already know the target service (for example `["github"]` or `["gitlab"]`).
   - Pass `context` with known identifiers or previous attempts when narrowing results.
   - Pass `summary_only: true` first if you only want lightweight summaries and tool IDs.
   - Re-run `discover_tools` whenever your task shifts to a different capability.

2. **Inspect schema when needed**:
   If you used `summary_only: true` or need the full JSON Schema, call `get_tool_schema` with the exact `tool_id` returned by `discover_tools`.

3. **Invoke with exact schema and optional projection**:
   Call `invoke_tool` with:
   - `tool_id`: exact ID from `discover_tools`
   - `arguments`: JSON object strictly matching the tool's `input_schema`
   - `fields`: optional dot-path array (such as `["logs.timestamp", "logs.message"]`) to keep large responses compact

## Scope, Safety, and Error Handling

- **Never fabricate or alter `_gw_ctx` / `_gw_ctx_*` fields**: The official plugin hook or extension automatically signs and attaches workspace provenance before execution.
- **403 `scope_required` / unbound repository**: When Gateway enforces repository isolation, the active working directory must be bound to services in the MCP Gateway desktop app (`仓库绑定`) or allowed under global defaults. Stop and inform the user which repository and agent need access rather than attempting workarounds.
- **403 read-only (`RO`) / `allow_write` denial**: Do not retry write operations in a read-only scope; report the boundary clearly.
- **Transient upstream errors**: Follow any `retry_after_seconds` guidance in structured tool errors instead of tight retry loops.

## Terminal / Script Alternative (`gatewayctl`)

When working inside shell pipelines or inspecting outputs offline, `gatewayctl` exposes the same flow as JSON on stdout:

```sh
gatewayctl list
gatewayctl -summary search "inspect recent error logs"
gatewayctl schema observability.search_error_logs
gatewayctl -args '{"service":"billing","minutes":10}' -fields 'logs.timestamp,logs.message' call observability.search_error_logs
```

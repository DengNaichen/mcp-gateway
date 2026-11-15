#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["tiktoken>=0.12,<1"]
# ///
"""Read local schemas and count compact JSON tokens; never invokes downstream tools.

uv run scripts/measure-tool-context.py --output docs/tool-context-measurement.json
Counts are tokenizer estimates, not a capture of a coding agent's actual prompt.
"""
import argparse
import datetime
import json
import sqlite3
import statistics
import urllib.request
from pathlib import Path

import tiktoken


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def stats(values):
    if not values:
        return None
    ordered = sorted(values)
    return {"count": len(values), "sum": sum(values),
            "mean": round(statistics.mean(values), 1),
            "median": statistics.median(values),
            "p90": ordered[min(len(ordered)-1, int(len(ordered)*.9))],
            "min": ordered[0], "max": ordered[-1]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8788")
    parser.add_argument("--db", default="data/gateway.db", help="Optional local history database; read-only")
    parser.add_argument("--output")
    args = parser.parse_args()

    def request(path, data=None):
        headers = {"Accept": "application/json, text/event-stream"}
        if data is not None:
            headers.update({"Content-Type": "application/json", "MCP-Protocol-Version": "2025-03-26"})
        req = urllib.request.Request(args.url.rstrip("/") + path,
                                     data=None if data is None else compact(data).encode(), headers=headers)
        with urllib.request.urlopen(req, timeout=20) as response:
            return json.load(response)

    bootstrap = request("/api/bootstrap")
    gateway = request("/mcp", {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}})["result"]["tools"]
    registry = bootstrap["registry"]
    servers = {s["id"]: s for s in registry["servers"] if not s.get("demo")}
    all_tools = [t for t in registry["tools"] if t["server_id"] in servers]
    active = [t for t in all_tools if t["enabled"] and servers[t["server_id"]]["enabled"]]

    # Minimum comparable function payload; excludes registry implementation metadata.
    # Actual clients may normalize schemas, add namespaces/annotations or use another format.
    def definition(t):
        return {"type": "function", "name": t["name"], "description": t["description"], "parameters": t["input_schema"]}

    gateway_defs = [{"type": "function", "name": t["name"], "description": t["description"], "parameters": t["inputSchema"]} for t in gateway]
    history = []
    if Path(args.db).is_file():
        with sqlite3.connect(Path(args.db).resolve().as_uri() + "?mode=ro", uri=True) as db:
            for timestamp, payload in db.execute("SELECT time, detail FROM activity WHERE kind='discover' AND status='selected' ORDER BY time DESC LIMIT 100"):
                detail = json.loads(payload)
                if detail.get("tools"):
                    history.append((timestamp, detail))

    report = {
        "measured_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "method": "tiktoken on compact JSON; counts content once, excludes model-specific hidden formatting, system prompt, dialogue, calls and execution results. 500-tool values are mean-based projections, not 500 observed enabled tools.",
        "registry": {"total": len(registry["tools"]), "non_demo": len(all_tools), "enabled_non_demo": len(active), "enabled_non_demo_servers": sum(bool(s["enabled"]) for s in servers.values()), "max_selected_tools": bootstrap["settings"]["max_tools"]},
        "gateway_tools": [t["name"] for t in gateway], "encodings": {},
        "history": {"database": args.db, "selected_records": len(history),
                    "newest": history[0][0] if history else None,
                    "oldest": history[-1][0] if history else None,
                    "router_prompt_tokens_reported": stats([d["prompt_tokens"] for _, d in history if d.get("prompt_tokens", 0) > 0])},
    }
    for encoding in ("o200k_base", "cl100k_base"):
        enc = tiktoken.get_encoding(encoding)
        def count(v):
            return len(enc.encode(compact(v), disallowed_special=()))
        fixed = count(gateway_defs)
        metrics = {"gateway_fixed_function_definitions": fixed,
                   "gateway_raw_mcp_tools_list": count(gateway), "catalogs": {}}
        for label, tools in (("all_non_demo", all_tools), ("enabled_non_demo", active)):
            sizes = [count(definition(t)) for t in tools]
            # Exact Tool struct JSON is returned by this gateway for selected tools.
            selected = [count(dict(t, reason="Selected by model relevance.")) for t in tools]
            mean = statistics.mean(sizes)
            metrics["catalogs"][label] = {
                "per_function": stats(sizes), "full_function_array": count([definition(t) for t in tools]),
                "projected_500_functions": round(mean*500),
                "selected_tool_payload": stats(selected),
                "projected_gateway_plus_selected": {str(k): round(fixed+statistics.mean(selected)*k) for k in (1,3,4,5)},
                "by_service": {sid: {"tools": len(group), "function_tokens": count([definition(t) for t in group])}
                               for sid in sorted(servers) if (group := [t for t in tools if t["server_id"] == sid])},
            }
        metrics["historical_discovery_payload_by_selected_count"] = {
            str(n): stats([count(d) for _, d in history if len(d["tools"]) == n])
            for n in sorted({len(d["tools"]) for _, d in history})}
        metrics["historical_gateway_plus_discovery_payload"] = stats([fixed+count(d) for _, d in history])
        report["encodings"][encoding] = metrics
    result = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        Path(args.output).write_text(result)
    print(result)


if __name__ == "__main__":
    main()

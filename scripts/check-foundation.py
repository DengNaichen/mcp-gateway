#!/usr/bin/env python3
"""Verify foundation, policy, routing, upstream and maintenance package boundaries using Go's actual dependency graph."""
import json
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[1]
output = subprocess.check_output(
    ["go", "list", "-json", "./internal/registry", "./internal/store", "./internal/secrets", "./internal/activity", "./internal/openapi", "./internal/model", "./internal/scope", "./internal/routing", "./internal/upstream", "./internal/maintenance", "./internal/maintenance/sandbox"],
    cwd=root, text=True,
)
allowed = {
    "gatewaylab/internal/registry": set(),
    "gatewaylab/internal/store": {"gatewaylab/internal/registry"},
    "gatewaylab/internal/routing": {"gatewaylab/internal/registry"},
    "gatewaylab/internal/upstream": {"gatewaylab/internal/registry", "gatewaylab/internal/procutil", "gatewaylab/internal/fault"},
}
allowed.update({
    "gatewaylab/internal/secrets": set(),
    "gatewaylab/internal/activity": {"gatewaylab/internal/store", "gatewaylab/internal/registry"},
    "gatewaylab/internal/openapi": {"gatewaylab/internal/registry"},
})
allowed.update({
    "gatewaylab/internal/model": {"gatewaylab/internal/registry", "gatewaylab/internal/store", "gatewaylab/internal/secrets"},
    "gatewaylab/internal/scope": {"gatewaylab/internal/registry", "gatewaylab/internal/store", "gatewaylab/internal/workspace", "gatewaylab/internal/buildinfo"},
})
allowed.update({
    "gatewaylab/internal/maintenance": {
        "gatewaylab/internal/maintenance/sandbox", "gatewaylab/internal/registry",
        "gatewaylab/internal/store", "gatewaylab/internal/procutil",
    },
    "gatewaylab/internal/maintenance/sandbox": {"gatewaylab/internal/registry", "gatewaylab/internal/procutil"},
})
decoder = json.JSONDecoder()
while output.strip():
    package, end = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[end:]
    internal = {p for p in package.get("Deps", []) if p.startswith("gatewaylab/")}
    unexpected = internal - allowed[package["ImportPath"]]
    assert not unexpected, f'{package["ImportPath"]} depends on {sorted(unexpected)}'
    if package["ImportPath"] == "gatewaylab/internal/routing":
        forbidden = {"database/sql", "net/http", "os", "log/slog", "sync", "sync/atomic"}
        assert not forbidden.intersection(package.get("Imports", [])), "routing must stay pure"
    print(f'{package["ImportPath"]} -> {", ".join(sorted(internal)) or "no internal dependencies"}')

# SQLite ownership stays in store as the remaining domains are extracted.
for path in sorted(p for name in ("gateway", "secrets", "activity", "openapi", "model", "scope", "routing", "upstream", "maintenance", "maintenance/sandbox")
                   for p in (root / "internal" / name).glob("*.go")):
    if path.name.endswith("_test.go"):
        continue
    assert not re.search(r'\b(?:SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|PRAGMA)\b', path.read_text()), f"SQL outside store: {path.relative_to(root)}"
print("PASS: foundation/policy/routing/upstream/maintenance dependency direction; SQL stays in store")

# Domain errors carry classifications; only transport adapters choose HTTP status.
for name in ("openapi", "model", "scope", "routing", "maintenance", "maintenance/sandbox"):
    for path in (root / "internal" / name).glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        assert not re.search(r'\b(?:appError|problem)\b|http\.Status[A-Z]', path.read_text()), f"transport error in {path.relative_to(root)}"
print("PASS: policy domain errors contain no HTTP status mapping")

# Provider protocols have one implementation, in model. Keep composition and
# policy packages from silently growing a second request builder/parser.
provider_function = re.compile(
    r"^func (?:resolveDecisionProtocol|postDecisions|buildDecisionsCalls|"
    r"(?:openAI|gemini|systemOne)(?:Choice)?Calls?|parse(?:OpenAI|Map)(?:Answers(?:Range)?|Choice))\(",
    re.MULTILINE,
)
for name in ("gateway", "routing"):
    for path in (root / "internal" / name).glob("*.go"):
        if not path.name.endswith("_test.go"):
            assert not provider_function.search(path.read_text()), f"provider implementation outside model: {path.relative_to(root)}"
print("PASS: provider request/parsing implementation stays in model")

# Maintenance owns its state and depends on injected capabilities, never Gateway.
for path in sorted((root / "internal/maintenance").rglob("*.go")):
    if path.name.endswith("_test.go"):
        continue
    source = path.read_text()
    assert not re.search(r"\*Gateway\b|\bappError\b|\bproblem\(|\.HandleFunc\(", source), path
print("PASS: maintenance/sandbox contain no Gateway or HTTP handler coupling")

# Exactly one implementation of each process primitive per target platform.
process_definitions = {}
for path in sorted((root / "internal").rglob("*.go")):
    if path.name.endswith("_test.go"):
        continue
    for name in re.findall(r"^func (ProcessEnvironment|ConfigureProcess|processEnvironment|configureProcess)\(", path.read_text(), re.MULTILINE):
        process_definitions.setdefault(name, []).append(str(path.relative_to(root)))
assert process_definitions == {
    "ProcessEnvironment": ["internal/procutil/environment.go"],
    "ConfigureProcess": ["internal/procutil/process_other.go", "internal/procutil/process_unix.go"],
}, process_definitions
print("PASS: process environment and platform cancellation implementations stay in procutil")

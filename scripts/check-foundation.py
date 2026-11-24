#!/usr/bin/env python3
"""Verify foundation, policy, routing, upstream and maintenance package boundaries using Go's actual dependency graph."""
import json
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[1]
output = subprocess.check_output(
    ["go", "list", "-json", "./internal/registry", "./internal/store", "./internal/secrets", "./internal/activity", "./internal/openapi", "./internal/model", "./internal/scope", "./internal/policy", "./internal/cliexec", "./internal/routing", "./internal/upstream", "./internal/maintenance", "./internal/maintenance/sandbox", "./internal/access", "./internal/market", "./internal/demo", "./internal/procutil", "./internal/input", "./internal/jsonutil", "./internal/mcputil", "./internal/contracttest", "./internal/fault", "./internal/retry", "./internal/apitypes", "./internal/apiclient", "./internal/buildinfo", "./internal/i18n", "./internal/workspace", "./internal/clientconfig", "./internal/clientplugin", "./internal/clientplugin/github"],
    cwd=root, text=True,
)
allowed = {
    "gatewaylab/internal/registry": set(),
    "gatewaylab/internal/store": {"gatewaylab/internal/registry"},
    "gatewaylab/internal/policy": set(),
    "gatewaylab/internal/cliexec": {"gatewaylab/internal/registry", "gatewaylab/internal/procutil", "gatewaylab/internal/fault", "gatewaylab/internal/input"},
    "gatewaylab/internal/routing": {"gatewaylab/internal/registry"},
    "gatewaylab/internal/upstream": {"gatewaylab/internal/registry", "gatewaylab/internal/procutil", "gatewaylab/internal/fault", "gatewaylab/internal/input", "gatewaylab/internal/jsonutil"},
}
allowed.update({
    "gatewaylab/internal/secrets": set(),
    "gatewaylab/internal/activity": {"gatewaylab/internal/store", "gatewaylab/internal/registry"},
    "gatewaylab/internal/openapi": {"gatewaylab/internal/registry"},
})
allowed.update({
    "gatewaylab/internal/model": {"gatewaylab/internal/registry", "gatewaylab/internal/store", "gatewaylab/internal/secrets", "gatewaylab/internal/retry"},
    "gatewaylab/internal/scope": {"gatewaylab/internal/registry", "gatewaylab/internal/store", "gatewaylab/internal/workspace", "gatewaylab/internal/buildinfo"},
})
allowed.update({
    "gatewaylab/internal/maintenance": {
        "gatewaylab/internal/maintenance/sandbox", "gatewaylab/internal/registry",
        "gatewaylab/internal/store", "gatewaylab/internal/procutil", "gatewaylab/internal/input", "gatewaylab/internal/jsonutil", "gatewaylab/internal/mcputil", "gatewaylab/internal/contracttest", "gatewaylab/internal/secrets",
    },
    "gatewaylab/internal/maintenance/sandbox": {"gatewaylab/internal/registry", "gatewaylab/internal/procutil", "gatewaylab/internal/input", "gatewaylab/internal/secrets"},
})
allowed.update({
    "gatewaylab/internal/access": {"gatewaylab/internal/registry", "gatewaylab/internal/input", "gatewaylab/internal/fault", "gatewaylab/internal/secrets"},
    "gatewaylab/internal/market": {"gatewaylab/internal/registry", "gatewaylab/internal/input", "gatewaylab/internal/fault", "gatewaylab/internal/i18n"},
    "gatewaylab/internal/demo": {"gatewaylab/internal/registry"},
    "gatewaylab/internal/procutil": set(),
    "gatewaylab/internal/input": set(),
    "gatewaylab/internal/jsonutil": set(),
    "gatewaylab/internal/mcputil": set(),
    "gatewaylab/internal/contracttest": {"gatewaylab/internal/input", "gatewaylab/internal/mcputil"},
    "gatewaylab/internal/fault": set(),
    "gatewaylab/internal/retry": set(),
    "gatewaylab/internal/apitypes": set(),
    "gatewaylab/internal/apiclient": {"gatewaylab/internal/apitypes"},
    "gatewaylab/internal/buildinfo": set(),
    "gatewaylab/internal/i18n": set(),
    "gatewaylab/internal/workspace": {"gatewaylab/internal/buildinfo"},
    "gatewaylab/internal/clientconfig": {"gatewaylab/internal/buildinfo", "gatewaylab/internal/i18n"},
    "gatewaylab/internal/clientplugin": {"gatewaylab/internal/buildinfo", "gatewaylab/internal/clientconfig", "gatewaylab/internal/i18n", "gatewaylab/internal/workspace", "gatewaylab/internal/clientplugin/github", "gatewaylab/plugins"},
    "gatewaylab/internal/clientplugin/github": set(),
})
stdlib = set(subprocess.check_output(["go", "list", "std"], cwd=root, text=True).splitlines())
decoder = json.JSONDecoder()
while output.strip():
    package, end = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[end:]
    internal = {p for p in package.get("Deps", []) if p.startswith("gatewaylab/")}
    unexpected = internal - allowed[package["ImportPath"]]
    assert not unexpected, f'{package["ImportPath"]} depends on {sorted(unexpected)}'
    if package["ImportPath"] in {"gatewaylab/internal/routing", "gatewaylab/internal/policy"}:
        forbidden = {"database/sql", "net/http", "os", "log/slog", "sync", "sync/atomic"}
        assert not forbidden.intersection(package.get("Imports", [])), f'{package["ImportPath"]} must stay pure'
    if package["ImportPath"] == "gatewaylab/internal/policy":
        external = set(package.get("Imports", [])) - stdlib
        assert not external, f"policy imports non-standard packages {sorted(external)}"
    print(f'{package["ImportPath"]} -> {", ".join(sorted(internal)) or "no internal dependencies"}')

# SQLite ownership stays in store as the remaining domains are extracted.
for path in sorted(p for name in ("gateway", "secrets", "activity", "openapi", "model", "scope", "policy", "cliexec", "routing", "upstream", "maintenance", "maintenance/sandbox", "access", "market", "demo", "input", "jsonutil", "mcputil", "procutil", "contracttest", "fault", "apiclient", "buildinfo", "workspace", "clientconfig", "clientplugin", "clientplugin/github")
                   for p in (root / "internal" / name).glob("*.go")):
    if path.name.endswith("_test.go"):
        continue
    assert not re.search(r'\b(?:SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|PRAGMA)\b', path.read_text()), f"SQL outside store: {path.relative_to(root)}"
print("PASS: foundation/policy/routing/upstream/maintenance dependency direction; SQL stays in store")

# Domain errors carry classifications; only transport adapters choose HTTP status.
for name in ("gateway", "openapi", "model", "scope", "policy", "cliexec", "routing", "maintenance", "maintenance/sandbox", "access", "market", "demo", "input", "jsonutil", "mcputil", "procutil", "contracttest", "fault", "retry", "buildinfo", "workspace", "clientconfig", "clientplugin"):
    for path in (root / "internal" / name).glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        assert not re.search(r'\b(?:appError|problem)\b|http\.Status[A-Z]', path.read_text()), f"transport error in {path.relative_to(root)}"
for path in (root / "internal" / "apiclient").glob("*.go"):
    if path.name.endswith("_test.go"):
        continue
    assert not re.search(r'\b(?:appError|problem)\b', path.read_text()), f"transport error in {path.relative_to(root)}"
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

# Enforce reverse edges across all buildable production packages, including commands.
output = subprocess.check_output(["go", "list", "-json", "./..."], cwd=root, text=True)
while output.strip():
    package, end = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[end:]
    name = package["ImportPath"]
    imports = set(package.get("Imports", []))
    if "gatewaylab/internal/api" in imports:
        assert name.startswith("gatewaylab/cmd/"), f"only commands may import api: {name}"
    if "gatewaylab/internal/gateway" in imports:
        assert name == "gatewaylab/internal/api" or name.startswith("gatewaylab/cmd/"), f"gateway imported outside composition boundary: {name}"
    if "gatewaylab/internal/apitypes" in imports:
        assert name in {"gatewaylab/internal/api", "gatewaylab/internal/apiclient"} or name.startswith("gatewaylab/cmd/"), f"only api, apiclient, and commands may import the wire types: {name}"
    if "gatewaylab/internal/apiclient" in imports:
        assert name.startswith("gatewaylab/cmd/"), f"only commands may import apiclient: {name}"
    if "gatewaylab/internal/clientconfig" in imports:
        assert name in {"gatewaylab/internal/clientplugin", "gatewaylab/scripts"} or name.startswith("gatewaylab/cmd/"), f"clientconfig must stay local-only: {name}"
    if "gatewaylab/internal/clientplugin" in imports:
        assert name == "gatewaylab/scripts" or name.startswith("gatewaylab/cmd/"), f"clientplugin must stay local-only: {name}"
    if name == "gatewaylab/internal/maintenance/sandbox":
        assert "gatewaylab/internal/upstream" not in package.get("Deps", []), "sandbox must not depend on upstream"
    if name == "gatewaylab/cmd/gateway-desktop":
        # The desktop UI reaches the backend over the /api HTTP contract only.
        # depguard additionally limits internal/api to server.go (hosting).
        direct = imports | set(package.get("TestImports", [])) | set(package.get("XTestImports", []))
        internal = {p for p in direct if p.startswith("gatewaylab/internal/")}
        desktop_allowed = {
            "gatewaylab/internal/api", "gatewaylab/internal/apiclient", "gatewaylab/internal/apitypes",
            "gatewaylab/internal/buildinfo", "gatewaylab/internal/clientconfig", "gatewaylab/internal/clientplugin",
            "gatewaylab/internal/i18n",
        }
        assert internal <= desktop_allowed, f"gateway-desktop imports backend packages {sorted(internal - desktop_allowed)}; use internal/apitypes over HTTP"
print("PASS: cmd -> api -> gateway reverse edges; apitypes stays at the HTTP edge; desktop uses the HTTP contract; sandbox is independent of upstream")
subprocess.run(["go", "run", "scripts/check-api-boundaries.go"], cwd=root, check=True)

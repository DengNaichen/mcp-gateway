#!/usr/bin/env python3
"""Verify foundation and policy package boundaries using Go's actual dependency graph."""
import json
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[1]
output = subprocess.check_output(
    ["go", "list", "-json", "./internal/registry", "./internal/store", "./internal/secrets", "./internal/activity", "./internal/openapi"],
    cwd=root, text=True,
)
allowed = {
    "gatewaylab/internal/registry": set(),
    "gatewaylab/internal/store": {"gatewaylab/internal/registry"},
}
allowed.update({
    "gatewaylab/internal/secrets": set(),
    "gatewaylab/internal/activity": {"gatewaylab/internal/store", "gatewaylab/internal/registry"},
    "gatewaylab/internal/openapi": {"gatewaylab/internal/registry"},
})
decoder = json.JSONDecoder()
while output.strip():
    package, end = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[end:]
    internal = {p for p in package.get("Deps", []) if p.startswith("gatewaylab/")}
    unexpected = internal - allowed[package["ImportPath"]]
    assert not unexpected, f'{package["ImportPath"]} depends on {sorted(unexpected)}'
    print(f'{package["ImportPath"]} -> {", ".join(sorted(internal)) or "no internal dependencies"}')

# SQLite ownership stays in store as the remaining domains are extracted.
for path in sorted((root / "internal/gateway").glob("*.go")):
    if path.name.endswith("_test.go"):
        continue
    assert not re.search(r'\b(?:SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|PRAGMA)\b', path.read_text()), f"SQL outside store: {path.relative_to(root)}"
print("PASS: foundation/policy dependency direction; gateway contains no SQL")

#!/usr/bin/env python3
"""Offline preflight for routing-eval: validate case suites, run scorer unit tests, and optionally probe gateway."""

import argparse
import json
import subprocess
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
EVAL_DIR = ROOT / "evals" / "routing"


def validate_case_file(path: Path) -> tuple[list[str], int]:
    errors: list[str] = []
    rel = path.relative_to(ROOT).as_posix()
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        return [f"{rel}: invalid JSON: {exc}"], 0

    if not isinstance(data, dict):
        return [f"{rel}: root JSON must be an object"], 0
    if not isinstance(data.get("description"), str) or not data["description"].strip():
        errors.append(f"{rel}: missing non-empty 'description'")

    cases = data.get("cases")
    if not isinstance(cases, list) or not cases:
        return errors + [f"{rel}: 'cases' must be a non-empty array"], 0

    seen_intents: set[str] = set()
    for idx, case in enumerate(cases, start=1):
        if not isinstance(case, dict):
            errors.append(f"{rel}: case #{idx} must be an object")
            continue
        intent = case.get("intent")
        if not isinstance(intent, str) or not intent.strip():
            errors.append(f"{rel}: case #{idx} missing non-empty 'intent'")
        elif intent in seen_intents:
            errors.append(f"{rel}: case #{idx} duplicate intent: {intent!r}")
        else:
            seen_intents.add(intent)

        no_match = bool(case.get("no_match", False))
        for field in ("expected", "any_of", "acceptable"):
            val = case.get(field)
            if val is not None and (
                not isinstance(val, list) or not all(isinstance(x, str) and x.strip() for x in val)
            ):
                errors.append(f"{rel}: case #{idx} '{field}' must be an array of non-empty strings")

        if not no_match and not case.get("expected") and not case.get("any_of"):
            errors.append(f"{rel}: case #{idx} must specify 'expected' or 'any_of' when not 'no_match'")

    return errors, len(cases)


def probe_gateway(url: str) -> list[str]:
    target = url.rstrip("/") + "/api/health"
    try:
        with urllib.request.urlopen(target, timeout=3) as resp:
            if resp.status != 200:
                return [f"Gateway probe {target} returned HTTP {resp.status}"]
            payload = json.loads(resp.read().decode("utf-8"))
            print(f"Gateway reachable at {target}: status={payload.get('status', 'ok')}")
            return []
    except Exception as exc:
        return [f"Gateway probe failed at {target}: {exc}"]


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Offline preflight for routing-eval (no paid model calls)."
    )
    parser.add_argument(
        "--check-gateway",
        help="Optional gateway base URL for read-only /api/health probe (e.g. http://127.0.0.1:8789).",
    )
    parser.add_argument(
        "--skip-go-test",
        action="store_true",
        help="Skip running `go test ./cmd/routingeval/...`.",
    )
    args = parser.parse_args()

    if not EVAL_DIR.is_dir():
        print(f"Missing directory: {EVAL_DIR}", file=sys.stderr)
        return 1

    suite_files = sorted(EVAL_DIR.glob("*.json"))
    if not suite_files:
        print(f"No case JSON files found under {EVAL_DIR}", file=sys.stderr)
        return 1

    errors: list[str] = []
    total_cases = 0
    for suite_path in suite_files:
        file_errors, count = validate_case_file(suite_path)
        errors.extend(file_errors)
        total_cases += count

    if not args.skip_go_test:
        proc = subprocess.run(
            ["go", "test", "-count=1", "./cmd/routingeval/..."],
            cwd=ROOT,
            capture_output=True,
            text=True,
        )
        if proc.returncode != 0:
            msg = (proc.stderr or proc.stdout).strip()
            errors.append(f"`go test ./cmd/routingeval/...` failed:\n{msg}")

    if args.check_gateway:
        errors.extend(probe_gateway(args.check_gateway))

    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1

    print(
        f"PASS: validated {len(suite_files)} routing eval suite(s) ({total_cases} cases) "
        "and routingeval scorer tests (live model eval NOT executed)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

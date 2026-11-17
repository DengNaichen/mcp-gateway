#!/usr/bin/env python3
"""Serve prototypes/ over localhost HTTP for reliable browser preview and QA."""

import argparse
import functools
import http.server
import os
import signal
import sys
import webbrowser
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
PROTOTYPES_DIR = ROOT / "prototypes"


def discover_prototypes() -> list[Path]:
    if not PROTOTYPES_DIR.is_dir():
        return []
    return sorted(p for p in PROTOTYPES_DIR.rglob("*.html") if p.is_file())


def resolve_target(target: str | None, candidates: list[Path]) -> Path | None:
    if not candidates:
        return None
    if target is None:
        return candidates[0] if len(candidates) == 1 else None

    raw = Path(target)
    for base in (ROOT, PROTOTYPES_DIR):
        candidate = (base / raw).resolve()
        if candidate in candidates:
            return candidate

    needle = target.lower()
    matches = [
        p
        for p in candidates
        if needle in p.relative_to(PROTOTYPES_DIR).as_posix().lower()
    ]
    if len(matches) == 1:
        return matches[0]
    if len(matches) > 1:
        names = ", ".join(p.relative_to(PROTOTYPES_DIR).as_posix() for p in matches)
        raise SystemExit(f"Ambiguous target '{target}'; matches: {names}")
    raise SystemExit(f"Prototype not found for target '{target}'. Run with --list to see options.")


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        # Suppress default stderr spam unless PROTOTYPE_HTTP_VERBOSE=1.
        if os.environ.get("PROTOTYPE_HTTP_VERBOSE") == "1":
            super().log_message(format, *args)


def main() -> int:
    parser = argparse.ArgumentParser(description="Serve MCP Gateway UI prototypes over localhost.")
    parser.add_argument("--list", action="store_true", help="List available prototype HTML files and exit.")
    parser.add_argument("--target", help="Prototype path or name fragment (e.g. control-plane).")
    parser.add_argument("--host", default="127.0.0.1", help="Bind host (default: 127.0.0.1).")
    parser.add_argument("--port", type=int, default=0, help="Bind port (default: 0 for auto-assigned free port).")
    parser.add_argument("--open", action="store_true", help="Open the target URL in the default browser.")
    args = parser.parse_args()

    candidates = discover_prototypes()
    if args.list:
        if not candidates:
            print("No prototype HTML files found under prototypes/")
            return 1
        for p in candidates:
            print(p.relative_to(ROOT).as_posix())
        return 0

    if not PROTOTYPES_DIR.is_dir():
        print(f"Missing directory: {PROTOTYPES_DIR}", file=sys.stderr)
        return 1

    target_path = resolve_target(args.target, candidates)
    handler = functools.partial(QuietHandler, directory=str(PROTOTYPES_DIR))
    server = http.server.ThreadingHTTPServer((args.host, args.port), handler)

    def handle_signal(_signum: int, _frame: object) -> None:
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, handle_signal)

    actual_host, actual_port = server.server_address[:2]
    rel_url = (
        target_path.relative_to(PROTOTYPES_DIR).as_posix()
        if target_path is not None
        else ""
    )
    url = f"http://{actual_host}:{actual_port}/{rel_url}"

    print(f"Serving prototypes/ at {url} (pid={os.getpid()})", flush=True)
    if args.open:
        webbrowser.open(url)

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

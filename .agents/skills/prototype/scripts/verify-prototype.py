#!/usr/bin/env python3
"""Verify prototypes/ index coverage, links, HTML references, inline JS syntax, and HTTP serving."""

import argparse
import functools
import http.server
import re
import shutil
import subprocess
import sys
import threading
import urllib.request
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[4]
PROTOTYPES_DIR = ROOT / "prototypes"
INDEX_PATH = PROTOTYPES_DIR / "README.md"
FORBIDDEN_FRAGMENTS = ("file://", ".gemini")


class PrototypeHTMLParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=False)
        self.has_html = False
        self.has_body = False
        self.scripts: list[tuple[int, str, str]] = []
        self.refs: list[tuple[int, str, str]] = []
        self._in_inline_script = False
        self._script_line = 0
        self._script_type = ""
        self._script_chunks: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        lower = tag.lower()
        attr_map = {k.lower(): (v or "") for k, v in attrs}
        line = self.getpos()[0]

        if lower == "html":
            self.has_html = True
        elif lower == "body":
            self.has_body = True
        elif lower == "script":
            src = attr_map.get("src", "").strip()
            if src:
                self.refs.append((line, "src", src))
            else:
                self._in_inline_script = True
                self._script_line = line
                self._script_type = attr_map.get("type", "").strip().lower()
                self._script_chunks = []

        for key in ("href", "src"):
            if lower == "script" and key == "src":
                continue
            val = attr_map.get(key, "").strip()
            if val:
                self.refs.append((line, key, val))

    def handle_data(self, data: str) -> None:
        if self._in_inline_script:
            self._script_chunks.append(data)

    def handle_endtag(self, tag: str) -> None:
        if tag.lower() == "script" and self._in_inline_script:
            self.scripts.append((self._script_line, self._script_type, "".join(self._script_chunks)))
            self._in_inline_script = False
            self._script_chunks = []


def check_index_coverage(html_files: list[Path], md_files: list[Path]) -> list[str]:
    errors: list[str] = []
    if not INDEX_PATH.is_file():
        return [f"Missing prototype index: {INDEX_PATH.relative_to(ROOT).as_posix()}"]

    index_text = INDEX_PATH.read_text(encoding="utf-8")
    linked_targets: set[Path] = set()
    for match in re.finditer(r"\[[^\]\n]*\]\(([^)\n]+)\)", index_text):
        target = match.group(1).strip().strip("<>")
        url = urlsplit(target)
        if url.scheme or not url.path:
            continue
        linked_targets.add((INDEX_PATH.parent / unquote(url.path)).resolve())

    for artifact in html_files + [p for p in md_files if p != INDEX_PATH]:
        if artifact.resolve() not in linked_targets:
            errors.append(
                f"{INDEX_PATH.relative_to(ROOT).as_posix()}: unindexed prototype artifact: "
                f"{artifact.relative_to(PROTOTYPES_DIR).as_posix()}"
            )
    return errors


def check_html_file(html_path: Path, check_js: bool, node_bin: str | None) -> tuple[list[str], int]:
    errors: list[str] = []
    rel = html_path.relative_to(ROOT).as_posix()
    text = html_path.read_text(encoding="utf-8")

    if "<!doctype html" not in text[:512].lower():
        errors.append(f"{rel}:1: missing <!doctype html> declaration")

    parser = PrototypeHTMLParser()
    parser.feed(text)
    parser.close()

    if not parser.has_html or not parser.has_body:
        errors.append(f"{rel}: missing <html> or <body> structure")

    for line, attr, ref in parser.refs:
        if "${" in ref:
            # Template literal fragments inside embedded JS/HTML strings are not static attributes.
            continue
        lower_ref = ref.lower()
        for bad in FORBIDDEN_FRAGMENTS:
            if bad in lower_ref:
                errors.append(f"{rel}:{line}: forbidden {attr} reference '{ref}'")
                break
        parsed = urlsplit(ref)
        if parsed.scheme or ref.startswith(("#", "//")):
            continue
        if ref.startswith("/"):
            errors.append(f"{rel}:{line}: use a relative {attr} instead of root-absolute '{ref}'")
            continue
        resolved = (html_path.parent / unquote(parsed.path)).resolve()
        if not resolved.is_relative_to(ROOT) or not resolved.exists():
            errors.append(f"{rel}:{line}: missing local {attr} target '{ref}'")

    js_blocks = 0
    if check_js:
        if node_bin is None:
            errors.append(f"{rel}: 'node' not found in PATH for inline <script> syntax check")
            return errors, js_blocks
        for start_line, script_type, body in parser.scripts:
            if script_type and script_type not in ("text/javascript", "application/javascript", "module"):
                continue
            if not body.strip():
                continue
            js_blocks += 1
            cmd = [node_bin, "--check"]
            if script_type == "module":
                cmd.append("--input-type=module")
            proc = subprocess.run(cmd, input=body, text=True, capture_output=True)
            if proc.returncode != 0:
                detail = (proc.stderr or proc.stdout).strip().splitlines()[:6]
                errors.append(
                    f"{rel}:{start_line}: inline <script> syntax error:\n  " + "\n  ".join(detail)
                )

    return errors, js_blocks


class SilentHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        pass


def check_http_serving(html_files: list[Path]) -> list[str]:
    if not html_files:
        return []
    errors: list[str] = []
    handler = functools.partial(SilentHandler, directory=str(PROTOTYPES_DIR))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    port = server.server_address[1]

    try:
        for html_path in html_files:
            rel_url = html_path.relative_to(PROTOTYPES_DIR).as_posix()
            url = f"http://127.0.0.1:{port}/{rel_url}"
            with urllib.request.urlopen(url, timeout=5) as resp:
                if resp.status != 200:
                    errors.append(f"HTTP smoke failed for {rel_url}: status {resp.status}")
                    continue
                body = resp.read()
                expected = html_path.read_bytes()
                if body != expected:
                    errors.append(f"HTTP smoke mismatch for {rel_url}: served {len(body)}B != {len(expected)}B")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify prototypes/ index, links, HTML/JS syntax, and HTTP serving.")
    parser.add_argument("--skip-js", action="store_true", help="Skip node --check validation of inline <script> blocks.")
    parser.add_argument("--skip-serve", action="store_true", help="Skip localhost HTTP serving smoke test.")
    args = parser.parse_args()

    if not PROTOTYPES_DIR.is_dir():
        print(f"Missing directory: {PROTOTYPES_DIR}", file=sys.stderr)
        return 1

    html_files = sorted(p for p in PROTOTYPES_DIR.rglob("*.html") if p.is_file())
    md_files = sorted(p for p in PROTOTYPES_DIR.rglob("*.md") if p.is_file())
    if not html_files:
        print("No prototype HTML files found under prototypes/", file=sys.stderr)
        return 1

    errors: list[str] = []
    errors.extend(check_index_coverage(html_files, md_files))

    doc_check = subprocess.run(
        [sys.executable, str(ROOT / "scripts" / "check-doc-links.py")],
        capture_output=True,
        text=True,
    )
    if doc_check.returncode != 0:
        msg = (doc_check.stderr or doc_check.stdout).strip()
        errors.append(f"check-doc-links.py failed:\n{msg}")

    node_bin = shutil.which("node")
    total_scripts = 0
    for html_path in html_files:
        file_errors, js_blocks = check_html_file(html_path, check_js=not args.skip_js, node_bin=node_bin)
        errors.extend(file_errors)
        total_scripts += js_blocks

    if not args.skip_serve:
        errors.extend(check_http_serving(html_files))

    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1

    print(
        f"PASS: verified {len(html_files)} prototype HTML file(s), "
        f"{len(md_files)} markdown doc(s), and {total_scripts} inline script block(s)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

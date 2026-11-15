#!/usr/bin/env python3
"""Check tracked Markdown local links without depending on the checkout path."""
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

root = Path(__file__).resolve().parents[1]
paths = subprocess.check_output(
    ['git', '-C', str(root), 'ls-files', '--cached', '--others', '--exclude-standard', '-z', '--', '*.md']
).decode().split('\0')
errors = []
checked = 0
for name in sorted(set(filter(None, paths))):
    doc = root / name
    if not doc.is_file():
        continue
    text = doc.read_text()
    for match in re.finditer(r'!?\[[^\]\n]*\]\(([^)\n]+)\)', text):
        target = match.group(1).strip().strip('<>')
        url = urlsplit(target)
        line = text.count('\n', 0, match.start()) + 1
        if url.scheme and url.scheme != 'file':
            continue
        if url.scheme == 'file' or target.startswith('/'):
            errors.append(f'{name}:{line}: use a repository-relative link: {target}')
        elif url.path:
            checked += 1
            resolved = (doc.parent / unquote(url.path)).resolve()
            if not resolved.is_relative_to(root) or not resolved.exists():
                errors.append(f'{name}:{line}: missing local target: {target}')
if errors:
    print('\n'.join(errors), file=sys.stderr)
    sys.exit(1)
print(f'Checked {checked} local links in {len(set(filter(None, paths)))} Markdown files')

#!/usr/bin/env python3
"""Preview (default) or remove rebuildable outputs, never runtime data."""
import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def worktrees(root):
    raw = subprocess.check_output(['git', '-C', str(root), 'worktree', 'list', '--porcelain', '-z'])
    return [Path(os.fsdecode(line[9:])) for line in raw.split(b'\0') if line.startswith(b'worktree ')]


def project_outputs(root):
    for name in ('bin', 'build', 'gateway-desktop'):
        path = root / name
        if path.exists() or path.is_symlink():
            yield path, root


def temp_outputs(base):
    # importcfg proves module ownership; the name and age alone are insufficient.
    for path in sorted(base.glob('go-build*')):
        if path.is_symlink() or not path.is_dir() or not re.fullmatch(r'go-build\d+', path.name):
            continue
        if time.time() - path.stat().st_mtime < 3600:
            continue
        if any('packagefile gatewaylab/' in cfg.read_text(errors='replace')
               for cfg in path.glob('*/importcfg')):
            yield path, None


def size(path):
    if path.is_symlink():
        return 0
    # du reports allocated space and does not traverse symlinks.
    return int(run('du', '-sk', str(path)).split()[0]) * 1024


def process_snapshot():
    commands = run('ps', '-axo', 'comm=').splitlines()
    builders = [cmd for cmd in commands if Path(cmd.strip()).name in
                {'go', 'compile', 'link', 'mygo', 'golangci-lint'}]
    result = subprocess.run(['lsof', '-nP', '-a', '-u', str(os.getuid()), '-Fn'],
                            capture_output=True, text=True)
    if result.returncode not in (0, 1) or result.stderr.strip():
        raise RuntimeError('cannot reliably inspect open files: ' + result.stderr.strip())
    opened = [Path(line[1:]).resolve() for line in result.stdout.splitlines() if line.startswith('n/')]
    return builders, opened


def blocked_reason(path, root, builders, opened):
    if path.is_symlink():
        return 'symlink'
    if root and run('git', '-C', str(root), 'ls-files', '--', str(path.relative_to(root))):
        return 'contains tracked files'
    if builders:
        return 'build/test tooling is running'
    resolved = path.resolve()
    if any(p == resolved or resolved in p.parents for p in opened):
        return 'in use by a process'
    return None


def clean(candidates, apply=False, min_age_hours=0):
    total = 0
    skipped = 0
    for path, root in candidates:
        # Refresh before each deletion; do not run cleanup concurrently with builds.
        builders, opened = process_snapshot()
        reason = blocked_reason(path, root, builders, opened)
        if not reason and min_age_hours:
            newest = max([path.lstat().st_mtime] +
                         [p.lstat().st_mtime for p in path.rglob('*')])
            if time.time() - newest < min_age_hours * 3600:
                reason = f'updated within {min_age_hours:g} hour(s)'
        if reason:
            print(f'SKIP {path}: {reason}')
            skipped += 1
            continue
        count = size(path)
        print(f'{"REMOVE" if apply else "WOULD REMOVE"} {count / 1024**2:.1f} MiB  {path}')
        if apply:
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink()
        total += count
    print(f'{"Removed" if apply else "Reclaimable"}: {total / 1024**2:.1f} MiB; skipped: {skipped}')
    return bool(skipped)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[4])
    parser.add_argument('--all-worktrees', action='store_true', help='include the primary checkout and registered worktrees')
    parser.add_argument('--temp-builds', action='store_true', help='include idle gatewaylab go-build directories older than one hour')
    parser.add_argument('--go-cache', action='store_true', help='also clear the shared Go build/test cache (not module downloads)')
    parser.add_argument('--apply', action='store_true', help='delete; without this flag only preview')
    args = parser.parse_args()
    root = args.root.resolve()
    if Path(run('git', '-C', str(root), 'rev-parse', '--show-toplevel')).resolve() != root:
        parser.error('--root must be a checkout root')
    roots = worktrees(root) if args.all_worktrees else [root]
    candidates = [item for checkout in roots for item in project_outputs(checkout)]
    if args.temp_builds:
        candidates.extend(temp_outputs(Path(tempfile.gettempdir())))
    skipped = clean(candidates, args.apply, min_age_hours=1 if args.all_worktrees else 0)
    if args.go_cache:
        cache = Path(run('go', 'env', 'GOCACHE'))
        builders, opened = process_snapshot()
        reason = blocked_reason(cache, None, builders, opened)
        if reason:
            print(f'SKIP Go cache: {reason}')
            skipped = True
        elif cache.is_dir():
            print(f'{"CLEAR" if args.apply else "WOULD CLEAR"} shared Go cache: {size(cache) / 1024**3:.2f} GiB  {cache}')
            if args.apply:
                subprocess.run(['go', 'clean', '-cache', '-testcache'], check=True)
    return int(skipped)


if __name__ == '__main__':
    raise SystemExit(main())

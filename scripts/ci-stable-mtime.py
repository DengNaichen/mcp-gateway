#!/usr/bin/env python3
"""Make Go's test result cache survive fresh CI checkouts.

go test fingerprints files a test opens by size + mtime (not content), and
directories by their own stat plus their entries' stats. A fresh checkout
stamps every file with the checkout time, so packages that read repo files
never hit the cache. This sets each tracked file's mtime from its git blob
hash (same content -> same mtime, changed content -> new mtime) and every
directory to a fixed time. All times are far older than go test's 2s cutoff.
"""

import os
import subprocess

BASE = 1_000_000_000  # 2001-09-09


def main():
    listing = subprocess.run(
        ["git", "ls-files", "--stage", "-z"], capture_output=True, check=True
    ).stdout
    dirs = {b"."}
    for record in filter(None, listing.split(b"\0")):
        meta, path = record.split(b"\t", 1)
        mode, blob, _stage = meta.split()
        if mode == b"160000":  # submodule
            continue
        stamp = BASE + int(blob[:7], 16)
        os.utime(path, (stamp, stamp), follow_symlinks=False)
        parent = os.path.dirname(path)
        while parent:
            dirs.add(parent)
            parent = os.path.dirname(parent)
    for directory in dirs:
        os.utime(directory, (BASE, BASE))


if __name__ == "__main__":
    main()

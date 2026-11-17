#!/usr/bin/env python3
"""Verify the real macOS cross-build artifacts without executing target code."""

import json
import plistlib
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROFILES = json.loads((ROOT / "internal/buildinfo/profiles.json").read_text())
CPU_TYPES = {"arm64": 0x0100000C, "amd64": 0x01000007}


def verify_binary(path, arch):
    with path.open("rb") as binary:
        header = binary.read(8)
    assert len(header) == 8, f"Missing Mach-O header: {path}"
    magic, cpu_type = struct.unpack("<II", header)
    assert magic == 0xFEEDFACF, f"Expected 64-bit Mach-O: {path}"
    assert cpu_type == CPU_TYPES[arch], f"Wrong architecture: {path}"
    assert path.stat().st_mode & 0o111, f"Missing executable permissions: {path}"


def main():
    for channel in ("dev", "stable"):
        profile = PROFILES[channel]
        for arch in CPU_TYPES:
            app = ROOT / "build" / channel / f"darwin-{arch}" / f"{profile['name']}.app"
            contents = app / "Contents"
            with (contents / "Info.plist").open("rb") as source:
                info = plistlib.load(source)
            assert info["CFBundleIdentifier"] == profile["identifier"], f"Wrong identity: {app}"
            assert info["CFBundleExecutable"] == profile["name"], f"Wrong executable: {app}"
            version = info["CFBundleShortVersionString"]
            expected = PROFILES["version"]
            if channel == "stable":
                assert version == expected, f"Wrong Stable version: {app}"
            else:
                assert version.startswith(expected + "-dev."), f"Wrong Dev version: {app}"
            verify_binary(contents / "MacOS" / info["CFBundleExecutable"], arch)
            for helper in (profile["slug"] + "-hook", profile["slug"] + "ctl"):
                verify_binary(contents / "Resources" / helper, arch)
            assert (contents / "Resources/AppIcon.icns").stat().st_size > 0, f"Missing icon: {app}"
            print(f"PASS: {channel} darwin/{arch} identity, version, icon and three Mach-O executables")


if __name__ == "__main__":
    main()

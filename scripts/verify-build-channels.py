#!/usr/bin/env python3
"""Exercise both macOS bundles with private data and a shared temporary client home.
Run after: make package; node scripts/build-channel.mjs package stable -skip-dmg
Artifacts are retained under a printed /tmp directory. Only spawned processes stop.
"""
import json, os, pathlib, plistlib, subprocess, tempfile, time, tomllib, urllib.request
from concurrent.futures import ThreadPoolExecutor

ROOT = pathlib.Path(__file__).resolve().parents[1]
ARTIFACTS = pathlib.Path(tempfile.mkdtemp(prefix="gateway-channels-"))
CLIENTS = ["codex", "claude", "cursor", "opencode", "pi"]
processes, sockets, states = {}, {}, {}

def control(channel, path, data=None):
    args = ["curl", "-fsS", "--max-time", "3", "--unix-socket", str(sockets[channel]), "-H", "Content-Type: application/json"]
    if data is not None:
        args += ["-d", json.dumps(data)]
    return json.loads(subprocess.check_output(args + ["http://localhost" + path], stderr=subprocess.DEVNULL))

def api(channel, path, data=None):
    req = urllib.request.Request(states[channel]["ui_url"] + path,
        data=None if data is None else json.dumps(data).encode(),
        headers={"Content-Type": "application/json"}, method="GET" if data is None else "PUT")
    return json.load(urllib.request.urlopen(req, timeout=5))

def entries(row):
    raw = pathlib.Path(row["Path"]).read_text()
    data = tomllib.loads(raw) if row["ID"] == "codex" else json.loads(raw)
    if row["ID"] == "codex": return data.get("mcp_servers", {})
    if row["ID"] == "opencode": return data.get("mcp", {}).get("servers", {})
    return data.get("mcpServers", {})

try:
    for channel, name in [("dev", "MCP Gateway Dev"), ("stable", "MCP Gateway")]:
        bundle = next((ROOT / "build" / channel).glob("*/*.app"))
        info = plistlib.loads((bundle / "Contents/Info.plist").read_bytes())
        expected = "NaichengDeng.mcp-gateway" + (".dev" if channel == "dev" else "")
        assert info["CFBundleIdentifier"] == expected, info
        assert info["CFBundleName"] == name, info
        sockets[channel] = ARTIFACTS / (channel + ".sock")
        env = dict(os.environ, GATEWAY_DESKTOP_CONTROL=str(sockets[channel]), GATEWAY_CLIENT_CONFIG_HOME=str(ARTIFACTS / "clients"))
        for key in ["GATEWAY_PUBLIC_URL", "GATEWAY_DEV_PUBLIC_URL", "GATEWAY_AUTH_FILE", "GATEWAY_BUILD_CHANNEL"]:
            env.pop(key, None)
        # Opposite runtime channel must not change compiled identity.
        env["GATEWAY_BUILD_CHANNEL"] = "stable" if channel == "dev" else "dev"
        log = open(ARTIFACTS / (channel + ".log"), "w")
        processes[channel] = subprocess.Popen([str(bundle / "Contents/MacOS" / name), "-addr", "127.0.0.1:0", "-data", str(ARTIFACTS / channel)], env=env, stdout=log, stderr=subprocess.STDOUT)
        log.close()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            assert processes[channel].poll() is None, (channel, "app exited", ARTIFACTS)
            try:
                states[channel] = control(channel, "/status")
                if states[channel].get("ready") and states[channel].get("window_exists"): break
            except (subprocess.CalledProcessError, json.JSONDecodeError): pass
            time.sleep(.1)
        else: raise AssertionError((channel, "startup timed out", ARTIFACTS))
        state = states[channel]
        assert state["mode"] == "owned"
        assert name in state["window_title"]
        assert state["build"]["version"] in state["window_title"]
        assert state["build"]["channel"] == channel
        assert state["build"]["identifier"] == expected
        assert state["updates_enabled"] == (channel == "stable")
        health = api(channel, "/api/health")
        assert health["application"] == expected
        assert health["channel"] == channel
        assert health["version"] == info["CFBundleShortVersionString"]
        states[channel]["health"] = health
        control(channel, "/view", {"view": "clients"})
    assert states["dev"]["listen_addr"] != states["stable"]["listen_addr"]
    assert states["dev"]["health"]["data_identity"] != states["stable"]["health"]["data_identity"]
    assert states["dev"]["health"]["instance_id"] != states["stable"]["health"]["instance_id"]
    original = api("stable", "/api/bootstrap")["settings"]
    changed = dict(api("dev", "/api/bootstrap")["settings"], max_tools=7)
    api("dev", "/api/settings", changed)
    assert api("dev", "/api/bootstrap")["settings"]["max_tools"] == 7
    assert api("stable", "/api/bootstrap")["settings"] == original
    for client in CLIENTS:
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(control, channel, "/clients", {"ID": client, "Action": "write"}) for channel in ["stable", "dev"]]
            for future in futures: future.result()
    for row in control("dev", "/clients"):
        current = entries(row)
        assert current["mcp-gateway"]["url"] == states["stable"]["mcp_url"]
        assert current["mcp-gateway-dev"]["url"] == states["dev"]["mcp_url"]
    for channel in ["dev", "stable"]:
        subprocess.run(["curl", "-fsS", "--unix-socket", str(sockets[channel]), "http://localhost/screenshot", "-o", str(ARTIFACTS / (channel + ".png"))], check=True)
    for client in CLIENTS:
        control("dev", "/clients", {"ID": client, "Action": "revoke"})
    for row in control("stable", "/clients"):
        assert row["CanRevoke"]
        assert "mcp-gateway" in entries(row)
        assert "mcp-gateway-dev" not in entries(row)
    for client in CLIENTS:
        control("stable", "/clients", {"ID": client, "Action": "revoke"})
    for row in control("stable", "/clients"):
        assert not entries(row)
    assert (ARTIFACTS / "clients/.mcp-gateway/client-config").is_dir()
    assert (ARTIFACTS / "clients/.mcp-gateway-dev/client-config").is_dir()
    states["verification"] = {
        "git_head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "git_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip()),
        "workspace": str(ROOT),
    }
    (ARTIFACTS / "results.json").write_text(json.dumps(states, indent=2))
    print("PASS: bundle identity, runtime identity, update policy, data isolation, five shared client configs and independent revoke")
finally:
    for channel, process in processes.items():
        if process.poll() is None:
            try: control(channel, "/tray/quit", {})
            except Exception: process.terminate()
            try: process.wait(timeout=5)
            except subprocess.TimeoutExpired: process.kill(); process.wait()
    print("Verification artifacts:", ARTIFACTS)

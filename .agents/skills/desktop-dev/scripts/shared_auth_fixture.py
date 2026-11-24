"""Create a disposable shared-mode login for packaged-app acceptance scripts."""

import hashlib
import json
import secrets


def shared_auth_fixture(root, env):
    token = secrets.token_hex(32)
    config = root / "access.json"
    config.write_text(json.dumps({"principals": [{
        "id": "desktop-test",
        "groups": ["developer"],
        "servers": ["*"],
        "allow_write": True,
        "admin": True,
        "token_sha256": hashlib.sha256(token.encode()).hexdigest(),
    }]}))
    env["GATEWAY_AUTH_FILE"] = str(config)
    return token

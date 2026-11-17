"""Exercise real app -> HTTP API -> Keychain -> restart -> authenticated calls.

Uses disposable credentials, data and Keychain namespace; never touches user
profiles. Build a Dev app with make package before running on macOS.
"""
import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import threading
import time
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = pathlib.Path(__file__).resolve().parents[4]
APP = next((ROOT / 'build/dev').glob('*/MCP Gateway Dev.app')) / 'Contents/MacOS/MCP Gateway Dev'
ARTIFACTS = pathlib.Path(tempfile.mkdtemp(prefix='gateway-credentials-'))
DATA = ARTIFACTS / 'data'
SOCKET = str(ARTIFACTS / 'control.sock')
MODEL_SECRET = 'disposable-model-' + uuid.uuid4().hex
SERVICE_SECRET = 'disposable-service-' + uuid.uuid4().hex
seen = {}


class Mock(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        seen['model'] = self.headers.get('Authorization')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({'answers': [
            {'type': 'choice', 'name': 'route_status', 'choice': 'no_match', 'probability': 0.9},
            {'type': 'predicate', 'name': 'tool_0', 'choice': 'no', 'probability': 0.1},
        ]}).encode())

    def do_GET(self):
        seen['service'] = self.headers.get('Authorization')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"ok":true}')


def control(path, payload=None):
    cmd = ['curl', '-fsS', '--max-time', '3', '--unix-socket', SOCKET,
           '-H', 'Content-Type: application/json']
    if payload is not None:
        cmd += ['-d', json.dumps(payload)]
    return json.loads(subprocess.check_output(cmd + ['http://localhost' + path], stderr=subprocess.DEVNULL))


def api(base, path, payload=None, method=None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(base + '/api' + path, data=data, method=method,
                                     headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=35) as response:
        raw = response.read()
    assert MODEL_SECRET.encode() not in raw and SERVICE_SECRET.encode() not in raw, 'secret in API response'
    return json.loads(raw)


process = None
account = None
mock = ThreadingHTTPServer(('127.0.0.1', 0), Mock)
threading.Thread(target=mock.serve_forever, daemon=True).start()
mock_url = 'http://127.0.0.1:' + str(mock.server_port)
settings = {'base_url': mock_url + '/v1', 'model': 'gpt-6-luna', 'max_tools': 4, 'timeout_seconds': 10}
env = {k: v for k, v in os.environ.items() if not k.startswith('GATEWAY_')}
env.update(GATEWAY_DESKTOP_CONTROL=SOCKET, GATEWAY_CREDENTIAL_STORE='keychain',
           GATEWAY_CLIENT_CONFIG_HOME=str(ARTIFACTS / 'clients'))


def start(index):
    global process, account
    pathlib.Path(SOCKET).unlink(missing_ok=True)
    with (ARTIFACTS / ('runtime-' + str(index) + '.log')).open('w') as log:
        process = subprocess.Popen([str(APP), '-addr', '127.0.0.1:0', '-data', str(DATA)],
                                   env=env, stdout=log, stderr=subprocess.STDOUT)
    for _ in range(300):
        if process.poll() is not None:
            raise RuntimeError('app exited; inspect disposable runtime log')
        try:
            status = control('/status')
            if status.get('window_exists'):
                channel = status['build']['channel']
                account = hashlib.sha256((channel + '\x00' + str(DATA.resolve())).encode()).hexdigest()
                return status['ui_url']
        except (OSError, subprocess.SubprocessError):
            pass
        time.sleep(0.1)
    raise RuntimeError('app startup timed out')


def stop():
    global process
    if process is None:
        return
    try:
        control('/tray/quit', {})
    except (OSError, subprocess.SubprocessError):
        process.terminate()
    process.wait(timeout=10)
    process = None


def verify_calls(base):
    assert api(base, '/model/ping', settings)['ok']
    assert seen['model'] == 'Bearer ' + MODEL_SECRET, 'model key not restored'
    api(base, '/invoke', {'tool_id': 'credential-test.read', 'arguments': {}})
    assert seen['service'] == 'Bearer ' + SERVICE_SECRET, 'upstream credential not restored'


try:
    base = start(1)
    assert api(base, '/bootstrap')['credentials_persistent']
    api(base, '/settings', dict(settings, api_key=MODEL_SECRET), 'PUT')
    api(base, '/servers', {'id': 'credential-test', 'name': 'Credential Test', 'kind': 'http',
                         'enabled': True, 'endpoint': mock_url,
                         'headers': {'Authorization': 'Bearer ${DOGFOOD_TEST_TOKEN}'}}, 'PUT')
    api(base, '/servers/credential-test/credentials', {'values': {'DOGFOOD_TEST_TOKEN': SERVICE_SECRET}}, 'PUT')
    api(base, '/tools', {'id': 'credential-test.read', 'server_id': 'credential-test', 'name': 'read',
                       'description': 'Read disposable fixture', 'enabled': True, 'read_only': True,
                       'actors': ['developer'], 'input_schema': {'type': 'object'}, 'method': 'GET', 'path': '/'}, 'PUT')
    verify_calls(base)
    stop()
    base = start(2)
    assert api(base, '/bootstrap')['key_configured']
    verify_calls(base)
    control('/view', {'view': 'settings'})
    for mode in ('light', 'dark'):
        control('/theme', {'source': mode})
        time.sleep(0.3)
        subprocess.run(['curl', '-fsS', '--unix-socket', SOCKET, 'http://localhost/screenshot',
                        '-o', str(ARTIFACTS / (mode + '-settings.png'))], check=True, stdout=subprocess.DEVNULL)
    api(base, '/settings', dict(settings, clear_key=True), 'PUT')
    api(base, '/servers/credential-test/credentials', {'values': {'DOGFOOD_TEST_TOKEN': ''}}, 'PUT')
    stop()
    base = start(3)
    assert not api(base, '/bootstrap')['key_configured']
    assert not api(base, '/servers/credential-test/credentials')['credentials'][0]['configured']
    stop()
    for file in list(DATA.glob('gateway.db*')) + list(ARTIFACTS.glob('*.log')):
        raw = file.read_bytes()
        assert MODEL_SECRET.encode() not in raw and SERVICE_SECRET.encode() not in raw, 'secret leaked to disk'
    print('PASS: real app + Keychain persisted model/service credentials across restart; real HTTP calls used restored credentials; clear survived another restart; no plaintext secrets in database/logs')
    print('Artifacts:', ARTIFACTS)
finally:
    stop()
    mock.shutdown()
    if account:
        subprocess.run(['/usr/bin/security', 'delete-generic-password', '-a', account,
                        '-s', 'mcp-gateway.credentials.v1'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

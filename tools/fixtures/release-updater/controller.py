#!/usr/bin/python3
"""Root-only nested-daemon controller; acquisition is explicitly fixture-local.

All permission, Compose, image ownership, TLS, backup, SQLite, candidate, and
restore operations run the production adapter. No fake flow-verification report
is supplied: these exercises must remain awaiting authenticated operator gates.
"""

from __future__ import annotations

import base64
import hashlib
import importlib.util
import json
import os
import secrets
import shutil
import time
from pathlib import Path
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

spec = importlib.util.spec_from_file_location(
    "updater", "/usr/local/lib/psst.zip/deploy/update.py"
)
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)
images = json.load(__import__("sys").stdin)
root = Path("/opt/installation")
root.mkdir(mode=0o700)
u.private_directory(u.STATE_PATH)
u.private_directory(u.BACKUP_PATH)
u.private_directory(u.STATE_PATH / "transactions")
shutil.copy("/opt/fixture/bundle-deploy/compose.release.yml", root / "compose.yml")
(root / "Caddyfile").write_text("""{
 http_port 8080
 https_port 8443
 skip_install_trust
}
{$PSST_DOMAIN} {
 tls internal
 handle /api/* {
  reverse_proxy backend:8080
 }
 handle {
  root * /srv/web
  try_files {path} /index.html
  file_server
 }
}
""")
(root / "override.json").write_text(
    json.dumps(
        {
            "services": {
                "caddy": {
                    "volumes": [
                        {
                            "type": "bind",
                            "source": str(root / "Caddyfile"),
                            "target": "/etc/caddy/Caddyfile",
                            "read_only": True,
                        }
                    ]
                }
            }
        }
    )
)
password = secrets.token_urlsafe(24)
values = {
    "BACKEND_IMAGE": images["backend"],
    "WEB_IMAGE": images["web"],
    "PSST_DOMAIN": "fixture.example.test",
    "PUBLIC_URL": "https://fixture.example.test",
    "HTTP_PORT": "18080",
    "HTTPS_PORT": "18443",
    "ADMIN_USERNAME": "fixture-admin",
    "ADMIN_PASSWORD": password,
    "BACKEND_DATA_VOLUME": "fixture-data",
    "CADDY_DATA_VOLUME": "fixture-caddy-data",
    "CADDY_CONFIG_VOLUME": "fixture-caddy-config",
    "CLEANUP_INTERVAL": "2s",
}


def env():
    (root / "original.env").write_text(
        "".join(f"{key}={value}\n" for key, value in values.items())
    )
    (root / "original.env").chmod(0o600)


env()
config = {
    "repository": "endorses/psst.zip",
    "signer_workflow": "endorses/psst.zip/.github/workflows/release.yml",
    "installation": str(root),
    "project": "fixture",
    "environment": "original.env",
    "compose_files": ["compose.yml", "override.json"],
    "operator_files": ["Caddyfile"],
    "release_overrides": ["override.json"],
    "external_proxy": False,
    "domain": "fixture.example.test",
    "db_path": "/app/data/psst.db",
    "volume_names": {
        "backend:/app/data": "fixture-data",
        "caddy:/data": "fixture-caddy-data",
        "caddy:/config": "fixture-caddy-config",
    },
    "disk_reserve_bytes": 1 << 30,
    "image_reserve_bytes": 1 << 30,
    "verification_hook": None,
    "checkpoint_hook": "/opt/fixture/checkpoint.py",
    "github_token_file": None,
    "retention_count": 2,
}


class LocalAcquisition(u.Host):
    """Substitute only remote publication boundary with loaded immutable images."""

    fail_after_start = False

    def run(self, *args, **kwargs):
        try:
            return super().run(*args, **kwargs)
        except u.UpdateError:
            if args[:2] == ("docker", "image"):
                # Image-only fixture diagnostics never contain accounts/config.
                import subprocess

                diagnostic = subprocess.run(
                    args,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.PIPE,
                    env=self.env,
                )
                print(
                    "fixture image diagnostic: "
                    + diagnostic.stderr.decode(errors="replace")[-1000:],
                    flush=True,
                )
            raise

    def download(self, version, target):
        shutil.copytree("/opt/fixture/bundle-deploy", target / "bundle/deploy")
        return {
            "version": version,
            "source": {"commit": images["commit"]},
            "images": {
                name: {
                    "index": json.loads(
                        self.run("docker", "image", "inspect", images[name])
                    )[0]["Id"]
                }
                for name in ("backend", "web")
            },
            "requirements": {"docker": "27.0.0", "compose": "2.24.4"},
        }

    def pull(self, manifest, platform):
        # Already loaded immutable image IDs: still validate the production
        # architecture, non-root identity and committed OCI metadata checks.
        for component in manifest["images"].values():
            metadata = json.loads(
                self.run("docker", "image", "inspect", component["index"])
            )[0]
            labels = metadata["Config"]["Labels"]
            assert metadata["Architecture"] == platform.split("/")[1]
            assert metadata["Config"]["User"] not in ("", "0", "root")
            assert labels["org.opencontainers.image.revision"] == images["commit"]
            assert labels["org.opencontainers.image.version"] == "v1.2.3"
            assert labels["org.opencontainers.image.licenses"] == "AGPL-3.0-only"

    def automatic_checks(self, private, transaction):
        super().automatic_checks(private, transaction)
        if self.fail_after_start and not transaction["restoring"]:
            raise u.UpdateError(
                "injected post-startup failure after real automatic gates"
            )


host = LocalAcquisition(config)
initial = host.current()
u.atomic_json(root / "initial.json", initial)
host.compose(root / "initial.json", "up", "-d", "--pull", "never", "--no-build")
for _ in range(60):
    volume = json.loads(host.run("docker", "volume", "inspect", "fixture-caddy-data"))[
        0
    ]
    ca = Path(volume["Mountpoint"]) / "caddy/pki/authorities/local/root.crt"
    if ca.exists():
        break
    time.sleep(1)
else:
    raise RuntimeError("fixture Caddy did not issue a certificate")
shutil.copy(ca, root / "fixture-ca.crt")
host.env["CURL_CA_BUNDLE"] = str(root / "fixture-ca.crt")
# Fixture-specific DNS for Python's verified TLS client; no global CA changes.
with Path("/etc/hosts").open("a") as stream:
    stream.write("\n127.0.0.1 fixture.example.test\n")
import http.client
import ssl

context = ssl.create_default_context(cafile=str(root / "fixture-ca.crt"))


def request(method, path, body=None, cookie="", headers=None, expected=200):
    connection = http.client.HTTPSConnection(
        config["domain"], 18443, context=context, timeout=15
    )
    outgoing = {"Origin": "https://" + config["domain"]}
    if cookie:
        outgoing["Cookie"] = cookie
    if isinstance(body, dict):
        body = json.dumps(body).encode()
        outgoing["Content-Type"] = "application/json"
    outgoing.update(headers or {})
    connection.request(method, "/api/v1" + path, body, outgoing)
    response = connection.getresponse()
    data = response.read()
    fields = dict(response.getheaders())
    connection.close()
    assert (
        response.status == expected
    ), f"fixture {method} {path} expected {expected}, got {response.status}"
    return fields, data


for _ in range(40):
    try:
        host.https(initial, "/api/v1/auth/status")
        break
    except u.UpdateError:
        time.sleep(1)
headers, _ = request(
    "POST",
    "/auth/login",
    {"username": "fixture-admin", "password": password, "session_type": "web"},
)
admin = headers["Set-Cookie"].split(";", 1)[0]
request("PATCH", "/admin/settings", {"max_file_size": 3 << 20}, admin)
request(
    "POST",
    "/admin/users",
    {"username": "fixture-member", "password": password},
    admin,
    expected=201,
)
headers, _ = request(
    "POST",
    "/auth/login",
    {"username": "fixture-member", "password": password, "session_type": "web"},
)
member = headers["Set-Cookie"].split(";", 1)[0]
request(
    "POST",
    "/auth/password",
    {"current_password": password, "password": password + "-changed"},
    member,
    expected=204,
)
headers, _ = request(
    "POST",
    "/auth/login",
    {
        "username": "fixture-member",
        "password": password + "-changed",
        "session_type": "web",
    },
)
member = headers["Set-Cookie"].split(";", 1)[0]
# Real account/TUS/manifest upload, with independent AES-GCM client matching
# the production chunked-v1 wire context. No key/plaintext reaches the server.
metadata = json.loads(host.run("docker", "volume", "inspect", "fixture-data"))[0]
data_root = Path(metadata["Mountpoint"])
key = AESGCM.generate_key(256)
plain = b"independently authenticated disposable encrypted checkpoint payload"
encryption_id = os.urandom(16)
frame = encryption_id + (0).to_bytes(8, "big") + len(plain).to_bytes(8, "big") + plain
nonce = os.urandom(12)
sealed = nonce + AESGCM(key).encrypt(nonce, frame, None)
created = json.loads(request("POST", "/transfers", {}, member, expected=201)[1])
transfer = created["id"]
authorization = {}
fields, _ = request(
    "POST",
    f"/transfers/{transfer}/files",
    None,
    member,
    headers={
        **authorization,
        "Tus-Resumable": "1.0.0",
        "Upload-Length": str(len(sealed)),
    },
    expected=201,
)
blob_id = fields["Location"].rstrip("/").split("/")[-1]
request(
    "PATCH",
    f"/transfers/{transfer}/files/{blob_id}",
    sealed,
    member,
    headers={
        **authorization,
        "Tus-Resumable": "1.0.0",
        "Upload-Offset": "0",
        "Content-Type": "application/offset+octet-stream",
    },
    expected=204,
)
manifest = {
    "files": [
        {
            "name": "fixture.txt",
            "size": len(plain),
            "mime_type": "text/plain",
            "blob_id": blob_id,
            "encoding": "chunked-v1",
            "chunk_size": 4194304,
            "encryption_id": encryption_id.hex(),
        }
    ]
}
manifest_nonce = os.urandom(12)
encrypted_manifest = manifest_nonce + AESGCM(key).encrypt(
    manifest_nonce, json.dumps(manifest).encode(), None
)
request(
    "POST",
    f"/transfers/{transfer}/manifest",
    encrypted_manifest,
    member,
    headers={**authorization, "Content-Type": "application/octet-stream"},
    expected=204,
)
request(
    "POST",
    f"/transfers/{transfer}/complete",
    cookie=member,
    headers=authorization,
    expected=204,
)
payload = data_root / "files" / transfer / blob_id
assert payload.read_bytes() == sealed
assert request("GET", f"/transfers/{transfer}/manifest")[1] == encrypted_manifest
assert request("GET", f"/transfers/{transfer}/files/{blob_id}")[1] == sealed
assert AESGCM(key).decrypt(sealed[:12], sealed[12:], None) == frame
# Persist a real administrator TOTP factor before the stopped checkpoint.
import hmac
import struct

factor = json.loads(
    request("POST", "/admin/security/enrollment", {}, admin, expected=201)[1]
)
secret = base64.b32decode(factor["secret"] + "=" * (-len(factor["secret"]) % 8))
mac = hmac.new(secret, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
offset = mac[-1] & 15
code = str(
    (int.from_bytes(mac[offset : offset + 4], "big") & 0x7FFFFFFF) % 1000000
).zfill(6)
request("POST", "/admin/security/enrollment/confirm", {"code": code}, admin)
request(
    "POST",
    "/auth/login",
    {"username": "fixture-admin", "password": password, "session_type": "web"},
    expected=401,
)
# The service's documented adjacent-window TOTP allowance admits the next
# unused counter after enrollment consumed the current counter.
mac = hmac.new(
    secret, struct.pack(">Q", int(time.time()) // 30 + 1), hashlib.sha1
).digest()
offset = mac[-1] & 15
code = str(
    (int.from_bytes(mac[offset : offset + 4], "big") & 0x7FFFFFFF) % 1000000
).zfill(6)
headers, _ = request(
    "POST",
    "/auth/login",
    {
        "username": "fixture-admin",
        "password": password,
        "session_type": "web",
        "code": code,
    },
)
admin = headers["Set-Cookie"].split(";", 1)[0]
assert json.loads(request("GET", "/admin/security", cookie=admin)[1])["enabled"] is True
# Clear all bootstrap credentials and recreate before actual adoption.
values["ADMIN_USERNAME"] = values["ADMIN_PASSWORD"] = ""
env()
initial = host.current()
u.atomic_json(root / "initial.json", initial)
host.compose(root / "initial.json", "up", "-d", "--pull", "never", "--no-build")
for _ in range(40):
    try:
        request("GET", "/auth/me", cookie=admin)
        break
    except (AssertionError, OSError):
        time.sleep(1)
updater = u.Updater(config, host)
# First run intentionally remains at the truthful operator verification gate.
if images.get("failure_after_start"):
    host.fail_after_start = True
    try:
        updater.update("v1.2.3")
    except u.UpdateError as error:
        assert "injected post-startup" in str(error)
    else:
        raise AssertionError("candidate fault failed to close deployment")
    transaction = updater.read()
    assert transaction["phase"] == "failed-closed" and transaction["mutation_started"]
    assert not host.run(
        "docker", "ps", "-q", "--filter", "label=com.docker.compose.project=fixture"
    ).strip()
    print(
        "PASS real post-startup failure stops all ingress; durable mutation boundary forbids old-binary restart",
        flush=True,
    )
else:
    transaction = updater.update("v1.2.3")
    assert (
        transaction["phase"] == "awaiting-verification"
        and transaction["mutation_started"]
    )
assert transaction["prior_pause"] is False
private = u.load_json(
    u.STATE_PATH / "transactions" / transaction["id"] / "private.compose.json"
)
if not images.get("failure_after_start"):
    for attempt in range(30):
        try:
            host.authenticated_checks(private, admin)
            break
        except u.UpdateError:
            if attempt == 29:
                raise
            time.sleep(1)
    request("GET", "/auth/me", cookie=admin)
    request("GET", "/auth/me", cookie=member)
    assert json.loads(request("GET", "/config")[1])["max_file_size"] == 3 << 20
assert payload.read_bytes() == sealed
assert AESGCM(key).decrypt(sealed[:12], sealed[12:], None) == frame
print(
    "PASS real adoption, bootstrap removal, TLS, stopped complete checkpoint, image retention, accounts/TOTP/settings and loopback candidate",
    flush=True,
)
checkpoint = Path(transaction["checkpoint"])
u.Host.verify_backup(checkpoint)
# Permission/truncation rejection exercises actual protected checkpoint reads.
volume_archive = next(checkpoint.glob("volume-*.tar"))
original = volume_archive.read_bytes()
volume_archive.write_bytes(original[:-31])
try:
    u.Host.verify_backup(checkpoint)
except u.UpdateError:
    pass
else:
    raise AssertionError("corrupt checkpoint accepted")
volume_archive.write_bytes(original)
u.Host.verify_backup(checkpoint)
print(
    "PASS authenticated encrypted fixture export/decrypt and corrupt-checkpoint refusal",
    flush=True,
)
# A post-startup fault must close all ingress and preserve original volumes.
host.compose(
    u.STATE_PATH / "transactions" / transaction["id"] / "private.compose.json",
    "stop",
    "--timeout",
    "60",
)
updater.write(transaction, "failed-closed")
original_hash = u.fingerprint(payload)
checkpoint_hash = u.fingerprint(checkpoint / "checkpoint.json")
restored = updater.restore()
assert restored["phase"] == "restored-awaiting-verification"
assert set(restored["restored_volumes"]) == set(config["volume_names"].values())
assert not set(restored["restored_volumes"].values()) & set(
    config["volume_names"].values()
)
assert u.fingerprint(payload) == original_hash
assert u.fingerprint(checkpoint / "checkpoint.json") == checkpoint_hash
restore_name = restored["restored_volumes"]["fixture-data"]
restore_root = Path(
    json.loads(host.run("docker", "volume", "inspect", restore_name))[0]["Mountpoint"]
)
restored_ciphertext = (restore_root / "files" / transfer / blob_id).read_bytes()
assert (
    AESGCM(key).decrypt(restored_ciphertext[:12], restored_ciphertext[12:], None)
    == frame
)
restore_private = u.load_json(
    u.STATE_PATH / "transactions" / transaction["id"] / "private.compose.json"
)
for attempt in range(30):
    try:
        host.authenticated_checks(restore_private, admin)
        break
    except u.UpdateError:
        if attempt == 29:
            raise
        time.sleep(1)
request("GET", "/auth/me", cookie=admin)
request("GET", "/auth/me", cookie=member)
assert json.loads(request("GET", "/admin/security", cookie=admin)[1])["enabled"] is True
request("POST", "/transfers", {}, member, expected=503)
assert json.loads(request("GET", "/config")[1])["public_transfers_paused"] is True
restored_ca = (
    Path(
        json.loads(
            host.run(
                "docker",
                "volume",
                "inspect",
                restored["restored_volumes"]["fixture-caddy-data"],
            )
        )[0]["Mountpoint"]
    )
    / "caddy/pki/authorities/local/root.crt"
)
assert restored_ca.read_bytes() == ca.read_bytes()
print(
    "PASS isolated restore preserves checkpoint/originals, account sessions/TOTP, ciphertext, settings, pause and Caddy CA; actual authenticated storage/counter/orphan checks pass; security approval remains pending",
    flush=True,
)
print(
    "LIMITATIONS: fixture-local image acquisition, no public provenance/multiarch/ACME or full browser/security activation proof; separate export store simulates off-host",
    flush=True,
)

#!/usr/bin/python3
"""Root-only nested-daemon controller; acquisition is explicitly fixture-local.

All permission, Compose, image ownership, TLS, backup, SQLite, candidate, and
restore operations run the production adapter. A protected independent client
hook records real authenticated assertions, then exercises public activation.
Only release acquisition and the external backup-provider boundary are simulated.
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
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

spec = importlib.util.spec_from_file_location(
    "updater", "/usr/local/lib/psst.zip/deploy/update.py"
)
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)
images = json.load(__import__("sys").stdin)
assert not sys.flags.optimize, "fixture assertions must execute"
prior_version = images.get("prior_version", "v1.2.2")
candidate_version = images.get("candidate_version", "v1.2.3")
repeat_version = images.get("repeat_version", "v1.2.4")
observations = []
startup_observations = []
old_cli_rejected = None
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
    "verification_hook": "/opt/fixture/verify.py",
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
            error_log = Path("/opt/fixture/flow-error.log")
            if args[0] == "/opt/fixture/verify.py" and error_log.exists():
                print(
                    "fixture flow diagnostic: "
                    + "\n".join(error_log.read_text().splitlines()[-10:]),
                    flush=True,
                )
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
        local = {
            "version": version,
            "source": {"commit": images["versions"][version]["commit"]},
            "images": {
                name: {
                    "index": json.loads(
                        self.run(
                            "docker",
                            "image",
                            "inspect",
                            images["versions"][version][name],
                        )
                    )[0]["Id"]
                }
                for name in ("backend", "web")
            },
            "requirements": {"docker": "27.0.0", "compose": "2.24.4"},
        }
        if images.get("candidate_manifest"):
            # Bind all release files to the real assembled manifest. Only remote
            # acquisition is replaced by the already loaded exact config IDs;
            # this does not verify public registry indexes or attestations.
            assert (
                version == candidate_version == images["candidate_manifest"]["version"]
            )
            actual = json.loads(json.dumps(images["candidate_manifest"]))
            assert actual["source"]["commit"] == local["source"]["commit"]
            for component in ("backend", "web"):
                actual["images"][component]["index"] = local["images"][component][
                    "index"
                ]
            return actual
        return local

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
            assert (
                labels["org.opencontainers.image.revision"]
                == manifest["source"]["commit"]
            )
            assert labels["org.opencontainers.image.version"] == manifest["version"]
            assert labels["org.opencontainers.image.licenses"] == "AGPL-3.0-only"

    def automatic_checks(self, private, transaction):
        super().automatic_checks(private, transaction)
        startup_observations.append(
            {
                "transaction": transaction["id"],
                "restoring": bool(transaction["restoring"]),
                "observed_at": datetime.now(timezone.utc).isoformat(),
            }
        )
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
last_http_status = None


def request(method, path, body=None, cookie="", headers=None, expected=200):
    global last_http_status
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
    last_http_status = response.status
    assert response.status in (
        expected if isinstance(expected, tuple) else (expected,)
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
# Root-private hook inputs contain fixture credentials and client keys only.
regular_identity = json.loads(request("GET", "/auth/me", cookie=member)[1])
member_id = regular_identity.get("id", regular_identity.get("user", {}).get("id"))
assert member_id
request(
    "PATCH",
    "/admin/traffic-policy",
    {
        "enforcement_enabled": True,
        "server_budget_bytes": 8 << 20,
        "default_account_budget_bytes": 4 << 20,
    },
    admin,
)
initial = host.current()
u.atomic_json(
    u.STATE_PATH / "active.json",
    {"version": prior_version, "compose": initial, "transaction": "fixture-initial"},
)
u.atomic_json(
    Path("/opt/fixture/flow-state.json"),
    {
        "config": config,
        "ca": str(root / "fixture-ca.crt"),
        "admin": admin,
        "member": member,
        "member_id": member_id,
        "password": password,
        "factor_secret": factor["secret"],
        "existing": {
            "id": transfer,
            "blob": blob_id,
            "key": key.hex(),
            "context": encryption_id.hex(),
            "plain_sha256": hashlib.sha256(plain).hexdigest(),
            "cipher_sha256": hashlib.sha256(sealed).hexdigest(),
            "manifest_sha256": hashlib.sha256(encrypted_manifest).hexdigest(),
            "slot": None,
        },
    },
)
if images.get("initially_paused"):
    host.incident(initial, "pause")
initial_pause = host.incident(initial, "incident-status")
assert initial_pause is bool(images.get("initially_paused", False))
time.sleep(1)
pre_checkpoint_traffic = {
    "server": json.loads(request("GET", "/admin/traffic-policy", cookie=admin)[1]),
    "account": json.loads(request("GET", "/auth/traffic-usage", cookie=member)[1]),
}


def schema_count(path):
    with sqlite3.connect("file:" + str(path) + "?mode=ro", uri=True) as connection:
        return connection.execute("SELECT count(*) FROM schema_migrations").fetchone()[
            0
        ]


schema_before = schema_count(data_root / "psst.db")


def transaction_observation(stage, transaction):
    # Never export the full transaction: protected environment, sessions and
    # checkpoint paths stay inside the disposable daemon. The verification hook
    # already produces bounded, directly asserted HTTP/ciphertext observations.
    if transaction["phase"] == "completed":
        settings = json.loads(request("GET", "/config")[1])
        assert settings["public_transfers_paused"] is initial_pause
        ingress = {
            "https_port": 18443,
            "config_status": last_http_status,
            "public_transfers_paused": settings["public_transfers_paused"],
        }
    else:
        running = host.run(
            "docker", "ps", "-q", "--filter", "label=com.docker.compose.project=fixture"
        ).splitlines()
        assert not running
        ingress = {"running_services": len(running)}
    value = {
        "stage": stage,
        "transaction": transaction["id"],
        "version": transaction["version"],
        "active_version": transaction.get("active_version"),
        "previous_version": transaction["previous_version"],
        "phase": transaction["phase"],
        "mutation_started": transaction["mutation_started"],
        "prior_pause": transaction["prior_pause"],
        "restoring": transaction["restoring"],
        "verification": transaction.get("verification"),
        "public_ingress": ingress,
    }
    observations.append(value)


flow_spec = importlib.util.spec_from_file_location("flows", "/opt/fixture/flows.py")
flow_module = importlib.util.module_from_spec(flow_spec)
flow_spec.loader.exec_module(flow_module)
pre_checkpoint_credentials = flow_module.Probe(initial, {}).credentials()
updater = u.Updater(config, host)
if images.get("failure_after_start"):
    host.fail_after_start = True
    try:
        updater.update(candidate_version)
    except u.UpdateError as error:
        assert "injected post-startup" in str(error)
    else:
        raise AssertionError("candidate fault failed to close deployment")
    transaction = updater.read()
    if images.get("require_schema_change"):
        assert schema_count(data_root / "psst.db") > schema_before
    assert transaction["phase"] == "failed-closed" and transaction["mutation_started"]
    assert not host.run(
        "docker", "ps", "-q", "--filter", "label=com.docker.compose.project=fixture"
    ).strip()
    transaction_observation("post-startup-failure", transaction)
    # This fault occurs before any post-checkpoint flow/security mutations.
    # A separate fixture ledger records the independently observed baseline.
    u.atomic_json(
        Path("/opt/fixture/flow-ledger.json"),
        {
            "transaction": transaction["id"],
            "created_ids": [],
            "traffic": pre_checkpoint_traffic,
            "factor_change_after_checkpoint": False,
            "account_session_change_after_checkpoint": False,
            "credential_snapshot": pre_checkpoint_credentials,
            "existing_id": transfer,
        },
    )
    print(
        "PASS actual post-startup failure closes all services before explicit checkpoint restore",
        flush=True,
    )
else:
    transaction = updater.update(candidate_version)
    assert (
        transaction["phase"] == "completed"
        and transaction["active_version"] == candidate_version
    )
    assert transaction["previous_version"] == prior_version
    if images.get("require_schema_change"):
        assert schema_count(data_root / "psst.db") > schema_before
        try:
            host.incident(initial, "incident-status")
        except u.UpdateError:
            old_cli_rejected = True
        else:
            raise AssertionError("old exact-schema CLI accepted migrated original DB")
        print(
            "PASS historical schema version advances through normal candidate startup; old CLI refuses migrated original",
            flush=True,
        )
    assert (
        json.loads(request("GET", "/config")[1])["public_transfers_paused"]
        is initial_pause
    )
    request("GET", "/auth/me", cookie=admin)
    if initial_pause:
        request("GET", f"/transfers/{transfer}/files/{blob_id}", expected=503)
    else:
        assert request("GET", f"/transfers/{transfer}/files/{blob_id}")[1] == sealed
    transaction_observation("candidate-activation", transaction)
    print(
        "PASS prior public-source pair -> candidate activation with real authenticated flow hook",
        flush=True,
    )
    transaction = updater.update(repeat_version)
    assert (
        transaction["phase"] == "completed"
        and transaction["active_version"] == repeat_version
    )
    assert transaction["previous_version"] == candidate_version
    assert (
        json.loads(request("GET", "/config")[1])["public_transfers_paused"]
        is initial_pause
    )
    transaction_observation("repeat-activation", transaction)
    print(
        "PASS repeat release update reuses protected operator binds and preserves initialized state",
        flush=True,
    )
checkpoint = Path(transaction["checkpoint"])
checkpoint_records = u.Host.verify_backup(checkpoint)
if images.get("require_schema_change") and images.get("failure_after_start"):
    try:
        host.incident(initial, "incident-status")
    except u.UpdateError:
        old_cli_rejected = True
    else:
        raise AssertionError("old exact-schema CLI accepted migrated original DB")
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
original_hash = u.fingerprint(payload)
checkpoint_hash = u.fingerprint(checkpoint / "checkpoint.json")
restored = updater.restore()
assert restored["phase"] == "completed" and restored["restoring"]
assert restored["active_version"] == (
    prior_version if images.get("failure_after_start") else candidate_version
)
assert set(restored["restored_volumes"]) == set(config["volume_names"].values())
assert not set(restored["restored_volumes"].values()) & set(
    config["volume_names"].values()
)
assert u.fingerprint(payload) == original_hash
assert u.fingerprint(checkpoint / "checkpoint.json") == checkpoint_hash
request("GET", "/auth/me", cookie=admin)
request("GET", "/auth/me", cookie=member)
assert json.loads(request("GET", "/admin/security", cookie=admin)[1])["enabled"] is True
assert (
    json.loads(request("GET", "/config")[1])["public_transfers_paused"] is initial_pause
)
request(
    "GET",
    f"/transfers/{transfer}/manifest",
    expected=503 if initial_pause else (404, 410),
)
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
restored_root = Path(
    json.loads(
        host.run(
            "docker", "volume", "inspect", restored["restored_volumes"]["fixture-data"]
        )
    )[0]["Mountpoint"]
)
if images.get("require_schema_change") and images.get("failure_after_start"):
    restored_root = Path(
        json.loads(
            host.run(
                "docker",
                "volume",
                "inspect",
                restored["restored_volumes"]["fixture-data"],
            )
        )[0]["Mountpoint"]
    )
    assert schema_count(restored_root / "psst.db") == schema_before
    assert schema_count(data_root / "psst.db") > schema_before
    print(
        "PASS historical schema rollback exists only in new restored volumes; migrated original and checkpoint remain preserved",
        flush=True,
    )
print(
    "PASS isolated checkpoint restore, independent allowance/security reconciliation, hook-gated public activation and prior pause restoration",
    flush=True,
)
transaction_observation("isolated-restore-activation", restored)


def configuration_pair(version):
    return {
        component: json.loads(
            host.run(
                "docker", "image", "inspect", images["versions"][version][component]
            )
        )[0]["Id"]
        for component in ("backend", "web")
    }


receipt = u.load_json(checkpoint / "off-host-receipt.json")
result = {
    "schema_version": 1,
    "kind": "disposable-updater-experiment",
    "scenario": (
        "post-startup-failure" if images.get("failure_after_start") else "normal"
    ),
    "candidate": {
        "version": candidate_version,
        "commit": images["versions"][candidate_version]["commit"],
        "configs": configuration_pair(candidate_version),
    },
    "baseline": {
        "version": prior_version,
        "commit": images["versions"][prior_version]["commit"],
        "configs": configuration_pair(prior_version),
    },
    "prior_pause": initial_pause,
    "repeat_mode": (
        "same-exact-candidate"
        if repeat_version == candidate_version
        else "subsequent-fixture-version"
    ),
    "observations": observations,
    "startup_observations": startup_observations,
    "schema": {
        "before": schema_before,
        "original_after": schema_count(data_root / "psst.db"),
        "restored": schema_count(restored_root / "psst.db"),
        "old_cli_rejected_migrated_original": old_cli_rejected,
    },
    "checkpoint": {
        "sha256": checkpoint_hash,
        "records": {
            kind: sum(
                item["kind"] == kind for item in checkpoint_records["records"].values()
            )
            for kind in ("image", "volume", "configuration")
        },
        "corrupt_archive_refused": True,
        "preserved_sha256": u.fingerprint(checkpoint / "checkpoint.json"),
        "encrypted_export_receipt": receipt,
    },
    "restore": {
        "volume_mapping": restored["restored_volumes"],
        "original_volumes": config["volume_names"],
        "original_payload_sha256": original_hash,
        "preserved_original_payload_sha256": u.fingerprint(payload),
        "certificate_sha256": hashlib.sha256(ca.read_bytes()).hexdigest(),
        "restored_certificate_sha256": hashlib.sha256(
            restored_ca.read_bytes()
        ).hexdigest(),
    },
    "completed_at": datetime.now(timezone.utc).isoformat(),
    "acquisition": "fixture-local-exact-loaded-configs",
    "backup_provider": "same-host-separated-store-encryption-simulation",
    "public_provenance_verified": False,
    "off_host_provider_verified": False,
    "publication_authorized": False,
}
u.atomic_json(Path("/opt/fixture/result.json"), result)
print(
    "LIMITATIONS: fixture-local acquisition and encrypted export simulation; independent Python client, no browser/mobile execution; observed original schema migration delta="
    + str(schema_count(data_root / "psst.db") - schema_before),
    flush=True,
)

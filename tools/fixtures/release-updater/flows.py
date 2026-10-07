#!/usr/bin/python3
"""Authenticated API flow evidence for the isolated updater fixture.

Keys/cookies are loaded only from its root-private fixture state. Reports contain
asserted statuses, counters and ciphertext hashes, never credentials/plaintext.
HPKE is an independent RFC 9180 X25519/HKDF-SHA256/AES-256-GCM client; the server
stores its receive envelope opaquely, just as it stores browser-client ciphertext.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import http.client
import importlib.util
import json
import os
import ssl
import sqlite3
import struct
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives.asymmetric.x25519 import (
    X25519PrivateKey,
    X25519PublicKey,
)
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

spec = importlib.util.spec_from_file_location(
    "updater", "/usr/local/lib/psst.zip/deploy/update.py"
)
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)
STATE = Path("/opt/fixture/flow-state.json")
LEDGER = Path("/opt/fixture/flow-ledger.json")


def extract(salt, value):
    return hmac.new(salt or bytes(32), value, hashlib.sha256).digest()


def expand(prk, info, length):
    result, previous = b"", b""
    for counter in range(1, (length + 31) // 32 + 1):
        previous = hmac.new(
            prk, previous + info + bytes([counter]), hashlib.sha256
        ).digest()
        result += previous
    return result[:length]


def schedule(shared, info, encapsulated, recipient):
    kem = b"KEM\x00\x20"
    suite = b"HPKE\x00\x20\x00\x01\x00\x02"

    def labelled_extract(salt, label, value, identity=suite):
        return extract(salt, b"HPKE-v1" + identity + label + value)

    def labelled_expand(prk, label, context, length, identity=suite):
        return expand(
            prk,
            length.to_bytes(2, "big") + b"HPKE-v1" + identity + label + context,
            length,
        )

    eae = labelled_extract(b"", b"eae_prk", shared, kem)
    secret = labelled_expand(eae, b"shared_secret", encapsulated + recipient, 32, kem)
    context = (
        b"\x00"
        + labelled_extract(b"", b"psk_id_hash", b"")
        + labelled_extract(b"", b"info_hash", info)
    )
    prk = labelled_extract(secret, b"secret", b"")
    return labelled_expand(prk, b"key", context, 32), labelled_expand(
        prk, b"base_nonce", context, 12
    )


def info(slot, transfer, public):
    return (
        b"psst.zip/receive-key/v2\0\x00\x20\x00\x01\x00\x02"
        + uuid.UUID(slot).bytes
        + uuid.UUID(transfer).bytes
        + public
    )


def wrap(public, slot, transfer, key):
    ephemeral = X25519PrivateKey.generate()
    enc = ephemeral.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
    wrapping, nonce = schedule(
        ephemeral.exchange(X25519PublicKey.from_public_bytes(public)),
        info(slot, transfer, public),
        enc,
        public,
    )
    return enc + AESGCM(wrapping).encrypt(nonce, key, None)


def unwrap(private, public, slot, transfer, wrapped):
    wrapping, nonce = schedule(
        private.exchange(X25519PublicKey.from_public_bytes(wrapped[:32])),
        info(slot, transfer, public),
        wrapped[:32],
        public,
    )
    return AESGCM(wrapping).decrypt(nonce, wrapped[32:], None)


class Probe:
    def __init__(self, compose, transaction):
        u.protected(STATE)
        self.state = u.load_json(STATE)
        self.compose, self.transaction = compose, transaction
        self.host = u.Host(self.state["config"])
        self.host.env["CURL_CA_BUNDLE"] = self.state["ca"]
        self.admin, self.member = self.state["admin"], self.state["member"]
        self.context = ssl.create_default_context(cafile=self.state["ca"])
        self.checks = {}
        self.events = []
        self.created = []
        self.data = Path(
            json.loads(
                self.host.run(
                    "docker",
                    "volume",
                    "inspect",
                    u.Host.bindings(compose, "backend")["/app/data"],
                )
            )[0]["Mountpoint"]
        )

    def request(
        self, method, path, body=None, *, cookie=None, headers=None, expected=200
    ):
        port = next(
            item["published"]
            for item in self.compose["services"]["caddy"]["ports"]
            if item["target"] == 8443
        )
        connection = http.client.HTTPSConnection(
            self.state["config"]["domain"], int(port), context=self.context, timeout=15
        )
        outgoing = {"Origin": "https://" + self.state["config"]["domain"]}
        if cookie:
            outgoing["Cookie"] = cookie
        if isinstance(body, dict):
            body = json.dumps(body).encode()
            outgoing["Content-Type"] = "application/json"
        outgoing.update(headers or {})
        connection.request(method, "/api/v1" + path, body, outgoing)
        response = connection.getresponse()
        data, fields = response.read(), dict(response.getheaders())
        connection.close()
        assert response.status in (
            expected if isinstance(expected, tuple) else (expected,)
        ), f"{method} {path} expected {expected}, got {response.status}; code={fields.get('X-Psst-Error-Code', '')}"
        self.events.append(
            {
                "method": method,
                "path": path,
                "status": response.status,
                "body_sha256": hashlib.sha256(data).hexdigest(),
            }
        )
        return fields, data

    def get(self, path, cookie=None):
        return json.loads(self.request("GET", path, cookie=cookie)[1])

    def evidence(self, name, details):
        # The assertions occur before this receipt is recorded; details are exact
        # runtime statuses/counters/hashes or comparisons, not operator claims.
        self.checks[name] = json.dumps(details, sort_keys=True, separators=(",", ":"))
        assert 12 <= len(self.checks[name]) <= 1000

    def upload(self, *, slot=None, recipient=None, policy=None):
        if slot:
            created = json.loads(
                self.request("POST", f"/slots/{slot}/transfers", {}, expected=201)[1]
            )
            auth, cookie = {"Authorization": "Bearer " + created["delete_token"]}, None
        else:
            created = json.loads(
                self.request(
                    "POST", "/transfers", policy or {}, cookie=self.member, expected=201
                )[1]
            )
            auth, cookie = {}, self.member
        transfer = created["id"]
        key, context = os.urandom(32), os.urandom(16)
        plaintext = os.urandom(75)
        frame = context + bytes(8) + len(plaintext).to_bytes(8, "big") + plaintext
        nonce = os.urandom(12)
        sealed = nonce + AESGCM(key).encrypt(nonce, frame, None)
        fields, _ = self.request(
            "POST",
            f"/transfers/{transfer}/files",
            cookie=cookie,
            headers={
                **auth,
                "Tus-Resumable": "1.0.0",
                "Upload-Length": str(len(sealed)),
            },
            expected=201,
        )
        blob = fields["Location"].rstrip("/").split("/")[-1]
        self.request(
            "PATCH",
            f"/transfers/{transfer}/files/{blob}",
            sealed,
            cookie=cookie,
            headers={
                **auth,
                "Tus-Resumable": "1.0.0",
                "Upload-Offset": "0",
                "Content-Type": "application/offset+octet-stream",
            },
            expected=204,
        )
        manifest = {
            "files": [
                {
                    "name": "fixture.bin",
                    "size": len(plaintext),
                    "mime_type": "application/octet-stream",
                    "blob_id": blob,
                    "encoding": "chunked-v1",
                    "chunk_size": 4194304,
                    "encryption_id": context.hex(),
                }
            ]
        }
        nonce = os.urandom(12)
        encrypted = nonce + AESGCM(key).encrypt(
            nonce, json.dumps(manifest).encode(), None
        )
        if slot:
            encrypted = b"PSSTRCV2" + wrap(recipient, slot, transfer, key) + encrypted
        self.request(
            "POST",
            f"/transfers/{transfer}/manifest",
            encrypted,
            cookie=cookie,
            headers={**auth, "Content-Type": "application/octet-stream"},
            expected=204,
        )
        self.request(
            "POST",
            f"/transfers/{transfer}/complete",
            cookie=cookie,
            headers=auth,
            expected=204,
        )
        record = {
            "id": transfer,
            "blob": blob,
            "key": key.hex(),
            "context": context.hex(),
            "plain_sha256": hashlib.sha256(plaintext).hexdigest(),
            "cipher_sha256": hashlib.sha256(sealed).hexdigest(),
            "manifest_sha256": hashlib.sha256(encrypted).hexdigest(),
            "slot": slot,
        }
        self.created.append(record)
        return record

    def download(self, record, private=None, public=None):
        cookie = self.member if record.get("slot") else None
        envelope = self.request(
            "GET", f'/transfers/{record["id"]}/manifest', cookie=cookie
        )[1]
        key = bytes.fromhex(record["key"])
        if record.get("slot"):
            assert envelope[:8] == b"PSSTRCV2"
            key = unwrap(private, public, record["slot"], record["id"], envelope[8:88])
            assert key.hex() == record["key"]
            try:
                unwrap(private, public, str(uuid.uuid4()), record["id"], envelope[8:88])
            except InvalidTag:
                pass
            else:
                raise AssertionError("HPKE context substitution authenticated")
            envelope = envelope[88:]
        manifest = json.loads(AESGCM(key).decrypt(envelope[:12], envelope[12:], None))
        assert manifest["files"][0]["blob_id"] == record["blob"]
        assert manifest["files"][0]["encryption_id"] == record["context"]
        sealed = self.request(
            "GET", f'/transfers/{record["id"]}/files/{record["blob"]}', cookie=cookie
        )[1]
        frame = AESGCM(key).decrypt(sealed[:12], sealed[12:], None)
        assert frame[:16].hex() == record["context"] and frame[16:24] == bytes(8)
        assert int.from_bytes(frame[24:32], "big") == len(frame[32:])
        assert hashlib.sha256(frame[32:]).hexdigest() == record["plain_sha256"]
        assert hashlib.sha256(sealed).hexdigest() == record["cipher_sha256"]
        try:
            altered = sealed[:-1] + bytes([sealed[-1] ^ 1])
            AESGCM(key).decrypt(altered[:12], altered[12:], None)
        except InvalidTag:
            pass
        else:
            raise AssertionError("ciphertext tampering authenticated")
        return hashlib.sha256(sealed).hexdigest()

    def wait_reconciliation(self):
        for attempt in range(45):
            try:
                self.host.authenticated_checks(self.compose, self.admin)
                return
            except u.UpdateError:
                if attempt == 44:
                    raise
                time.sleep(1)

    def traffic(self):
        # Payload accounting flushes on an asynchronous short batch timer.
        time.sleep(1)
        return {
            "server": self.get("/admin/traffic-policy", self.admin),
            "account": self.get("/auth/traffic-usage", self.member),
        }

    def credentials(self):
        database = self.data / self.state["config"]["db_path"].removeprefix(
            "/app/data/"
        )
        with sqlite3.connect(
            "file:" + str(database) + "?mode=ro", uri=True
        ) as connection:
            sessions = sorted(
                row[0] for row in connection.execute("SELECT token_hash FROM sessions")
            )
            factors = connection.execute(
                "SELECT secret,revision FROM admin_security WHERE secret<>''"
            ).fetchall()
            recovery = sorted(
                row[0]
                for row in connection.execute("SELECT hash FROM admin_recovery_codes")
            )
        return {
            "session_count": len(sessions),
            "session_credentials_sha256": hashlib.sha256(
                b"".join(sessions)
            ).hexdigest(),
            "factor_and_recovery_credentials_sha256": hashlib.sha256(
                json.dumps(factors).encode() + b"".join(recovery)
            ).hexdigest(),
        }

    def refresh_factor(self):
        # Read only the disposable fixture DB. Authentication updates are made
        # exclusively through the real factor API, never through direct SQL.
        database = self.data / self.state["config"]["db_path"].removeprefix(
            "/app/data/"
        )
        with sqlite3.connect(
            "file:" + str(database) + "?mode=ro", uri=True
        ) as connection:
            counter, secret = connection.execute(
                "SELECT last_counter,secret FROM admin_security WHERE secret<>''"
            ).fetchone()
        assert secret == self.state["factor_secret"]
        floor = max(counter, self.state.get("factor_counter", -1))
        while int(time.time()) // 30 + 1 <= floor:
            time.sleep(1)
        selected = max(int(time.time()) // 30, floor + 1)
        decoded = base64.b32decode(secret + "=" * (-len(secret) % 8))
        mac = hmac.new(decoded, struct.pack(">Q", selected), hashlib.sha1).digest()
        offset = mac[-1] & 15
        code = str(
            (int.from_bytes(mac[offset : offset + 4], "big") & 0x7FFFFFFF) % 1000000
        ).zfill(6)
        result = json.loads(
            self.request(
                "POST",
                "/admin/security/reauth",
                {"password": self.state["password"], "code": code},
                cookie=self.admin,
            )[1]
        )
        assert result["recent_until"]
        self.state["factor_counter"] = selected
        u.atomic_json(STATE, self.state)
        self.factor_counter = selected

    def verify(self):
        self.host.runtime_checks(self.compose)
        assert all(
            port.get("host_ip") == "127.0.0.1"
            for service in self.compose["services"].values()
            for port in service.get("ports", [])
        )
        assert self.host.incident(self.compose, "incident-status") is True
        self.request("POST", "/transfers", {}, cookie=self.member, expected=503)
        administrator = self.get("/auth/me", self.admin)
        regular = self.get("/auth/me", self.member)
        assert (
            administrator.get("role", administrator.get("user", {}).get("role"))
            == "admin"
        )
        assert regular.get("role", regular.get("user", {}).get("role")) == "user"
        assert self.get("/admin/security", self.admin)["enabled"] is True
        self.request(
            "POST",
            "/auth/login",
            {
                "username": "fixture-admin",
                "password": self.state["password"],
                "session_type": "web",
            },
            expected=401,
        )
        self.refresh_factor()
        settings = self.get("/config")
        assert settings["max_file_size"] == 3 << 20
        self.evidence(
            "existing_account_and_session",
            {
                "admin_me": 200,
                "member_me": 200,
                "member_role": "user",
                "sessions": "pre-checkpoint cookies accepted",
            },
        )
        self.evidence(
            "administrator_second_factor",
            {
                "enabled": True,
                "password_only_login": 401,
                "pre_checkpoint_factor_authenticated_session": 200,
                "fresh_totp_reauthentication": 200,
                "accepted_counter": self.factor_counter,
            },
        )
        self.evidence(
            "persisted_settings_and_quotas",
            {
                "max_file_size": settings["max_file_size"],
                "paused_creation": 503,
                "traffic_policy": {
                    key: settings["traffic_policy"][key]
                    for key in (
                        "server_budget_bytes",
                        "default_account_budget_bytes",
                        "enforcement_enabled",
                    )
                },
            },
        )
        self.wait_reconciliation()
        baseline = self.traffic()
        credentials_before = self.credentials()
        self.host.incident(self.compose, "resume")
        try:
            existing = self.state["existing"]
            digest = self.download(existing)
            self.evidence(
                "existing_encrypted_download",
                {
                    "cipher_sha256": digest,
                    "manifest_and_file": 200,
                    "decryption_context": "authenticated frame matches stored plaintext hash",
                },
            )
            send = self.upload()
            self.download(send)
            pair = X25519PrivateKey.generate()
            public = pair.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
            slot = self.get_slot(public)
            received = self.upload(slot=slot, recipient=public)
            self.request(
                "GET",
                f'/slots/{slot}/transfers/{received["id"]}/membership',
                cookie=self.member,
            )
            self.download(received, pair, public)
            self.evidence(
                "new_upload_and_receive",
                {
                    "send_complete": 204,
                    "receive_complete": 204,
                    "membership": 200,
                    "send_cipher_sha256": send["cipher_sha256"],
                    "receive_cipher_sha256": received["cipher_sha256"],
                    "receive_protocol": 2,
                },
            )
            self.evidence(
                "client_authenticated_decryption",
                {
                    "aes_gcm_tamper": "InvalidTag",
                    "hpke_wrong_slot": "InvalidTag",
                    "send_frame_sha256": send["plain_sha256"],
                    "receive_frame_sha256": received["plain_sha256"],
                },
            )
            self.policy_tests(send)
            self.request("DELETE", f"/slots/{slot}", cookie=self.member, expected=204)
            self.request("GET", f"/slots/{slot}/availability", expected=(404, 410))
            # Remove all newly created authority/payload state after proving it.
            for record in self.created:
                self.request(
                    "DELETE",
                    f'/transfers/{record["id"]}',
                    cookie=self.member,
                    expected=(204, 404),
                )
            for attempt in range(45):
                if all(
                    not (self.data / "files" / record["id"]).exists()
                    for record in self.created
                ):
                    break
                time.sleep(1)
            else:
                raise AssertionError("cleanup did not remove revoked/expired payloads")
            self.wait_reconciliation()
            if self.transaction.get("restoring"):
                self.restore_security(baseline)
            ledger = self.traffic()
            assert self.credentials() == credentials_before
            u.atomic_json(
                LEDGER,
                {
                    "transaction": self.transaction["id"],
                    "created_ids": [record["id"] for record in self.created],
                    "traffic": ledger,
                    "factor_change_after_checkpoint": False,
                    "factor_counter": self.factor_counter,
                    "account_session_change_after_checkpoint": False,
                    "credential_snapshot": self.credentials(),
                    "administrator_recent_proof_refreshed": True,
                    "existing_id": existing["id"],
                },
            )
        finally:
            self.host.incident(self.compose, "pause")
        if self.transaction.get("restoring"):
            # The hook checks its conservative reconciliation after restart;
            # the privileged updater independently validates this bounded change
            # again before durably accepting the returned evidence/activation.
            self.transaction["adopted"]["baseline_config"] = (
                u.reconciled_restore_settings(
                    self.transaction["adopted"]["baseline_config"], self.get("/config")
                )
            )
        private_path = (
            u.STATE_PATH
            / "transactions"
            / self.transaction["id"]
            / "private.compose.json"
        )
        self.host.compose(private_path, "restart", "backend")
        for attempt in range(40):
            try:
                self.host.automatic_checks(self.compose, self.transaction)
                self.wait_reconciliation()
                break
            except u.UpdateError:
                if attempt == 39:
                    raise
                time.sleep(1)
        self.request("GET", "/auth/me", cookie=self.member)
        assert self.get("/config")["public_transfers_paused"] is True
        self.evidence(
            "cleanup_and_restart",
            {
                "created_payload_directories_absent": len(self.created),
                "backend_restart": "actual Compose restart",
                "restored_pause": True,
                "member_session_after_restart": 200,
                "storage_counter_orphan": "checked no pending/issues/errors",
            },
        )
        assert set(self.checks) == u.FLOW_CHECKS | (
            u.RESTORE_CHECKS if self.transaction.get("restoring") else set()
        )
        identity = self.transaction.get(
            "verification_identity",
            self.transaction["manifest"]["source"]
            | {"version": self.transaction["manifest"]["version"]},
        )
        return {
            "checks": self.checks,
            "observed_at": datetime.now(timezone.utc).isoformat(),
            "version": identity["version"],
            "source_commit": identity.get("source_commit", identity.get("commit")),
        }

    def get_slot(self, public):
        result = json.loads(
            self.request(
                "POST",
                "/slots",
                {
                    "receive_protocol": 2,
                    "recipient_public_key": base64.urlsafe_b64encode(public)
                    .decode()
                    .rstrip("="),
                },
                cookie=self.member,
                expected=201,
            )[1]
        )
        slot = result["id"]
        assert self.get(f"/slots/{slot}/availability")["available"] is True
        return slot

    def policy_tests(self, send):
        limited = self.upload(policy={"max_downloads": 1})
        self.download(limited)
        self.request(
            "GET", f'/transfers/{limited["id"]}/files/{limited["blob"]}', expected=410
        )
        expiring = self.upload(policy={"expires_in_seconds": 2})
        time.sleep(3)
        self.request(
            "GET", f'/transfers/{expiring["id"]}/manifest', expected=(404, 410)
        )
        expired_status = self.events[-1]["status"]
        self.request(
            "DELETE", f'/transfers/{send["id"]}', cookie=self.member, expected=204
        )
        self.request("GET", f'/transfers/{send["id"]}/manifest', expected=(404, 410))
        revoked_status = self.events[-1]["status"]
        oversized = json.loads(
            self.request("POST", "/transfers", {}, cookie=self.member, expected=201)[1]
        )
        self.request(
            "POST",
            f'/transfers/{oversized["id"]}/files',
            cookie=self.member,
            headers={"Tus-Resumable": "1.0.0", "Upload-Length": str((3 << 20) + 61)},
            expected=413,
        )
        self.request(
            "DELETE", f'/transfers/{oversized["id"]}', cookie=self.member, expected=204
        )
        policy = self.get("/admin/traffic-policy", self.admin)["policy"]
        account_id = self.state["member_id"]
        current = self.get("/auth/traffic-usage", self.member)
        budget = max(1, current["usage"]["charged_bytes"])
        self.request(
            "PATCH",
            f"/admin/users/{account_id}/traffic-policy",
            {"account_budget_bytes": budget},
            cookie=self.admin,
        )
        try:
            self.request(
                "GET",
                f'/transfers/{self.state["existing"]["id"]}/files/{self.state["existing"]["blob"]}',
                expected=429,
            )
        finally:
            self.request(
                "PATCH",
                f"/admin/users/{account_id}/traffic-policy",
                {"account_budget_bytes": None},
                cookie=self.admin,
            )
        assert self.get("/admin/traffic-policy", self.admin)["policy"] == policy
        self.evidence(
            "expiry_budget_and_revocation",
            {
                "download_limit_second_request": 410,
                "expired_manifest": expired_status,
                "expiry_wait_seconds": 3,
                "revoked_manifest": revoked_status,
                "oversized_upload": 413,
                "account_traffic_exhausted": 429,
                "quota_override_removed": True,
            },
        )

    def restore_security(self, baseline):
        u.protected(LEDGER)
        previous = u.load_json(LEDGER)
        assert previous["transaction"] == self.transaction["id"]
        assert previous["factor_change_after_checkpoint"] is False
        assert self.factor_counter > previous.get("factor_counter", -1)
        u.atomic_json(
            LEDGER.with_name(
                "independent-before-restore-" + self.transaction["id"] + ".json"
            ),
            previous,
        )
        assert previous["account_session_change_after_checkpoint"] is False
        assert previous["credential_snapshot"] == self.credentials()
        for transfer in previous["created_ids"]:
            self.request("GET", f"/transfers/{transfer}/manifest", expected=(404, 410))
        # Existing download authority may regain a spent allowance on restore.
        # Revoke it rather than granting extra usage based on old counters.
        self.request(
            "DELETE",
            f'/transfers/{previous["existing_id"]}',
            cookie=self.member,
            expected=204,
        )
        self.request(
            "GET", f'/transfers/{previous["existing_id"]}/manifest', expected=(404, 410)
        )
        recorded = previous["traffic"]
        current = self.traffic()
        policy = current["server"]["policy"]
        lost_server = max(
            0,
            recorded["server"]["usage"]["charged_bytes"]
            - baseline["server"]["usage"]["charged_bytes"],
        )
        lost_account = max(
            0,
            recorded["account"]["usage"]["charged_bytes"]
            - baseline["account"]["usage"]["charged_bytes"],
        )
        policy["server_budget_bytes"] -= lost_server
        policy["default_account_budget_bytes"] -= lost_account
        assert (
            policy["server_budget_bytes"] > 0
            and policy["default_account_budget_bytes"] > 0
        )
        self.request("PATCH", "/admin/traffic-policy", policy, cookie=self.admin)
        reconciled = self.traffic()
        assert (
            reconciled["server"]["usage"]["remaining_bytes"]
            <= recorded["server"]["usage"]["remaining_bytes"]
        )
        assert (
            reconciled["account"]["usage"]["remaining_bytes"]
            <= recorded["account"]["usage"]["remaining_bytes"]
        )
        self.evidence(
            "post_checkpoint_changes_reviewed",
            {
                "external_transaction_matches": True,
                "post_checkpoint_created_ids": len(previous["created_ids"]),
                "absent_on_restore": True,
                "restored_existing_authority_revoked": True,
            },
        )
        self.evidence(
            "restored_sessions_links_and_factors_reconciled",
            {
                "post_checkpoint_factor_changes": False,
                "post_checkpoint_session_credential_changes": False,
                "credential_snapshot": self.credentials(),
                "recent_proof": "fresh factor proof required after restore",
                "factor_enabled": True,
                "totp_counter_above_independent_record": self.factor_counter,
                "post_checkpoint_created_links": "absent",
                "pre_checkpoint_download_authority": "revoked to avoid allowance resurrection",
            },
        )
        self.evidence(
            "independent_traffic_allowances_reconciled",
            {
                "lost_server_charge_deducted_from_budget": lost_server,
                "lost_account_charge_deducted_from_budget": lost_account,
                "remaining_server_not_increased": True,
                "remaining_account_not_increased": True,
                "counters": "never cleared or edited",
            },
        )

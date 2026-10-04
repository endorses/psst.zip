#!/usr/bin/env python3
"""Disposable external TLS proxy integration gate for psst.zip.

Run from any directory: python3 tools/test_external_proxy.py
Requires Docker Compose >=2.24.4, OpenSSL, and Docker build/pull access.
Only uniquely named test resources are changed. No repository .env is read.
TLS uses a disposable CA passed explicitly to clients, never global trust.
--certificate-state exercises managed internal-CA storage backup/restore;
it does not test public ACME issuance or renewal.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import http.client
import json
import os
import secrets
import shutil
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import time
from pathlib import Path


class GateFailure(Exception):
    """A sanitized diagnostic suitable for operator output."""


def check(condition: bool, message: str) -> None:
    if not condition:
        raise GateFailure(message)


def phase(message: str) -> None:
    print(message, flush=True)


def unused_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


# The helper receives only test data on stdin. Its output contains status codes,
# never response bodies, cookies, credentials, or request/configuration dumps.
CLIENT_SCRIPT = r"""
import http.client, json, ssl, sys, time
job = json.load(sys.stdin)
context = ssl.create_default_context(cafile="/test-ca/ca.crt")
statuses = []
started = time.monotonic()
for i in range(job["count"]):
    if job["tls"]:
        connection = http.client.HTTPSConnection(job["host"], job["port"], context=context, timeout=5)
    else:
        connection = http.client.HTTPConnection(job["host"], job["port"], timeout=5)
    body = json.dumps({"username": "absent-" + job["nonce"] + "-" + str(i), "password": "invalid-disposable-password", "session_type": "web"})
    headers = {"Content-Type": "application/json", "Origin": job["origin"], "Host": job["authority"], "X-Forwarded-For": "198.51.100." + str(i + 1), "X-Forwarded-Proto": "https", "Forwarded": "for=203.0.113." + str(i + 1) + ";proto=https"}
    connection.request("POST", "/api/v1/auth/login", body, headers)
    response = connection.getresponse()
    statuses.append(response.status)
    response.read()
    connection.close()
print(json.dumps({"statuses": statuses, "elapsed": time.monotonic() - started}))
"""


class Harness:
    def __init__(self, client_image: str, certificate_state: bool = False):
        self.root = Path(__file__).resolve().parents[1]
        self.project = "psst-proxy-gate-" + secrets.token_hex(6)
        self.temp = Path(tempfile.mkdtemp(prefix=self.project + "-"))
        self.temp.chmod(0o700)
        self.client_image = client_image
        self.certificate_state = certificate_state
        self.helpers: list[str] = []
        self.volumes: list[str] = []
        self.images = [self.project + "-backend", self.project + "-web"]
        self.http_port = unused_port()
        self.port = unused_port()
        while self.port == self.http_port:
            self.port = unused_port()
        self.origin = f"https://localhost:{self.port}"
        self.admin = "operator-" + secrets.token_hex(5)
        self.password = secrets.token_urlsafe(32)
        self.cookie = ""
        # Separate /29s, with room for two genuinely distinct client peers.
        octet = secrets.randbelow(200) + 20
        self.private = f"172.28.{octet}"
        self.proxy = f"172.29.{octet}"
        self.env = {
            "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
            "DOCKER_CONFIG": str(self.temp / "docker-config"),
            "COMPOSE_PROJECT_NAME": self.project,
        }
        (self.temp / "docker-config").mkdir()
        self.compose_files = [
            self.root / "docker-compose.yml",
            self.root / "deploy/external-proxy.compose.yml",
            self.temp / "fixture.compose.yml",
        ]

    def command(
        self, args: list[str], *, data: str | None = None, timeout: int = 600
    ) -> str:
        try:
            result = subprocess.run(
                args,
                cwd=self.root,
                env=self.env,
                input=data,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=timeout,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise GateFailure(f"{args[0]} unavailable or command timed out") from error
        # Never echo the command/output: it may include bootstrap credentials.
        if result.returncode != 0:
            detail = "output withheld"
            if args[:2] == ["docker", "run"] and result.returncode == 125:
                # Docker's startup diagnostics describe these disposable
                # containers, whose command lines contain no auth secrets.
                # Keep the first line bounded and redact private host paths.
                detail = (
                    result.stderr.splitlines()[0][:512] if result.stderr else detail
                )
                detail = detail.replace(str(self.temp), "<test-directory>")
            raise GateFailure(
                f"{' '.join(args[:2])} command failed (exit {result.returncode}); {detail}"
            )
        return result.stdout

    def compose(self, *args: str, timeout: int = 600) -> str:
        command = [
            "docker",
            "compose",
            "--env-file",
            str(self.temp / "test.env"),
            "-p",
            self.project,
        ]
        for path in self.compose_files:
            command += ["-f", str(path)]
        return self.command(command + list(args), timeout=timeout)

    def certificate(self) -> None:
        tls = self.temp / "tls"
        tls.mkdir(mode=0o755)
        if self.certificate_state:
            fixture = self.temp / "tls.d"
            fixture.mkdir(mode=0o755)
            (fixture / "test.caddy").write_text("tls internal\n")
            # Fixture-only: do not install even this disposable root into the
            # gateway's trust store. No host/APK trust store is ever changed.
            gateway = (self.root / "deploy/external-proxy/Caddyfile").read_text()
            (self.temp / "gateway.Caddyfile").write_text(
                gateway.replace("{\n", "{\n\tskip_install_trust\n", 1)
            )
            return
        self.command(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "1",
                "-keyout",
                str(tls / "ca.key"),
                "-out",
                str(tls / "ca.crt"),
                "-subj",
                "/CN=psst disposable integration CA",
                "-addext",
                "basicConstraints=critical,CA:TRUE",
                "-addext",
                "keyUsage=critical,keyCertSign,cRLSign",
                "-addext",
                "subjectKeyIdentifier=hash",
            ]
        )
        self.command(
            [
                "openssl",
                "req",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-keyout",
                str(tls / "server.key"),
                "-out",
                str(tls / "server.csr"),
                "-subj",
                "/CN=localhost",
            ]
        )
        extension = self.temp / "leaf.ext"
        extension.write_text(
            "subjectAltName=DNS:localhost,DNS:external-proxy,IP:127.0.0.1\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\n"
        )
        self.command(
            [
                "openssl",
                "x509",
                "-req",
                "-in",
                str(tls / "server.csr"),
                "-CA",
                str(tls / "ca.crt"),
                "-CAkey",
                str(tls / "ca.key"),
                "-CAcreateserial",
                "-days",
                "1",
                "-out",
                str(tls / "server.crt"),
                "-extfile",
                str(extension),
            ]
        )
        # The parent temp directory remains mode 0700; only disposable mounted
        # leaf credentials are readable by the unprivileged Caddy container.
        (tls / "server.key").chmod(0o644)
        (tls / "ca.key").chmod(0o600)
        fixture = self.temp / "tls.d"
        fixture.mkdir(mode=0o755)
        (fixture / "test.caddy").write_text(
            "tls /test-tls/server.crt /test-tls/server.key\n"
        )
        self.context = ssl.create_default_context(cafile=str(tls / "ca.crt"))

    def configure(self) -> None:
        values = {
            "COMPOSE_PROJECT_NAME": self.project,
            "PSST_DOMAIN": "localhost",
            "PUBLIC_URL": self.origin,
            "AUTH_ALLOW_INSECURE_HTTP": "false",
            "ADMIN_USERNAME": self.admin,
            "ADMIN_PASSWORD": self.password,
            "PSST_PRIVATE_SUBNET": self.private + ".0/29",
            "PSST_BACKEND_IP": self.private + ".2",
            "PSST_PROXY_IP": self.private + ".3",
            "PSST_EXTERNAL_PROXY_SUBNET": self.proxy + ".0/29",
            "PSST_EXTERNAL_PROXY_IP": self.proxy + ".2",
            "PSST_WEB_PROXY_IP": self.proxy + ".3",
        }
        env_file = self.temp / "test.env"
        env_file.write_text(
            "".join(f"{key}={value}\n" for key, value in values.items())
        )
        env_file.chmod(0o600)
        # JSON scalars are valid YAML; avoid ad hoc escaping of bind paths.
        mounts = [f"{self.temp / 'tls.d'}:/etc/caddy/tls.d:ro"]
        if self.certificate_state:
            mounts.append(f"{self.temp / 'gateway.Caddyfile'}:/etc/caddy/Caddyfile:ro")
        else:
            mounts.extend(
                [
                    f"{self.temp / 'tls' / 'server.crt'}:/test-tls/server.crt:ro",
                    f"{self.temp / 'tls' / 'server.key'}:/test-tls/server.key:ro",
                ]
            )
        (self.temp / "fixture.compose.yml").write_text(
            "services:\n"
            f"  backend:\n    image: {self.images[0]}\n"
            "  external-proxy:\n    ports: !override\n"
            f"      - {json.dumps(f'127.0.0.1:{self.http_port}:8080')}\n"
            f"      - {json.dumps(f'127.0.0.1:{self.port}:8443')}\n"
            "    volumes:\n"
            + "".join(f"      - {json.dumps(mount)}\n" for mount in mounts)
        )

    def request(
        self,
        method: str,
        path: str,
        body: dict | None = None,
        *,
        origin: str | None = "canonical",
        cookie: str | None = None,
        headers: dict | None = None,
    ) -> tuple[int, dict, dict]:
        connection = http.client.HTTPSConnection(
            "localhost", self.port, context=self.context, timeout=10
        )
        outgoing = {"Content-Type": "application/json"}
        if origin is not None:
            outgoing["Origin"] = self.origin if origin == "canonical" else origin
        if cookie is None:
            cookie = self.cookie
        if cookie:
            outgoing["Cookie"] = cookie
        outgoing.update(headers or {})
        try:
            connection.request(
                method,
                "/api/v1" + path,
                json.dumps(body) if body is not None else None,
                outgoing,
            )
            response = connection.getresponse()
            payload = response.read()
            fields = dict(response.getheaders())
            decoded = json.loads(payload) if payload else {}
            return response.status, fields, decoded
        finally:
            connection.close()

    def expect(
        self, status: int, method: str, path: str, body: dict | None = None, **kwargs
    ) -> tuple[dict, dict]:
        actual, headers, payload = self.request(method, path, body, **kwargs)
        check(
            actual == status,
            f"{method} {path.split('?')[0]} expected {status}, received {actual}",
        )
        return headers, payload

    def wait_ready(self) -> None:
        deadline = time.monotonic() + 60
        last_failure = "no response"
        while time.monotonic() < deadline:
            try:
                self.expect(200, "GET", "/health")
                return
            except (OSError, http.client.HTTPException, GateFailure) as error:
                # GateFailure messages are explicitly sanitized. Raw exception
                # strings can contain data, so expose only their type otherwise.
                last_failure = (
                    str(error)
                    if isinstance(error, GateFailure)
                    else type(error).__name__
                )
                if isinstance(error, ssl.SSLCertVerificationError):
                    raise GateFailure(
                        f"disposable TLS certificate verification failed: code={error.verify_code} reason={error.verify_message}"
                    ) from error
                time.sleep(0.5)
        for service in ("backend", "caddy", "external-proxy"):
            try:
                state = self.inspect(self.container(service))["State"]
                phase(
                    f"Startup diagnostic {service}: status={state['Status']} exit={state['ExitCode']}"
                )
            except GateFailure:
                phase(f"Startup diagnostic {service}: state unavailable")
        raise GateFailure(
            f"verified HTTPS health did not become ready within 60 seconds ({last_failure})"
        )

    def container(self, service: str) -> str:
        container = self.compose("ps", "-q", service).strip()
        check(
            bool(container) and "\n" not in container,
            f"expected one disposable {service} container",
        )
        return container

    def inspect(self, container: str) -> dict:
        return json.loads(self.command(["docker", "inspect", container]))[0]

    def check_hardening(self) -> None:
        for service in ("backend", "caddy", "external-proxy"):
            container = self.container(service)
            details = self.inspect(container)
            host = details["HostConfig"]
            check(host["ReadonlyRootfs"], f"{service} root filesystem is writable")
            check("ALL" in host["CapDrop"], f"{service} lacks cap_drop ALL")
            check(
                any(
                    option.startswith("no-new-privileges")
                    for option in host["SecurityOpt"]
                ),
                f"{service} lacks no-new-privileges",
            )
            check(
                host["PidsLimit"] > 0 and host["Memory"] > 0 and host["NanoCpus"] > 0,
                f"{service} lacks process/memory/CPU bounds",
            )
            log = host["LogConfig"]
            check(
                log["Type"] == "local"
                and log["Config"].get("max-size") == "10m"
                and log["Config"].get("max-file") == "3",
                f"{service} logging is unbounded",
            )
            check(
                self.command(["docker", "exec", container, "id", "-u"]).strip() != "0",
                f"{service} runs as root",
            )
            check(
                host["Tmpfs"].get("/tmp"),
                f"{service} lacks bounded temporary filesystem",
            )
            if service != "external-proxy":
                check(
                    not host["PortBindings"], f"{service} unexpectedly publishes a port"
                )
            else:
                for bindings in host["PortBindings"].values():
                    check(
                        all(binding["HostIp"] == "127.0.0.1" for binding in bindings),
                        "test gateway publishes beyond loopback",
                    )
        backend = self.inspect(self.container("backend"))
        settings = dict(item.split("=", 1) for item in backend["Config"]["Env"])
        check(
            settings["TRUSTED_PROXIES"] == self.private + ".3/32",
            "backend proxy trust is broader than its one inner peer",
        )
        check(
            settings["AUTH_ALLOW_INSECURE_HTTP"] == "false"
            and settings["PUBLIC_URL"] == self.origin,
            "test auth transport/canonical URL mismatch",
        )

    def login(self, username: str, password: str) -> str:
        headers, _ = self.expect(
            200,
            "POST",
            "/auth/login",
            {"username": username, "password": password, "session_type": "web"},
            cookie="",
        )
        value = headers.get("Set-Cookie", "")
        check(
            all(
                flag in value
                for flag in ("Secure", "HttpOnly", "SameSite=Strict", "Path=/api/v1")
            ),
            "session cookie security flags missing",
        )
        check(headers.get("Cache-Control") == "no-store", "login lacks no-store")
        return value.split(";", 1)[0]

    def auth_and_origin(self) -> None:
        body = {
            "username": self.admin,
            "password": self.password,
            "session_type": "web",
        }
        self.expect(403, "POST", "/auth/login", body, origin=None)
        self.expect(403, "POST", "/auth/login", body, origin="https://foreign.invalid")
        self.expect(
            403,
            "POST",
            "/auth/login",
            body,
            origin="https://foreign.invalid",
            headers={
                "X-Forwarded-Host": "foreign.invalid",
                "X-Forwarded-Proto": "https",
                "Forwarded": "host=foreign.invalid;proto=https",
            },
        )
        self.cookie = self.login(self.admin, self.password)
        headers, security = self.expect(200, "GET", "/admin/security")
        check(
            security["enabled"] is False and bool(security["recent_until"]),
            "fresh test administrator lacks recent proof or unexpectedly has TOTP",
        )
        check(
            headers.get("Cache-Control") == "no-store", "admin security lacks no-store"
        )
        _, config = self.expect(200, "GET", "/config")
        baseline = config["max_file_size"]
        for origin in (None, "https://foreign.invalid"):
            self.expect(
                403,
                "PATCH",
                "/admin/settings",
                {"max_file_size": 2 << 20},
                origin=origin,
                headers={
                    "X-Forwarded-Host": "foreign.invalid",
                    "X-Forwarded-Proto": "https",
                },
            )
            _, unchanged = self.expect(200, "GET", "/config")
            check(
                unchanged["max_file_size"] == baseline,
                "foreign/missing Origin changed persisted settings",
            )
        headers, _ = self.expect(
            200, "PATCH", "/admin/settings", {"max_file_size": 3 << 20}
        )
        check(
            headers.get("Cache-Control") == "no-store",
            "settings mutation lacks no-store",
        )
        _, config = self.expect(200, "GET", "/config")
        check(
            config["max_file_size"] == 3 << 20,
            "canonical settings save did not persist",
        )

    def streams_and_revocation(self) -> None:
        temporary = secrets.token_urlsafe(32)
        password = secrets.token_urlsafe(32)
        username = "recipient-" + secrets.token_hex(5)
        self.expect(
            201, "POST", "/admin/users", {"username": username, "password": temporary}
        )
        owner_cookie = self.login(username, temporary)
        self.expect(
            204,
            "POST",
            "/auth/password",
            {"current_password": temporary, "password": password},
            cookie=owner_cookie,
        )
        self.expect(401, "GET", "/auth/me", cookie=owner_cookie)
        owner_cookie = self.login(username, password)
        _, slot = self.expect(
            201,
            "POST",
            "/slots",
            {
                "receive_protocol": 2,
                "recipient_public_key": base64.urlsafe_b64encode(
                    secrets.token_bytes(32)
                )
                .decode()
                .rstrip("="),
            },
            cookie=owner_cookie,
        )
        self.slot_id = slot["id"]
        # Do not call read(): first frame must arrive while the SSE response is
        # still open, proving real flush behavior through both proxy layers.
        connection = http.client.HTTPSConnection(
            "localhost", self.port, context=self.context, timeout=5
        )
        try:
            connection.request(
                "GET",
                f"/api/v1/slots/{self.slot_id}/events",
                headers={"Cookie": owner_cookie},
            )
            response = connection.getresponse()
            check(
                response.status == 200
                and response.getheader("Content-Type", "").startswith(
                    "text/event-stream"
                ),
                "SSE response missing",
            )
            frame = b""
            while not frame.endswith(b"\n\n") and len(frame) < 4096:
                line = response.readline(4096)
                check(bool(line), "SSE completed before first event")
                frame += line
            check(
                b"event: connected\n" in frame and not response.isclosed(),
                "SSE first event was buffered until body completion",
            )
        finally:
            connection.close()
        _, availability = self.expect(
            200, "GET", f"/slots/{self.slot_id}/availability", cookie=""
        )
        check(
            availability["available"] is True,
            "test receive link was not publicly available before revocation",
        )
        status, _, _ = self.request(
            "POST", f"/admin/resources/slot/{self.slot_id}/revoke", {}
        )
        check(status in (200, 202), "administrator slot revocation failed")
        status, _, _ = self.request(
            "GET", f"/slots/{self.slot_id}/availability", cookie=""
        )
        check(status in (404, 410), "revoked slot remained publicly available")
        self.owner_cookie = owner_cookie

    def pause(self, value: bool) -> None:
        _, state = self.expect(
            200, "PATCH", "/admin/incident-state", {"public_transfers_paused": value}
        )
        check(
            state["public_transfers_paused"] is value,
            "incident state mutation mismatch",
        )
        _, config = self.expect(200, "GET", "/config")
        check(
            config["public_transfers_paused"] is value, "public incident state mismatch"
        )
        self.expect(
            503 if value else 201, "POST", "/slots", {}, cookie=self.owner_cookie
        )

    def persistence(self) -> None:
        self.pause(True)
        self.compose("restart", "backend")
        self.wait_ready()
        self.expect(200, "GET", "/auth/me")
        _, config = self.expect(200, "GET", "/config")
        check(
            config["public_transfers_paused"] is True
            and config["max_file_size"] == 3 << 20,
            "restart lost pause or settings",
        )
        self.expect(503, "POST", "/slots", {}, cookie=self.owner_cookie)

        # Stop the sole database writer before snapshotting. Preserve original,
        # copy it to a backup, and restore that backup into a third NEW volume.
        backend = self.inspect(self.container("backend"))
        original = next(
            mount["Name"]
            for mount in backend["Mounts"]
            if mount["Destination"] == "/app/data"
        )
        self.compose("stop", "backend")
        for suffix in ("backup", "restored"):
            volume = self.project + "-" + suffix
            self.volumes.append(volume)
            self.command(
                [
                    "docker",
                    "volume",
                    "create",
                    "--label",
                    f"com.docker.compose.project={self.project}",
                    volume,
                ]
            )
        for source, destination in (
            (original, self.volumes[0]),
            (self.volumes[0], self.volumes[1]),
        ):
            name = self.project + "-copy"
            self.helpers.append(name)
            self.command(
                [
                    "docker",
                    "run",
                    "--rm",
                    "--name",
                    name,
                    "--label",
                    f"com.docker.compose.project={self.project}",
                    "--network",
                    "none",
                    "--read-only",
                    "--cap-drop",
                    "ALL",
                    "--security-opt",
                    "no-new-privileges",
                    "--user",
                    "1000:1000",
                    "--entrypoint",
                    "/bin/sh",
                    "-v",
                    f"{source}:/source:ro",
                    "-v",
                    f"{destination}:/app/data",
                    self.images[0],
                    "-ec",
                    "cp -a /source/. /app/data/",
                ]
            )
        restore = self.temp / "restore.compose.yml"
        restore.write_text(
            f"volumes:\n  psst-data:\n    external: true\n    name: {self.volumes[1]}\n"
        )
        self.compose_files.append(restore)
        self.compose(
            "up", "-d", "--no-build", "--no-deps", "--force-recreate", "backend"
        )
        self.wait_ready()
        details = self.inspect(self.container("backend"))
        check(
            next(
                mount["Name"]
                for mount in details["Mounts"]
                if mount["Destination"] == "/app/data"
            )
            == self.volumes[1],
            "backend did not attach restored working copy",
        )
        self.expect(200, "GET", "/auth/me")
        _, config = self.expect(200, "GET", "/config")
        check(
            config["public_transfers_paused"] is True
            and config["max_file_size"] == 3 << 20,
            "offline restore lost policy/pause",
        )
        self.expect(503, "POST", "/slots", {}, cookie=self.owner_cookie)
        status, _, _ = self.request(
            "GET", f"/slots/{self.slot_id}/availability", cookie=""
        )
        check(status in (404, 410), "offline restore resurrected revoked slot")
        self.pause(False)
        self.compose("restart", "backend")
        self.wait_ready()
        _, config = self.expect(200, "GET", "/config")
        check(config["public_transfers_paused"] is False, "resume was not durable")
        saved = self.cookie
        self.expect(204, "POST", "/auth/logout")
        self.expect(401, "GET", "/auth/me", cookie=saved)
        self.compose("restart", "backend")
        self.wait_ready()
        self.expect(401, "GET", "/auth/me", cookie=saved)
        self.cookie = ""

    def load_internal_ca(self) -> None:
        destination = self.temp / "tls" / "ca.crt"
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            try:
                self.command(
                    [
                        "docker",
                        "cp",
                        self.container("external-proxy")
                        + ":/data/caddy/pki/authorities/local/root.crt",
                        str(destination),
                    ],
                    timeout=10,
                )
                destination.chmod(0o600)
                self.context = ssl.create_default_context(cafile=str(destination))
                return
            except (GateFailure, OSError, ssl.SSLError):
                time.sleep(0.5)
        raise GateFailure(
            "disposable gateway did not persist its internal CA within 60 seconds"
        )

    def leaf_fingerprint(self) -> str:
        with (
            socket.create_connection(("localhost", self.port), timeout=10) as peer,
            self.context.wrap_socket(peer, server_hostname="localhost") as tls,
        ):
            certificate = tls.getpeercert(binary_form=True)
            check(
                bool(certificate),
                "verified gateway did not provide a leaf certificate",
            )
            return hashlib.sha256(certificate).hexdigest()

    def gateway_manifest(self, volume: str) -> dict:
        # Report only digests/permissions of this stopped disposable state. Never
        # export private key bytes, config contents or certificates to stdout.
        script = """
import hashlib, json, os, pathlib, stat
root = pathlib.Path('/source')
manifest = {}
total = 0
for parent, directories, files in os.walk(root, followlinks=False):
    for name in directories:
        if (pathlib.Path(parent) / name).is_symlink():
            raise RuntimeError('unexpected storage symlink')
    for name in files:
        path = pathlib.Path(parent) / name
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > 4 * 1024 * 1024:
            raise RuntimeError('unexpected storage entry')
        total += info.st_size
        if len(manifest) >= 1000 or total > 16 * 1024 * 1024:
            raise RuntimeError('unexpected storage size')
        manifest[str(path.relative_to(root))] = {
            'sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
            'mode': stat.S_IMODE(info.st_mode),
            'uid': info.st_uid, 'gid': info.st_gid,
        }
print(json.dumps(manifest))
"""
        name = self.project + "-gateway-inventory"
        self.helpers.append(name)
        raw = self.command(
            [
                "docker",
                "run",
                "--rm",
                "--name",
                name,
                "--label",
                f"com.docker.compose.project={self.project}",
                "--network",
                "none",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--user",
                "10001:10001",
                "--pids-limit",
                "32",
                "--memory",
                "64m",
                "--cpus",
                "1",
                "--log-driver",
                "local",
                "--log-opt",
                "max-size=1m",
                "--log-opt",
                "max-file=2",
                "--mount",
                f"type=volume,source={volume},target=/source,readonly",
                "--entrypoint",
                "python3",
                self.client_image,
                "-c",
                script,
            ]
        )
        manifest = json.loads(raw)
        check(bool(manifest), "gateway state snapshot was empty")
        return manifest

    def copy_gateway_volume(self, source: str, destination: str) -> None:
        name = self.project + "-gateway-copy"
        self.helpers.append(name)
        self.command(
            [
                "docker",
                "run",
                "--rm",
                "--name",
                name,
                "--label",
                f"com.docker.compose.project={self.project}",
                "--network",
                "none",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--user",
                "10001:10001",
                "--pids-limit",
                "32",
                "--memory",
                "64m",
                "--cpus",
                "1",
                "--log-driver",
                "local",
                "--log-opt",
                "max-size=1m",
                "--log-opt",
                "max-file=2",
                "--mount",
                f"type=volume,source={source},target=/source,readonly",
                "--mount",
                f"type=volume,source={destination},target=/data",
                "--entrypoint",
                "/bin/sh",
                self.images[1],
                "-ec",
                "cp -a /source/. /data/",
            ]
        )

    def gateway_restore(self) -> None:
        leaf = self.leaf_fingerprint()
        details = self.inspect(self.container("external-proxy"))
        original_container = details["Id"]
        originals = {
            field: next(
                mount["Name"]
                for mount in details["Mounts"]
                if mount["Destination"] == path
            )
            for field, path in (("data", "/data"), ("config", "/config"))
        }
        # Stop the only gateway writer, keep its originals intact, snapshot them
        # into backups, then restore separate NEW writable volumes.
        self.compose("stop", "external-proxy")
        original_manifests = {}
        copies = {}
        for field, source in originals.items():
            original_manifests[field] = self.gateway_manifest(source)
            copies[field] = {}
            for stage in ("backup", "restored"):
                target = self.project + f"-gateway-{field}-{stage}"
                self.volumes.append(target)
                self.command(
                    [
                        "docker",
                        "volume",
                        "create",
                        "--label",
                        f"com.docker.compose.project={self.project}",
                        target,
                    ]
                )
                copies[field][stage] = target
            self.copy_gateway_volume(source, copies[field]["backup"])
            self.copy_gateway_volume(copies[field]["backup"], copies[field]["restored"])
            for stage in ("backup", "restored"):
                check(
                    self.gateway_manifest(copies[field][stage])
                    == original_manifests[field],
                    f"gateway {field} {stage} changed contents, ownership or permissions",
                )
        state = original_manifests["data"]
        for path in (
            "caddy/pki/authorities/local/root.key",
            "caddy/pki/authorities/local/intermediate.key",
        ):
            check(
                path in state and state[path]["mode"] & 0o077 == 0,
                "gateway CA key was absent or readable by other users",
            )
        check(
            any(path.endswith("/localhost.key") for path in state),
            "managed gateway leaf key was absent from persistent state",
        )
        check(
            all(
                entry["mode"] & 0o077 == 0
                and entry["uid"] == 10001
                and entry["gid"] == 10001
                for path, entry in state.items()
                if path.endswith(".key")
            ),
            "gateway private key permissions or ownership were unsafe",
        )
        override = self.temp / "gateway-restore.compose.yml"
        override.write_text(
            "volumes:\n"
            + "".join(
                f"  external-proxy-{field}:\n    external: true\n    name: {copies[field]['restored']}\n"
                for field in ("data", "config")
            )
        )
        self.compose_files.append(override)
        self.compose(
            "up", "-d", "--no-build", "--no-deps", "--force-recreate", "external-proxy"
        )
        # Keep the original client CA context. Loading a newly generated CA here
        # would hide lost certificate state and a trust-breaking restore.
        self.wait_ready()
        restored = self.inspect(self.container("external-proxy"))
        check(restored["Id"] != original_container, "gateway was not recreated")
        for field, path in (("data", "/data"), ("config", "/config")):
            check(
                next(
                    mount["Name"]
                    for mount in restored["Mounts"]
                    if mount["Destination"] == path
                )
                == copies[field]["restored"],
                f"gateway did not use restored {field} working volume",
            )
            check(
                self.gateway_manifest(originals[field]) == original_manifests[field],
                "gateway restore mutated its preserved original",
            )
            check(
                self.gateway_manifest(copies[field]["backup"])
                == original_manifests[field],
                "gateway restore mutated its preserved backup",
            )
        check(
            self.leaf_fingerprint() == leaf,
            "restore silently replaced the managed leaf certificate",
        )
        self.expect(200, "GET", "/auth/me")
        self.compose("restart", "external-proxy")
        self.wait_ready()
        check(
            self.leaf_fingerprint() == leaf,
            "restart after restore replaced its leaf certificate",
        )
        self.expect(200, "GET", "/auth/me")
        phase(
            "PASS gateway data/config backup-to-new-volume restore, private key permissions, original trust and authenticated restart"
        )

    def helper(self, index: int) -> str:
        name = self.project + f"-client-{index}"
        self.helpers.append(name)
        self.command(
            [
                "docker",
                "run",
                "-d",
                "--name",
                name,
                "--label",
                f"com.docker.compose.project={self.project}",
                "--network",
                self.project + "_proxy",
                "--ip",
                self.proxy + f".{index + 3}",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--user",
                "65534:65534",
                "--pids-limit",
                "32",
                "--memory",
                "64m",
                "--cpus",
                "0.5",
                "--log-driver",
                "local",
                "--log-opt",
                "max-size=1m",
                "--log-opt",
                "max-file=3",
                "-v",
                f"{self.temp / 'tls' / 'ca.crt'}:/test-ca/ca.crt:ro",
                "--entrypoint",
                "python3",
                self.client_image,
                "-c",
                "import time; time.sleep(3600)",
            ]
        )
        self.command(
            [
                "docker",
                "network",
                "connect",
                "--ip",
                self.private + f".{index + 3}",
                self.project + "_private",
                name,
            ]
        )
        check(
            self.command(
                [
                    "docker",
                    "exec",
                    name,
                    "python3",
                    "-c",
                    "import ssl; print('stdlib-ready')",
                ]
            ).strip()
            == "stdlib-ready",
            "helper Python/SSL unavailable",
        )
        return name

    def probe(self, helper: str, host: str, port: int, tls: bool, count: int) -> dict:
        job = {
            "host": host,
            "port": port,
            "tls": tls,
            "count": count,
            "origin": self.origin,
            "authority": f"localhost:{self.port}",
            "nonce": secrets.token_hex(8),
        }
        return json.loads(
            self.command(
                ["docker", "exec", "-i", helper, "python3", "-c", CLIENT_SCRIPT],
                data=json.dumps(job),
                timeout=30,
            )
        )

    def limiter(self) -> None:
        first, second = self.helper(1), self.helper(2)
        for path, host, port, tls in (
            ("external TLS", "external-proxy", 8443, True),
            ("untrusted inner HTTP", "caddy", 8080, False),
            ("untrusted backend HTTP", "backend", 8080, False),
        ):
            # Restart only this disposable backend to reset in-memory buckets.
            self.compose("restart", "backend")
            self.wait_ready()
            blocked = self.probe(first, host, port, tls, 12)
            check(
                blocked["elapsed"] < 5,
                f"{path} probe exceeded one token refill interval; cannot establish burst behavior",
            )
            check(
                blocked["statuses"] == [401] * 10 + [429] * 2,
                f"{path} changing spoofed forwarding headers escaped the ten-attempt IP bucket",
            )
            independent = self.probe(second, host, port, tls, 1)
            check(
                independent["statuses"] == [401],
                f"{path} collapsed unrelated real clients into one bucket",
            )
            phase(
                f"PASS {path}: spoofed IPs bounded; distinct peer remains independent"
            )

    def cleanup(self) -> bool:
        failures = []

        def remove(args: list[str]) -> None:
            try:
                self.command(args, timeout=60)
            except GateFailure:
                failures.append(args[1:3])

        # Query ONLY exact project labels. This also captures a partially
        # created stack if startup was interrupted before compose returned.
        try:
            ids = self.command(
                [
                    "docker",
                    "ps",
                    "-aq",
                    "--filter",
                    f"label=com.docker.compose.project={self.project}",
                ]
            ).split()
            if ids:
                remove(["docker", "rm", "-f", *ids])
            for kind in ("network", "volume"):
                ids = self.command(
                    [
                        "docker",
                        kind,
                        "ls",
                        "-q",
                        "--filter",
                        f"label=com.docker.compose.project={self.project}",
                    ]
                ).split()
                if ids:
                    remove(["docker", kind, "rm", *ids])
            existing = self.command(
                ["docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}"]
            ).split()
            owned = [
                name + ":latest" for name in self.images if name + ":latest" in existing
            ]
            if owned:
                remove(["docker", "image", "rm", *owned])
        except GateFailure:
            failures.append(["docker", "resource inventory"])
        shutil.rmtree(self.temp)
        if failures:
            phase(
                f"FAIL cleanup incomplete for disposable project {self.project}; inspect resources with that exact project label"
            )
        else:
            phase(
                "PASS cleanup: disposable project resources and private temporary files removed"
            )
        return not failures

    def run(self) -> None:
        phase(
            "Preparing disposable CA, credentials, isolated networks and loopback ports"
        )
        self.certificate()
        self.configure()
        self.compose("config", "--quiet")
        phase("Building current backend and compiled web/Caddy images once")
        self.compose("build", "backend", "caddy", timeout=1200)
        self.command(["docker", "pull", self.client_image], timeout=600)
        self.compose("up", "-d", "--no-build")
        if self.certificate_state:
            self.load_internal_ca()
        self.wait_ready()
        self.check_hardening()
        phase(
            "PASS verified TLS, loopback ingress, unpublished inner/backend and container hardening"
        )
        self.auth_and_origin()
        phase(
            "PASS canonical Origin, foreign/missing/spoofed Origin denial, cookie flags, no-store and settings"
        )
        if self.certificate_state:
            self.gateway_restore()
            return
        self.streams_and_revocation()
        phase(
            "PASS regular account password replacement, live SSE first event and public slot revocation"
        )
        self.persistence()
        phase(
            "PASS pause/restart/resume/logout persistence and offline backup-to-new-volume restore"
        )
        self.limiter()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--client-image",
        default="python:3.13-alpine",
        help="Docker image with python3 and Python SSL stdlib (default: %(default)s)",
    )
    parser.add_argument(
        "--certificate-state",
        action="store_true",
        help="test managed internal-CA gateway data/config backup and restore instead of the static-certificate proxy flow",
    )
    arguments = parser.parse_args()

    def interrupted(signum: int, frame: object) -> None:
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    harness = Harness(arguments.client_image, arguments.certificate_state)
    success = False
    try:
        harness.run()
        success = True
    except GateFailure as error:
        phase(f"FAIL {error}")
    except KeyboardInterrupt:
        phase("FAIL interrupted; cleaning disposable resources")
    except Exception as error:
        # Exception strings can contain cookies or payloads. Report type only.
        phase(f"FAIL unexpected {type(error).__name__}; details withheld")
    finally:
        success = harness.cleanup() and success
    if success:
        phase(
            "PASS external-proxy integration gate (test CA; public ACME/native flows untested)"
        )
    return 0 if success else 1


if __name__ == "__main__":
    sys.exit(main())

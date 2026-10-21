#!/usr/bin/env python3
"""Bounded CLI/backend/production-TypeScript interoperability on disposable data.

Run from any directory:
  python3 client/scripts/check-integration.py --binary /absolute/psst \
      [--backend-binary /absolute/server]

Requires installed web npm dependencies and a Node release supporting TypeScript
stripping. An omitted backend is built once. The 60-second deadline includes that
build; CI should supply its existing backend binary to avoid duplicate builds.
This checks real API/auth/cryptography, not browser rendering. A loopback gateway
serves the production HTML template for the SDK's two web-identity probes.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import http.client
import http.cookiejar
import json
import os
import re
import secrets
import signal
import socket
import sqlite3
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class Failure(Exception):
    """Only fixed, non-secret fixture descriptions may be displayed."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise Failure(message)


class Deadline:
    def __init__(self) -> None:
        self.end = time.monotonic() + 60

    def remaining(self, maximum: float = 60) -> float:
        seconds = min(maximum, self.end - time.monotonic())
        require(seconds > 0, "Integration exceeded its 60-second deadline")
        return seconds


def stop(process: subprocess.Popen) -> None:
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()


def run(
    args: list[str], deadline: Deadline, *, data: bytes = b"", **kwargs
) -> subprocess.CompletedProcess:
    process = subprocess.Popen(
        args,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
        **kwargs,
    )
    try:
        out, _ = process.communicate(data, timeout=deadline.remaining())
        return subprocess.CompletedProcess(args, process.returncode, out)
    except subprocess.TimeoutExpired:
        raise Failure("Integration child exceeded the 60-second deadline") from None
    finally:
        stop(process)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        return None


class API:
    def __init__(self, origin: str, deadline: Deadline):
        self.origin, self.deadline = origin, deadline
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(self.cookies),
            NoRedirect(),
        )

    def request(
        self,
        method: str,
        path: str,
        body: dict | None = None,
        *,
        token: str | None = None,
        status: int = 200,
    ) -> dict:
        headers = {"Origin": self.origin}
        if token:
            headers["Authorization"] = "Bearer " + token
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(
            self.origin + "/api/v1" + path, data=data, method=method, headers=headers
        )
        try:
            response = self.opener.open(request, timeout=self.deadline.remaining(5))
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read(2 * 1024 * 1024)
            safe_path = re.sub(r"[0-9a-fA-F-]{36}", "RESOURCE", path)
            require(
                response.status == status,
                f"Disposable API {method} {safe_path} returned {response.status}, expected {status}",
            )
            return json.loads(raw) if raw else {}


class Gateway(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, port: int, deadline: Deadline):
        self.backend_port, self.deadline = port, deadline
        self.receipt_failure: str | None = None
        self.gets: dict[str, int] = {}
        self.guard = threading.Lock()
        super().__init__(("127.0.0.1", 0), GatewayRequest)


class GatewayRequest(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def respond(self, status: int, data: bytes, headers=()) -> None:
        self.send_response(status)
        for name, value in headers:
            if name.lower() not in {
                "connection",
                "transfer-encoding",
                "content-length",
                "server",
                "date",
            }:
                self.send_header(name, value)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def proxy(self) -> None:
        fixture: Gateway = self.server
        if self.command == "GET" and self.path in {
            "/d/_connection_check",
            "/u/_connection_check",
        }:
            self.respond(
                200,
                (ROOT / "web/src/app.html").read_bytes(),
                [("Content-Type", "text/html")],
            )
            return
        if not self.path.startswith("/api/v1/"):
            self.respond(404, b"")
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        with fixture.guard:
            if self.command == "GET" and "/files/" in self.path:
                fixture.gets[self.path] = fixture.gets.get(self.path, 0) + 1
            if self.command == "POST" and self.path == fixture.receipt_failure:
                fixture.receipt_failure = None
                self.respond(
                    503,
                    b'{"code":"fixture_receipt_retry","error":"disposable receipt interruption"}',
                    [
                        ("Content-Type", "application/json"),
                        ("X-Psst-Error-Code", "fixture_receipt_retry"),
                    ],
                )
                return
        headers = {
            name: value
            for name, value in self.headers.items()
            if name.lower() not in {"connection", "transfer-encoding"}
        }
        connection = http.client.HTTPConnection(
            "127.0.0.1", fixture.backend_port, timeout=fixture.deadline.remaining(5)
        )
        try:
            connection.request(self.command, self.path, body, headers)
            response = connection.getresponse()
            self.respond(response.status, response.read(), response.getheaders())
        except (ConnectionError, TimeoutError, http.client.HTTPException):
            self.respond(502, b"{}", [("Content-Type", "application/json")])
        finally:
            connection.close()

    do_GET = do_POST = do_PATCH = do_DELETE = do_HEAD = proxy


def bootstrap(api: API) -> tuple[str, str]:
    # ADMIN_PASSWORD is generated by main and supplied only to this child.
    admin_password = api.admin_password
    api.request(
        "POST",
        "/auth/login",
        {
            "username": "integration-admin",
            "password": admin_password,
            "session_type": "web",
        },
    )
    enrollment = api.request("POST", "/admin/security/enrollment", {}, status=201)
    secret = base64.b32decode(
        enrollment["secret"].upper() + "=" * (-len(enrollment["secret"]) % 8)
    )
    digest = hmac.digest(secret, struct.pack(">Q", int(time.time()) // 30), "sha1")
    offset = digest[-1] & 15
    code = str(
        (int.from_bytes(digest[offset : offset + 4], "big") & 0x7FFFFFFF) % 1000000
    ).zfill(6)
    confirmed = api.request(
        "POST", "/admin/security/enrollment/confirm", {"code": code}
    )
    api.request(
        "POST",
        "/auth/login",
        {
            "username": "integration-admin",
            "password": admin_password,
            "session_type": "web",
            "recovery_code": confirmed["recovery_codes"][0],
        },
    )
    temporary = "Temporary-" + secrets.token_urlsafe(24)
    password = "Member-" + secrets.token_urlsafe(24)
    api.request(
        "POST",
        "/admin/users",
        {"username": "integration-member", "password": temporary, "role": "user"},
        status=201,
    )
    first = api.request(
        "POST",
        "/auth/login",
        {
            "username": "integration-member",
            "password": temporary,
            "session_type": "device",
        },
    )
    api.request(
        "POST",
        "/auth/password",
        {"current_password": temporary, "password": password},
        token=first["token"],
        status=204,
    )
    session = api.request(
        "POST",
        "/auth/login",
        {
            "username": "integration-member",
            "password": password,
            "session_type": "device",
        },
    )
    return password, session["token"]


class CLI:
    def __init__(self, binary: Path, directory: Path, origin: str, deadline: Deadline):
        self.binary, self.directory, self.origin, self.deadline = (
            binary,
            directory,
            origin,
            deadline,
        )
        self.passphrase = secrets.token_urlsafe(32).encode()
        self.environment = {**os.environ, "XDG_CACHE_HOME": str(directory / "cache")}

    def call(
        self,
        args: list[str],
        *,
        data: str = "",
        expected: int = 0,
        backup_passphrase: bytes | None = None,
    ) -> dict:
        read_fd, write_fd = os.pipe()
        backup_fd = -1
        try:
            os.write(write_fd, self.passphrase)
            os.close(write_fd)
            write_fd = -1
            descriptors = [read_fd]
            if backup_passphrase is not None:
                backup_fd, backup_write = os.pipe()
                try:
                    os.write(backup_write, backup_passphrase)
                finally:
                    os.close(backup_write)
                descriptors.append(backup_fd)
                args = [*args, "--backup-passphrase-fd", str(backup_fd)]
            result = run(
                [
                    str(self.binary),
                    "--json",
                    "--non-interactive",
                    "--timeout",
                    "10s",
                    "--state-dir",
                    str(self.directory / "vault"),
                    "--vault-passphrase-fd",
                    str(read_fd),
                    *args,
                ],
                self.deadline,
                data=data.encode(),
                env=self.environment,
                pass_fds=tuple(descriptors),
            )
        finally:
            os.close(read_fd)
            if write_fd >= 0:
                os.close(write_fd)
            if backup_fd >= 0:
                os.close(backup_fd)
        require(
            result.returncode == expected,
            f"CLI {args[0]} returned status {result.returncode}, expected {expected}",
        )
        decoded = json.loads(result.stdout)
        require(
            decoded.get("schema_version") == 1 and decoded.get("ok") == (expected == 0),
            "CLI JSON result schema or success state was invalid",
        )
        return decoded


def peer(action: str, origin: str, deadline: Deadline, **data):
    result = run(
        [
            "node",
            "--experimental-strip-types",
            str(ROOT / "client/scripts/integration-crypto.mjs"),
            action,
        ],
        deadline,
        data=json.dumps({"origin": origin, **data}).encode(),
        cwd=ROOT,
    )
    require(
        result.returncode == 0,
        f"Production TypeScript crypto interoperability failed during {action}",
    )
    return json.loads(result.stdout)


def compare_download(directory: Path, files: list[dict]) -> None:
    actual = {path.name: path for path in directory.iterdir() if path.is_file()}
    require(
        set(actual) == {file["name"] for file in files},
        "CLI published unexpected files or partial outputs",
    )
    for file in files:
        require(
            hashlib.sha256(actual[file["name"]].read_bytes()).digest()
            == hashlib.sha256(Path(file["path"]).read_bytes()).digest(),
            "CLI authenticated download did not match the source",
        )


def exercise(binary: Path, backend: Path, directory: Path, deadline: Deadline) -> None:
    with socket.socket() as selected:
        selected.bind(("127.0.0.1", 0))
        backend_port = selected.getsockname()[1]
    gateway = Gateway(backend_port, deadline)
    origin = f"http://127.0.0.1:{gateway.server_port}"
    thread = threading.Thread(
        target=gateway.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True
    )
    thread.start()
    admin_password = "Admin-" + secrets.token_urlsafe(24)
    environment = {
        "PATH": os.environ.get("PATH", ""),
        "LANG": "C.UTF-8",
        "ADMIN_USERNAME": "integration-admin",
        "ADMIN_PASSWORD": admin_password,
        "DB_PATH": str(directory / "server.db"),
        "STORAGE_PATH": str(directory / "files"),
        "LISTEN_ADDR": f"127.0.0.1:{backend_port}",
        "PUBLIC_URL": origin,
        "AUTH_ALLOW_INSECURE_HTTP": "true",
        # Shared loopback functional fixtures use documented operator limits;
        # authentication/factor/recent-auth/ownership policy remains enabled.
        "RATE_LIMIT_GLOBAL": "200",
        "RATE_LIMIT_BURST": "400",
        "RATE_LIMIT_CREATION": "20",
        "RATE_LIMIT_CREATION_BURST": "50",
    }
    process = None
    try:
        process = subprocess.Popen(
            [str(backend)],
            env=environment,
            cwd=directory,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        api = API(origin, deadline)
        api.admin_password = admin_password
        while True:
            require(
                process.poll() is None, "Disposable backend exited before readiness"
            )
            try:
                if api.request("GET", "/health").get("service") == "psst.zip":
                    break
            except (Failure, urllib.error.URLError, TimeoutError):
                deadline.remaining()
                time.sleep(0.02)
        password, token = bootstrap(api)
        cli = CLI(binary, directory, origin, deadline)
        cli.call(
            [
                "auth",
                "login",
                "--server",
                origin,
                "--username",
                "integration-member",
                "--password-stdin",
            ],
            data=password + "\n",
        )
        large = directory / "boundary-ä.bin"
        large.write_bytes(bytes(range(256)) * 16384 + b"second authenticated frame")
        empty = directory / "empty.txt"
        empty.write_bytes(b"")
        files = [{"name": path.name, "path": str(path)} for path in (large, empty)]

        sent = cli.call(["send", str(large), str(empty), "--max-downloads", "1"])[
            "data"
        ]
        verified = peer(
            "send-decrypt", origin, deadline, link=sent["link"], files=files
        )
        require(len(verified) == 2, "TypeScript did not authenticate both CLI files")
        exhausted = api.request(
            "GET", f"/transfers/{sent['id']}/files/{verified[0]['file_id']}", status=410
        )
        require(
            exhausted.get("code") == "download_limit",
            "Download allowance did not stay exhausted",
        )

        browser = peer("send-create", origin, deadline, token=token, files=files)
        gateway.receipt_failure = f"/api/v1/transfers/{browser['id']}/downloaded"
        destination = directory / "download"
        failed = cli.call(
            ["fetch", "--link-stdin", "--output-dir", str(destination)],
            data=browser["link"] + "\n",
            expected=5,
        )
        require(
            failed["error"]["code"] == "receipt_pending",
            "Interrupted acknowledgement did not preserve pending receipt",
        )
        compare_download(destination, files)
        before = dict(gateway.gets)
        retried = cli.call(
            ["fetch", "--link-stdin", "--output-dir", str(destination)],
            data=browser["link"] + "\n",
        )
        require(
            retried["data"].get("receipt_pending") is False and before == gateway.gets,
            "Receipt retry redownloaded files or remained pending",
        )
        compare_download(destination, files)

        receive = cli.call(["receive", "create", "--max-files", "1"])["data"]
        submission = peer(
            "receive-upload", origin, deadline, link=receive["link"], files=files[:1]
        )
        received = directory / "received"
        backup = directory / "receive-backup.age"
        backup_phrase = secrets.token_urlsafe(32).encode()
        cli.call(
            ["vault", "backup", "--output", str(backup)],
            backup_passphrase=backup_phrase,
        )
        require(
            backup.stat().st_mode & 0o077 == 0,
            "Encrypted CLI backup has unsafe permissions",
        )
        recovered = CLI(binary, directory / "recovered", origin, deadline)
        recovered.call(
            ["vault", "restore", "--input", str(backup)],
            backup_passphrase=backup_phrase,
        )
        recovered.call(
            ["receive", "fetch", receive["id"], "--output-dir", str(received)]
        )
        compare_download(received / submission["id"], files[:1])
        exhausted = api.request(
            "POST", f"/slots/{receive['id']}/transfers", {}, status=403
        )
        require(
            exhausted.get("code") == "receive_file_limit",
            "Receive allocation allowance was replenished",
        )

        browser_receive = peer("receive-create", origin, deadline, token=token)
        cli.call(
            ["upload", "--link-stdin", str(large)], data=browser_receive["link"] + "\n"
        )
        verified = peer(
            "receive-decrypt",
            origin,
            deadline,
            token=token,
            slot=browser_receive,
            files=files[:1],
        )
        require(
            len(verified) == 1,
            "TypeScript owner could not authenticate CLI guest upload",
        )

        cli.call(["receive", "revoke", receive["id"]])
        revoked = api.request(
            "POST", f"/slots/{receive['id']}/transfers", {}, status=404
        )
        require(
            revoked.get("code") == "resource_not_found",
            "Revoked Receive invitation accepted new uploads",
        )
        # Age only this disposable fixture, without sleeping through retention.
        with sqlite3.connect(directory / "server.db", timeout=1) as database:
            database.execute(
                "UPDATE slots SET expires_at='2000-01-01' WHERE id=?",
                (browser_receive["id"],),
            )
        expired = api.request(
            "POST", f"/slots/{browser_receive['id']}/transfers", {}, status=410
        )
        require(
            expired.get("code") == "link_expired",
            "Expired Receive invitation accepted new uploads",
        )
    finally:
        if process is not None:
            stop(process)
        gateway.shutdown()
        gateway.server_close()
        thread.join(timeout=1)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--backend-binary", type=Path)
    args = parser.parse_args()
    require(
        args.binary.is_absolute() and args.binary.is_file(),
        "Supply an absolute built CLI binary",
    )
    require(
        (ROOT / "web/node_modules").is_dir(),
        "Install web dependencies before integration",
    )
    deadline = Deadline()
    with tempfile.TemporaryDirectory(prefix="psst-cli-integration-") as temporary:
        directory = Path(temporary)
        backend = args.backend_binary
        if backend is None:
            backend = directory / "server"
            built = run(
                ["go", "build", "-o", str(backend), "./cmd/server"],
                deadline,
                cwd=ROOT / "backend",
            )
            require(built.returncode == 0, "Disposable backend build failed")
        require(
            backend.is_absolute() and backend.is_file(),
            "Supply an absolute built backend binary",
        )
        exercise(args.binary, backend, directory, deadline)
    print(
        f"CLI integration passed in {60 - deadline.remaining():.1f}s: "
        "bidirectional Send/Receive web crypto, multiframe/empty files, "
        "encrypted Receive-key recovery, receipt retry, expiry/revocation/allowances."
    )


if __name__ == "__main__":
    try:
        main()
    except Failure as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, json.JSONDecodeError, urllib.error.URLError):
        print(
            "Disposable CLI integration failed; private child output withheld.",
            file=sys.stderr,
        )
        sys.exit(1)

#!/usr/bin/env python3
"""Request a version-only update through the installed restricted SSH helper.

No release code is downloaded or executed on the Actions runner. The root-owned
host helper authenticates the ready manifest, image pair and bundle before use.
"""

from __future__ import annotations

import base64
import ipaddress
import json
import os
from pathlib import Path
import re
import selectors
import struct
import subprocess
import tempfile
import time

VERSION = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")
KEY_TYPES = {
    "ssh-ed25519",
    "ssh-rsa",
    "ecdsa-sha2-nistp256",
    "ecdsa-sha2-nistp384",
    "ecdsa-sha2-nistp521",
}
MAX_OUTPUT = 16384
TIMEOUT = 2400
PROCESS_ENV = {"PATH": "/usr/bin:/bin", "LANG": "C"}


class RequestError(Exception):
    """A bounded diagnostic containing no private input or remote output."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RequestError(message)


def host_name(value: str) -> str:
    require(isinstance(value, str) and 0 < len(value) <= 253, "Configure VPS_HOST")
    try:
        ipaddress.ip_address(value)
        require("%" not in value, "VPS_HOST must not contain an interface scope")
        return value
    except ValueError:
        pass
    require(
        all(
            re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", part)
            for part in value.split(".")
        ),
        "VPS_HOST must be a literal IP or DNS hostname without a port",
    )
    return value


def pinned_hosts(value: str, host: str) -> str:
    require(
        isinstance(value, str) and 0 < len(value) <= MAX_OUTPUT,
        "Configure VPS_KNOWN_HOSTS",
    )
    lines = value.splitlines()
    require(0 < len(lines) <= 8, "Invalid pinned host key records")
    for line in lines:
        fields = line.split()
        require(
            len(fields) == 3 and fields[0] == host and fields[1] in KEY_TYPES,
            "Pinned keys must name the exact VPS_HOST and a supported key type",
        )
        try:
            key = base64.b64decode(fields[2], validate=True)
            size = struct.unpack("!I", key[:4])[0]
            embedded = key[4 : 4 + size].decode("ascii")
        except (ValueError, UnicodeError, struct.error):
            raise RequestError("Invalid pinned host key encoding") from None
        require(
            embedded == fields[1] and 16 <= len(key) <= 8192 and len(key) > 4 + size,
            "Invalid pinned host key data",
        )
    return "\n".join(lines) + "\n"


def ssh_arguments(host: str, key: Path, known_hosts: Path, command: str) -> list[str]:
    return [
        "/usr/bin/ssh",
        "-F",
        "/dev/null",
        "-T",
        "-p",
        "22",
        "-o",
        "BatchMode=yes",
        "-o",
        "IdentitiesOnly=yes",
        "-o",
        "IdentityAgent=none",
        "-o",
        "StrictHostKeyChecking=yes",
        "-o",
        f"UserKnownHostsFile={known_hosts}",
        "-o",
        "GlobalKnownHostsFile=/dev/null",
        "-o",
        "UpdateHostKeys=no",
        "-o",
        "ClearAllForwardings=yes",
        "-o",
        "PermitLocalCommand=no",
        "-o",
        "ConnectionAttempts=1",
        "-o",
        "ConnectTimeout=15",
        "-o",
        "ServerAliveInterval=15",
        "-o",
        "ServerAliveCountMax=3",
        "-i",
        str(key),
        f"psst-deploy@{host}",
        command,
    ]


def bounded_ssh(args: list[str]) -> tuple[int, bytes]:
    # Drain both pipes with a combined bound; never echo stderr or raw stdout.
    process = subprocess.Popen(
        args,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=PROCESS_ENV,
    )
    output = bytearray()
    count = 0
    deadline = time.monotonic() + TIMEOUT
    try:
        with selectors.DefaultSelector() as poll:
            poll.register(process.stdout, selectors.EVENT_READ, True)
            poll.register(process.stderr, selectors.EVENT_READ, False)
            while poll.get_map():
                remaining = deadline - time.monotonic()
                require(
                    remaining > 0,
                    "SSH request timed out; inspect durable host status before retrying",
                )
                for selected, _ in poll.select(min(remaining, 1)):
                    data = os.read(selected.fileobj.fileno(), 4096)
                    if not data:
                        poll.unregister(selected.fileobj)
                        continue
                    count += len(data)
                    require(
                        count <= MAX_OUTPUT,
                        "SSH output exceeded its bound; inspect host status locally",
                    )
                    if selected.data:
                        output.extend(data)
            return process.wait(timeout=max(0.01, deadline - time.monotonic())), bytes(
                output
            )
    except subprocess.TimeoutExpired:
        raise RequestError(
            "SSH request timed out; inspect durable host status before retrying"
        ) from None
    finally:
        if process.poll() is None:
            process.kill()
        process.wait()
        process.stdout.close()
        process.stderr.close()


def completed_status(output: bytes, version: str) -> None:
    try:
        record = json.loads(output)
    except (ValueError, UnicodeError):
        raise RequestError(
            "Invalid updater response; inspect host status locally"
        ) from None
    require(
        isinstance(record, dict)
        and record.get("version") == version
        and record.get("active_version") == version
        and record.get("phase") == "completed",
        "Updater did not confirm the requested release active; inspect host status locally",
    )


def request(environment: dict[str, str]) -> None:
    version = environment.get("PSST_RELEASE_VERSION", "")
    require(
        len(version) <= 64 and VERSION.fullmatch(version) is not None,
        "Select vMAJOR.MINOR.PATCH",
    )
    host = host_name(environment.get("PSST_VPS_HOST", ""))
    known = pinned_hosts(environment.get("PSST_KNOWN_HOSTS", ""), host)
    key = environment.get("PSST_DEPLOY_KEY", "")
    # Check marker tokens without embedding a private-key block in source. The
    # real key parser below validates the encoding and cryptographic contents.
    markers = key.splitlines()
    require(
        0 < len(key) <= 65536
        and bool(markers)
        and markers[0].split() == ["-----BEGIN", "OPENSSH", "PRIVATE", "KEY-----"]
        and markers[-1].split() == ["-----END", "OPENSSH", "PRIVATE", "KEY-----"],
        "Configure the separate deployment OpenSSH private key",
    )
    with tempfile.TemporaryDirectory(prefix="psst-deploy-ssh-") as folder:
        root = Path(folder)
        root.chmod(0o700)
        private, hosts = root / "identity", root / "known_hosts"
        for file, contents in ((private, key.rstrip() + "\n"), (hosts, known)):
            fd = os.open(file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as target:
                target.write(contents)
        parsed = subprocess.run(
            ["/usr/bin/ssh-keygen", "-y", "-P", "", "-f", str(private)],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=10,
            check=False,
            env=PROCESS_ENV,
        )
        require(
            parsed.returncode == 0,
            "Deployment key must parse without an interactive passphrase",
        )
        code, output = bounded_ssh(
            ssh_arguments(host, private, hosts, "update " + version)
        )
        if code == 20:
            raise RequestError(
                "Candidate awaits local administrator verification; production success is not confirmed"
            )
        require(
            code == 0,
            "SSH update failed; inspect durable host status using the maintenance identity",
        )
        completed_status(output, version)
    print("Verified updater completed " + version)


if __name__ == "__main__":
    try:
        request(os.environ)
    except (RequestError, OSError, subprocess.SubprocessError) as error:
        print(
            str(error)
            if isinstance(error, RequestError)
            else "Deployment request failed; inspect host status locally"
        )
        raise SystemExit(1) from None

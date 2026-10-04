#!/usr/bin/env python3
"""Run real filesystem-pressure checks on disposable, capped Docker tmpfs mounts.

Requires Linux Go with race support and Docker. No live instance, host disk,
repository .env, published ports, external network or privileged container is used.
"""

from __future__ import annotations

import argparse
import os
import secrets
import shutil
import subprocess
import tempfile
from pathlib import Path


def run(args: list[str], *, cwd: Path, env: dict[str, str], timeout: int) -> None:
    subprocess.run(args, cwd=cwd, env=env, timeout=timeout, check=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--runtime-image",
        default="debian:bookworm-slim",
        help="glibc Linux image able to run the host-compiled Go race binary",
    )
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    name = "psst-pressure-gate-" + secrets.token_hex(6)
    temporary = Path(tempfile.mkdtemp(prefix=name + "-"))
    temporary.chmod(0o700)
    env = os.environ.copy()
    env["GOCACHE"] = str(temporary / "go-cache")
    env["GOTMPDIR"] = str(temporary / "go-tmp")
    env["DOCKER_CONFIG"] = str(temporary / "docker-config")
    for key in ("GOTMPDIR", "DOCKER_CONFIG"):
        Path(env[key]).mkdir(mode=0o700)
    binary = temporary / "api.test"
    try:
        print(
            "Compile current backend's physical-pressure tests with race support.",
            flush=True,
        )
        run(
            ["go", "test", "-race", "-c", "-o", str(binary), "./internal/api"],
            cwd=root / "backend",
            env=env,
            timeout=600,
        )
        print(
            "Run three repetitions on isolated 64 MiB payload/shared storage.",
            flush=True,
        )
        run(
            [
                "docker",
                "run",
                "--rm",
                "--name",
                name,
                "--network",
                "none",
                "--read-only",
                "--user",
                "10001:10001",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges:true",
                "--pids-limit",
                "128",
                "--memory",
                "512m",
                "--cpus",
                "2",
                "--log-driver",
                "local",
                "--log-opt",
                "max-size=1m",
                "--log-opt",
                "max-file=2",
                "--tmpfs",
                "/pressure:rw,noexec,nosuid,nodev,size=64m,uid=10001,gid=10001,mode=0700",
                "--tmpfs",
                "/tmp:rw,noexec,nosuid,nodev,size=64m,uid=10001,gid=10001,mode=0700",
                "--mount",
                f"type=bind,source={binary},target=/api.test,readonly",
                "--env",
                "PSST_STORAGE_PRESSURE_ROOT=/pressure",
                "--entrypoint",
                "/api.test",
                args.runtime_image,
                "-test.run",
                "^TestIsolatedPhysicalStoragePressure$",
                "-test.count=3",
                "-test.timeout=90s",
                "-test.v",
            ],
            cwd=root,
            env=env,
            timeout=120,
        )
        print("Physical reserve/ENOSPC and cleanup checks passed.", flush=True)
    finally:
        # Also catches Ctrl-C/timeouts: remove only this uniquely named container.
        try:
            subprocess.run(
                ["docker", "rm", "-f", name],
                cwd=root,
                env=env,
                timeout=30,
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        finally:
            shutil.rmtree(temporary)


if __name__ == "__main__":
    main()

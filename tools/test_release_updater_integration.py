#!/usr/bin/env python3
"""Real disposable root/Docker updater gate, with explicitly local acquisition.

Requires Docker with privileged nested-container support. No host socket, host
root bind, published outer port, real account, registry publication, or VPS is
used. Builds committed source with shared BuildKit cache, then destroys only its
own images/container/temp directory. The local acquisition fixture does not
validate public release attestations or multiarchitecture registry indexes.
"""

from __future__ import annotations

import argparse
import json
import secrets
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def run(*args: str, data: bytes | None = None, timeout: int = 900) -> bytes:
    result = subprocess.run(args, input=data, capture_output=True, timeout=timeout)
    if result.returncode:
        # Commands contain fixture data only; response bodies and config are withheld.
        detail = result.stderr.decode(errors="replace").splitlines()[-18:]
        if args[:2] == ("docker", "exec"):
            detail += result.stdout.decode(errors="replace").splitlines()[-8:]
        raise RuntimeError(
            f"{args[0]} {args[1]} failed: exit {result.returncode}; {detail}"
        )
    return result.stdout


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source", default="HEAD", help="Committed candidate source to build"
    )
    parser.add_argument(
        "--previous-source",
        default="4210414",
        help="Committed prior application source",
    )
    parser.add_argument(
        "--failure-after-start",
        action="store_true",
        help="Inject a fault only after all real candidate startup checks pass",
    )
    parser.add_argument(
        "--require-schema-change",
        action="store_true",
        help="Require actual historical migration-count advancement",
    )
    parser.add_argument("--backend-image")
    parser.add_argument("--web-image")
    options = parser.parse_args()
    if bool(options.backend_image) != bool(options.web_image):
        parser.error("supply both existing fixture image names, or neither")
    identity = "psst-update-gate-" + secrets.token_hex(6)
    temp = Path(tempfile.mkdtemp(prefix=identity + "-"))
    owned_images = [identity + "-daemon"]
    started = time.monotonic()
    try:
        revision = (
            run("git", "-C", str(ROOT), "rev-parse", options.source).decode().strip()
        )
        source = temp / "source"
        source.mkdir()
        archive = run("git", "-C", str(ROOT), "archive", revision)
        run("tar", "-xf", "-", "-C", str(source), data=archive)
        versions = {}
        print(
            "Building prior/candidate/next release image pairs from committed source",
            flush=True,
        )
        for version, selected_revision in (
            (
                "v1.2.2",
                run("git", "-C", str(ROOT), "rev-parse", options.previous_source)
                .decode()
                .strip(),
            ),
            ("v1.2.3", revision),
            ("v1.2.4", revision),
        ):
            context_root = temp / version
            context_root.mkdir()
            run(
                "tar",
                "-xf",
                "-",
                "-C",
                str(context_root),
                data=run("git", "-C", str(ROOT), "archive", selected_revision),
            )
            pair = {"commit": selected_revision}
            for component in ("backend", "web"):
                image = identity + "-" + component + ":" + version
                if version == "v1.2.3" and options.backend_image:
                    image = (
                        options.backend_image
                        if component == "backend"
                        else options.web_image
                    )
                else:
                    owned_images.append(image)
                    run(
                        "docker",
                        "build",
                        "-t",
                        image,
                        "-f",
                        str(context_root / component / "Dockerfile"),
                        "--build-arg",
                        "VERSION=" + version,
                        "--build-arg",
                        "REVISION=" + selected_revision,
                        str(
                            context_root / "backend"
                            if component == "backend"
                            else context_root
                        ),
                        timeout=1800,
                    )
                pair[component] = image
            versions[version] = pair
        print(
            "Starting isolated nested daemon (no host socket or external ports)",
            flush=True,
        )
        run(
            "docker",
            "build",
            "-t",
            owned_images[0],
            str(ROOT / "tools/fixtures/release-updater"),
        )
        run(
            "docker",
            "run",
            "-d",
            "--name",
            identity,
            "--privileged",
            "--network",
            "none",
            "--env",
            "DOCKER_TLS_CERTDIR=",
            owned_images[0],
            "--storage-driver",
            "vfs",
        )
        for _ in range(60):
            try:
                run("docker", "exec", identity, "docker", "info", timeout=10)
                break
            except RuntimeError:
                time.sleep(1)
        else:
            raise RuntimeError("isolated Docker daemon did not start")
        images = run(
            "docker",
            "image",
            "save",
            *[
                pair[component]
                for pair in versions.values()
                for component in ("backend", "web")
            ],
        )
        run("docker", "exec", "-i", identity, "docker", "image", "load", data=images)
        del images
        run(
            "docker",
            "exec",
            identity,
            "mkdir",
            "-p",
            "/usr/local/lib/psst.zip/deploy",
            "/usr/local/lib/psst.zip/tools",
            "/opt/fixture",
        )
        for local, remote in (
            (ROOT / "deploy/update.py", "/usr/local/lib/psst.zip/deploy/update.py"),
            (
                ROOT / "tools/release_artifacts.py",
                "/usr/local/lib/psst.zip/tools/release_artifacts.py",
            ),
            (
                ROOT / "tools/fixtures/release-updater/controller.py",
                "/opt/fixture/controller.py",
            ),
            (
                ROOT / "tools/fixtures/release-updater/checkpoint.py",
                "/opt/fixture/checkpoint.py",
            ),
        ):
            run("docker", "cp", str(local), identity + ":" + remote)
        for name in ("flows.py", "verify.py"):
            run(
                "docker",
                "cp",
                str(ROOT / "tools/fixtures/release-updater" / name),
                identity + ":/opt/fixture/" + name,
            )
        run(
            "docker",
            "cp",
            str(source / "deploy"),
            identity + ":/opt/fixture/bundle-deploy",
        )
        run(
            "docker",
            "exec",
            identity,
            "chown",
            "-R",
            "0:0",
            "/usr/local/lib/psst.zip",
            "/opt/fixture",
        )
        run(
            "docker",
            "exec",
            identity,
            "chmod",
            "755",
            "/usr/local",
            "/usr/local/lib",
            "/opt",
            "/opt/fixture",
            "/opt/fixture/checkpoint.py",
            "/opt/fixture/verify.py",
        )
        run(
            "docker",
            "exec",
            identity,
            "chmod",
            "644",
            "/usr/local/lib/psst.zip/deploy/update.py",
            "/usr/local/lib/psst.zip/tools/release_artifacts.py",
        )
        spec = {
            "versions": versions,
            **versions["v1.2.2"],
            "failure_after_start": options.failure_after_start,
            "require_schema_change": options.require_schema_change,
        }
        output = run(
            "docker",
            "exec",
            "-i",
            identity,
            "python3",
            "-I",
            "/opt/fixture/controller.py",
            data=json.dumps(spec).encode(),
            timeout=1800,
        )
        print(output.decode(), end="", flush=True)
        print(
            f"PASS disposable updater gate ({time.monotonic()-started:.1f}s)",
            flush=True,
        )
    finally:
        subprocess.run(["docker", "rm", "-fv", identity], capture_output=True)
        for image in owned_images:
            subprocess.run(["docker", "image", "rm", image], capture_output=True)
        shutil.rmtree(temp)


if __name__ == "__main__":
    main()

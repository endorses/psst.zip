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
from dataclasses import dataclass
import json
import secrets
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class ExactCandidate:
    """Already validated local release inputs; no public acquisition claim."""

    version: str
    commit: str
    platform: str
    configs: dict[str, str]
    saved_pair: Path
    bundle_root: Path
    manifest: dict


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


def cleanup_owned(identity: str, owned_images: list[str], temp: Path) -> None:
    """Require bounded removal of this experiment's privileged resources.

    Absence is checked through successful Docker queries. Query/removal errors
    are never interpreted as absence, and diagnostic text excludes daemon output.
    """
    failures = []
    try:
        selected = run(
            "docker",
            "container",
            "ls",
            "-aq",
            "--filter",
            "name=^/" + identity + "$",
            timeout=55,
        ).strip()
        if selected:
            run("docker", "rm", "-fv", identity, timeout=55)
        if run(
            "docker",
            "container",
            "ls",
            "-aq",
            "--filter",
            "name=^/" + identity + "$",
            timeout=55,
        ).strip():
            raise RuntimeError("Owned container remains")
    except Exception:
        failures.append("container-removal")
    for image in owned_images:
        try:
            selected = run(
                "docker",
                "image",
                "ls",
                "-q",
                "--filter",
                "reference=" + image,
                timeout=55,
            ).strip()
            if selected:
                run("docker", "image", "rm", image, timeout=55)
            if run(
                "docker",
                "image",
                "ls",
                "-q",
                "--filter",
                "reference=" + image,
                timeout=55,
            ).strip():
                raise RuntimeError("Owned image remains")
        except Exception:
            failures.append("image-removal:" + image)
    try:
        shutil.rmtree(temp)
    except Exception:
        failures.append("temporary-directory-removal")
    if failures:
        raise RuntimeError(
            "Disposable cleanup failed; no success measurement: "
            + ", ".join(failures)
            + "; owned scope="
            + identity
        ) from None


def execute_experiment(
    *,
    source: str,
    previous_source: str,
    failure_after_start: bool = False,
    require_schema_change: bool = False,
    backend_image: str | None = None,
    web_image: str | None = None,
    exact_candidate: ExactCandidate | None = None,
    initially_paused: bool = False,
    root: Path = ROOT,
) -> dict:
    """Run actual isolated checks and read facts written only after assertions.

    The measurement bridge uses exact saved candidate bytes and the assembled
    deployment bundle. The legacy CLI still builds its three fixture versions.
    A repeated exact candidate is an idempotence experiment, not a fictitious
    subsequent release. Secrets and complete protected transactions stay inside
    the disposable daemon.
    """
    options = argparse.Namespace(
        source=source,
        previous_source=previous_source,
        failure_after_start=failure_after_start,
        require_schema_change=require_schema_change,
        backend_image=backend_image,
        web_image=web_image,
    )
    if bool(backend_image) != bool(web_image):
        raise ValueError("supply both fixture image names, or neither")
    if exact_candidate and (backend_image or web_image):
        raise ValueError("exact saved candidate cannot use unrelated image aliases")
    identity = "psst-update-gate-" + secrets.token_hex(6)
    temp = Path(tempfile.mkdtemp(prefix=identity + "-"))
    owned_images = [identity + "-daemon"]
    started = time.monotonic()
    try:
        revision = (
            run("git", "-C", str(root), "rev-parse", options.source).decode().strip()
        )
        source = temp / "source"
        source.mkdir()
        archive = run("git", "-C", str(root), "archive", revision)
        run("tar", "-xf", "-", "-C", str(source), data=archive)
        versions = {}
        print(
            "Building prior/candidate/next release image pairs from committed source",
            flush=True,
        )
        prior_version = "v0.0.0" if exact_candidate else "v1.2.2"
        candidate_version = exact_candidate.version if exact_candidate else "v1.2.3"
        repeat_version = candidate_version if exact_candidate else "v1.2.4"
        if exact_candidate and (
            exact_candidate.commit != revision or candidate_version == prior_version
        ):
            raise ValueError("exact candidate differs from source/baseline version")
        selected = dict(
            (
                (
                    prior_version,
                    run("git", "-C", str(root), "rev-parse", options.previous_source)
                    .decode()
                    .strip(),
                ),
                (candidate_version, revision),
                (repeat_version, revision),
            )
        )
        for version, selected_revision in selected.items():
            context_root = temp / version
            context_root.mkdir()
            run(
                "tar",
                "-xf",
                "-",
                "-C",
                str(context_root),
                data=run("git", "-C", str(root), "archive", selected_revision),
            )
            pair = {"commit": selected_revision}
            for component in ("backend", "web"):
                image = identity + "-" + component + ":" + version
                if version == candidate_version and exact_candidate:
                    image = exact_candidate.configs[component]
                elif version == "v1.2.3" and options.backend_image:
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
            str(root / "tools/fixtures/release-updater"),
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
        image_archive = temp / "built-fixture-pairs.docker.tar"
        run(
            "docker",
            "image",
            "save",
            "--output",
            str(image_archive),
            *[
                pair[component]
                for version, pair in versions.items()
                if not exact_candidate or version != candidate_version
                for component in ("backend", "web")
            ],
        )
        for saved in [image_archive] + (
            [exact_candidate.saved_pair] if exact_candidate else []
        ):
            with saved.open("rb") as stream:
                subprocess.run(
                    ["docker", "exec", "-i", identity, "docker", "image", "load"],
                    stdin=stream,
                    capture_output=True,
                    timeout=900,
                    check=True,
                )
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
        installed = exact_candidate.bundle_root if exact_candidate else root
        for local, remote in (
            (
                installed / "deploy/update.py",
                "/usr/local/lib/psst.zip/deploy/update.py",
            ),
            (
                installed / "tools/release_artifacts.py",
                "/usr/local/lib/psst.zip/tools/release_artifacts.py",
            ),
            (
                root / "tools/fixtures/release-updater/controller.py",
                "/opt/fixture/controller.py",
            ),
            (
                root / "tools/fixtures/release-updater/checkpoint.py",
                "/opt/fixture/checkpoint.py",
            ),
        ):
            run("docker", "cp", str(local), identity + ":" + remote)
        for name in ("flows.py", "verify.py"):
            run(
                "docker",
                "cp",
                str(root / "tools/fixtures/release-updater" / name),
                identity + ":/opt/fixture/" + name,
            )
        run(
            "docker",
            "cp",
            str(installed / "deploy" if exact_candidate else source / "deploy"),
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
            **versions[prior_version],
            "prior_version": prior_version,
            "candidate_version": candidate_version,
            "repeat_version": repeat_version,
            "initially_paused": initially_paused,
            "failure_after_start": options.failure_after_start,
            "require_schema_change": options.require_schema_change,
        }
        if exact_candidate:
            spec["candidate_manifest"] = exact_candidate.manifest
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
        facts = json.loads(
            run("docker", "exec", identity, "cat", "/opt/fixture/result.json")
        )
        facts["nested_tools"] = {
            "docker": run(
                "docker",
                "exec",
                identity,
                "docker",
                "version",
                "--format",
                "{{.Server.Version}}",
            )
            .decode()
            .strip(),
            "compose": run(
                "docker", "exec", identity, "docker", "compose", "version", "--short"
            )
            .decode()
            .strip(),
            "architecture": run(
                "docker",
                "exec",
                identity,
                "docker",
                "info",
                "--format",
                "{{.Architecture}}",
            )
            .decode()
            .strip(),
        }
        return facts
    finally:
        cleanup_owned(identity, owned_images, temp)


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
        help="Inject a fault after real startup checks pass",
    )
    parser.add_argument(
        "--require-schema-change",
        action="store_true",
        help="Require historical migration advancement",
    )
    parser.add_argument("--backend-image")
    parser.add_argument("--web-image")
    options = parser.parse_args()
    if bool(options.backend_image) != bool(options.web_image):
        parser.error("supply both fixture image names, or neither")
    execute_experiment(**vars(options))


if __name__ == "__main__":
    main()

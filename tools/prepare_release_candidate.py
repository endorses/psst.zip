#!/usr/bin/env python3
"""Compatibility adapter for Go candidate preparation; never publish or attest.

The retained Python release harness imports these names while its measurements
move to Go. Candidate policy, Git identity checks, immutable base resolution and
actual native build records live in tools/release/internal/containerbuild.
"""

from __future__ import annotations

import argparse
import os
import tempfile
from functools import cache
from pathlib import Path

from release_artifacts import (
    PLATFORMS as PLATFORMS,
)
from release_artifacts import (
    InvalidRelease,
    json_bytes,
    read_json,
)
from release_process import command

ROOT = Path(__file__).resolve().parents[1]
# Transitional exported constants. The defaults command is the canonical source;
# adapter tests bind these exact tags to it until the remaining imports migrate.
BASES = {
    "golang": "docker.io/library/golang:1.27.3-alpine3.24",
    "alpine": "docker.io/library/alpine:3.24.2",
    "node": "docker.io/library/node:26.12.1-alpine3.24",
    "caddy": "docker.io/library/caddy:2.11.7-alpine",
    "buildkit": "docker.io/moby/buildkit:v0.34.1",
}
INDEX_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}
IMAGE_TYPES = {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
}


@cache
def _release_tool() -> str:
    return (
        command(
            ["sh", str(ROOT / "tools/release/build.sh")],
            environment=dict(os.environ),
            timeout=300,
        )
        .decode()
        .strip()
    )


def _invoke(root: Path, mode: str, *args: str) -> bytes:
    return command(
        [
            _release_tool(),
            "--root",
            str(root.resolve()),
            "container-build",
            mode,
            *args,
        ],
        environment=dict(os.environ),
        timeout=1800,
    )


def validated_tag(root: Path, ref: str, event_sha: str) -> tuple[str, str]:
    value = read_json(
        _invoke(root, "validate-tag", "--ref", ref, "--event-sha", event_sha)
    )
    return value["version"], value["commit"]


def validated_source(
    root: Path, ref: str, event_sha: str, planned_version: str
) -> tuple[str, str]:
    value = read_json(
        _invoke(
            root,
            "validate-source",
            "--ref",
            ref,
            "--event-sha",
            event_sha,
            "--planned-version",
            planned_version,
        )
    )
    return value["version"], value["commit"]


def validate_candidate(value: object) -> dict:
    with tempfile.TemporaryDirectory(prefix="psst-candidate-input-") as temporary:
        candidate = Path(temporary) / "candidate.json"
        candidate.write_bytes(json_bytes(value))
        return read_json(
            _invoke(ROOT, "validate-candidate", "--candidate", str(candidate))
        )


def resolve_bases(version: str, commit: str) -> dict:
    with tempfile.TemporaryDirectory(prefix="psst-candidate-bases-") as temporary:
        return read_json(
            _invoke(
                ROOT,
                "resolve-bases",
                "--version",
                version,
                "--commit",
                commit,
                "--output",
                str(Path(temporary) / "candidate.json"),
            )
        )


def record_build(candidate: dict, platform: str, metadata: dict, archive: Path) -> dict:
    with tempfile.TemporaryDirectory(prefix="psst-candidate-build-") as temporary:
        folder = Path(temporary)
        (folder / "candidate.json").write_bytes(json_bytes(candidate))
        # Keep exact original metadata objects; the Go boundary validates their
        # shape and actual image/config identities before recording tool output.
        from release_artifacts import fields

        fields(metadata, {"backend", "web"}, "paired Buildx metadata")
        for component in ("backend", "web"):
            (folder / (component + ".json")).write_bytes(
                json_bytes(metadata[component])
            )
        return read_json(
            _invoke(
                ROOT,
                "record-build",
                "--candidate",
                str(folder / "candidate.json"),
                "--platform",
                platform,
                "--backend-metadata",
                str(folder / "backend.json"),
                "--web-metadata",
                str(folder / "web.json"),
                "--archive",
                str(archive.resolve()),
                "--output",
                str(folder / "record.json"),
            )
        )


def build_bundle(
    root: Path,
    output: Path,
    version: str,
    profile: str = "artifact-foundation",
    *,
    expected_commit: str,
) -> Path:
    value = read_json(
        _invoke(
            root,
            "bundle",
            "--output",
            str(output.resolve()),
            "--version",
            version,
            "--payload-profile",
            profile,
            "--expected-commit",
            expected_commit,
        )
    )
    return Path(value["path"])


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="mode", required=True)
    for name in ("validate-tag", "validate-source"):
        sub = commands.add_parser(name)
        sub.add_argument("--root", type=Path, required=True)
        sub.add_argument("--ref", required=True)
        sub.add_argument("--event-sha", required=True)
        sub.add_argument("--github-output", type=Path)
        if name == "validate-source":
            sub.add_argument("--planned-version", required=True)
    resolve = commands.add_parser("resolve-bases")
    for name in ("version", "commit", "output"):
        resolve.add_argument("--" + name, required=True)
    resolve.add_argument("--github-output")
    record = commands.add_parser("record-build")
    for name in (
        "candidate",
        "platform",
        "backend-metadata",
        "web-metadata",
        "archive",
        "output",
    ):
        record.add_argument("--" + name, required=True)
    args = vars(parser.parse_args())
    mode = args.pop("mode")
    root = args.pop("root", ROOT)
    flags = [
        part
        for key, value in args.items()
        if value is not None
        for part in ("--" + key.replace("_", "-"), str(value))
    ]
    try:
        print(_invoke(root, mode, *flags).decode(), end="")
    except (InvalidRelease, OSError, ValueError) as error:
        parser.exit(1, f"Candidate rejected: {error}\n")


if __name__ == "__main__":
    main()

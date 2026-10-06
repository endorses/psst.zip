#!/usr/bin/env python3
"""Prepare read-only release candidate checks; never publish or attest a release."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess

from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    VERSION,
    InvalidRelease,
    create_output,
    fields,
    git,
    json_bytes,
    matches,
    read_bounded_file,
    read_json,
    require,
)

BASES = {
    "golang": "docker.io/library/golang:1.26.8-alpine",
    "alpine": "docker.io/library/alpine:3.21",
    "node": "docker.io/library/node:22-alpine",
    "caddy": "docker.io/library/caddy:2-alpine",
    "buildkit": "docker.io/moby/buildkit:buildx-stable-1",
}
INDEX_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}
IMAGE_TYPES = {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
}


def run(*command: str) -> bytes:
    result = subprocess.run(command, capture_output=True, timeout=180, check=False)
    require(result.returncode == 0, "Candidate check command failed")
    require(
        len(result.stdout) <= 16 * 1024 * 1024, "Candidate command output too large"
    )
    return result.stdout


def validated_tag(root: Path, ref: str, event_sha: str) -> tuple[str, str]:
    require(ref.startswith("refs/tags/"), "Candidates require a version tag")
    version = matches(ref.removeprefix("refs/tags/"), VERSION, "Invalid version tag")
    matches(event_sha, COMMIT, "Invalid event SHA")
    commit = git(root, "rev-parse", "--verify", f"{ref}^{{commit}}").decode().strip()
    event_commit = (
        git(root, "rev-parse", "--verify", f"{event_sha}^{{commit}}").decode().strip()
    )
    matches(commit, COMMIT, "Invalid tagged source commit")
    require(commit == event_commit, "Event SHA and version tag differ")
    result = subprocess.run(
        [
            "git",
            "-C",
            str(root),
            "merge-base",
            "--is-ancestor",
            commit,
            "refs/remotes/origin/main",
        ],
        capture_output=True,
    )
    require(result.returncode == 0, "Tagged commit is not reachable from origin/main")
    return version, commit


def index_record(value: object) -> tuple[str, dict[str, str]]:
    require(isinstance(value, dict), "Base index must be an object")
    require(
        type(value.get("schemaVersion")) is int and value["schemaVersion"] == 2,
        "Unsupported base schema",
    )
    require(
        isinstance(value.get("mediaType"), str) and value["mediaType"] in INDEX_TYPES,
        "Base must be a multi-platform index",
    )
    index_digest = matches(value.get("digest"), DIGEST, "Invalid base index digest")
    descriptors = value.get("manifests")
    require(
        isinstance(descriptors, list) and descriptors, "Base index has no manifests"
    )
    platforms = {}
    for item in descriptors:
        require(isinstance(item, dict), "Invalid base descriptor")
        platform = item.get("platform", {})
        require(isinstance(platform, dict), "Invalid base platform")
        name = f"{platform.get('os')}/{platform.get('architecture')}"
        if name not in PLATFORMS:
            continue
        require(
            isinstance(platform.get("variant", ""), str)
            and platform.get("variant", "")
            in ({"", "v8"} if name == "linux/arm64" else {""}),
            "Unexpected base architecture variant",
        )
        require(name not in platforms, "Duplicate base architecture")
        require(
            isinstance(item.get("mediaType"), str) and item["mediaType"] in IMAGE_TYPES,
            "Base architecture is not an image",
        )
        require(
            type(item.get("size")) is int and item["size"] > 0,
            "Invalid base descriptor size",
        )
        platforms[name] = matches(
            item.get("digest"), DIGEST, "Invalid base platform digest"
        )
    require(
        set(platforms) == set(PLATFORMS),
        "Base index must cover both release architectures",
    )
    require(
        len(set(platforms.values())) == 2 and index_digest not in platforms.values(),
        "Base index and child digests must be distinct",
    )
    return index_digest, platforms


def resolve_bases(version: str, commit: str) -> dict:
    matches(version, VERSION, "Invalid candidate version")
    matches(commit, COMMIT, "Invalid candidate commit")
    references = {}
    children = {}
    for name, tagged in BASES.items():

        def inspect(reference):
            return index_record(
                read_json(
                    run(
                        "docker",
                        "buildx",
                        "imagetools",
                        "inspect",
                        reference,
                        "--format",
                        "{{json .Manifest}}",
                    )
                )
            )

        digest, platforms = inspect(tagged)
        pinned = tagged.rsplit(":", 1)[0] + "@" + digest
        # The second lookup uses the immutable digest. A mutable tag changing
        # between requests cannot change the recorded build inputs.
        require(inspect(pinned) == (digest, platforms), "Pinned base index differs")
        references[name] = pinned
        children[name] = platforms
    return {
        "schema_version": 1,
        "kind": "release-candidate",
        "candidate_only": True,
        "version": version,
        "source_commit": commit,
        "platforms": PLATFORMS,
        "base_images": references,
        "base_platform_digests": children,
    }


def validate_candidate(value: object) -> dict:
    value = fields(
        value,
        {
            "schema_version",
            "kind",
            "candidate_only",
            "version",
            "source_commit",
            "platforms",
            "base_images",
            "base_platform_digests",
        },
        "candidate",
    )
    require(
        type(value["schema_version"]) is int and value["schema_version"] == 1,
        "Invalid candidate schema",
    )
    require(
        value["kind"] == "release-candidate" and value["candidate_only"] is True,
        "This is not a candidate-only record",
    )
    matches(value["version"], VERSION, "Invalid candidate version")
    matches(value["source_commit"], COMMIT, "Invalid candidate commit")
    require(value["platforms"] == PLATFORMS, "Invalid candidate platforms")
    references = fields(value["base_images"], set(BASES), "base images")
    children = fields(
        value["base_platform_digests"], set(BASES), "base platform digests"
    )
    for name, tagged in BASES.items():
        prefix = tagged.rsplit(":", 1)[0] + "@"
        require(
            isinstance(references[name], str) and references[name].startswith(prefix),
            "Unexpected base image repository",
        )
        index_digest = matches(
            references[name][len(prefix) :], DIGEST, "Invalid pinned base"
        )
        pair = fields(children[name], set(PLATFORMS), "base architecture digests")
        for child in pair.values():
            matches(child, DIGEST, "Invalid base child digest")
        require(
            len(set(pair.values())) == 2 and index_digest not in pair.values(),
            "Invalid base digest pair",
        )
    return value


def record_build(candidate: dict, platform: str, metadata: dict, archive: Path) -> dict:
    validate_candidate(candidate)
    require(platform in PLATFORMS, "Invalid build platform")
    fields(metadata, {"backend", "web"}, "paired Buildx metadata")
    for component in ("backend", "web"):
        require(isinstance(metadata[component], dict), "Invalid Buildx metadata")
        matches(
            metadata[component].get("containerimage.config.digest"),
            DIGEST,
            "Missing built image config digest",
        )
        matches(
            metadata[component].get("containerimage.digest"),
            DIGEST,
            "Missing built image digest",
        )
    require(
        archive.is_file() and not archive.is_symlink(),
        "Missing candidate image archive",
    )
    checksum = hashlib.sha256()
    with archive.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            checksum.update(chunk)
    native = run("docker", "info", "--format", "{{.Architecture}}").decode().strip()
    native = {"x86_64": "amd64", "aarch64": "arm64"}.get(native, native)
    require(
        platform == f"linux/{native}", "Candidate checks require a native Docker host"
    )
    bases = candidate["base_images"]

    def output(*command):
        text = run(*command).decode().strip()
        require(
            bool(text) and len(text) <= 8192,
            "Missing or oversized actual toolchain output",
        )
        return text

    return {
        **candidate,
        "checked_platform": platform,
        "native_execution": True,
        "toolchain_output": {
            "go": output(
                "docker",
                "run",
                "--rm",
                "--network",
                "none",
                "--platform",
                platform,
                bases["golang"],
                "go",
                "version",
            ),
            "node": output(
                "docker",
                "run",
                "--rm",
                "--network",
                "none",
                "--platform",
                platform,
                bases["node"],
                "node",
                "--version",
            ),
            "docker": output("docker", "version", "--format", "{{.Server.Version}}"),
            "compose": output("docker", "compose", "version", "--short"),
            "buildx": output("docker", "buildx", "version"),
            "builder": output("docker", "buildx", "inspect"),
        },
        "build_metadata": metadata,
        "image_archive": {
            "name": archive.name,
            "sha256": checksum.hexdigest(),
            "size": archive.stat().st_size,
        },
        "limitations": [
            "No registry publication or authenticated provenance.",
            "No vulnerability approval, complete runtime-license review, updater or production deployment readiness.",
        ],
    }


def github_output(path: Path | None, values: dict[str, str]) -> None:
    if path:
        require(
            all("\n" not in value and "\r" not in value for value in values.values()),
            "Unsafe workflow output",
        )
        with path.open("a") as stream:
            for name, value in values.items():
                stream.write(f"{name}={value}\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    tag = commands.add_parser("validate-tag")
    tag.add_argument("--root", type=Path, required=True)
    tag.add_argument("--ref", required=True)
    tag.add_argument("--event-sha", required=True)
    tag.add_argument("--github-output", type=Path)
    bases = commands.add_parser("resolve-bases")
    bases.add_argument("--version", required=True)
    bases.add_argument("--commit", required=True)
    bases.add_argument("--output", type=Path, required=True)
    bases.add_argument("--github-output", type=Path)
    record = commands.add_parser("record-build")
    record.add_argument("--candidate", type=Path, required=True)
    record.add_argument("--platform", choices=PLATFORMS, required=True)
    record.add_argument("--backend-metadata", type=Path, required=True)
    record.add_argument("--web-metadata", type=Path, required=True)
    record.add_argument("--archive", type=Path, required=True)
    record.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "validate-tag":
            version, commit = validated_tag(args.root, args.ref, args.event_sha)
            github_output(args.github_output, {"version": version, "commit": commit})
            print(
                json.dumps(
                    {"version": version, "commit": commit, "candidate_only": True}
                )
            )
        elif args.command == "resolve-bases":
            value = resolve_bases(args.version, args.commit)
            validate_candidate(value)
            create_output(args.output, json_bytes(value))
            github_output(
                args.github_output,
                {
                    f"{name}_image": image
                    for name, image in value["base_images"].items()
                },
            )
        else:
            candidate = validate_candidate(read_json(read_bounded_file(args.candidate)))
            metadata = {
                name: read_json(read_bounded_file(getattr(args, f"{name}_metadata")))
                for name in ("backend", "web")
            }
            create_output(
                args.output,
                json_bytes(
                    record_build(candidate, args.platform, metadata, args.archive)
                ),
            )
    except (InvalidRelease, OSError, ValueError, subprocess.TimeoutExpired) as error:
        parser.exit(1, f"Candidate rejected: {error}\n")


if __name__ == "__main__":
    main()

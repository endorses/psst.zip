#!/usr/bin/env python3
"""Create and structurally validate deterministic psst.zip release artifacts.

Validation establishes consistency, not authenticity. Verify GitHub artifact
attestations against the trusted repository, workflow, and source commit before
using a release bundle or running any tooling from it.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile

VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
COMMIT = re.compile(r"[0-9a-f]{40}\Z")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
REPOSITORY = re.compile(r"[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*\Z")
PLATFORMS = ["linux/amd64", "linux/arm64"]
REQUIREMENTS = {"docker": "27.0.0", "compose": "2.24.4"}
REQUIRED_FILES = frozenset(
    {
        "LICENSE",
        "backend/licenses/AGPL-3.0-only.txt",
        "backend/licenses/THIRD_PARTY_NOTICES.txt",
        "backend/licenses/dependency-inventory.json",
        "web/static/licenses/AGPL-3.0-only.txt",
        "web/static/licenses/THIRD_PARTY_NOTICES.txt",
        "web/static/licenses/dependency-inventory.json",
        "web/static/licenses/lucide.txt",
        "web/static/licenses/qr-scanner.txt",
        "deploy/compose.release.yml",
        "deploy/external-proxy.release.compose.yml",
        "deploy/release.env.example",
        "deploy/external-proxy/Caddyfile",
        "deploy/external-proxy/trusted-proxy.caddy",
        "deploy/release-manifest.schema.json",
        "tools/release_artifacts.py",
    }
)
METADATA = "release-bundle.json"
MAX_BUNDLE_BYTES = 16 * 1024 * 1024


class InvalidRelease(ValueError):
    """Invalid, incomplete, or inconsistent release input."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise InvalidRelease(message)


def fields(value: object, expected: set[str], label: str) -> dict:
    require(isinstance(value, dict), f"{label} must be an object")
    require(set(value) == expected, f"{label} has missing or unknown fields")
    return value


def matches(value: object, pattern: re.Pattern, label: str) -> str:
    require(isinstance(value, str) and pattern.fullmatch(value) is not None, label)
    return value


def repository_name(value: object) -> str:
    result = matches(value, REPOSITORY, "invalid trusted repository")
    owner, name = result.split("/")
    require(
        len(owner) <= 39
        and re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", owner) is not None
        and len(name) <= 100
        and name not in {".", ".."},
        "invalid repository",
    )
    return result


def allowed_path(name: str) -> bool:
    if name in REQUIRED_FILES or name == "deploy/update.py":
        return True
    parts = PurePosixPath(name).parts
    return (
        len(parts) >= 4
        and str(PurePosixPath(name)) == name
        and parts[:2] == ("backend", "licenses")
        and parts[-1]
        in {
            "LICENSE",
            "NOTICE",
            "COPYING",
            "COPYRIGHT",
            "SQLITE-LICENSE",
            "COPYRIGHT-MUSL",
            "LICENSE-GO",
            "LICENSE-MMAP-GO",
            "LICENSE-LOGO",
        }
        and all(
            re.fullmatch(r"[a-zA-Z0-9_.@+-]+", part) and part not in {".", ".."}
            for part in parts
        )
        and "\\" not in name
    )


def json_bytes(value: object) -> bytes:
    return (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()


def git(root: Path, *args: str) -> bytes:
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True)
    require(result.returncode == 0, "cannot read release inputs from Git")
    return result.stdout


def create_output(path: Path, content: bytes) -> None:
    # Exclusive creation prevents accidentally replacing a published artifact.
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("xb") as stream:
        stream.write(content)


def build_bundle(root: Path, output: Path, version: str) -> Path:
    matches(version, VERSION, "version must be vMAJOR.MINOR.PATCH")
    root = root.resolve()
    commit = git(root, "rev-parse", "HEAD").decode().strip()
    matches(commit, COMMIT, "source commit must be a full SHA-1")
    timestamp = int(git(root, "show", "-s", "--format=%ct", commit))
    tree = git(root, "ls-tree", "-rz", "--full-tree", commit)
    blobs = {}
    for entry in tree.split(b"\0"):
        if not entry:
            continue
        attributes, raw_name = entry.split(b"\t", 1)
        name = raw_name.decode("utf-8")
        if not allowed_path(name):
            continue
        mode, kind, oid = attributes.split()
        require(
            mode in {b"100644", b"100755"} and kind == b"blob",
            f"non-regular release input: {name}",
        )
        target = root / name
        require(
            not target.is_symlink() and target.is_file(),
            f"missing or unsafe release input: {name}",
        )
        content = git(root, "cat-file", "blob", oid.decode())
        require(target.read_bytes() == content, f"uncommitted release input: {name}")
        # A staged-only edit must not be hidden by reverting its working copy.
        require(
            not git(root, "diff", "--cached", commit, "--", name),
            f"staged release input: {name}",
        )
        require(
            not git(root, "diff", commit, "--", name), f"dirty release input: {name}"
        )
        blobs[name] = content
    candidates = git(
        root, "ls-files", "-z", "--cached", "--others", "--exclude-standard"
    )
    for raw_name in candidates.split(b"\0"):
        if raw_name:
            name = raw_name.decode("utf-8")
            require(
                not allowed_path(name) or name in blobs,
                f"untracked or newly staged release input: {name}",
            )
    require(
        not (root / "deploy/update.py").exists() or "deploy/update.py" in blobs,
        "updater exists but is not a tracked release input",
    )
    require(
        sum(len(content) for content in blobs.values()) <= MAX_BUNDLE_BYTES,
        "expanded bundle exceeds size limit",
    )
    require(
        REQUIRED_FILES <= blobs.keys(),
        f"missing tracked release inputs: {', '.join(sorted(REQUIRED_FILES - blobs.keys()))}",
    )
    require(
        any(name.startswith("backend/licenses/") for name in blobs),
        "dependency licenses missing",
    )
    profile = (
        "deployment-ready" if "deploy/update.py" in blobs else "artifact-foundation"
    )
    blobs[METADATA] = json_bytes(
        {
            "schema_version": 1,
            "version": version,
            "source_commit": commit,
            "payload_profile": profile,
        }
    )
    buffer = io.BytesIO()
    with gzip.GzipFile(
        fileobj=buffer, mode="wb", filename="", mtime=timestamp
    ) as zipped:
        with tarfile.open(
            fileobj=zipped, mode="w", format=tarfile.USTAR_FORMAT
        ) as archive:
            for name, content in sorted(blobs.items()):
                entry = tarfile.TarInfo(name)
                entry.size = len(content)
                entry.mtime = timestamp
                entry.uid = entry.gid = 0
                entry.mode = 0o644
                archive.addfile(entry, io.BytesIO(content))
    data = buffer.getvalue()
    require(len(data) <= MAX_BUNDLE_BYTES, "deployment bundle exceeds size limit")
    expand_bundle(data)
    destination = output / f"psst.zip-deployment-{version}.tar.gz"
    create_output(destination, data)
    return destination


def validate_manifest(
    value: object, trusted_repository: str = "endorses/psst.zip"
) -> dict:
    trusted_repository = repository_name(trusted_repository)
    value = fields(
        value,
        {
            "schema_version",
            "version",
            "payload_profile",
            "source",
            "platforms",
            "images",
            "bundle",
            "requirements",
            "notes",
            "build",
        },
        "manifest",
    )
    require(
        type(value["schema_version"]) is int and value["schema_version"] == 1,
        "unsupported schema version",
    )
    version = matches(value["version"], VERSION, "invalid release version")
    require(
        isinstance(value["payload_profile"], str)
        and value["payload_profile"] in {"artifact-foundation", "deployment-ready"},
        "invalid payload profile",
    )
    source = fields(value["source"], {"repository", "commit", "archive_url"}, "source")
    require(
        repository_name(source["repository"]) == trusted_repository,
        "unexpected source repository",
    )
    matches(source["commit"], COMMIT, "invalid source commit")
    require(
        source["archive_url"]
        == f"https://github.com/{trusted_repository}/archive/{source['commit']}.tar.gz",
        "source archive must match trusted repository and commit",
    )
    require(
        value["platforms"] == PLATFORMS,
        "release must cover linux/amd64 and linux/arm64",
    )
    images = fields(value["images"], {"backend", "web"}, "images")
    owner = trusted_repository.split("/")[0]
    for component, image in images.items():
        image = fields(image, {"index", "platform_digests"}, f"{component} image")
        prefix = f"ghcr.io/{owner}/psst-zip-{component}@"
        require(
            isinstance(image["index"], str) and image["index"].startswith(prefix),
            "unexpected registry/image reference",
        )
        index_digest = matches(
            image["index"][len(prefix) :], DIGEST, "invalid image index digest"
        )
        children = fields(image["platform_digests"], set(PLATFORMS), "platform digests")
        for digest in children.values():
            matches(digest, DIGEST, "invalid architecture image digest")
        require(
            len(set(children.values())) == 2 and index_digest not in children.values(),
            "index and platform digests must be distinct",
        )
    bundle = fields(value["bundle"], {"name", "sha256"}, "bundle")
    require(
        bundle["name"] == f"psst.zip-deployment-{version}.tar.gz",
        "invalid deployment bundle name",
    )
    matches(bundle["sha256"], re.compile(r"[0-9a-f]{64}\Z"), "invalid bundle checksum")
    require(
        value["requirements"] == REQUIREMENTS, "unsupported Docker/Compose requirements"
    )
    notes = fields(
        value["notes"], {"migration", "rollback", "checkpoint_required"}, "notes"
    )
    require(notes["checkpoint_required"] is True, "stopped checkpoint is required")
    for key in ("migration", "rollback"):
        require(
            isinstance(notes[key], str)
            and bool(notes[key].strip())
            and len(notes[key]) <= 8192,
            f"missing or invalid {key} notes",
        )
    build = fields(value["build"], {"base_images", "toolchains"}, "build")
    for key, required in (
        ("base_images", {"golang", "node", "alpine", "caddy"}),
        ("toolchains", {"go", "node"}),
    ):
        entries = build[key]
        require(
            isinstance(entries, dict) and required <= entries.keys(), f"missing {key}"
        )
        for name, entry in entries.items():
            require(
                re.fullmatch(r"[a-z][a-z0-9_-]*", name) is not None,
                f"invalid {key} name",
            )
            require(
                isinstance(entry, str) and bool(entry.strip()) and len(entry) < 512,
                f"invalid {key} value",
            )
            if key == "base_images":
                require(
                    re.fullmatch(r"[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}", entry)
                    is not None,
                    "base image must be digest-pinned",
                )
            else:
                require(
                    re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9._+/-]*", entry) is not None,
                    "invalid toolchain version",
                )
    return value


def read_bounded_file(path: Path) -> bytes:
    require(
        path.is_file() and not path.is_symlink(), "release input must be a regular file"
    )
    require(path.stat().st_size <= MAX_BUNDLE_BYTES, "release input exceeds size limit")
    with path.open("rb") as stream:
        content = stream.read(MAX_BUNDLE_BYTES + 1)
    require(len(content) <= MAX_BUNDLE_BYTES, "release input exceeds size limit")
    return content


def expand_bundle(contents: bytes) -> bytes:
    # Bound all tar bytes, including PAX/global headers consumed internally by
    # tarfile before it yields members. Counting visible member payloads alone
    # does not defend against hidden metadata expansion.
    with gzip.GzipFile(fileobj=io.BytesIO(contents), mode="rb") as stream:
        expanded = stream.read(MAX_BUNDLE_BYTES + 1)
    require(len(expanded) <= MAX_BUNDLE_BYTES, "expanded bundle exceeds size limit")
    return expanded


def read_json(content: bytes) -> object:
    def pairs(items: list[tuple[str, object]]) -> dict:
        result = {}
        for key, value in items:
            require(key not in result, f"duplicate JSON key: {key}")
            result[key] = value
        return result

    def nonfinite(value: str) -> None:
        raise InvalidRelease(f"non-finite JSON number: {value}")

    require(len(content) <= MAX_BUNDLE_BYTES, "JSON exceeds size limit")
    return json.loads(content, object_pairs_hook=pairs, parse_constant=nonfinite)


def validate_bundle(manifest: dict, bundle_path: Path) -> None:
    require(
        bundle_path.name == manifest["bundle"]["name"], "unexpected bundle filename"
    )
    require(
        bundle_path.is_file() and not bundle_path.is_symlink(),
        "bundle must be a regular file",
    )
    require(bundle_path.stat().st_size <= MAX_BUNDLE_BYTES, "bundle exceeds size limit")
    contents = read_bounded_file(bundle_path)
    require(
        hashlib.sha256(contents).hexdigest() == manifest["bundle"]["sha256"],
        "bundle checksum mismatch",
    )
    files = {}
    size = 0
    expanded = expand_bundle(contents)
    with tarfile.open(fileobj=io.BytesIO(expanded), mode="r:") as archive:
        for member in archive:
            require(member.name not in files, "duplicate bundle path")
            require(
                member.name == METADATA or allowed_path(member.name),
                "unexpected or unsafe bundle path",
            )
            require(
                member.isfile()
                and member.mode == 0o644
                and member.uid == 0
                and member.gid == 0,
                "unsafe bundle entry type or permissions",
            )
            size += member.size
            require(
                0 <= member.size <= MAX_BUNDLE_BYTES and size <= MAX_BUNDLE_BYTES,
                "expanded bundle exceeds size limit",
            )
            stream = archive.extractfile(member)
            require(stream is not None, "unreadable bundle member")
            files[member.name] = stream.read()
    require(
        REQUIRED_FILES <= files.keys() and METADATA in files,
        "incomplete deployment bundle",
    )
    require(
        any(name.startswith("backend/licenses/") for name in files),
        "dependency licenses missing",
    )
    metadata = fields(
        read_json(files[METADATA]),
        {"schema_version", "version", "source_commit", "payload_profile"},
        "bundle metadata",
    )
    require(
        type(metadata["schema_version"]) is int and metadata["schema_version"] == 1,
        "invalid bundle schema",
    )
    require(
        metadata["payload_profile"] == manifest["payload_profile"],
        "bundle payload profile mismatch",
    )
    require(
        ("deploy/update.py" in files)
        == (manifest["payload_profile"] == "deployment-ready"),
        "updater/profile mismatch",
    )
    require(
        metadata["version"] == manifest["version"]
        and metadata["source_commit"] == manifest["source"]["commit"],
        "bundle source/version does not match manifest",
    )


def assignments(values: list[str]) -> dict:
    entries = {}
    for value in values:
        name, separator, entry = value.partition("=")
        require(
            separator == "=" and name not in entries,
            "invalid or duplicate NAME=VALUE assignment",
        )
        entries[name] = entry
    return entries


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    bundle_parser = subparsers.add_parser("build-bundle")
    bundle_parser.add_argument("--root", type=Path, required=True)
    bundle_parser.add_argument("--output", type=Path, required=True)
    bundle_parser.add_argument("--version", required=True)
    manifest_parser = subparsers.add_parser("create-manifest")
    manifest_parser.add_argument("--version", required=True)
    manifest_parser.add_argument("--source-commit", required=True)
    manifest_parser.add_argument(
        "--payload-profile",
        choices=("artifact-foundation", "deployment-ready"),
        default="artifact-foundation",
    )
    manifest_parser.add_argument("--repository", default="endorses/psst.zip")
    manifest_parser.add_argument("--bundle", type=Path, required=True)
    manifest_parser.add_argument("--output", type=Path, required=True)
    for component in ("backend", "web"):
        manifest_parser.add_argument(f"--{component}-index", required=True)
        for architecture in ("amd64", "arm64"):
            manifest_parser.add_argument(f"--{component}-{architecture}", required=True)
    manifest_parser.add_argument("--base-image", action="append", default=[])
    manifest_parser.add_argument("--toolchain", action="append", default=[])
    manifest_parser.add_argument("--migration-notes", required=True)
    manifest_parser.add_argument("--rollback-notes", required=True)
    validation_parser = subparsers.add_parser("validate")
    validation_parser.add_argument("--manifest", type=Path, required=True)
    validation_parser.add_argument("--bundle", type=Path)
    validation_parser.add_argument("--repository", default="endorses/psst.zip")
    args = parser.parse_args()
    try:
        if args.command == "build-bundle":
            print(build_bundle(args.root, args.output, args.version))
        elif args.command == "create-manifest":
            value = {
                "schema_version": 1,
                "version": args.version,
                "payload_profile": args.payload_profile,
                "source": {
                    "repository": args.repository,
                    "commit": args.source_commit,
                    "archive_url": f"https://github.com/{args.repository}/archive/{args.source_commit}.tar.gz",
                },
                "platforms": PLATFORMS,
                "images": {
                    component: {
                        "index": getattr(args, f"{component}_index"),
                        "platform_digests": {
                            f"linux/{architecture}": getattr(
                                args, f"{component}_{architecture}"
                            )
                            for architecture in ("amd64", "arm64")
                        },
                    }
                    for component in ("backend", "web")
                },
                "bundle": {
                    "name": args.bundle.name,
                    "sha256": hashlib.sha256(
                        read_bounded_file(args.bundle)
                    ).hexdigest(),
                },
                "requirements": REQUIREMENTS,
                "notes": {
                    "migration": args.migration_notes,
                    "rollback": args.rollback_notes,
                    "checkpoint_required": True,
                },
                "build": {
                    "base_images": assignments(args.base_image),
                    "toolchains": assignments(args.toolchain),
                },
            }
            validate_manifest(value, args.repository)
            validate_bundle(value, args.bundle)
            create_output(args.output, json_bytes(value))
        else:
            value = validate_manifest(
                read_json(read_bounded_file(args.manifest)), args.repository
            )
            if args.bundle:
                validate_bundle(value, args.bundle)
            print(
                "Release structure validated; provenance verification is also required."
            )
    except (InvalidRelease, OSError, ValueError, EOFError, tarfile.TarError) as error:
        parser.exit(1, f"Release rejected: {error}\n")


if __name__ == "__main__":
    main()

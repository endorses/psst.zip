#!/usr/bin/env python3
"""Validate local OCI export bytes and assemble deterministic release indexes.

Archive validation establishes correspondence to the tested image configuration,
not license approval, scanner success, provenance, or publication permission.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
from pathlib import Path
import re
import tarfile

from prepare_release_candidate import IMAGE_TYPES, INDEX_TYPES
from publish_container_release import sha256, validate_registry_index
from release_artifacts import (
    COMMIT,
    DIGEST,
    PLATFORMS,
    VERSION,
    InvalidRelease,
    assignments,
    create_output,
    fields,
    json_bytes,
    matches,
    read_json,
    repository_name,
    require,
)

KEYS = {
    f"{component}-{arch}"
    for component in ("backend", "web")
    for arch in ("amd64", "arm64")
}
MAX_ARCHIVE = 2 * 1024**3
MAX_BLOBS = 256
MAX_DOCUMENT = 1024 * 1024
BLOB_PATH = re.compile(r"blobs/sha256/([0-9a-f]{64})\Z")
CONFIG_TYPES = {
    "application/vnd.oci.image.config.v1+json",
    "application/vnd.docker.container.image.v1+json",
}
LAYER_TYPES = {
    "application/vnd.oci.image.layer.v1.tar",
    "application/vnd.oci.image.layer.v1.tar+gzip",
    "application/vnd.docker.image.rootfs.diff.tar.gzip",
}


class StrictTarInfo(tarfile.TarInfo):
    def _proc_member(self, archive):
        # Reject PAX/sparse headers before tarfile expands hidden metadata.
        require(
            self.type in {tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE},
            "OCI archive contains unsupported headers or entry types",
        )
        return super()._proc_member(archive)


def inspect_archive(
    path: Path,
    *,
    platform: str,
    repository: str,
    version: str,
    commit: str,
    tested_config: str,
    component: str,
) -> dict:
    require(
        platform in PLATFORMS and component in {"backend", "web"},
        "Invalid OCI component/platform",
    )
    repository_name(repository)
    matches(version, VERSION, "Invalid OCI release version")
    matches(commit, COMMIT, "Invalid OCI source commit")
    matches(tested_config, DIGEST, "Missing actual tested configuration digest")
    require(
        path.is_file() and not path.is_symlink(), "OCI archive must be a regular file"
    )
    require(0 < path.stat().st_size <= MAX_ARCHIVE, "OCI archive exceeds bounds")
    blobs, documents, seen = {}, {}, set()
    total = 0
    with path.open("rb") as source:
        # Buildx's OCI exporter produces an uncompressed tar container; layer
        # compression stays inside content-addressed blobs.
        with tarfile.open(fileobj=source, mode="r:", tarinfo=StrictTarInfo) as archive:
            for member in archive:
                name = member.name
                require(
                    name not in seen and len(seen) < MAX_BLOBS + 8,
                    "Duplicate/oversized OCI archive inventory",
                )
                seen.add(name)
                if member.isdir():
                    require(
                        name in {"blobs", "blobs/sha256"} and member.size == 0,
                        "Unexpected OCI directory",
                    )
                    continue
                match = BLOB_PATH.fullmatch(name)
                require(
                    name in {"oci-layout", "index.json"} or match is not None,
                    "Unsafe or unexpected OCI archive path",
                )
                require(
                    not member.sparse and 0 <= member.size <= MAX_ARCHIVE,
                    "Unsupported sparse/oversized OCI blob",
                )
                total += member.size
                require(total <= MAX_ARCHIVE, "OCI expanded payload exceeds bounds")
                stream = archive.extractfile(member)
                require(stream is not None, "Unreadable OCI blob")
                hasher, content, length = hashlib.sha256(), bytearray(), 0
                with stream:
                    while block := stream.read(1024 * 1024):
                        hasher.update(block)
                        length += len(block)
                        if member.size <= MAX_DOCUMENT:
                            content.extend(block)
                require(length == member.size, "Truncated OCI blob")
                if match:
                    digest = "sha256:" + match[1]
                    require(
                        hasher.hexdigest() == match[1],
                        "OCI blob checksum differs from its path",
                    )
                    blobs[digest] = member.size
                    if member.size <= MAX_DOCUMENT:
                        documents[digest] = bytes(content)
                else:
                    require(member.size <= MAX_DOCUMENT, "OCI metadata exceeds bounds")
                    documents[name] = bytes(content)
            # Prevent a second hidden archive after the tar end marker.
            source.seek(archive.offset)
            tail = source.read(1024 * 1024 + 1)
            require(
                len(tail) <= 1024 * 1024 and not tail.strip(b"\0"),
                "OCI archive has trailing data",
            )
        source.seek(0)
        archive_digest = "sha256:" + hashlib.file_digest(source, "sha256").hexdigest()
    require(
        "oci-layout" in documents and "index.json" in documents,
        "Missing OCI layout/index",
    )
    layout = fields(
        read_json(documents["oci-layout"]), {"imageLayoutVersion"}, "OCI layout"
    )
    require(layout["imageLayoutVersion"] == "1.0.0", "Unsupported OCI layout")
    used = set()

    def descriptor(value: dict, allowed: set) -> tuple[str, bytes | None]:
        require(
            isinstance(value, dict) and value.get("mediaType") in allowed,
            "Unexpected OCI descriptor type",
        )
        require(
            not value.get("urls") and not value.get("data"),
            "External/inline OCI payload rejected",
        )
        digest = matches(value.get("digest"), DIGEST, "Invalid OCI descriptor digest")
        require(
            type(value.get("size")) is int
            and value["size"] > 0
            and blobs.get(digest) == value["size"],
            "OCI descriptor size differs or payload is missing",
        )
        used.add(digest)
        return digest, documents.get(digest)

    def document(raw: bytes | None) -> dict:
        require(raw is not None, "OCI document exceeds bounds")
        value = read_json(raw)
        require(isinstance(value, dict), "OCI document is not an object")
        require(
            type(value.get("schemaVersion")) is int and value["schemaVersion"] == 2,
            "Invalid OCI document schema",
        )
        return value

    index = document(documents["index.json"])
    require(index.get("mediaType") in INDEX_TYPES, "Export is not an OCI index")
    for _ in range(4):
        children = index.get("manifests")
        require(
            isinstance(children, list) and len(children) == 1,
            "Native OCI archive must contain one runnable image and no attached attestations",
        )
        child = children[0]
        digest, raw = descriptor(child, IMAGE_TYPES | INDEX_TYPES)
        announced = child.get("platform")
        if announced is not None:
            require(
                isinstance(announced, dict)
                and announced.get("os") == "linux"
                and announced.get("architecture") == platform.split("/")[1]
                and announced.get("variant", "")
                in ({"", "v8"} if platform.endswith("arm64") else {""}),
                "OCI archive announces another platform",
            )
        image = document(raw)
        require(
            image.get("mediaType") == child["mediaType"],
            "OCI descriptor/document media types differ",
        )
        if image["mediaType"] in IMAGE_TYPES:
            break
        index = image
    else:
        raise InvalidRelease("OCI index nesting exceeds bounds")
    config_digest, config_raw = descriptor(image.get("config"), CONFIG_TYPES)
    require(
        config_digest == tested_config,
        "OCI export differs from the image configuration actually tested",
    )
    require(config_raw is not None, "OCI configuration exceeds bounds")
    config = read_json(config_raw)
    require(
        isinstance(config, dict)
        and config.get("os") == "linux"
        and config.get("architecture") == platform.split("/")[1],
        "OCI configuration has another platform",
    )
    settings = config.get("config")
    require(isinstance(settings, dict), "Missing OCI runtime configuration")
    labels = settings.get("Labels")
    expected = {
        "org.opencontainers.image.title": "psst.zip " + component,
        "org.opencontainers.image.source": "https://github.com/" + repository,
        "org.opencontainers.image.version": version,
        "org.opencontainers.image.revision": commit,
        "org.opencontainers.image.licenses": "AGPL-3.0-only",
        "zip.psst.source.archive": "https://github.com/"
        + repository
        + "/archive/"
        + commit
        + ".tar.gz",
    }
    require(
        isinstance(labels, dict)
        and all(labels.get(key) == value for key, value in expected.items()),
        "OCI configuration has substituted release/source labels",
    )
    layers = image.get("layers")
    require(
        isinstance(layers, list) and 0 < len(layers) <= 128,
        "Invalid OCI layer inventory",
    )
    for layer in layers:
        descriptor(layer, LAYER_TYPES)
    rootfs = config.get("rootfs")
    require(
        isinstance(rootfs, dict)
        and rootfs.get("type") == "layers"
        and isinstance(rootfs.get("diff_ids"), list)
        and len(rootfs["diff_ids"]) == len(layers),
        "OCI configuration layer inventory differs",
    )
    for diff_id in rootfs["diff_ids"]:
        matches(diff_id, DIGEST, "Invalid OCI uncompressed layer identifier")
    require(used == set(blobs), "OCI export contains unreferenced blobs")
    # The tested configuration binds uncompressed diff_ids. Checking only the
    # compressed blob digests would permit replacing a layer while retaining a
    # previously tested config. Verify both identities without extracting files.
    expanded = 0
    with path.open("rb") as source:
        require(
            "sha256:" + hashlib.file_digest(source, "sha256").hexdigest()
            == archive_digest,
            "OCI archive changed before layer verification",
        )
        source.seek(0)
        with tarfile.open(fileobj=source, mode="r:", tarinfo=StrictTarInfo) as archive:
            for layer, expected_diff in zip(layers, rootfs["diff_ids"], strict=True):
                name = "blobs/sha256/" + layer["digest"][7:]
                encoded = archive.extractfile(name)
                require(encoded is not None, "Missing OCI layer during replay")
                with encoded:
                    decoded = (
                        gzip.GzipFile(fileobj=encoded, mode="rb")
                        if layer["mediaType"].endswith("gzip")
                        else encoded
                    )
                    hasher, layer_size = hashlib.sha256(), 0
                    try:
                        while block := decoded.read(65536):
                            layer_size += len(block)
                            expanded += len(block)
                            require(
                                layer_size <= 512 * 1024**2 and expanded <= MAX_ARCHIVE,
                                "OCI uncompressed layers exceed bounds",
                            )
                            hasher.update(block)
                    finally:
                        if decoded is not encoded:
                            decoded.close()
                require(
                    "sha256:" + hasher.hexdigest() == expected_diff,
                    "OCI layer differs from the tested image configuration",
                )
        source.seek(0)
        require(
            "sha256:" + hashlib.file_digest(source, "sha256").hexdigest()
            == archive_digest,
            "OCI archive changed during layer verification",
        )
    return {
        "platform": platform,
        "manifest_digest": digest,
        "manifest_size": child["size"],
        "manifest_media_type": child["mediaType"],
        "config_digest": config_digest,
        "archive_digest": archive_digest,
        "blob_count": len(blobs),
    }


def assemble(
    archives: dict[str, Path],
    tested_configs: dict[str, str],
    *,
    repository: str,
    version: str,
    commit: str,
    output: Path,
) -> dict:
    fields(archives, KEYS, "four native OCI exports")
    fields(tested_configs, KEYS, "four actually tested image configurations")
    require(
        not output.is_symlink() and (not output.exists() or output.is_dir()),
        "OCI output must be a directory, not a link",
    )
    require(
        all(
            not (output / name).exists() and not (output / name).is_symlink()
            for name in (
                "backend-index.json",
                "web-index.json",
                "oci-correspondence.json",
            )
        ),
        "OCI output already exists; never replace release inputs",
    )
    children, images = {}, {}
    # Validate all four before creating any output files.
    for key in sorted(KEYS):
        component, arch = key.split("-")
        children[key] = inspect_archive(
            archives[key],
            platform="linux/" + arch,
            repository=repository,
            version=version,
            commit=commit,
            tested_config=tested_configs[key],
            component=component,
        )
    for component in ("backend", "web"):
        raw = json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.index.v1+json",
                "manifests": [
                    {
                        "mediaType": children[f"{component}-{platform.split('/')[1]}"][
                            "manifest_media_type"
                        ],
                        "digest": children[f"{component}-{platform.split('/')[1]}"][
                            "manifest_digest"
                        ],
                        "size": children[f"{component}-{platform.split('/')[1]}"][
                            "manifest_size"
                        ],
                        "platform": {
                            "os": "linux",
                            "architecture": platform.split("/")[1],
                        },
                    }
                    for platform in PLATFORMS
                ],
            }
        )
        record = {
            "index": f"ghcr.io/{repository.split('/')[0]}/psst-zip-{component}@{sha256(raw)}",
            "platform_digests": {
                platform: children[f"{component}-{platform.split('/')[1]}"][
                    "manifest_digest"
                ]
                for platform in PLATFORMS
            },
        }
        validate_registry_index(raw, record)
        images[component] = record
        create_output(output / f"{component}-index.json", raw)
    result = {
        "schema_version": 1,
        "kind": "local-oci-correspondence",
        "repository": repository,
        "version": version,
        "source_commit": commit,
        "images": images,
        "children": children,
        "publication_authorized": False,
    }
    create_output(output / "oci-correspondence.json", json_bytes(result))
    return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--archive", action="append", default=[], metavar="COMPONENT-ARCH=PATH"
    )
    parser.add_argument(
        "--tested-config", action="append", default=[], metavar="COMPONENT-ARCH=DIGEST"
    )
    parser.add_argument("--repository", default="endorses/psst.zip")
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    assemble(
        {key: Path(value) for key, value in assignments(args.archive).items()},
        assignments(args.tested_config),
        repository=args.repository,
        version=args.version,
        commit=args.commit,
        output=args.output,
    )


if __name__ == "__main__":
    main()

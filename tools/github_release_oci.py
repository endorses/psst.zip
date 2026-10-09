"""Create runner-owned OCI layouts from already validated native exports.

Copy only verified content bytes, never tar ownership, permissions, or paths.
The canonical index selects the exact runnable child on every publisher host.
"""

from __future__ import annotations

import hashlib
from io import BytesIO
import os
from pathlib import Path
import stat
import tarfile

from assemble_release_oci import StrictTarInfo
from publish_container_release import sha256, source_digest
from release_artifacts import json_bytes, read_json, require


def stage_verified_oci(
    archive: Path, destination: Path, correspondence: dict
) -> tuple[tuple[str, str, int], ...]:
    """Stage a private canonical layout after inspect_archive established trust."""
    destination.mkdir(mode=0o700)
    (destination / "blobs").mkdir(mode=0o700)
    (destination / "blobs/sha256").mkdir(mode=0o700)
    inventory = []

    def write(name, stream, size, expected):
        path = destination / name
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        digest, length = hashlib.sha256(), 0
        with os.fdopen(fd, "wb") as output:
            while block := stream.read(min(1024 * 1024, size - length + 1)):
                length += len(block)
                require(length <= size, "OCI staging payload exceeds descriptor")
                digest.update(block)
                output.write(block)
        actual = "sha256:" + digest.hexdigest()
        require(length == size and actual == expected, "OCI staging payload changed")
        inventory.append((name, actual, length))

    def metadata(name, raw):
        write(name, BytesIO(raw), len(raw), sha256(raw))

    child_digest = correspondence["manifest_digest"]
    child_name = "blobs/sha256/" + child_digest[7:]
    with tarfile.open(archive, "r:", tarinfo=StrictTarInfo) as source:
        stream = source.extractfile(child_name)
        require(stream is not None, "Missing verified OCI child during staging")
        with stream:
            raw = stream.read(correspondence["manifest_size"] + 1)
        require(
            len(raw) == correspondence["manifest_size"] and sha256(raw) == child_digest,
            "Verified OCI child changed during staging",
        )
        image = read_json(raw)
        descriptors = [
            {
                "digest": child_digest,
                "size": correspondence["manifest_size"],
            },
            image["config"],
            *image["layers"],
        ]
        copied = set()
        for descriptor in descriptors:
            digest = descriptor["digest"]
            if digest in copied:
                continue
            copied.add(digest)
            name = "blobs/sha256/" + digest[7:]
            member = source.getmember(name)
            require(
                member.isfile() and member.size == descriptor["size"],
                "Verified OCI payload changed during staging",
            )
            stream = source.extractfile(member)
            require(stream is not None, "Missing OCI payload during staging")
            with stream:
                write(name, stream, descriptor["size"], digest)
    metadata("oci-layout", json_bytes({"imageLayoutVersion": "1.0.0"}))
    metadata(
        "index.json",
        json_bytes(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.oci.image.index.v1+json",
                "manifests": [
                    {
                        "mediaType": correspondence["manifest_media_type"],
                        "digest": child_digest,
                        "size": correspondence["manifest_size"],
                        "platform": {
                            "os": "linux",
                            "architecture": correspondence["platform"].split("/")[1],
                        },
                    }
                ],
            }
        ),
    )
    require(
        source_digest(archive) == correspondence["archive_digest"],
        "OCI archive changed during staging",
    )
    return tuple(sorted(inventory))


def validate_staged_oci(
    directory: Path, inventory: tuple[tuple[str, str, int], ...]
) -> None:
    """Reject substitutions, links, or extra files immediately before publication."""
    expected = {name for name, _, _ in inventory}
    seen = set()
    for root, directories, files in os.walk(directory, followlinks=False):
        parent = Path(root)
        for path in [parent, *(parent / name for name in directories)]:
            info = path.lstat()
            require(
                stat.S_ISDIR(info.st_mode)
                and info.st_uid == os.getuid()
                and stat.S_IMODE(info.st_mode) == 0o700,
                "Unsafe staged OCI directory",
            )
            require(
                path == directory
                or path.relative_to(directory).as_posix() in {"blobs", "blobs/sha256"},
                "Unexpected staged OCI directory",
            )
        for name in files:
            path = parent / name
            relative = path.relative_to(directory).as_posix()
            info = path.lstat()
            require(
                relative in expected
                and stat.S_ISREG(info.st_mode)
                and info.st_uid == os.getuid()
                and stat.S_IMODE(info.st_mode) == 0o600
                and info.st_nlink == 1,
                "Unsafe staged OCI payload",
            )
            seen.add(relative)
    require(seen == expected, "Staged OCI inventory changed")
    for name, digest, size in inventory:
        path = directory / name
        require(
            path.stat().st_size == size and source_digest(path) == digest,
            "Staged OCI payload changed before push",
        )

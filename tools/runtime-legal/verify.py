#!/usr/bin/env python3
"""Verify pinned notice evidence; never approve a runtime distribution."""

from __future__ import annotations

import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile

ROOT = Path(__file__).resolve().parent
LIMIT = 16 * 1024 * 1024
SCOPED_ORIGINS = {"alpine-base", "alpine-baselayout", "alpine-keys", "ca-certificates"}
COMMIT = re.compile(r"[0-9a-f]{40}\Z")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def read_local(root: Path, name: str, limit: int = LIMIT) -> bytes:
    relative = PurePosixPath(name)
    require(
        isinstance(name, str)
        and bool(name)
        and not relative.is_absolute()
        and ".." not in relative.parts
        and "\\" not in name,
        "Unsafe evidence path",
    )
    path = root / name
    require(path.resolve().is_relative_to(root.resolve()), "Evidence path escaped root")
    require(path.is_file() and not path.is_symlink(), "Missing evidence file")
    require(path.stat().st_size <= limit, "Evidence file exceeds bounds")
    return path.read_bytes()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def key(item: dict) -> tuple[str, str, str]:
    return item["origin"], item["version"], item["aports_commit"]


def verify_evidence(root: Path = ROOT) -> dict:
    review = json.loads(read_local(root, "review.json", 1024 * 1024))
    require(review["schema_version"] == 1, "Unsupported review schema")
    require(review["review_required"] is True, "Evidence cannot approve distribution")
    require(bool(review["remaining_distribution_gates"]), "Distribution gates missing")
    documents = {}
    for document in review["documents"]:
        name = document["path"]
        require(name not in documents, "Duplicate evidence document")
        data = read_local(root, name)
        require(
            len(data) == document["size"] and sha256(data) == document["sha256"],
            f"Evidence checksum differs: {name}",
        )
        if "git_blob_sha1" in document:
            blob = hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data)
            require(blob.hexdigest() == document["git_blob_sha1"], "Git blob differs")
        documents[name] = document
    origins = {}
    for origin in review["origins"]:
        require(origin["origin"] in SCOPED_ORIGINS, "Unexpected review origin")
        require(COMMIT.fullmatch(origin["aports_commit"]) is not None, "Invalid commit")
        require(key(origin) not in origins, "Duplicate reviewed origin revision")
        require(origin["review_required"] is True, "Origin review cannot be waived")
        require(bool(origin["remaining_review"]), "Origin review findings missing")
        require(origin["recipe_path"] in documents, "Recipe evidence missing")
        for notice in origin["notice_paths"]:
            require(notice in documents, "Referenced notice missing")
        origins[key(origin)] = origin
    require(
        {item[0] for item in origins} == SCOPED_ORIGINS,
        "Missing scoped runtime origin",
    )
    for image in review["base_images"]:
        require(
            image["architecture"] == "amd64", "Unexpected reviewed base architecture"
        )
        require("@sha256:" in image["base_ref"], "Mutable base reference")
        for package in image["packages"]:
            require(key(package) in origins, "Base package lacks matching review")
            require(
                package["license"] == origins[key(package)]["declared_license"],
                "Base license declaration differs",
            )
    return review


def archive_member(archive: bytes, name: str, basename: bool = False) -> bytes:
    require(len(archive) <= LIMIT, "Scoped source archive exceeds bounds")
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:*") as source:
        found = []
        expanded = 0
        for count, member in enumerate(source, 1):
            expanded += member.size
            require(
                count <= 2048 and expanded <= LIMIT, "Scoped archive exceeds bounds"
            )
            path = PurePosixPath(member.name)
            require(
                not path.is_absolute()
                and ".." not in path.parts
                and "\\" not in member.name,
                "Unsafe source member path",
            )
            if (PurePosixPath(member.name).name if basename else member.name) == name:
                require(
                    member.isfile() and member.size <= LIMIT, "Invalid source member"
                )
                found.append(source.extractfile(member).read())
        require(len(found) == 1, "Missing or ambiguous reviewed source member")
        return found[0]


def verify_collection(root: Path, collection: Path, review: dict) -> int:
    inventory = json.loads(read_local(collection, "runtime-inventory.json"))
    require(inventory["review_required"] is True, "Collection approval is unsupported")
    origins = {key(item): item for item in review["origins"]}
    documents = {item["path"]: item for item in review["documents"]}
    sources = {}
    for source in inventory["sources"]:
        if source["origin"] not in SCOPED_ORIGINS:
            continue
        identity = key(source)
        require(identity not in sources, "Duplicate collected source revision")
        require(identity in origins, f"Unreviewed runtime source revision: {identity}")
        reviewed = origins[identity]
        relative = PurePosixPath(source["source_file"])
        data = read_local(collection, str(relative))
        require(
            sha256(data) == source["source_sha256"], "Collected source checksum differs"
        )
        recipe = read_local(
            collection, str(relative.parent.parent / "recipe" / "APKBUILD")
        )
        require(
            sha256(recipe) == documents[reviewed["recipe_path"]]["sha256"],
            "Collected recipe differs from reviewed revision",
        )
        for expected in reviewed["recipe_files"]:
            content = read_local(
                collection,
                str(relative.parent.parent / "recipe" / expected["name"]),
            )
            require(
                len(content) == expected["size"]
                and sha256(content) == expected["sha256"],
                "Collected recipe helper differs from reviewed revision",
            )
        for expected in reviewed["external_inputs"]:
            original = archive_member(data, expected["name"], basename=True)
            require(
                hashlib.sha512(original).hexdigest() == expected["sha512"],
                "Original source input differs from reviewed recipe",
            )
            require(
                sha256(original) == expected["sha256"],
                "Original source checksum differs",
            )
            for member in expected.get("members", []):
                content = archive_member(original, member["name"])
                require(
                    len(content) == member["size"]
                    and sha256(content) == member["sha256"],
                    "Embedded notice source member differs",
                )
                notice = read_local(root, member["notice_path"])
                start = member["notice_start"]
                require(
                    content[start : start + member["notice_length"]] == notice,
                    "Embedded notice differs from source comment",
                )
        sources[identity] = source
    require(bool(sources), "Collection has no scoped source revisions")
    for package in inventory["packages"]:
        if package["origin"] in SCOPED_ORIGINS:
            require(
                key(package) in sources,
                "Scoped package lacks matching collected source",
            )
            require(
                package["license"] == origins[key(package)]["declared_license"],
                "Collected package license declaration differs",
            )
    return len(sources)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--collection", type=Path, help="Match a fresh collector output read-only"
    )
    parser.add_argument(
        "--require-complete",
        action="store_true",
        help="Fail while distribution review is pending",
    )
    args = parser.parse_args()
    try:
        review = verify_evidence()
        matched = (
            verify_collection(ROOT, args.collection, review) if args.collection else 0
        )
        require(
            not args.require_complete, "Runtime distribution review remains incomplete"
        )
        print(
            json.dumps(
                {
                    "verified_documents": len(review["documents"]),
                    "matched_source_revisions": matched,
                    "review_required": True,
                }
            )
        )
    except (OSError, KeyError, TypeError, ValueError, tarfile.TarError) as error:
        parser.exit(1, f"Runtime legal evidence rejected: {error}\n")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Replay exact runtime source assets against tested OCI bytes; never approve distribution."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import tempfile

from assemble_release_oci import inspect_archive
import collect_caddy_sources as caddy
from collect_runtime_notices import command, packages, safe_member, source_notices
import package_runtime_sources as pack
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    InvalidRelease,
    json_bytes,
    matches,
    require,
)

LIMIT = 2 * 1024**3
MEMBER_LIMIT = 512 * 1024**2


def extract_source_asset(asset: Path, destination: Path, expected: str) -> None:
    matches(expected, DIGEST, "Missing externally bound runtime source digest")
    require(asset.is_file() and not asset.is_symlink(), "Unsafe runtime source asset")
    require(0 < asset.stat().st_size <= LIMIT, "Runtime source asset exceeds bounds")
    require(
        "sha256:" + pack.file_hash(asset) == expected,
        "Bound runtime source asset differs",
    )
    total, seen = 0, set()
    with tarfile.open(asset, "r:gz") as archive:
        for member in archive:
            relative = safe_member(member.name)
            require(
                str(relative) == member.name and member.name not in seen,
                "Duplicate or noncanonical source asset member",
            )
            require(
                len(seen) < 100_000 and member.isfile() and not member.sparse,
                "Unsupported source asset member",
            )
            require(
                0 <= member.size <= MEMBER_LIMIT, "Source asset member exceeds bounds"
            )
            total += member.size
            require(total <= LIMIT, "Expanded source asset exceeds bounds")
            seen.add(member.name)
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            with archive.extractfile(member) as stream, target.open("xb") as output:
                while block := stream.read(65536):
                    output.write(block)
            require(
                target.stat().st_size == member.size, "Truncated source asset member"
            )
    require(
        "sha256:" + pack.file_hash(asset) == expected,
        "Runtime source asset changed during replay",
    )


def check_recipe_inputs(folder: Path, inventory: dict) -> None:
    for source in inventory["sources"]:
        relative = safe_member(source["source_file"])
        recipe = folder / relative.parents[2] / "recipe"
        original = folder / relative
        text = pack.read(recipe, "APKBUILD").decode()
        declaration = re.search(r'^sha512sums=(["\'])(.*?)\1\s*$', text, re.M | re.S)
        expected = declaration.group(2) if declaration else ""
        require(
            expected.strip() == source["source_sha512sums"].strip(),
            "Source checksum table differs from retained APKBUILD",
        )
        checksummed = {
            line.split()[-1] for line in expected.splitlines() if line.strip()
        }
        with tarfile.open(original) as source_archive:
            members = {member.name for member in source_archive if member.isfile()}
        for path in recipe.rglob("*"):
            require(not path.is_symlink(), "Symlinked recipe input")
            if path.is_file():
                name = "recipe/" + path.relative_to(recipe).as_posix()
                # Full aports recipes can retain unused patches alongside the
                # APKBUILD. abuild srcpkg includes its declared source inputs;
                # unused recipe files remain separately in this exact asset.
                if name not in members:
                    require(
                        path.name not in checksummed,
                        "Checksummed recipe helper missing from source package",
                    )
                    continue
                require(
                    caddy.archive_member(original, name, exact=True)
                    == path.read_bytes(),
                    "Source package lacks exact retained recipe/helper input",
                )


def whiteout_targets(name: str, tracked: set[str]) -> set[str]:
    path = PurePosixPath(name)
    if path.name == ".wh..wh..opq":
        prefix = "" if str(path.parent) == "." else str(path.parent)
        return {
            target
            for target in tracked
            if not prefix or target.startswith(prefix + "/")
        }
    if path.name.startswith(".wh."):
        removed = str(path.parent / path.name[4:])
        return {
            target
            for target in tracked
            if target == removed or target.startswith(removed + "/")
        }
    return set()


def replay_image(
    path: Path,
    component: str,
    binding: dict,
    inventory: dict,
    overlay: dict,
    additional: dict,
    identity: dict,
    tested_config: str,
) -> tuple[dict, bytes]:
    verified = inspect_archive(
        path,
        platform="linux/" + identity["architecture"],
        repository=identity["repository"],
        version=identity["version"],
        commit=identity["revision"],
        tested_config=tested_config,
        component=component,
    )
    binary_path = binding["binary_path"].lstrip("/")
    target = (
        "app/licenses/runtime/"
        if component == "backend"
        else "srv/web/licenses/runtime/"
    )
    expected_files = {target + name: checksum for name, checksum in overlay.items()}
    expected_files.update(
        {name.lstrip("/"): checksum for name, checksum in additional.items()}
    )
    tracked = set(expected_files) | {binary_path, "lib/apk/db/installed"}
    found, retained, databases, binary = {}, {}, [], None
    with tarfile.open(path, "r:") as archive:
        manifest = json.load(
            archive.extractfile("blobs/sha256/" + verified["manifest_digest"][7:])
        )
        config = json.load(
            archive.extractfile("blobs/sha256/" + verified["config_digest"][7:])
        )
        diff_ids = config["rootfs"]["diff_ids"]
        require(
            diff_ids[: len(binding["rootfs_layers"])] == binding["rootfs_layers"],
            "OCI image inherited runtime layers differ",
        )
        require(
            pack.digest(json_bytes(config["config"]))
            == binding["runtime_config_sha256"],
            "OCI runtime configuration differs from source binding",
        )
        for index, descriptor in enumerate(manifest["layers"]):
            raw = archive.extractfile("blobs/sha256/" + descriptor["digest"][7:])
            decoded = (
                gzip.GzipFile(fileobj=raw)
                if descriptor["mediaType"].endswith("gzip")
                else raw
            )
            with raw, decoded, tarfile.open(fileobj=decoded, mode="r|*") as layer:
                seen, expanded = set(), 0
                for entry in layer:
                    name = str(safe_member(entry.name))
                    require(
                        name not in seen and len(seen) < 1_000_000,
                        "Duplicate or oversized OCI layer file inventory",
                    )
                    seen.add(name)
                    expanded += entry.size
                    require(
                        expanded <= LIMIT and entry.size <= MEMBER_LIMIT,
                        "Expanded OCI layer exceeds bounds",
                    )
                    for deleted in whiteout_targets(name, tracked):
                        found.pop(deleted, None)
                        if deleted == binary_path:
                            binary = None
                    if any(target.startswith(name + "/") for target in tracked):
                        require(
                            entry.isdir(),
                            "Tracked runtime path ancestor is not a directory",
                        )
                    if name not in tracked:
                        continue
                    require(
                        entry.isfile() and not entry.sparse,
                        "Tracked runtime file is not regular",
                    )
                    data = layer.extractfile(entry).read()
                    found[name] = pack.digest(data)
                    if name == binary_path:
                        binary = data
                    if name == "lib/apk/db/installed":
                        rows = packages(data)
                        for row in rows:
                            retained[
                                (row["name"], row["version"], row["aports_commit"])
                            ] = row
                        databases.append(
                            {
                                "layer": index,
                                "layer_sha256": diff_ids[index][7:],
                                "database_sha256": pack.digest(data),
                            }
                        )
                        final = pack.graph(rows)
    require(
        databases == inventory["layer_databases"],
        "Actual retained APK layer databases differ",
    )
    require(
        pack.graph(list(retained.values()))
        == pack.graph(inventory["packages"])
        == binding["retained_packages"],
        "Actual retained package graph lacks exact source coverage",
    )
    require(
        pack.digest(json_bytes(binding["retained_packages"]))
        == binding["retained_graph_sha256"],
        "Retained graph fingerprint differs",
    )
    require(final == binding["final_packages"], "Final runtime package graph differs")
    require(
        found.get("lib/apk/db/installed")
        == pack.digest(
            pack.read(
                identity["source_root"] / (component + "-runtime"), "installed-apk-db"
            )
        ),
        "Final OCI package database differs from source asset",
    )
    require(
        binary is not None and pack.digest(binary) == binding["binary_sha256"],
        "Final OCI executable differs from corresponding source binding",
    )
    require(
        all(found.get(name) == checksum for name, checksum in expected_files.items()),
        "Final OCI notice/source/discovery bytes differ",
    )
    require(
        "sha256:" + pack.file_hash(path) == verified["archive_digest"],
        "OCI archive changed during source replay",
    )
    return verified, binary


def verify_caddy_collection(
    folder: Path, cosign: Path, binary: bytes, binding: dict
) -> tuple[dict, dict, bytes, dict[str, bytes]]:
    inventory = json.loads(pack.read(folder, "caddy-source-inventory.json"))
    require(
        inventory["image_id"] == binding["original_image_id"],
        "Caddy source collection belongs to another runtime",
    )
    signatures = pack.verify_caddy_signatures(folder, cosign)
    require(
        not inventory["nested_non_archives_requiring_review"],
        "Unreviewed Caddy source fixture",
    )
    go_notices = caddy.verify_go_source(folder, inventory)
    go_archive_name = inventory["go_source"]["file"]
    notices = {}
    for asset in inventory["sources"]:
        name = asset["file"]
        path = folder / safe_member(name)
        require(
            pack.file_hash(path) == asset["sha256"],
            "Caddy retained source asset differs",
        )
        if name == go_archive_name:
            notices.update(
                {name + "::" + member: data for member, data in go_notices.items()}
            )
        elif name.endswith(".tar.gz") and "_linux_" not in name:
            invalid = []
            notices.update(
                {
                    name + "::" + member: data
                    for member, data in source_notices(
                        path, non_archives=invalid
                    ).items()
                }
            )
            require(not invalid, "Unreviewed Caddy nested source fixture")
        elif name.startswith("go-"):
            notices[name] = pack.read(folder, name)
    require(
        {name: pack.digest(data) for name, data in notices.items()}
        == inventory["notices"],
        "Caddy notice attribution differs from original sources",
    )
    full = b"psst.zip Caddy runtime notices\n" + b"\n".join(
        name.encode() + b"\n" + data for name, data in sorted(notices.items())
    )
    require(
        pack.read(folder, "THIRD_PARTY_NOTICES.txt", 64 * 1024**2) == full,
        "Caddy full supplied notices differ",
    )
    short = inventory["version"][1:]
    binary_archive = folder / f"caddy_{short}_linux_{binding['architecture']}.tar.gz"
    require(
        caddy.archive_member(binary_archive, "caddy", MEMBER_LIMIT) == binary,
        "Runtime Caddy executable differs from signed upstream archive",
    )
    require(
        pack.file_hash(folder / "build-info.json") == inventory["build_info_sha256"],
        "Recorded Caddy build information differs",
    )
    actual = caddy.binary_build_info(binary)
    expected = json.loads(pack.read(folder, "build-info.json"))
    require(actual == expected, "Actual Caddy executable module/build metadata differs")
    caddy.verify_binary_go_source(actual, inventory)
    source = folder / f"caddy_{short}_buildable-artifact.tar.gz"
    caddy.verify_modules(
        actual,
        caddy.archive_member(source, "go.sum", exact=True),
        caddy.archive_member(source, "vendor/modules.txt", exact=True),
    )
    settings = {item["Key"]: item["Value"] for item in actual["Settings"]}
    require(
        actual["GoVersion"] == inventory["go_version"]
        and settings.get("vcs.revision") == inventory["source_revision"]
        and settings.get("vcs.modified") == "false",
        "Caddy source/toolchain revision differs",
    )
    return inventory, signatures, full, go_notices


def verify(
    pack_root: Path,
    archives: dict[str, Path],
    smoke_path: Path,
    cosign: Path,
    *,
    repository: str,
    version: str,
    revision: str,
    source_sha256: str,
) -> dict:
    matches(version, VERSION, "Invalid release version")
    matches(revision, COMMIT, "Invalid source revision")
    manifest_raw = pack.read(pack_root, "runtime-pack.json")
    manifest = json.loads(manifest_raw)
    require(
        manifest["schema_version"] == 1
        and manifest["version"] == version
        and manifest["revision"] == revision
        and manifest["architecture"] in {"amd64", "arm64"},
        "Runtime pack release identity differs",
    )
    asset_record = manifest["source_asset"]
    require(
        "sha256:" + asset_record["sha256"] == source_sha256,
        "Pack metadata differs from externally bound source asset",
    )
    asset = pack_root / safe_member(asset_record["file"])
    require(
        asset.stat().st_size == asset_record["size"],
        "Runtime source asset size differs",
    )
    smoke_raw = pack.read(smoke_path.parent, smoke_path.name)
    smoke = json.loads(smoke_raw)
    require(
        smoke["schema_version"] == 1
        and smoke["kind"] == "release-image-smoke"
        and smoke["execution"] == "native"
        and smoke["platform"] == "linux/" + manifest["architecture"]
        and smoke["version"] == version
        and smoke["revision"] == revision
        and smoke["runtime_pack_sha256"] == "sha256:" + pack.digest(manifest_raw),
        "Native smoke measurement does not bind this source pack",
    )
    require(
        "runtime-offer" in smoke["checks"],
        "Runtime source discovery was not smoke-tested",
    )
    with tempfile.TemporaryDirectory(prefix="psst-runtime-source-replay-") as temporary:
        root = Path(temporary)
        extract_source_asset(asset, root, source_sha256)
        go_policy_bytes = pack.read(
            caddy.GO_SOURCE_POLICY.parent, caddy.GO_SOURCE_POLICY.name
        )
        require(
            pack.read(root, "go-runtime-sources.json") == go_policy_bytes,
            "Archived Go runtime source policy differs from trusted selected source",
        )
        instructions = pack.read(root, "SOURCE.md").decode()
        require(
            all(
                value in instructions
                for value in [
                    "Release: " + version,
                    "Application source revision: " + revision,
                    "Architecture: linux/" + manifest["architecture"],
                    asset_record["url"],
                    "runtime-inventory.json",
                    "APKBUILD",
                    "caddy/build-info.json",
                ]
            ),
            "Runtime source rebuilding instructions are missing or substituted",
        )
        require(
            pack.read(root, "runtime-sources.Dockerfile")
            == (pack.ROOT / "tools/runtime-sources.Dockerfile").read_bytes(),
            "Recorded source collector build recipe differs",
        )
        require(
            json.loads(pack.read(root, "image-bindings.json")) == manifest["bindings"],
            "Archived image binding differs from pack",
        )
        archived_review = pack.REVIEW.verify_evidence(root / "legal-review")
        review = pack.REVIEW.verify_evidence()
        require(
            archived_review == review,
            "Archived legal review differs from trusted exact-source evidence",
        )
        for doc in review["documents"]:
            require(
                pack.read(root / "legal-review", doc["path"])
                == pack.read(pack.LEGAL, doc["path"]),
                "Archived notice evidence bytes differ",
            )
        standards = {
            doc["license_id"]: doc for doc in review["documents"] if "license_id" in doc
        }
        identity = {
            "repository": repository,
            "version": version,
            "revision": revision,
            "architecture": manifest["architecture"],
            "source_root": root,
        }
        inventories, notices, image_results, binaries = {}, {}, {}, {}
        for component in ["backend", "web"]:
            folder = root / (component + "-runtime")
            inventories[component], notices[component] = pack.verify_apk_collection(
                folder, standards, review
            )
            require(
                inventories[component]["image_id"]
                == manifest["bindings"][component]["original_image_id"]
                and inventories[component]["architecture"] == manifest["architecture"],
                "Runtime collection binding differs",
            )
            check_recipe_inputs(folder, inventories[component])
            image_results[component], binaries[component] = replay_image(
                archives[component],
                component,
                manifest["bindings"][component],
                inventories[component],
                manifest["overlays"][component],
                manifest["additional_files"][component],
                identity,
                smoke["tested_configs"][component],
            )
        caddy_inventory, signatures, caddy_notices, go_notices = (
            verify_caddy_collection(
                root / "caddy",
                cosign,
                binaries["web"],
                {
                    **manifest["bindings"]["web"],
                    "architecture": manifest["architecture"],
                },
            )
        )
        backend_build = caddy.binary_build_info(binaries["backend"])
        backend_go_inventory, backend_go_notices = pack.backend_go_runtime(
            root, caddy_inventory, backend_build["GoVersion"]
        )
        require(
            manifest["bindings"]["backend"].get("go_version")
            == backend_build["GoVersion"]
            and manifest["bindings"]["web"].get("go_version")
            == caddy_inventory["go_version"],
            "Retained executable GoVersion bindings differ from actual OCI binaries",
        )
        provenance = pack.artifact_provenance(
            inventories["backend"], inventories["web"], caddy_inventory, signatures
        )
        require(
            json.loads(pack.read(root, "artifact-provenance.json"))
            == provenance
            == manifest["artifact_provenance"],
            "Per-artifact source/signature treatment differs",
        )
        combined = (
            notices["backend"]
            + b"\nWeb image runtime\n"
            + notices["web"]
            + b"\nCaddy, vendored modules and Go runtime\n"
            + caddy_notices
        )
        backend_notices = (
            notices["backend"]
            + b"\nGo runtime\n"
            + caddy.go_notice_text(backend_go_notices)
        )
        for component, full in [("backend", backend_notices), ("web", combined)]:
            require(
                pack.digest(full)
                == manifest["overlays"][component]["THIRD_PARTY_NOTICES.txt"],
                "Source-derived full notices differ from final overlay",
            )
        require(
            pack.read(caddy.GO_SOURCE_POLICY.parent, caddy.GO_SOURCE_POLICY.name)
            == go_policy_bytes,
            "Trusted Go runtime source policy changed during replay",
        )
    require(
        "sha256:" + pack.file_hash(asset) == source_sha256,
        "Runtime source asset changed after verification",
    )
    require(
        pack.read(pack_root, "runtime-pack.json") == manifest_raw
        and pack.read(smoke_path.parent, smoke_path.name) == smoke_raw,
        "Source pack or native smoke measurement changed during verification",
    )
    for component, archive in archives.items():
        require(
            "sha256:" + pack.file_hash(archive)
            == image_results[component]["archive_digest"],
            "OCI archive changed after source verification",
        )
    return {
        "schema_version": 1,
        "kind": "runtime-source-completeness",
        "verified_at": datetime.now(timezone.utc).isoformat(),
        "repository": repository,
        "version": version,
        "revision": revision,
        "platform": "linux/" + manifest["architecture"],
        "runtime_source_asset_sha256": source_sha256,
        "runtime_pack_sha256": "sha256:" + pack.digest(manifest_raw),
        "native_smoke_report_sha256": "sha256:" + pack.digest(smoke_raw),
        "images": image_results,
        "coverage": {
            "backend_origins": len(inventories["backend"]["sources"]),
            "web_origins": len(inventories["web"]["sources"]),
            "retained_package_versions": sum(
                len(v["packages"]) for v in inventories.values()
            ),
            "go_runtime": {
                "sources": {
                    component: {
                        "version": inventory["go_version"],
                        "commit": inventory["go_source_revision"],
                        "archive_sha256": "sha256:" + inventory["go_source"]["sha256"],
                    }
                    for component, inventory in (
                        ("backend", backend_go_inventory),
                        ("web", caddy_inventory),
                    )
                },
                "executables": {
                    "backend": backend_build["GoVersion"],
                    "web": caddy_inventory["go_version"],
                },
            },
        },
        "caddy_signature_verification": signatures,
        "runtime_source_inputs_verified": True,
        "application_source_verified": False,
        "apk_binary_signatures_verified": False,
        "source_publication_verified": False,
        "distribution_authorized": False,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in [
        "pack",
        "backend-archive",
        "web-archive",
        "smoke-report",
        "cosign",
        "output",
    ]:
        parser.add_argument("--" + name, required=True, type=Path)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--source-sha256", required=True)
    args = parser.parse_args()
    try:
        require(
            not args.output.exists() and not args.output.is_symlink(),
            "Never replace source verification evidence",
        )
        result = verify(
            args.pack,
            {"backend": args.backend_archive, "web": args.web_archive},
            args.smoke_report,
            args.cosign,
            repository=args.repository,
            version=args.version,
            revision=args.revision,
            source_sha256=args.source_sha256,
        )
        with args.output.open("xb") as stream:
            stream.write(json_bytes(result))
        print(
            json.dumps(
                {
                    "runtime_source_inputs_verified": True,
                    "distribution_authorized": False,
                }
            )
        )
    except (
        InvalidRelease,
        OSError,
        ValueError,
        KeyError,
        TypeError,
        tarfile.TarError,
        subprocess.TimeoutExpired,
    ) as error:
        parser.exit(1, f"Runtime source replay rejected: {error}\n")


if __name__ == "__main__":
    main()

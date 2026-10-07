#!/usr/bin/env python3
"""Package exact collected runtime sources and legal overlays, without publication.

The source asset covers every retained origin revision. Overlay verification
checks the inherited layers, runtime configuration, APK database and executables.
The result is preparation evidence; source delivery/publication remains separate.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.parse

from collect_runtime_notices import (
    command,
    file_hash,
    image_info,
    layer_packages,
    packages,
    recipe_notices,
    safe_member,
    source_notices,
    verify_source_package,
)
from collect_caddy_sources import archive_member
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    InvalidRelease,
    json_bytes,
    matches,
    require,
)
from verify_caddy_source_signatures import verify as verify_caddy_signatures

ROOT = Path(__file__).resolve().parents[1]
LEGAL = ROOT / "tools/runtime-legal"
SPEC = importlib.util.spec_from_file_location(
    "runtime_legal_evidence", LEGAL / "verify.py"
)
REVIEW = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REVIEW)
FIELDS = (
    "name",
    "version",
    "architecture",
    "license",
    "origin",
    "aports_commit",
    "apk_checksum",
)
DECLARATION = re.compile(r"[A-Za-z0-9.+-]+|[()]")
PUBLIC_DOMAIN = {"Public-Domain", "Public Domain"}
FIXTURES = {
    "busybox-1.37.0/testsuite/unzip_bad_lzma_1.zip": "6917569008ed8665c736b1d5b2dbbf8d863edac2478a1f05e998d535dea5b017",
    "busybox-1.37.0/testsuite/unzip_bad_lzma_2.zip": "e41016555fae6a69548ace9acbafb65c060b9b2c9066724724e4082aaf3446a7",
}


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def read(root: Path, relative: str, limit: int = 8 * 1024 * 1024) -> bytes:
    path = root / safe_member(relative)
    require(
        path.resolve().is_relative_to(root.resolve()), "Collection path escaped root"
    )
    require(path.is_file() and not path.is_symlink(), "Missing regular collection file")
    require(path.stat().st_size <= limit, "Collection file exceeds bounds")
    return path.read_bytes()


def graph(rows: list[dict]) -> list[dict]:
    records = [{field: row[field] for field in FIELDS} for row in rows]
    identities = [
        tuple(record[field] for field in FIELDS[:2] + ("aports_commit",))
        for record in records
    ]
    require(
        len(set(identities)) == len(identities), "Duplicate retained package version"
    )
    return sorted(records, key=lambda row: tuple(row[field] for field in FIELDS))


def source_identity(record: dict) -> tuple[str, str, str]:
    return record["origin"], record["version"], record["aports_commit"]


def license_ids(expression: str, documents: dict[str, dict]) -> list[str]:
    if expression in PUBLIC_DOMAIN:
        return []
    tokens = DECLARATION.findall(expression)
    require(
        "".join(tokens) == re.sub(r"\s+", "", expression),
        "Unsupported runtime license expression",
    )
    position = 0
    identifiers = set()

    def atom():
        nonlocal position
        require(position < len(tokens), "Incomplete runtime license expression")
        token = tokens[position]
        position += 1
        if token == "(":
            expression_group()
            require(
                position < len(tokens) and tokens[position] == ")",
                "Unbalanced runtime license expression",
            )
            position += 1
        else:
            require(token in documents, "Runtime license terms require review")
            identifiers.add(token)

    def expression_group():
        nonlocal position
        atom()
        while position < len(tokens) and tokens[position] in {"AND", "OR"}:
            position += 1
            atom()

    expression_group()
    require(position == len(tokens), "Unsupported runtime license expression syntax")
    return sorted(identifiers)


def reviewed_fixtures(origin: str, entries: list[dict]) -> None:
    for entry in entries:
        require(
            origin == "busybox"
            and FIXTURES.get(entry.get("path")) == entry.get("sha256"),
            "Unreviewed malformed nested source archive",
        )


def verify_apk_collection(
    folder: Path, documents: dict[str, dict], review: dict
) -> tuple[dict, bytes]:
    inventory = json.loads(read(folder, "runtime-inventory.json"))
    require(
        inventory.get("schema_version") == 1
        and inventory.get("review_required") is True,
        "Unsupported runtime collection",
    )
    matches(inventory.get("image_id"), DIGEST, "Runtime image identity missing")
    require(
        inventory.get("architecture") in {"amd64", "arm64"},
        "Unsupported runtime architecture",
    )
    require(bool(inventory.get("layer_databases")), "Retained layer accounting missing")
    rows = graph(inventory["packages"])
    source_keys = {source_identity(row) for row in rows}
    sources = {}
    notice_blocks = []
    for source in inventory["sources"]:
        identity = source_identity(source)
        require(identity not in sources, "Duplicate retained source revision")
        require(identity in source_keys, "Collected source has no retained package")
        relative = safe_member(source["source_file"])
        archive = folder / relative
        require(
            archive.resolve().is_relative_to(folder.resolve())
            and archive.is_file()
            and not archive.is_symlink(),
            "Unsafe original source archive",
        )
        require(
            file_hash(archive) == source["source_sha256"],
            "Original collected source archive differs",
        )
        recipe = folder / relative.parents[2] / "recipe"
        require(recipe.is_dir() and not recipe.is_symlink(), "Runtime recipe missing")
        recipe_text = read(recipe, "APKBUILD")
        require(
            archive_member(archive, "recipe/APKBUILD", exact=True) == recipe_text,
            "Retained source package recipe differs from original recipe",
        )
        require(
            re.search(rb'^license=["\']([^"\']+)["\']$', recipe_text, re.M) is not None,
            "Runtime recipe license declaration missing",
        )
        verify_source_package(archive, source["source_sha512sums"])
        invalid = []
        notices = source_notices(archive, non_archives=invalid)
        notices.update(recipe_notices(recipe))
        require(
            {name: digest(content) for name, content in notices.items()}
            == source["notices"],
            "Collected notice bytes differ from original source",
        )
        require(
            invalid == source["nested_non_archives_requiring_review"],
            "Nested source fixture accounting differs",
        )
        reviewed_fixtures(source["origin"], invalid)
        declarations = sorted(
            {row["license"] for row in rows if source_identity(row) == identity}
        )
        require(
            all(
                row["name"] in source["declared_packages"]
                for row in rows
                if source_identity(row) == identity
            ),
            "Undeclared retained runtime package",
        )
        identifiers = sorted(
            {
                name
                for declaration in declarations
                for name in license_ids(declaration, documents)
            }
        )
        header = f"\n{source['origin']} {source['version']}\nPackaging commit: {source['aports_commit']}\nDeclared licenses: {'; '.join(declarations)}\n"
        notice_blocks.append(header.encode())
        for name, content in sorted(notices.items()):
            notice_blocks.append(f"\n{name}\n".encode() + content)
        require(
            notices or source["origin"] in REVIEW.SCOPED_ORIGINS,
            "Origin without supplied notice evidence requires review",
        )
        for identifier in identifiers:
            notice_blocks.append(
                f"\nFull {identifier} terms (standard reference; original attributions above or in retained source)\n".encode()
                + read(LEGAL, documents[identifier]["path"])
            )
        sources[identity] = source
    require(
        set(sources) == source_keys,
        "Retained runtime graph has missing source revisions",
    )
    REVIEW.verify_collection(LEGAL, folder, review)
    supplements = sorted(
        {
            path
            for record in review["origins"]
            if source_identity(record) in source_keys
            for path in record["notice_paths"]
        }
    )
    for path in supplements:
        notice_blocks.append(
            f"\nSupplemental exact-source notice / license reference: {path}\n".encode()
            + read(LEGAL, path)
        )
    notice_blocks.append(
        b"\nAlpine release metadata and public signing-key notice treatment: preserve the upstream MIT declaration, full MIT terms and every supplied notice/recipe/source input. No copyright owner was supplied for these data origins; none is invented here. Standard template placeholders do not attribute ownership.\n"
    )
    final = graph(
        [
            row
            for row in inventory["packages"]
            if row.get("present_in_final_filesystem") is True
        ]
    )
    require(
        graph(packages(read(folder, "installed-apk-db"))) == final,
        "Final APK database differs from retained graph",
    )
    return (
        inventory,
        b"psst.zip exact runtime notices and source references\n"
        + b"\n".join(notice_blocks),
    )


def image_binding(inventory: dict, component: str, version: str, revision: str) -> dict:
    info = image_info(inventory["image_id"])
    require(
        info["Architecture"] == inventory["architecture"],
        "Runtime image architecture differs",
    )
    labels = info["Config"].get("Labels") or {}
    require(
        labels.get("org.opencontainers.image.version") == version
        and labels.get("org.opencontainers.image.revision") == revision,
        "Runtime image source labels differ",
    )
    binary = "/app/server" if component == "backend" else "/usr/bin/caddy"
    actual = command(
        "docker",
        "run",
        "--rm",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--entrypoint",
        "cat",
        info["Id"],
        binary,
    )
    database = command(
        "docker",
        "run",
        "--rm",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--entrypoint",
        "cat",
        info["Id"],
        "/lib/apk/db/installed",
    )
    require(
        graph(packages(database))
        == graph(
            [
                row
                for row in inventory["packages"]
                if row.get("present_in_final_filesystem") is True
            ]
        ),
        "Runtime image package database differs",
    )
    with tempfile.TemporaryDirectory(prefix="psst-source-bind-") as temp:
        saved = Path(temp) / "image.tar"
        command(
            "docker", "image", "save", "--output", str(saved), info["Id"], timeout=600
        )
        retained, databases = layer_packages(saved, info["Id"])
    require(
        graph(retained) == graph(inventory["packages"]),
        "Actual retained runtime graph differs",
    )
    require(
        databases == inventory["layer_databases"],
        "Actual retained layer databases differ",
    )
    return {
        "original_image_id": info["Id"],
        "rootfs_layers": info["RootFS"]["Layers"],
        "runtime_config_sha256": digest(json_bytes(info["Config"])),
        "binary_path": binary,
        "binary_sha256": digest(actual),
        "final_packages": graph(packages(database)),
        "retained_packages": graph(inventory["packages"]),
        "retained_graph_sha256": digest(json_bytes(graph(inventory["packages"]))),
    }


def artifact_provenance(
    backend: dict, web: dict, caddy: dict, signatures: dict
) -> dict:
    apk = {}
    for component, inventory in (("backend", backend), ("web", web)):
        apk[component] = [
            {
                "origin": source["origin"],
                "version": source["version"],
                "aports_commit": source["aports_commit"],
                "source_file": source["source_file"],
                "sha256": source["source_sha256"],
                "verification": "original-source-SHA512-against-exact-packaging-recipe",
                "upstream_source_signature_verified": False,
                "apk_binary_signature_verified": False,
                "signature_limit": "The collector retains source inputs and filesystem/layer APK databases, not the original signed APK binary envelopes; recipe/checksum provenance is not APK signature verification.",
            }
            for source in inventory["sources"]
        ]
    signed = {record["artifact"] for record in signatures["verifications"]}
    authenticated = signed | {
        record["file"] for record in signatures["signed_sha512_bindings"]
    }
    proof_inputs = {name + ".sig" for name in signed} | {
        name.removesuffix(".tar.gz") + ".pem" for name in signed
    }
    return {
        "apk_origins": apk,
        "caddy_assets": [
            {
                "file": asset["file"],
                "sha256": asset["sha256"],
                "verification": (
                    "Caddy-Sigstore-proof-covered-asset"
                    if asset["file"] in authenticated
                    else (
                        "input-to-successful-Caddy-Sigstore-verification"
                        if asset["file"] in proof_inputs
                        else "immutable-source-reference-and-retained-SHA256"
                    )
                ),
                "signature_proof_covered": asset["file"]
                in authenticated | proof_inputs,
                "upstream_signature_verified": asset["file"] in signed,
                "signature_limit": "Only the exact signed blobs and their signed-checksum bindings are authenticated by Caddy proof; other archives retain pinned revision/hash provenance.",
            }
            for asset in caddy["sources"]
        ],
    }


def web_release_metadata(image: str, version: str, revision: str) -> bytes:
    info = image_info(image)
    source = info["Config"]["Labels"]["org.opencontainers.image.source"]
    raw = command(
        "docker",
        "run",
        "--rm",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--entrypoint",
        "cat",
        image,
        "/srv/web/licenses/release.json",
    )
    require(len(raw) <= 65536, "Hosted release metadata exceeds bounds")
    expected = {
        "name": "psst.zip",
        "version": version,
        "revision": revision,
        "license": "AGPL-3.0-only",
        "source": source,
        "source_archive": source + "/archive/" + revision + ".tar.gz",
        "notice_files": ["/licenses/backend/THIRD_PARTY_NOTICES.txt"],
    }
    require(json.loads(raw) == expected, "Original hosted release metadata differs")
    expected["notice_files"].append("/licenses/runtime/THIRD_PARTY_NOTICES.txt")
    return json_bytes(expected)


def copy_tree(source: Path, destination: Path) -> None:
    total = 0
    for path in sorted(source.rglob("*")):
        require(not path.is_symlink(), "Source collection contains a symlink")
        relative = path.relative_to(source)
        if "__pycache__" in relative.parts or path.suffix == ".pyc":
            continue
        if path.is_dir():
            (destination / relative).mkdir(parents=True, exist_ok=True)
        else:
            require(path.is_file(), "Source collection contains a special file")
            total += path.stat().st_size
            require(total <= 2 * 1024**3, "Source collection exceeds pack bounds")
            (destination / relative).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, destination / relative)


def source_url(base: str, name: str) -> str:
    parsed = urllib.parse.urlsplit(base)
    require(
        parsed.scheme == "https"
        and parsed.hostname
        and not parsed.username
        and not parsed.password
        and not parsed.query
        and not parsed.fragment
        and parsed.port in {None, 443}
        and not re.search(r"[\x00-\x20\x7f]", base),
        "Source delivery URL must be a plain HTTPS asset directory",
    )
    return base.rstrip("/") + "/" + name


def deterministic_archive(folder: Path, output: Path) -> None:
    with output.open("xb") as stream, gzip.GzipFile(
        filename="", fileobj=stream, mode="wb", mtime=0
    ) as compressed, tarfile.open(
        fileobj=compressed, mode="w|", format=tarfile.PAX_FORMAT
    ) as archive:
        for path in sorted(folder.rglob("*")):
            require(not path.is_symlink(), "Source pack contains a symlink")
            if path.is_dir():
                continue
            require(path.is_file(), "Source pack contains a special file")
            entry = tarfile.TarInfo(path.relative_to(folder).as_posix())
            entry.size = path.stat().st_size
            entry.mode = 0o644
            entry.mtime = 0
            with path.open("rb") as data:
                archive.addfile(entry, data)


def package(
    backend: Path,
    web: Path,
    caddy: Path,
    cosign: Path,
    version: str,
    revision: str,
    delivery_base: str,
    output: Path,
) -> dict:
    matches(version, VERSION, "Invalid runtime pack release version")
    matches(revision, COMMIT, "Invalid runtime pack source revision")
    require(not output.exists(), "Runtime pack output already exists")
    review = REVIEW.verify_evidence()
    standards = {
        doc["license_id"]: doc for doc in review["documents"] if "license_id" in doc
    }
    backend_inventory, backend_notices = verify_apk_collection(
        backend, standards, review
    )
    web_inventory, web_notices = verify_apk_collection(web, standards, review)
    require(
        backend_inventory["architecture"] == web_inventory["architecture"],
        "Runtime pack architectures differ",
    )
    caddy_inventory = json.loads(read(caddy, "caddy-source-inventory.json"))
    require(
        caddy_inventory["image_id"] == web_inventory["image_id"],
        "Caddy collection belongs to another image",
    )
    signatures = verify_caddy_signatures(caddy, cosign)
    require(
        not caddy_inventory["nested_non_archives_requiring_review"],
        "Unreviewed Caddy nested source fixture",
    )
    supplied = {}
    for asset in caddy_inventory["sources"]:
        name = asset["file"]
        path = caddy / safe_member(name)
        require(file_hash(path) == asset["sha256"], "Caddy source asset differs")
        if name.endswith(".tar.gz") and "_linux_" not in name:
            invalid = []
            for member, content in source_notices(path, non_archives=invalid).items():
                supplied[name + "::" + member] = content
            require(not invalid, "Unreviewed Caddy nested source fixture")
        elif name.startswith("go-"):
            supplied[name] = read(caddy, name)
    require(
        {name: digest(content) for name, content in supplied.items()}
        == caddy_inventory["notices"],
        "Caddy notices differ from retained original sources",
    )
    caddy_notices = b"psst.zip Caddy runtime notices\n" + b"\n".join(
        name.encode() + b"\n" + data for name, data in sorted(supplied.items())
    )
    require(
        read(caddy, "THIRD_PARTY_NOTICES.txt", 64 * 1024 * 1024) == caddy_notices,
        "Caddy aggregate notices differ",
    )
    require(
        file_hash(caddy / "build-info.json") == caddy_inventory["build_info_sha256"],
        "Caddy recorded build information differs",
    )
    bindings = {
        "backend": image_binding(backend_inventory, "backend", version, revision),
        "web": image_binding(web_inventory, "web", version, revision),
    }
    binary_name = f"caddy_{caddy_inventory['version'][1:]}_linux_{web_inventory['architecture']}.tar.gz"
    require(
        digest(archive_member(caddy / binary_name, "caddy", 512 * 1024 * 1024))
        == bindings["web"]["binary_sha256"],
        "Caddy source binding differs from runtime executable",
    )
    provenance = artifact_provenance(
        backend_inventory, web_inventory, caddy_inventory, signatures
    )
    release_metadata = web_release_metadata(
        web_inventory["image_id"], version, revision
    )
    arch = backend_inventory["architecture"]
    asset_name = f"psst.zip-{version}-runtime-sources-{arch}.tar.gz"
    url = source_url(delivery_base, asset_name)
    output.mkdir(parents=True, mode=0o700)
    content = output / "source-tree"
    content.mkdir()
    for name, folder in (
        ("backend-runtime", backend),
        ("web-runtime", web),
        ("caddy", caddy),
        ("legal-review", LEGAL),
    ):
        copy_tree(folder, content / name)
    (content / "caddy-signature-verification.json").write_bytes(json_bytes(signatures))
    instructions = f"""# psst.zip runtime corresponding sources

Release: {version}
Application source revision: {revision}
Architecture: linux/{arch}
Runtime source asset: {url}

This asset retains all origin revisions found in the final filesystems and
lower layers, not just the latest installed package versions. Original source
archives, packaging recipes, checksums, patches/helpers, complete collected
notices, standard license references and verification evidence are included.

For Alpine components, find the exact origin/version/aports commit in each
runtime-inventory.json. Its source_file names the complete abuild source package.
Unpack that source package in an isolated build environment and use its original
APKBUILD, source inputs and patches with the matching Alpine abuild toolchain.
The inventory records the original source SHA512 sums and actual collection
helper package graph. The helper is build tooling; its binaries are not shipped.

For Caddy, the buildable-artifact tarball includes main.go, go.mod, go.sum and
the complete vendor tree. Build it using the recorded Go version and settings
from caddy/build-info.json, following the retained release recipe. Exact
caddy-docker/dist archives and Go runtime license/patent notices are retained.

MIT metadata/public-key treatment preserves all notices actually supplied,
declared licensing and complete terms without assigning a fabricated owner.
The two hash-bound malformed BusyBox unzip fixtures are retained as upstream
test data rather than omitted or treated as valid archives.

Source publication, anonymous retrieval and final release provenance must still
be verified by the publisher before distributing the overlaid image pair.
This local pack does not publish assets or approve an App Store release.
"""
    (content / "SOURCE.md").write_text(instructions)
    (content / "runtime-sources.Dockerfile").write_bytes(
        (ROOT / "tools/runtime-sources.Dockerfile").read_bytes()
    )
    (content / "image-bindings.json").write_bytes(json_bytes(bindings))
    (content / "artifact-provenance.json").write_bytes(json_bytes(provenance))
    asset = output / asset_name
    deterministic_archive(content, asset)
    asset_record = {
        "file": asset_name,
        "sha256": file_hash(asset),
        "size": asset.stat().st_size,
        "url": url,
    }
    overlays = {}
    additional_files = {}
    combined = (
        backend_notices
        + b"\nWeb image runtime\n"
        + web_notices
        + b"\nCaddy, vendored modules and Go runtime\n"
        + caddy_notices
    )
    for component, notices in (("backend", backend_notices), ("web", combined)):
        context = output / "overlays" / component
        runtime = context / "runtime"
        runtime.mkdir(parents=True)
        (runtime / "THIRD_PARTY_NOTICES.txt").write_bytes(notices)
        inventory = {
            "schema_version": 1,
            "name": "psst.zip",
            "version": version,
            "revision": revision,
            "architecture": arch,
            "component": component,
            "source_asset": asset_record,
            "image_bindings": bindings,
            "runtime_collections": {"backend": backend_inventory, "web": web_inventory},
            "caddy": caddy_inventory,
            "signature_verification": signatures,
            "artifact_provenance": provenance,
            "source_pack_complete": True,
            "publication_pending": True,
        }
        (runtime / "runtime-inventory.json").write_bytes(json_bytes(inventory))
        (runtime / "SOURCE.txt").write_text(
            f"psst.zip {version}\nApplication revision: {revision}\nRuntime source archive: {url}\nArchive SHA256: {asset_record['sha256']}\nComplete source/recipe/license inventory: runtime-inventory.json\n"
        )
        copy_tree(LEGAL / "licenses", runtime / "licenses")
        target = (
            "/app/licenses/runtime"
            if component == "backend"
            else "/srv/web/licenses/runtime"
        )
        dockerfile = (
            "ARG ORIGINAL_IMAGE\nFROM ${ORIGINAL_IMAGE}\nCOPY runtime/ "
            + target
            + "/\n"
        )
        if component == "web":
            (context / "release.json").write_bytes(release_metadata)
            dockerfile += "COPY release.json /srv/web/licenses/release.json\n"
            additional_files[component] = {
                "/srv/web/licenses/release.json": digest(release_metadata)
            }
        else:
            additional_files[component] = {}
        (context / "Dockerfile").write_text(dockerfile)
        overlays[component] = {
            path.relative_to(runtime).as_posix(): file_hash(path)
            for path in sorted(runtime.rglob("*"))
            if path.is_file()
        }
    result = {
        "schema_version": 1,
        "version": version,
        "revision": revision,
        "architecture": arch,
        "source_asset": asset_record,
        "bindings": bindings,
        "overlays": overlays,
        "additional_files": additional_files,
        "artifact_provenance": provenance,
        "coverage": {
            "backend_retained_packages": len(backend_inventory["packages"]),
            "backend_origins": len(backend_inventory["sources"]),
            "web_retained_packages": len(web_inventory["packages"]),
            "web_origins": len(web_inventory["sources"]),
            "caddy_upstream_signatures_verified": True,
        },
        "source_pack_complete": True,
        "publication_pending": True,
        "distribution_review_required": True,
    }
    (output / "runtime-pack.json").write_bytes(json_bytes(result))
    shutil.rmtree(content)
    return result


def verify_overlays(pack: Path, backend_image: str, web_image: str) -> dict:
    manifest = json.loads(read(pack, "runtime-pack.json", 16 * 1024 * 1024))
    require(
        file_hash(pack / manifest["source_asset"]["file"])
        == manifest["source_asset"]["sha256"],
        "Runtime source asset differs from pack",
    )
    results = {}
    for component, image in (("backend", backend_image), ("web", web_image)):
        expected = manifest["bindings"][component]
        info = image_info(image)
        require(
            info["Architecture"] == manifest["architecture"],
            "Overlay architecture differs",
        )
        require(
            info["RootFS"]["Layers"][: len(expected["rootfs_layers"])]
            == expected["rootfs_layers"],
            "Overlay lost or changed inherited runtime layers",
        )
        require(
            digest(json_bytes(info["Config"])) == expected["runtime_config_sha256"],
            "Overlay changed runtime configuration",
        )

        def cat(path):
            return command(
                "docker",
                "run",
                "--rm",
                "--network",
                "none",
                "--read-only",
                "--cap-drop",
                "ALL",
                "--entrypoint",
                "cat",
                info["Id"],
                path,
            )

        require(
            digest(cat(expected["binary_path"])) == expected["binary_sha256"],
            "Overlay changed runtime executable",
        )
        require(
            graph(packages(cat("/lib/apk/db/installed"))) == expected["final_packages"],
            "Overlay changed runtime APK graph",
        )
        base = (
            "/app/licenses/runtime"
            if component == "backend"
            else "/srv/web/licenses/runtime"
        )
        for relative, checksum in manifest["overlays"][component].items():
            safe_member(relative)
            require(
                digest(cat(base + "/" + relative)) == checksum,
                "Runtime legal overlay bytes differ",
            )
        for path, checksum in manifest["additional_files"][component].items():
            require(
                path == "/srv/web/licenses/release.json" and component == "web",
                "Unsupported extra legal discovery file",
            )
            require(
                digest(cat(path)) == checksum,
                "Hosted runtime discovery metadata differs",
            )
        results[component] = {
            "image_id": info["Id"],
            "binary_sha256": expected["binary_sha256"],
            "retained_graph_sha256": expected["retained_graph_sha256"],
        }
    return {
        "schema_version": 1,
        "runtime_pack_sha256": file_hash(pack / "runtime-pack.json"),
        "images": results,
        "overlay_verified": True,
        "publication_pending": True,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    create = commands.add_parser("package")
    for name in ("backend-runtime", "web-runtime", "caddy-sources", "cosign", "output"):
        create.add_argument("--" + name, required=True, type=Path)
    for name in ("version", "revision", "source-base-url"):
        create.add_argument("--" + name, required=True)
    check = commands.add_parser("verify-overlays")
    check.add_argument("--pack", required=True, type=Path)
    check.add_argument("--backend-image", required=True)
    check.add_argument("--web-image", required=True)
    check.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.operation == "package":
            result = package(
                args.backend_runtime,
                args.web_runtime,
                args.caddy_sources,
                args.cosign.resolve(),
                args.version,
                args.revision,
                args.source_base_url,
                args.output,
            )
            print(json.dumps(result["coverage"]))
        else:
            require(not args.output.exists(), "Overlay evidence output already exists")
            result = verify_overlays(args.pack, args.backend_image, args.web_image)
            with args.output.open("xb") as output:
                output.write(json_bytes(result))
            print(json.dumps({"overlay_verified": True, "publication_pending": True}))
    except (
        InvalidRelease,
        OSError,
        ValueError,
        KeyError,
        TypeError,
        tarfile.TarError,
        subprocess.TimeoutExpired,
    ) as error:
        parser.exit(1, f"Runtime source packaging rejected: {error}\n")


if __name__ == "__main__":
    main()

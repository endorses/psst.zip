#!/usr/bin/env python3
"""Replay local static-build inventory facts without claiming source closure.

Observed Rollup inputs/output hashes do not reproduce transformed code, identify
every copied script's source, or verify a deployed OCI image.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re

INVENTORY = "licenses/browser-module-inventory.json"
MAX_FILE = 32 * 1024**2
MAX_TOTAL = 512 * 1024**2
MAX_MODULES = 25_000
MAX_OUTPUTS = 25_000
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
SEMVER = re.compile(
    r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?\Z"
)
MODULE_FIELDS = {
    "id",
    "kind",
    "module_path",
    "source_sha256",
    "package",
    "transform_input_sha256",
    "rollup_input_sha256",
    "rendered_in",
}
PACKAGE_FIELDS = {"name", "version", "integrity", "lock_path", "package_json_sha256"}
FLAGS = {
    "source_reproduction_verified",
    "browser_module_closure_verified",
    "publication_authorized",
}
BUILD_METADATA = {".vite/manifest.json", ".vite/ssr-manifest.json"}


def require(condition, message):
    if not condition:
        raise ValueError("Browser inventory: " + message)


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def canonical(value) -> bytes:
    return (
        json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=False,
            allow_nan=False,
        )
        + "\n"
    ).encode()


def fields(value, expected, name):
    require(
        isinstance(value, dict) and set(value) == set(expected),
        "Invalid " + name + " fields",
    )
    return value


def json_record(raw: bytes):
    require(0 < len(raw) <= MAX_FILE, "JSON exceeds bounds")

    def pairs(entries):
        result = {}
        for key, value in entries:
            require(key not in result, "Duplicate JSON key")
            result[key] = value
        return result

    def nonfinite(value):
        require(False, "Non-finite JSON value")

    record = json.loads(raw, object_pairs_hook=pairs, parse_constant=nonfinite)
    require(isinstance(record, dict), "JSON record must be an object")
    return record


def safe_path(value):
    require(isinstance(value, str) and 0 < len(value) <= 4096, "Invalid relative path")
    path = PurePosixPath(value)
    require(
        not path.is_absolute()
        and str(path) == value
        and value != "."
        and all(part not in {".", ".."} for part in path.parts)
        and not any(char in value for char in "\\?#")
        and not any(ord(char) < 32 or ord(char) == 127 for char in value),
        "Unsafe relative path",
    )
    return value


def root_directory(root: Path):
    require(
        root.is_dir() and not any(item.is_symlink() for item in (root, *root.parents)),
        "Root must be a real directory",
    )


def local_file(root: Path, name: str) -> Path:
    name = safe_path(name)
    root_directory(root)
    path = root / name
    require(
        path.is_file() and not any(item.is_symlink() for item in (path, *path.parents)),
        "Input must be a regular local file",
    )
    return path


def read_file(root: Path, name: str) -> bytes:
    with local_file(root, name).open("rb") as stream:
        raw = stream.read(MAX_FILE + 1)
    require(len(raw) <= MAX_FILE, "Local file exceeds bounds")
    return raw


def file_fact(root: Path, name: str) -> dict:
    path = local_file(root, name)
    before = path.stat()
    require(0 <= before.st_size <= MAX_FILE, "Local file exceeds bounds")
    checksum, size = hashlib.sha256(), 0
    with path.open("rb") as stream:
        while chunk := stream.read(1024**2):
            size += len(chunk)
            require(size <= MAX_FILE, "Local file grew beyond bounds")
            checksum.update(chunk)
    after = path.stat()
    require(
        size == before.st_size
        and (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
        == (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns),
        "File changed during verification",
    )
    return {"sha256": "sha256:" + checksum.hexdigest(), "size": size}


def package_location(module_path: str):
    parts = PurePosixPath(module_path).parts
    if "node_modules" not in parts:
        return None
    index = len(parts) - 1 - list(reversed(parts)).index("node_modules")
    require(len(parts) > index + 1, "Missing installed package path")
    scope = parts[index + 1]
    end = index + (3 if scope.startswith("@") else 2)
    require(
        not scope.startswith(".") and len(parts) > end, "Invalid package module path"
    )
    name = "/".join(parts[index + 1 : end])
    require(
        re.fullmatch(r"(?:@[a-zA-Z0-9_.-]+/)?[a-zA-Z0-9_.-]+", name) is not None,
        "Invalid package name",
    )
    return name, "/".join(parts[:end])


def checked_output(row, root: Path, *, metadata=False):
    fields(row, {"file", "type", "sha256", "size"}, "output")
    name = safe_path(row["file"])
    require(
        row["type"] in {"chunk", "asset"}
        and isinstance(row["sha256"], str)
        and DIGEST.fullmatch(row["sha256"])
        and type(row["size"]) is int
        and 0 <= row["size"] <= MAX_FILE,
        "Invalid output identity",
    )
    require(name != INVENTORY, "Inventory cannot list its own output")
    if metadata:
        require(
            name in BUILD_METADATA and row["type"] == "asset",
            "Unsupported build metadata output",
        )
    else:
        require(
            name not in BUILD_METADATA,
            "Build metadata incorrectly advertised as final output",
        )
    actual = file_fact(root, name)
    require(
        actual == {"sha256": row["sha256"], "size": row["size"]},
        "Output bytes differ from emitted inventory",
    )
    return name, {"type": row["type"], **actual}


def verify(root: Path, output_directory: Path, version: str, revision: str) -> dict:
    """Verify exact observed local inputs/outputs; all approval boundaries stay false."""
    require(
        isinstance(version, str)
        and (
            version == "dev"
            or re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version)
        ),
        "Invalid expected version",
    )
    require(
        isinstance(revision, str)
        and (revision == "main" or re.fullmatch(r"[0-9a-f]{40}", revision)),
        "Invalid expected revision",
    )
    root_directory(root)
    root_directory(output_directory)
    inventory_raw = read_file(output_directory, INVENTORY)
    inventory = fields(
        json_record(inventory_raw),
        {
            "schema_version",
            "kind",
            "source",
            "execution",
            "outputs",
            "build_metadata_outputs",
            "modules",
            "excluded_modules",
        }
        | FLAGS,
        "inventory",
    )
    require(
        type(inventory["schema_version"]) is int
        and inventory["schema_version"] == 1
        and inventory["kind"] == "browser-module-inventory"
        and inventory["execution"] == "vite-rollup-client-build"
        and all(inventory[flag] is False for flag in FLAGS),
        "Inventory schema/execution/approval boundary differs",
    )
    source = fields(
        inventory["source"], {"version", "revision", "package_lock"}, "source"
    )
    require(
        source["version"] == version and source["revision"] == revision,
        "Inventory source identity differs",
    )
    locked = fields(source["package_lock"], {"file", "sha256"}, "source lock")
    require(locked["file"] == "package-lock.json", "Unexpected source lock path")
    lock_raw = read_file(root, "package-lock.json")
    require(digest(lock_raw) == locked["sha256"], "Exact package lock bytes differ")
    lock = json_record(lock_raw)
    require(
        type(lock.get("lockfileVersion")) is int
        and lock["lockfileVersion"] == 3
        and isinstance(lock.get("packages"), dict)
        and 0 < len(lock["packages"]) <= MAX_MODULES,
        "Unsupported package lock",
    )
    outputs = inventory["outputs"]
    require(
        isinstance(outputs, list) and 0 < len(outputs) <= MAX_OUTPUTS,
        "Missing/oversized output inventory",
    )
    verified = {}
    for row in outputs:
        name, actual = checked_output(row, output_directory)
        require(name not in verified, "Duplicate output path")
        verified[name] = actual
    metadata = inventory["build_metadata_outputs"]
    require(
        isinstance(metadata, list) and len(metadata) <= len(BUILD_METADATA),
        "Invalid build metadata inventory",
    )
    metadata_verified = {}
    for row in metadata:
        name, actual = checked_output(
            row, root / ".svelte-kit/output/client", metadata=True
        )
        require(name not in metadata_verified, "Duplicate build metadata output")
        metadata_verified[name] = actual
    chunks = {name for name, row in verified.items() if row["type"] == "chunk"}
    require(bool(chunks), "Client chunk inventory is missing")
    modules, excluded = inventory["modules"], inventory["excluded_modules"]
    require(
        isinstance(modules, list)
        and isinstance(excluded, list)
        and 0 < len(modules)
        and len(modules) + len(excluded) <= MAX_MODULES,
        "Missing/oversized module inventory",
    )
    seen, physical, packages, rendered_packages = set(), {}, {}, {}
    source_total = 0
    counts = {
        "rendered_modules": len(modules),
        "excluded_modules": len(excluded),
        "physical_modules": 0,
        "virtual_modules": 0,
    }
    for is_excluded, rows in ((False, modules), (True, excluded)):
        for row in rows:
            fields(
                row, MODULE_FIELDS | ({"reason"} if is_excluded else set()), "module"
            )
            identifier = row["id"]
            require(
                isinstance(identifier, str)
                and 0 < len(identifier) <= 16384
                and identifier not in seen,
                "Duplicate/invalid module identifier",
            )
            seen.add(identifier)
            for key in ("transform_input_sha256", "rollup_input_sha256"):
                require(
                    row[key] is None
                    or isinstance(row[key], str)
                    and DIGEST.fullmatch(row[key]),
                    "Invalid opaque transformed input checksum",
                )
            rendered = row["rendered_in"]
            require(
                isinstance(rendered, list)
                and len(rendered) <= len(chunks)
                and (not rendered if is_excluded else bool(rendered)),
                "Invalid rendered module contributions",
            )
            if is_excluded:
                require(
                    row["reason"] in {"zero-rendered-length", "not-in-client-chunks"},
                    "Invalid excluded module reason",
                )
            contributed = set()
            for contribution in rendered:
                fields(
                    contribution, {"file", "rendered_length"}, "rendered contribution"
                )
                require(
                    contribution["file"] in chunks
                    and contribution["file"] not in contributed
                    and type(contribution["rendered_length"]) is int
                    and 0 < contribution["rendered_length"] <= MAX_FILE,
                    "Invalid chunk/module correspondence",
                )
                contributed.add(contribution["file"])
            if row["kind"] == "virtual":
                require(
                    re.fullmatch(r"virtual:[0-9a-f]{64}", identifier)
                    and row["module_path"] is None
                    and row["source_sha256"] is None
                    and row["package"] is None,
                    "Virtual input exposes physical identity",
                )
                counts["virtual_modules"] += 1
                continue
            name = safe_path(row["module_path"])
            require(
                identifier == "file:" + name
                or re.fullmatch(
                    re.escape("file:" + name) + r":variant:[0-9a-f]{64}", identifier
                ),
                "Physical module identifier differs from source path",
            )
            if name not in physical:
                physical[name] = file_fact(root, name)
                source_total += physical[name]["size"]
                require(
                    source_total <= MAX_TOTAL,
                    "Observed source input bytes exceed bounds",
                )
            require(
                physical[name]["sha256"] == row["source_sha256"],
                "Module source bytes changed after build",
            )
            counts["physical_modules"] += 1
            location = package_location(name)
            expected_kind = (
                "package-source"
                if location
                else "generated-application"
                if name.startswith(".svelte-kit/")
                else "application-source"
            )
            require(
                row["kind"] == expected_kind, "Module kind differs from source path"
            )
            if location is None:
                require(
                    row["package"] is None,
                    "Application module carries unexpected package identity",
                )
                continue
            package_name, lock_path = location
            package = fields(row["package"], PACKAGE_FIELDS, "package")
            require(
                package["name"] == package_name
                and package["lock_path"] == lock_path
                and isinstance(package["version"], str)
                and SEMVER.fullmatch(package["version"]),
                "Package identity differs from installed module path",
            )
            item = lock["packages"].get(lock_path)
            require(
                isinstance(item, dict)
                and not item.get("link")
                and item.get("version") == package["version"]
                and item.get("integrity") == package["integrity"],
                "Package identity/integrity differs from exact lock",
            )
            integrity = package["integrity"]
            require(
                isinstance(integrity, str)
                and re.fullmatch(r"sha512-[A-Za-z0-9+/]{86}==", integrity),
                "Invalid npm archive integrity",
            )
            decoded = base64.b64decode(integrity[7:], validate=True)
            require(
                len(decoded) == 64
                and "sha512-" + base64.b64encode(decoded).decode() == integrity,
                "Noncanonical npm archive integrity",
            )
            expected_url = (
                "https://registry.npmjs.org/"
                + package_name
                + "/-/"
                + package_name.split("/")[-1]
                + "-"
                + package["version"]
                + ".tgz"
            )
            require(
                item.get("resolved") == expected_url, "Unsupported npm archive registry"
            )
            if lock_path not in packages:
                package_raw = read_file(root, lock_path + "/package.json")
                installed = json_record(package_raw)
                require(
                    installed.get("name") == package_name
                    and installed.get("version") == package["version"]
                    and digest(package_raw) == package["package_json_sha256"],
                    "Installed package manifest differs from observed source",
                )
                packages[lock_path] = dict(package)
            require(
                packages[lock_path] == package, "Inconsistent observed package identity"
            )
            if not is_excluded:
                rendered_packages[lock_path] = dict(package)
    actual_files, unattributed = {}, []
    output_total = 0
    for index, path in enumerate(output_directory.rglob("*")):
        require(index < MAX_OUTPUTS * 2, "Static directory inventory exceeds bounds")
        require(not path.is_symlink(), "Linked static output is refused")
        if path.is_dir():
            continue
        require(path.is_file(), "Special static output is refused")
        name = safe_path(path.relative_to(output_directory).as_posix())
        require(
            len(actual_files) < MAX_OUTPUTS and path.stat().st_size <= MAX_FILE,
            "Static output tree exceeds bounds",
        )
        actual_files[name] = path.stat().st_size
        output_total += actual_files[name]
        require(output_total <= MAX_TOTAL, "Static output bytes exceed bounds")
        if name.endswith(".js") and name not in chunks:
            unattributed.append(
                {
                    "file": name,
                    **file_fact(output_directory, name),
                    "origin": "emitted-asset"
                    if name in verified
                    else "not-in-inventory",
                }
            )
    require(
        set(verified) <= set(actual_files),
        "Final output disappeared during verification",
    )
    require(
        read_file(output_directory, INVENTORY) == inventory_raw
        and read_file(root, "package-lock.json") == lock_raw,
        "Inventory or lock changed during verification",
    )
    # Recheck every observed physical input/output after walking the complete tree.
    require(
        all(file_fact(root, name) == value for name, value in physical.items()),
        "Observed module source changed during verification",
    )
    require(
        all(
            file_fact(output_directory, name)
            == {"sha256": value["sha256"], "size": value["size"]}
            for name, value in verified.items()
        ),
        "Observed output changed during verification",
    )
    require(
        all(
            digest(read_file(root, name + "/package.json"))
            == value["package_json_sha256"]
            for name, value in packages.items()
        ),
        "Observed package manifest changed during verification",
    )
    require(
        all(
            file_fact(root / ".svelte-kit/output/client", name)
            == {"sha256": value["sha256"], "size": value["size"]}
            for name, value in metadata_verified.items()
        ),
        "Build metadata changed during verification",
    )
    require(
        all(
            file_fact(output_directory, row["file"])
            == {"sha256": row["sha256"], "size": row["size"]}
            for row in unattributed
        ),
        "Unattributed script changed during verification",
    )
    return {
        "schema_version": 1,
        "kind": "browser-module-inventory-verification",
        "source": {
            "version": version,
            "revision": revision,
            "package_lock_sha256": digest(lock_raw),
        },
        "inventory_sha256": digest(inventory_raw),
        "verified_outputs": dict(sorted(verified.items())),
        "verified_output_count": len(verified),
        "verified_outputs_sha256": digest(canonical(verified)),
        "build_metadata_outputs": dict(sorted(metadata_verified.items())),
        "build_metadata_output_count": len(metadata_verified),
        "observed_module_count": len(seen),
        **counts,
        "verified_source_file_count": len(physical),
        "verified_package_manifest_count": len(packages),
        "rendered_package_count": len(rendered_packages),
        "rendered_packages": [
            rendered_packages[key] for key in sorted(rendered_packages)
        ],
        "unattributed_javascript_outputs": sorted(
            unattributed, key=lambda row: row["file"]
        ),
        "oci_image_verified": False,
        "git_source_binding_verified": False,
        **{flag: False for flag in FLAGS},
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("root", "output-directory", "output"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    args = parser.parse_args(argv)
    require(
        not args.output.exists() and not args.output.is_symlink(),
        "Measurement output already exists",
    )
    root_directory(args.output.parent)
    result = verify(args.root, args.output_directory, args.version, args.revision)
    descriptor = os.open(
        args.output, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600
    )
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(canonical(result))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, RecursionError):
        raise SystemExit(
            "Browser inventory verification failed; no approval was issued."
        ) from None

"""Inventory final backend notice bytes; source/authentication gates remain separate.

OCI integrity and whiteouts reuse the existing release readers. Expected runtime
hashes must come from the independently authenticated and replayed runtime pack.
No source archives, upstream generators, image builds or network are used here.
"""

from __future__ import annotations

import gzip
import hashlib
import io
from pathlib import Path
import tarfile

from assemble_release_oci import inspect_archive, StrictTarInfo
from generate_release_gate_reports import NativeSourceContext
from measure_browser_source_inventory import safe_path
from release_artifacts import (
    COMMIT,
    DIGEST,
    fields,
    git,
    json_bytes,
    matches,
    read_json,
    require,
)
from verify_runtime_source_pack import whiteout_targets

PREFIX = "app/licenses/"
MAX_FILE = 8 * 1024**2
MAX_TOTAL = 64 * 1024**2
MAX_FILES = 4096


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def committed_inputs(context: NativeSourceContext, root: Path) -> dict[str, bytes]:
    """Read the selected committed tree in one Git call, ignoring working edits."""
    raw = git(
        root,
        "archive",
        "--format=tar",
        context.commit,
        "backend/licenses",
        "LICENSE",
        "backend/go.mod",
        "backend/go.sum",
    )
    require(0 < len(raw) <= MAX_TOTAL, "Committed notice inputs exceed bounds")
    files, seen, total = {}, set(), 0
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            name = safe_path(member.name.rstrip("/") if member.isdir() else member.name)
            require(
                name not in seen and len(seen) < MAX_FILES * 2,
                "Invalid Git notice tree",
            )
            seen.add(name)
            if member.isdir():
                require(member.size == 0, "Invalid Git notice directory")
                continue
            require(
                member.isfile()
                and not member.sparse
                and 0 <= member.size <= MAX_FILE
                and (
                    name.startswith("backend/licenses/")
                    or name in {"LICENSE", "backend/go.mod", "backend/go.sum"}
                ),
                "Unsupported committed notice input",
            )
            total += member.size
            require(
                total <= MAX_TOTAL and len(files) < MAX_FILES,
                "Git notice bytes exceed bounds",
            )
            files[name] = archive.extractfile(member).read()
            require(len(files[name]) == member.size, "Truncated Git notice input")
    require(
        {
            "LICENSE",
            "backend/go.mod",
            "backend/go.sum",
            "backend/licenses/AGPL-3.0-only.txt",
            "backend/licenses/THIRD_PARTY_NOTICES.txt",
            "backend/licenses/dependency-inventory.json",
        }
        <= files.keys(),
        "Required committed notices are missing",
    )
    require(
        files["LICENSE"] == files["backend/licenses/AGPL-3.0-only.txt"],
        "Committed application license differs",
    )
    return files


def final_notice_files(archive: Path, image: dict) -> dict[str, bytes]:
    """Project only the final /app/licenses tree, applying lower-layer deletions."""
    files, total = {}, 0
    with tarfile.open(archive, "r:", tarinfo=StrictTarInfo) as source:
        manifest = read_json(
            source.extractfile("blobs/sha256/" + image["manifest_digest"][7:]).read()
        )
        for descriptor in manifest["layers"]:
            raw = source.extractfile("blobs/sha256/" + descriptor["digest"][7:])
            decoded = (
                gzip.GzipFile(fileobj=raw)
                if descriptor["mediaType"].endswith("gzip")
                else raw
            )
            additions, removed, seen, expanded, added_bytes = {}, set(), set(), 0, 0
            tracked = {PREFIX + name for name in files}
            with raw, decoded, tarfile.open(fileobj=decoded, mode="r|*") as layer:
                for member in layer:
                    name = member.name.removeprefix("./")
                    if member.isdir():
                        name = name.rstrip("/")
                    if name in {"", "."} and member.isdir() and member.size == 0:
                        continue
                    safe_path(name)
                    require(
                        name not in seen and len(seen) < 1_000_000,
                        "Duplicate or oversized OCI layer inventory",
                    )
                    seen.add(name)
                    require(
                        0 <= member.size <= 512 * 1024**2,
                        "OCI layer member exceeds bounds",
                    )
                    expanded += member.size
                    require(
                        expanded <= 2 * 1024**3, "Expanded OCI layer exceeds bounds"
                    )
                    deleted = whiteout_targets(name, tracked)
                    is_whiteout = name.rsplit("/", 1)[-1].startswith(".wh.")
                    if is_whiteout:
                        require(
                            member.isfile() and not member.sparse and member.size == 0,
                            "Unsupported OCI whiteout",
                        )
                        removed.update(deleted)
                        continue
                    if name in {"app", "app/licenses"}:
                        require(member.isdir(), "Notice ancestor is not a directory")
                        continue
                    if not name.startswith(PREFIX):
                        continue
                    relative = name[len(PREFIX) :]
                    if member.isdir():
                        require(
                            member.size == 0
                            and relative not in files
                            and relative not in additions,
                            "Notice directory replaces a file",
                        )
                        continue
                    require(
                        member.isfile()
                        and not member.sparse
                        and member.size <= MAX_FILE
                        and not any(
                            n.startswith(relative + "/") or relative.startswith(n + "/")
                            for n in files.keys() | additions.keys()
                        ),
                        "Notice path is not a bounded regular file",
                    )
                    data = layer.extractfile(member).read()
                    require(len(data) == member.size, "Truncated OCI notice")
                    additions[relative] = data
                    added_bytes += len(data)
                    require(
                        len(additions) <= MAX_FILES and added_bytes <= MAX_TOTAL,
                        "Notice layer exceeds bounds",
                    )
            for name in removed:
                total -= len(files.pop(name[len(PREFIX) :]))
            for name, data in additions.items():
                total += len(data) - len(files.get(name, b""))
                files[name] = data
            require(
                len(files) <= MAX_FILES and total <= MAX_TOTAL,
                "Final notice inventory exceeds bounds",
            )
    return files


def verify_backend_notices(
    context: NativeSourceContext, archive: Path, *, image: dict, pack: dict, root: Path
) -> dict:
    """Verify exact final notices; caller authenticates native/runtime evidence."""
    identity = context.checked()
    require(
        pack.get("version") == context.version
        and pack.get("revision") == context.commit
        and pack.get("architecture") == context.platform.split("/")[1],
        "Runtime notice pack belongs to another release",
    )
    verified = inspect_archive(
        archive,
        platform=context.platform,
        repository=context.repository,
        version=context.version,
        commit=context.commit,
        tested_config=image.get("config_digest"),
        component="backend",
    )
    require(
        all(image.get(key) == value for key, value in verified.items()),
        "Final notice image differs from authenticated image",
    )
    actual = final_notice_files(archive, verified)
    committed = committed_inputs(context, root)
    expected = {
        name.removeprefix("backend/licenses/"): digest(raw)
        for name, raw in committed.items()
        if name.startswith("backend/licenses/")
    }
    source = (
        f"psst.zip {context.version}\nRevision: {context.commit}\nSource: https://github.com/{context.repository}/archive/{context.commit}.tar.gz\nLicense: AGPL-3.0-only\n"
    ).encode()
    require(
        "SOURCE.txt" not in expected and "go/LICENSE" not in expected,
        "Committed notices shadow generated inputs",
    )
    expected["SOURCE.txt"] = digest(source)
    overlays = pack.get("overlays", {}).get("backend")
    require(
        isinstance(overlays, dict)
        and {"runtime-inventory.json", "SOURCE.txt", "THIRD_PARTY_NOTICES.txt"}
        <= overlays.keys()
        and len(overlays) <= MAX_FILES,
        "Backend runtime notice overlay is missing",
    )
    for name, checksum in overlays.items():
        name = "runtime/" + safe_path(name)
        require(name not in expected, "Runtime overlay shadows a committed notice")
        expected[name] = matches(
            "sha256:" + str(checksum), DIGEST, "Invalid runtime notice hash"
        )
        require(
            digest(actual.get(name, b"")) == expected[name] and name in actual,
            "Final runtime notice differs from replayed source pack",
        )
    additional = pack.get("additional_files", {}).get("backend")
    require(
        isinstance(additional, dict) and len(additional) <= MAX_FILES,
        "Invalid additional backend notices",
    )
    for path, checksum in additional.items():
        require(
            isinstance(path, str) and path.startswith("/" + PREFIX),
            "Unsupported additional backend notice path",
        )
        name = safe_path(path[len(PREFIX) + 1 :])
        require(
            name not in expected and name != "go/LICENSE",
            "Additional backend notice shadows an expected file",
        )
        expected[name] = matches(
            "sha256:" + str(checksum), DIGEST, "Invalid additional notice hash"
        )
    runtime = read_json(actual["runtime/runtime-inventory.json"])
    require(
        isinstance(runtime, dict)
        and runtime.get("version") == context.version
        and runtime.get("revision") == context.commit
        and runtime.get("architecture") == pack["architecture"]
        and runtime.get("component") == "backend",
        "Embedded runtime notice inventory differs",
    )
    go_runtime = runtime.get("backend_go_runtime", runtime.get("caddy", {}))
    require(isinstance(go_runtime, dict), "Invalid runtime Go notice inventory")
    go_source = go_runtime.get("go_source", {})
    require(
        isinstance(go_source, dict)
        and isinstance(go_source.get("version"), str)
        and bool(go_source["version"])
        and go_source.get("version")
        == go_runtime.get("go_version")
        == pack.get("bindings", {}).get("backend", {}).get("go_version"),
        "Go notice runtime identity differs",
    )
    matches(go_source.get("commit"), COMMIT, "Invalid Go notice source revision")
    require(
        go_source.get("file") == "go-" + go_source["commit"] + ".tar.gz"
        and isinstance(go_runtime.get("notices"), dict),
        "Go original notice source identity differs",
    )
    name = go_source["file"] + "::go-" + go_source["commit"] + "/LICENSE"
    expected["go/LICENSE"] = matches(
        "sha256:" + str(go_runtime.get("notices", {}).get(name)),
        DIGEST,
        "Go original license hash is missing",
    )
    inventory = fields(
        read_json(committed["backend/licenses/dependency-inventory.json"]),
        {"inputs", "modules"},
        "committed backend dependency inventory",
    )
    locks = fields(
        inventory["inputs"], {"go.mod", "go.sum"}, "committed backend notice locks"
    )
    require(
        all(locks[name] == digest(committed["backend/" + name])[7:] for name in locks),
        "Backend notice inventory lock hashes differ",
    )
    modules = inventory["modules"]
    require(
        isinstance(modules, list) and 0 < len(modules) <= MAX_FILES,
        "Backend dependency notices are missing",
    )
    seen_modules, seen_notices = set(), set()
    for module in modules:
        fields(
            module, {"module", "version", "notices"}, "backend dependency notice module"
        )
        key = (module["module"], module["version"])
        require(
            all(isinstance(value, str) and value for value in key)
            and key not in seen_modules,
            "Invalid or duplicate notice module",
        )
        seen_modules.add(key)
        require(
            isinstance(module["notices"], list)
            and 0 < len(module["notices"]) <= MAX_FILES,
            "Dependency notice list is empty",
        )
        for notice in module["notices"]:
            fields(notice, {"path", "sha256", "upstream_sha256"}, "dependency notice")
            name = safe_path(notice["path"])
            require(
                name.startswith("dependencies/" + key[0] + "@" + key[1] + "/")
                and name not in seen_notices,
                "Dependency notice attribution differs",
            )
            seen_notices.add(name)
            matches(
                "sha256:" + str(notice["upstream_sha256"]),
                DIGEST,
                "Invalid upstream notice hash",
            )
            require(
                expected.get(name) == "sha256:" + str(notice["sha256"]),
                "Committed dependency notice hash differs",
            )
    require(
        {name: digest(raw) for name, raw in actual.items()} == expected,
        "Final backend notice tree is missing, changed or unexpected",
    )
    with archive.open("rb") as stream:
        require(
            "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
            == verified["archive_digest"],
            "OCI archive changed during notice verification",
        )
    files = dict(
        sorted(
            (name, {"sha256": digest(raw), "size": len(raw)})
            for name, raw in actual.items()
        )
    )
    return {
        "schema_version": 1,
        "kind": "final-backend-notice-inventory",
        "source": identity,
        "image": verified,
        "files": files,
        "notice_inventory_digest": digest(json_bytes(files)),
        "committed_dependency_notice_count": len(seen_notices),
        "dependency_upstream_notices_replayed": False,
        "corresponding_source_completeness_verified": False,
        "publication_authorized": False,
    }

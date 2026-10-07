#!/usr/bin/env python3
"""Bind selective real builder observations to Git inputs and final native OCI bytes.

This bounded observation is neither complete browser source closure nor upstream
source reproduction or permission to distribute. It executes no package scripts.
"""

from __future__ import annotations

import base64
import gzip
import hashlib
import io
import json
import os
import re
from pathlib import Path
import tarfile
import tempfile
import uuid

from assemble_release_oci import inspect_archive, StrictTarInfo
import measure_browser_source_inventory as browser
from release_artifacts import (
    create_output,
    json_bytes,
    matches,
    DIGEST,
    require,
    InvalidRelease,
)
from verify_runtime_source_pack import whiteout_targets

RECIPE_CATALOG = "scripts/browser-source-recipes.json"
FIXED = {
    RECIPE_CATALOG,
    "src/app.html",
    "package.json",
    "package-lock.json",
    "vite.config.ts",
    "svelte.config.js",
    "tsconfig.json",
    "scripts/browser-module-inventory.mjs",
}
MAX_PACK = 512 * 1024**2


def unpack(path: Path, destination: Path) -> set[str]:
    require(
        path.is_file()
        and not path.is_symlink()
        and 0 < path.stat().st_size <= MAX_PACK + 64 * 1024**2,
        "Invalid browser evidence archive",
    )
    seen, total = set(), 0
    with path.open("rb") as source, tarfile.open(
        fileobj=source, mode="r:", tarinfo=StrictTarInfo
    ) as archive:
        for member in archive:
            name = browser.safe_path(member.name)
            require(
                member.isfile()
                and not member.sparse
                and name not in seen
                and len(seen) < 50000,
                "Invalid browser evidence member",
            )
            require(
                0 <= member.size <= browser.MAX_FILE,
                "Browser evidence member exceeds bounds",
            )
            seen.add(name)
            total += member.size
            require(total <= MAX_PACK, "Browser evidence expansion exceeds bounds")
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            with archive.extractfile(member) as stream, target.open("xb") as output:
                output.write(stream.read(member.size + 1))
            require(
                target.stat().st_size == member.size,
                "Truncated browser evidence member",
            )
        source.seek(archive.offset)
        tail = source.read(1024**2 + 1)
        require(
            len(tail) <= 1024**2 and not tail.strip(b"\0"),
            "Browser evidence has trailing payload",
        )
    return seen


def file_digest(path: Path) -> str:
    require(
        path.is_file() and not path.is_symlink(),
        "Browser evidence must be a regular file",
    )
    with path.open("rb") as stream:
        return "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()


def recipe_plan(folder: Path, inventory: dict) -> dict:
    """Associate only finite reviewed IDs/paths; unknowns remain opaque and unresolved."""
    catalog = browser.json_record(browser.read_file(folder, RECIPE_CATALOG))
    require(
        set(catalog)
        == {
            "schema_version",
            "kind",
            "vite",
            "kit",
            "source_reproduction_verified",
            "browser_module_closure_verified",
            "distribution_authorized",
        }
        and type(catalog["schema_version"]) is int
        and catalog["schema_version"] == 1
        and catalog["kind"] == "reviewed-browser-generator-associations"
        and all(
            catalog[key] is False
            for key in (
                "source_reproduction_verified",
                "browser_module_closure_verified",
                "distribution_authorized",
            )
        ),
        "Browser recipe catalog grants unsupported claims",
    )
    vite, kit = catalog["vite"], catalog["kit"]
    require(
        set(vite)
        == {
            "lock_path",
            "name",
            "version",
            "generator",
            "members",
            "commonjs_suffixes",
            "helper_ids",
        }
        and set(kit)
        == {"lock_path", "name", "version", "members", "generated", "generated_nodes"},
        "Unexpected browser recipe fields",
    )
    for recipe in (vite, kit):
        location = browser.safe_path(recipe["lock_path"])
        require(
            location == "node_modules/" + recipe["name"],
            "Unexpected generator package location",
        )
        require(
            isinstance(recipe["members"], list)
            and 0 < len(recipe["members"]) <= 64
            and len(set(recipe["members"])) == len(recipe["members"]),
            "Invalid generator members",
        )
        for name in recipe["members"]:
            browser.safe_path(name)
            require(
                not any(part.startswith(".") for part in name.split("/")),
                "Hidden generator input",
            )
    require(
        vite["commonjs_suffixes"]
        == ["?commonjs-exports", "?commonjs-module", "?commonjs-es-import"]
        and vite["helper_ids"] == ["commonjsHelpers.js", "vite/preload-helper.js"]
        and vite["generator"] in vite["members"],
        "Unreviewed virtual ID grammar",
    )
    require(
        isinstance(kit["generated"], dict) and len(kit["generated"]) <= 16,
        "Invalid generated recipe count",
    )
    for path, recipe in kit["generated"].items():
        browser.safe_path(path)
        require(
            path.startswith(".svelte-kit/generated/")
            and set(recipe) == {"generator", "function", "invocation"}
            and recipe["generator"] in kit["members"]
            and all(
                isinstance(recipe[key], str)
                and 0 < len(recipe[key]) <= 512
                and not any(ord(char) < 32 for char in recipe[key])
                for key in ("function", "invocation")
            ),
            "Unreviewed generated recipe",
        )
    nodes = kit["generated_nodes"]
    require(
        set(nodes) == {"prefix", "generator", "function", "invocation"}
        and nodes["prefix"] == ".svelte-kit/generated/client-optimized/nodes/"
        and nodes["generator"] in kit["members"]
        and all(
            isinstance(nodes[key], str)
            and 0 < len(nodes[key]) <= 512
            and not any(ord(char) < 32 for char in nodes[key])
            for key in ("function", "invocation")
        ),
        "Unreviewed generated node recipe",
    )
    physical = {
        row["module_path"]
        for row in inventory["modules"] + inventory["excluded_modules"]
        if row["module_path"] is not None
    }
    candidates = {}

    def candidate(raw, record):
        identifier = "virtual:" + hashlib.sha256(raw.encode()).hexdigest()
        require(identifier not in candidates, "Ambiguous reviewed virtual association")
        candidates[identifier] = record

    for name in sorted(physical):
        if not name.startswith("node_modules/"):
            continue
        browser.safe_path(name)
        for suffix in vite["commonjs_suffixes"]:
            candidate(
                "\0<root>/" + name + suffix,
                {
                    "family": "vite-commonjs" + suffix[9:],
                    "module_path": name,
                    "generator": vite["lock_path"] + "/" + vite["generator"],
                },
            )
    for helper in vite["helper_ids"]:
        candidate(
            "\0" + helper,
            {
                "family": (
                    "vite-commonjs-helper"
                    if helper == "commonjsHelpers.js"
                    else "vite-preload-helper"
                ),
                "module_path": None,
                "generator": vite["lock_path"] + "/" + vite["generator"],
            },
        )
    virtual, generated = [], []
    needed = set()
    for row in inventory["modules"] + inventory["excluded_modules"]:
        if row["kind"] == "virtual":
            match = candidates.get(row["id"])
            virtual.append(
                {
                    "id": row["id"],
                    "association": "reviewed-generator" if match else "unresolved",
                    **(match or {}),
                    "generated_byte_reproduction_verified": False,
                    "rendered_in_client_chunks": bool(row["rendered_in"]),
                    "excluded_reason": row.get("reason"),
                }
            )
            if match:
                needed.add("vite")
        elif row["kind"] == "generated-application":
            path = row["module_path"]
            match = kit["generated"].get(path)
            if match is None and path.startswith(nodes["prefix"]):
                index = path[len(nodes["prefix"]) :]
                if (
                    re.fullmatch(r"(0|[1-9][0-9]{0,4})\.js", index)
                    and int(index[:-3]) < browser.MAX_MODULES
                ):
                    match = nodes
            generated.append(
                {
                    "file": path,
                    "association": "reviewed-generator" if match else "unresolved",
                    **(
                        {
                            "generator": kit["lock_path"] + "/" + match["generator"],
                            "function": match["function"],
                            "invocation": match["invocation"],
                        }
                        if match
                        else {}
                    ),
                    "generated_byte_reproduction_verified": False,
                    "rendered_in_client_chunks": bool(row["rendered_in"]),
                    "excluded_reason": row.get("reason"),
                }
            )
            if match:
                needed.add("kit")
    recipes = {name: catalog[name] for name in sorted(needed)}
    inputs = {
        recipe["lock_path"] + "/" + member
        for recipe in recipes.values()
        for member in recipe["members"] + ["package.json"]
    }
    return {
        "recipes": recipes,
        "inputs": inputs,
        "virtual_inputs": sorted(virtual, key=lambda row: row["id"]),
        "generated_inputs": sorted(generated, key=lambda row: row["file"]),
    }


def copied_inputs(folder: Path, inventory: dict) -> set[str]:
    chunks = {row["file"] for row in inventory["outputs"] if row["type"] == "chunk"}
    names = set()
    for name in static_facts(folder / "build"):
        if not name.endswith(".js"):
            continue
        path = "static/" + name
        if name in {"appearance.js", "language.js"}:
            browser.read_file(
                folder, path
            )  # Known copied inputs must not silently lose their source.
            names.add(path)
        elif name not in chunks and (folder / path).exists():
            browser.read_file(folder, path)
            names.add(path)
    return names


def selected(inventory: dict, folder: Path | None = None) -> set[str]:
    names = set(FIXED)
    for row in inventory["modules"] + inventory["excluded_modules"]:
        if row["module_path"] is not None:
            name = browser.safe_path(row["module_path"])
            require(
                name in FIXED
                or name.startswith(
                    ("src/", "static/", "node_modules/", ".svelte-kit/")
                ),
                "Unsupported observed browser input",
            )
            require(
                not any(
                    part.startswith(".") and part not in {".svelte-kit", ".vite"}
                    for part in name.split("/")
                ),
                "Hidden observed browser input",
            )
            names.add(name)
            if row["package"] is not None:
                location = browser.safe_path(row["package"]["lock_path"])
                require(
                    location.startswith("node_modules/")
                    and not any(part.startswith(".") for part in location.split("/")),
                    "Unsupported package manifest input",
                )
                names.add(location + "/package.json")
    for row in inventory["build_metadata_outputs"]:
        require(row["file"] in browser.BUILD_METADATA, "Unsupported browser metadata")
        names.add(".svelte-kit/output/client/" + row["file"])
    if folder is not None:
        names.update(recipe_plan(folder, inventory)["inputs"])
        names.update(copied_inputs(folder, inventory))
    return names


def git_binding(
    folder: Path, revision: str, paths: set[str], source_root: Path, execute
) -> dict:
    verified, objects = {}, {}
    listing = execute(
        "git",
        "-C",
        str(source_root),
        "ls-tree",
        "-z",
        revision,
        "--",
        *["web/" + name for name in sorted(paths)],
    )
    require(
        len(listing) <= browser.MAX_FILE, "Git browser input listing exceeds bounds"
    )
    for line in listing.split(b"\0"):
        if not line:
            continue
        metadata, name = line.decode().split("\t", 1)
        mode, kind, oid = metadata.split()
        require(
            mode in {"100644", "100755"}
            and kind == "blob"
            and name.startswith("web/")
            and name[4:] in paths
            and name[4:] not in objects,
            "Observed input absent/nonregular in exact Git commit",
        )
        objects[name[4:]] = oid
    require(
        set(objects) == paths, "Observed input absent/nonregular in exact Git commit"
    )
    for path in sorted(paths):
        raw = browser.read_file(folder, path)
        committed = execute(
            "git", "-C", str(source_root), "cat-file", "blob", revision + ":web/" + path
        )
        require(
            raw == committed,
            "Builder application/config/lock differs from exact Git blob",
        )
        verified[path] = {"git_blob": objects[path], **browser.file_fact(folder, path)}
    return verified


def npm_members(
    folder: Path, inventory: dict, dependency_asset: Path | None = None
) -> dict:
    packages = {}
    for row in inventory["modules"] + inventory["excluded_modules"]:
        if row["package"] is not None:
            pkg = row["package"]
            packages.setdefault(pkg["lock_path"], {"package": pkg, "members": set()})[
                "members"
            ].add(row["module_path"])
    lock = browser.json_record(browser.read_file(folder, "package-lock.json"))[
        "packages"
    ]
    for recipe in recipe_plan(folder, inventory)["recipes"].values():
        location = recipe["lock_path"]
        installed_raw = browser.read_file(folder, location + "/package.json")
        installed = browser.json_record(installed_raw)
        locked = lock.get(location, {})
        require(
            installed.get("name") == recipe["name"]
            and installed.get("version") == recipe["version"] == locked.get("version")
            and not locked.get("link")
            and locked.get("resolved")
            == "https://registry.npmjs.org/"
            + recipe["name"]
            + "/-/"
            + recipe["name"].split("/")[-1]
            + "-"
            + recipe["version"]
            + ".tgz",
            "Generator package differs from reviewed recipe and lock",
        )
        pkg = {
            "name": recipe["name"],
            "version": recipe["version"],
            "lock_path": location,
            "integrity": locked.get("integrity"),
            "package_json_sha256": browser.digest(installed_raw),
        }
        if location in packages:
            require(
                packages[location]["package"] == pkg,
                "Generator package identity differs from inventory",
            )
        else:
            packages[location] = {"package": pkg, "members": set()}
        packages[location]["members"].update(
            location + "/" + name for name in recipe["members"]
        )
    paths = {}
    for location, item in packages.items():
        filename = (
            "npm/"
            + hashlib.sha256(
                (location + "@" + item["package"]["version"]).encode()
            ).hexdigest()
            + ".tgz"
        )
        paths[filename] = (location, item)
    if dependency_asset:
        require(
            dependency_asset.is_file()
            and not dependency_asset.is_symlink()
            and dependency_asset.stat().st_size <= 2 * 1024**3,
            "Invalid dependency input asset",
        )
        found = set()
        total = 0
        with tarfile.open(dependency_asset, "r:gz", tarinfo=StrictTarInfo) as archive:
            for member in archive:
                browser.safe_path(member.name)
                total += member.size
                require(
                    total <= 2 * 1024**3 and 0 <= member.size <= MAX_PACK,
                    "Dependency asset expansion exceeds bounds",
                )
                if member.name not in paths:
                    continue
                require(
                    member.isfile()
                    and member.name not in found
                    and member.size <= browser.MAX_FILE,
                    "Invalid retained npm archive",
                )
                found.add(member.name)
                target = folder / member.name
                target.parent.mkdir(parents=True, exist_ok=True)
                create_output(target, archive.extractfile(member).read())
        require(found == set(paths), "Observed browser npm archives are missing")
    verified = {}
    for filename, (location, item) in sorted(paths.items()):
        raw = browser.read_file(folder, filename)
        pkg = item["package"]
        require(
            "sha512-" + base64.b64encode(hashlib.sha512(raw).digest()).decode()
            == pkg["integrity"]
            == lock[location]["integrity"],
            "Retained npm archive integrity differs",
        )
        expected = set(item["members"]) | {location + "/package.json"}
        matched, names, total, archive_root = {}, set(), 0, None
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
            for member in archive:
                name = browser.safe_path(member.name.rstrip("/"))
                require(
                    name not in names
                    and len(names) < 100000
                    and (member.isfile() or member.isdir())
                    and not member.sparse,
                    "Invalid npm member",
                )
                names.add(name)
                total += member.size
                require(
                    total <= MAX_PACK and member.size <= browser.MAX_FILE,
                    "Npm expansion exceeds bounds",
                )
                parts = name.split("/", 1)
                if archive_root is None:
                    archive_root = parts[0]
                require(parts[0] == archive_root, "Npm archive has multiple roots")
                local = location + "/" + parts[1] if len(parts) == 2 else ""
                if local in expected:
                    require(member.isfile(), "Observed npm member is not regular")
                    data = archive.extractfile(member).read()
                    require(
                        data == browser.read_file(folder, local),
                        "Builder npm source/manifest differs from integrity-bound archive member",
                    )
                    matched[local] = browser.digest(data)
        require(
            set(matched) == expected, "Observed npm member absent from retained archive"
        )
        verified[filename] = {
            "sha256": browser.digest(raw),
            "integrity": pkg["integrity"],
            "members": matched,
        }
    return verified


def static_facts(folder: Path) -> dict:
    facts, total = {}, 0
    for path in folder.rglob("*"):
        require(not path.is_symlink(), "Linked browser static output")
        if path.is_dir():
            continue
        name = browser.safe_path(path.relative_to(folder).as_posix())
        facts[name] = browser.file_fact(folder, name)
        total += facts[name]["size"]
        require(
            len(facts) <= browser.MAX_OUTPUTS and total <= MAX_PACK,
            "Browser static output exceeds bounds",
        )
    return facts


def oci_static(archive: Path, image: dict, output: Path) -> dict:
    files, total = {}, 0
    with tarfile.open(archive, "r:", tarinfo=StrictTarInfo) as source:
        manifest = json.load(
            source.extractfile("blobs/sha256/" + image["manifest_digest"][7:])
        )
        for descriptor in manifest["layers"]:
            raw = source.extractfile("blobs/sha256/" + descriptor["digest"][7:])
            decoded = (
                gzip.GzipFile(fileobj=raw)
                if descriptor["mediaType"].endswith("gzip")
                else raw
            )
            with raw, decoded, tarfile.open(fileobj=decoded, mode="r|*") as layer:
                seen, expanded = set(), 0
                for member in layer:
                    name = member.name.removeprefix("./").rstrip("/")
                    browser.safe_path(name)
                    require(
                        name not in seen and len(seen) < 1000000,
                        "Duplicate/oversized OCI layer",
                    )
                    seen.add(name)
                    expanded += member.size
                    require(
                        expanded <= 2 * 1024**3 and member.size <= MAX_PACK,
                        "OCI layer expansion exceeds bounds",
                    )
                    tracked = {"srv/web/" + n for n in files}
                    for deleted in whiteout_targets(name, tracked):
                        old = files.pop(deleted.removeprefix("srv/web/"))
                        total -= old["size"]
                        (output / deleted.removeprefix("srv/web/")).unlink()
                    if name in {"srv", "srv/web"}:
                        require(member.isdir(), "OCI static ancestor is not directory")
                    if not name.startswith("srv/web/") or "/.wh." in name:
                        continue
                    relative = name[len("srv/web/") :]
                    if any(n.startswith(relative + "/") for n in files):
                        require(
                            member.isdir(), "OCI static ancestor is not a directory"
                        )
                    if member.isdir():
                        require(
                            relative not in files, "OCI static directory replaces file"
                        )
                        continue
                    require(
                        member.isfile()
                        and not member.sparse
                        and member.size <= browser.MAX_FILE,
                        "OCI static member is not bounded regular file",
                    )
                    data = layer.extractfile(member).read()
                    total += len(data) - files.get(relative, {}).get("size", 0)
                    require(
                        total <= MAX_PACK and len(files) <= browser.MAX_OUTPUTS,
                        "OCI static tree exceeds bounds",
                    )
                    target = output / relative
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(data)
                    files[relative] = {
                        "sha256": browser.digest(data),
                        "size": len(data),
                    }
    return dict(sorted(files.items()))


def replay(
    context,
    pack: Path,
    archive: Path,
    tested_config: str,
    runtime_pack: dict,
    *,
    source_root: Path | None = None,
    execute=None,
) -> dict:
    from github_release_transport import command

    source_root = source_root or Path(__file__).resolve().parents[1]
    execute = execute or (
        lambda *args: command(
            list(args), environment={"PATH": os.environ["PATH"]}, timeout=120
        )
    )
    require(
        pack.is_file()
        and not pack.is_symlink()
        and pack.stat().st_size <= MAX_PACK + 64 * 1024**2,
        "Browser pack exceeds bounds",
    )
    pack_digest = file_digest(pack)
    with tempfile.TemporaryDirectory(prefix="psst-browser-replay-") as temporary:
        folder = Path(temporary)
        names = unpack(pack, folder)
        inventory = browser.json_record(
            browser.read_file(folder / "build", browser.INVENTORY)
        )
        inputs = selected(inventory, folder)
        observation = browser.verify(
            folder, folder / "build", context.version, context.commit
        )
        app = {
            name
            for name in inputs
            if not name.startswith(("node_modules/", ".svelte-kit/"))
        }
        git = git_binding(folder, context.commit, app, source_root, execute)
        npm = npm_members(folder, inventory)
        require(
            names
            == inputs
            | {"build/" + name for name in static_facts(folder / "build")}
            | set(npm),
            "Extra or missing browser evidence payload",
        )
        image = inspect_archive(
            archive,
            platform=context.platform,
            repository=context.repository,
            version=context.version,
            commit=context.commit,
            tested_config=tested_config,
            component="web",
        )
        final = folder / "final"
        final.mkdir()
        actual = oci_static(archive, image, final)
        built = static_facts(folder / "build")
        backend = {}
        listing = (
            execute(
                "git",
                "-C",
                str(source_root),
                "ls-tree",
                "-r",
                context.commit,
                "--",
                "backend/licenses",
            )
            .decode()
            .splitlines()
        )
        for line in listing:
            metadata, path = line.split("\t", 1)
            mode, kind, oid = metadata.split()
            require(
                mode in {"100644", "100755"}
                and kind == "blob"
                and path.startswith("backend/licenses/"),
                "Invalid copied backend license Git input",
            )
            raw = execute("git", "-C", str(source_root), "cat-file", "blob", oid)
            backend["licenses/backend/" + path[len("backend/licenses/") :]] = {
                "sha256": browser.digest(raw),
                "size": len(raw),
            }
        allowed = set(backend)
        allowed.update(
            "licenses/runtime/" + name for name in runtime_pack["overlays"]["web"]
        )
        for name, checksum in runtime_pack["overlays"]["web"].items():
            require(
                actual.get("licenses/runtime/" + name, {}).get("sha256")
                == "sha256:" + checksum,
                "Final browser runtime overlay differs",
            )
        additional = runtime_pack.get("additional_files", {}).get("web", {})
        allowed.update(path.removeprefix("/srv/web/") for path in additional)
        require(
            set(built) <= set(actual) and set(actual) - set(built) <= allowed,
            "Final OCI static tree differs from real builder output",
        )
        require(
            all(actual.get(name) == fact for name, fact in backend.items()),
            "Copied backend notice bytes differ from Git",
        )
        for name, fact in built.items():
            if "/srv/web/" + name not in additional:
                require(
                    actual[name] == fact,
                    "Final OCI static bytes differ from real builder output",
                )
        for path, digest in additional.items():
            require(
                path == "/srv/web/licenses/release.json"
                and actual[path[len("/srv/web/") :]]["sha256"] == "sha256:" + digest,
                "Unexpected/changed final browser overlay",
            )
        final_observation = browser.verify(
            folder, final, context.version, context.commit
        )
        require(
            observation["inventory_sha256"] == final_observation["inventory_sha256"],
            "Final browser inventory differs",
        )
        require(
            file_digest(pack) == pack_digest
            and file_digest(archive) == image["archive_digest"],
            "Browser inputs or final OCI changed during replay",
        )
        origins = []
        copied = copied_inputs(folder, inventory)
        for path in sorted(copied):
            name = path.removeprefix("static/")
            fact = browser.file_fact(folder, path)
            require(
                path in git and built[name] == fact,
                "Copied browser JavaScript differs from Git-bound static original",
            )
            origins.append(
                {
                    "file": name,
                    "source_file": path,
                    "source_sha256": fact["sha256"],
                    "git_blob": git[path]["git_blob"],
                    "association": "byte-identical-git-static-original",
                }
            )
        unresolved = [
            {
                "file": row["file"],
                "origin": row["origin"],
                "sha256": row["sha256"],
                "association": "unresolved",
            }
            for row in observation["unattributed_javascript_outputs"]
            if "static/" + row["file"] not in copied
        ]
        plan = recipe_plan(folder, inventory)
        recipe_files = {
            name: browser.file_fact(folder, name) for name in sorted(plan["inputs"])
        }
        if "vite" in plan["recipes"]:
            upstream_notice = "node_modules/vite/LICENSE.md"
            public_notice = "licenses/vite-generated-browser-helpers-LICENSE.md"
            require(
                built.get(public_notice) == recipe_files[upstream_notice]
                and actual.get(public_notice) == recipe_files[upstream_notice],
                "Generated browser helpers notice differs from original integrity-bound Vite license",
            )
        return {
            "schema_version": 1,
            "kind": "native-browser-input-measurement",
            "source": context.checked(),
            "builder_observed_inputs_verified": True,
            "git_source_binding_verified": True,
            "npm_member_integrity_verified": True,
            "oci_image_verified": True,
            "tested_web_config": tested_config,
            "image": image,
            "git_inputs": git,
            "npm_archives": npm,
            "source_associations": {
                "catalog": {"file": RECIPE_CATALOG, **git[RECIPE_CATALOG]},
                "integrity_bound_recipe_files": recipe_files,
                "virtual_inputs": plan["virtual_inputs"],
                "generated_inputs": plan["generated_inputs"],
                "copied_javascript": origins,
                "unresolved_javascript": unresolved,
                "generated_byte_reproduction_verified": False,
                "preferred_source_complete": False,
            },
            "builder_static_files": built,
            "final_static_files": actual,
            "browser_inventory_verification": final_observation,
            "browser_inputs_sha256": pack_digest,
            "publication_authorized": False,
            "distribution_authorized": False,
            "browser_module_closure_verified": False,
            "source_reproduction_verified": False,
            "measurement_authentication_required": True,
        }


def collect(
    operations,
    context,
    builder_config: str,
    source_root: Path,
    dependency_collection: Path,
    archive: Path,
    tested_config: str,
    runtime_pack: dict,
    output: Path,
) -> dict:
    matches(builder_config, DIGEST, "Missing immutable original browser builder")
    info = operations.image(builder_config)
    require(
        info.get("Id") == builder_config
        and info.get("Os") == "linux"
        and info.get("Architecture") == context.platform.split("/")[1],
        "Browser builder config/platform differs",
    )
    script = browser.read_file(
        source_root / "web", "scripts/pack-browser-builder-inputs.mjs"
    ).decode()
    output.mkdir(mode=0o700)
    container = "psst-browser-" + uuid.uuid4().hex
    try:
        operations.run(
            "docker",
            "create",
            "--name",
            container,
            "--platform",
            context.platform,
            "--network",
            "none",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--user",
            "65532:65532",
            "--pids-limit",
            "64",
            "--memory",
            "1g",
            "--entrypoint",
            "node",
            builder_config,
            "--input-type=module",
            "-e",
            script,
        )
        operations.run("docker", "start", "--attach", container)
        status = operations.run(
            "docker", "inspect", "--format", "{{.State.ExitCode}}", container
        ).strip()
        require(status == b"0", "Browser builder selective capture failed")
        with tempfile.TemporaryDirectory(prefix="psst-browser-collect-") as temporary:
            folder = Path(temporary)
            captured = folder / "capture.tar"
            operations.run(
                "docker",
                "cp",
                container + ":/tmp/psst-browser-builder.tar",
                str(captured),
            )
            retained = folder / "retained"
            retained.mkdir()
            names = unpack(captured, retained)
            inventory = browser.json_record(
                browser.read_file(retained / "build", browser.INVENTORY)
            )
            inputs = selected(inventory, retained)
            require(
                names
                == inputs
                | {"build/" + name for name in static_facts(retained / "build")},
                "Builder capture contains extra/missing inputs",
            )
            browser.verify(
                retained, retained / "build", context.version, context.commit
            )
            app = {
                name
                for name in inputs
                if not name.startswith(("node_modules/", ".svelte-kit/"))
            }
            git_binding(retained, context.commit, app, source_root, operations.run)
            descriptor = browser.json_record(
                browser.read_file(dependency_collection, "dependency-collection.json")
            )
            require(
                descriptor.get("repository") == context.repository
                and descriptor.get("version") == context.version
                and descriptor.get("source_commit") == context.commit
                and descriptor.get("platform") == context.platform
                and descriptor.get("publication_authorized") is False,
                "Browser dependency collection differs",
            )
            asset = descriptor["asset"]
            name = browser.safe_path(asset["name"])
            require("/" not in name, "Unsafe dependency asset name")
            dependency = dependency_collection / name
            require(
                dependency.is_file()
                and not dependency.is_symlink()
                and dependency.stat().st_size == asset["size"],
                "Browser dependency asset differs",
            )
            with dependency.open("rb") as stream:
                require(
                    "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest()
                    == asset["sha256"],
                    "Browser dependency asset digest differs",
                )
            npm_members(retained, inventory, dependency)
            pack = output / "browser-inputs.tar"
            with tarfile.open(pack, "w:", format=tarfile.USTAR_FORMAT) as packed:
                for path in sorted(retained.rglob("*")):
                    if path.is_file():
                        entry = tarfile.TarInfo(path.relative_to(retained).as_posix())
                        raw = path.read_bytes()
                        entry.size, entry.mode = len(raw), 0o600
                        packed.addfile(entry, io.BytesIO(raw))
            result = replay(
                context,
                pack,
                archive,
                tested_config,
                runtime_pack,
                source_root=source_root,
                execute=operations.run,
            )
            result["builder_config"] = builder_config
            create_output(output / "browser-verification.json", json_bytes(result))
            return result
    finally:
        try:
            operations.run("docker", "rm", "--force", container, timeout=120)
        except InvalidRelease:
            pass  # Creation can fail before our unique container exists.

#!/usr/bin/env python3
"""Collect exact runtime source and notices without publishing any image.

APKBUILD execution is confined to an unprivileged disposable container. Source
archives are retained alongside notices; a release needs both, not just SPDX IDs.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import tarfile
import tempfile
import urllib.request
import urllib.error
import uuid
import zipfile

from release_artifacts import (
    COMMIT,
    DIGEST,
    InvalidRelease,
    json_bytes,
    matches,
    require,
)

NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,120}\Z")
NOTICE_NAME = re.compile(
    r"(?:LICEN[CS]E|COPYING|COPYRIGHT|NOTICE|PATENTS|AUTHORS)(?:[._-].*)?\Z", re.I
)
LIMIT = 512 * 1024 * 1024
EXPANDED_LIMIT = 2 * 1024 * 1024 * 1024
RECIPE_LIMIT = 8 * 1024 * 1024
API_ROOT = "https://api.github.com/repos/alpinelinux/aports/contents/"


def command(
    *args: str, timeout: int = 300, operation: str = "runtime-command"
) -> bytes:
    # Only a named operation and exit status cross the public diagnostic boundary.
    # Commands, environment and captured output can contain private paths or tokens.
    require(
        re.fullmatch(r"[a-z][a-z0-9-]{0,95}", operation) is not None,
        "Invalid runtime operation label",
    )
    try:
        result = subprocess.run(args, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise InvalidRelease(
            f"Runtime collection command timed out: {operation}"
        ) from None
    except OSError:
        raise InvalidRelease(
            f"Runtime collection command could not start: {operation}"
        ) from None
    require(
        result.returncode == 0,
        f"Runtime collection command failed: {operation} (exit {result.returncode})",
    )
    return result.stdout


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        raise InvalidRelease("Runtime source redirect rejected")


def read_url(url: str, limit: int, github_api: bool = False) -> bytes:
    headers = {
        "User-Agent": "psst.zip-runtime-source-collector",
        "Accept": "application/vnd.github+json",
    }
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    require(not github_api or url.startswith(API_ROOT), "Unexpected recipe API URL")
    if github_api and token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(url, headers=headers)
    # Reject before redirecting: checking response.url afterward is too late to
    # prevent a bearer token from reaching a redirected host.
    with urllib.request.build_opener(NoRedirect()).open(
        request, timeout=60
    ) as response:
        data = response.read(limit + 1)
    require(len(data) <= limit, "Runtime source download exceeds size limit")
    return data


def packages(data: bytes) -> list[dict]:
    result = []
    seen = set()
    for block in data.decode().strip().split("\n\n"):
        values = {}
        for line in block.splitlines():
            if line.startswith("F:"):
                break
            if len(line) > 2 and line[1] == ":":
                key, value = line[0], line[2:]
                require(key not in values, "Duplicate APK database metadata")
                values[key] = value
        for key in ("P", "V", "A", "L", "o", "c", "C"):
            require(bool(values.get(key)), f"APK metadata missing {key}")
        matches(values["P"], NAME, "Unsafe APK name")
        matches(values["o"], NAME, "Unsafe APK origin")
        matches(values["V"], NAME, "Unsafe APK version")
        matches(values["c"], COMMIT, "APK source packaging commit missing")
        require(
            values["A"] in {"x86_64", "aarch64", "noarch"},
            "Unsupported APK architecture",
        )
        require(values["P"] not in seen, "Duplicate installed APK package")
        seen.add(values["P"])
        result.append(
            {
                "name": values["P"],
                "version": values["V"],
                "architecture": values["A"],
                "license": values["L"],
                "origin": values["o"],
                "aports_commit": values["c"],
                "apk_checksum": values["C"],
            }
        )
    require(bool(result), "Empty runtime package inventory")
    return result


def safe_member(name: str) -> PurePosixPath:
    path = PurePosixPath(name)
    require(
        bool(name)
        and not path.is_absolute()
        and ".." not in path.parts
        and "\\" not in name,
        "Unsafe source archive path",
    )
    return path


def source_notices(
    archive: Path,
    budget: list[int] | None = None,
    depth: int = 0,
    non_archives: list[dict] | None = None,
) -> dict[str, bytes]:
    """Read source documents without extracting untrusted source files to the host."""
    result = {}
    require(depth <= 4, "Nested source archive depth exceeds limit")
    budget = [EXPANDED_LIMIT, 100_000, 64 * 1024 * 1024] if budget is None else budget
    seen = set()
    non_archives = [] if non_archives is None else non_archives

    def account(name: str, size: int) -> None:
        safe_member(name)
        require(name not in seen, "Duplicate source archive path")
        seen.add(name)
        budget[0] -= size
        budget[1] -= 1
        require(min(budget[:2]) >= 0, "Expanded source archive exceeds limit")

    def notice(name: str, data: bytes) -> None:
        budget[2] -= len(data)
        require(budget[2] >= 0, "Combined source notices exceed limit")
        result[name] = data

    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as source:
            for entry in source.infolist():
                account(entry.filename, entry.file_size)
                if not entry.is_dir() and NOTICE_NAME.fullmatch(
                    PurePosixPath(entry.filename).name
                ):
                    require(
                        entry.file_size <= RECIPE_LIMIT, "Source notice exceeds limit"
                    )
                    notice(entry.filename, source.read(entry))
    else:
        with tarfile.open(archive, "r:*") as source:
            for entry in source:
                account(entry.name, entry.size)
                if entry.isfile() and NOTICE_NAME.fullmatch(
                    PurePosixPath(entry.name).name
                ):
                    require(entry.size <= RECIPE_LIMIT, "Source notice exceeds limit")
                    notice(entry.name, source.extractfile(entry).read())
                # srcpkg contains upstream archives. Read each nested source in a
                # bounded scratch file, preserving its original bytes separately.
                elif entry.isfile() and re.search(
                    r"\.(?:tar\.(?:gz|bz2|xz)|tgz|zip)$", entry.name
                ):
                    require(entry.size <= LIMIT, "Nested source archive exceeds limit")
                    with tempfile.TemporaryDirectory(
                        prefix="psst-runtime-notice-"
                    ) as folder:
                        nested = Path(folder) / PurePosixPath(entry.name).name
                        with source.extractfile(entry) as stream, nested.open(
                            "wb"
                        ) as dest:
                            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                                dest.write(chunk)
                        try:
                            nested_notices = source_notices(
                                nested, budget, depth + 1, non_archives
                            )
                        except tarfile.ReadError:
                            # Upstream tests contain intentionally invalid archive
                            # fixtures. Original srcpkg inputs must still parse;
                            # deeper non-archives are retained and flagged for review.
                            require(depth > 0, "Original source archive cannot be read")
                            non_archives.append(
                                {"path": entry.name, "sha256": file_hash(nested)}
                            )
                            continue
                        for name, content in nested_notices.items():
                            result[entry.name + "::" + name] = content
    return result


def recipe(origin: str, commit: str, output: Path) -> str:
    """Fetch full exact origin recipe, verifying Git blob IDs from its Git tree."""

    matches(origin, NAME, "Unsafe recipe origin")
    matches(commit, COMMIT, "Unsafe recipe commit")
    budget = [32 * 1024 * 1024, 2048]

    def directory(relative: str, local: Path, entries: list, depth: int = 0) -> None:
        require(depth <= 6, "Recipe directory depth exceeds limit")
        require(
            isinstance(entries, list) and len(entries) <= 512,
            "Unexpected recipe listing",
        )
        local.mkdir(parents=True, exist_ok=True)
        names = set()
        for entry in entries:
            require(isinstance(entry, dict), "Invalid recipe entry")
            name = entry.get("name")
            require(
                isinstance(name, str)
                and PurePosixPath(name).name == name
                and name not in {".", ".."},
                "Unsafe recipe filename",
            )
            safe_member(name)
            require(name not in names, "Duplicate recipe name")
            names.add(name)
            budget[1] -= 1
            require(budget[1] >= 0, "Recipe file count exceeds limit")
            child = relative + "/" + name
            require(entry.get("path") == child, "Recipe tree path mismatch")
            if entry.get("type") == "dir":
                children = json.loads(
                    read_url(API_ROOT + child + "?ref=" + commit, RECIPE_LIMIT, True)
                )
                directory(child, local / name, children, depth + 1)
            else:
                require(entry.get("type") == "file", "Unsupported recipe entry")
                data = read_url(
                    "https://raw.githubusercontent.com/alpinelinux/aports/"
                    + commit
                    + "/"
                    + child,
                    RECIPE_LIMIT,
                )
                oid = hashlib.sha1(
                    b"blob " + str(len(data)).encode() + b"\0" + data
                ).hexdigest()
                require(oid == entry.get("sha"), "Recipe Git blob checksum differs")
                budget[0] -= len(data)
                require(budget[0] >= 0, "Recipe total size exceeds limit")
                (local / name).write_bytes(data)

    for repository in ("main", "community"):
        try:
            relative = repository + "/" + origin
            entries = json.loads(
                read_url(API_ROOT + relative + "?ref=" + commit, RECIPE_LIMIT, True)
            )
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
            continue
        # A missing child in an existing recipe is an integrity failure, not
        # permission to mix in a similarly named recipe from another repository.
        directory(relative, output, entries)
        require((output / "APKBUILD").is_file(), "Recipe lacks APKBUILD")
        return repository
    raise InvalidRelease("Installed APK origin recipe not found at exact commit")


def source_package(
    helper: str, package: dict, recipe_dir: Path, output: Path
) -> tuple[Path, dict]:
    output.mkdir(mode=0o700)
    uid, gid = os.getuid(), os.getgid()
    # Only this collector's fresh output directory is mounted writable. APKBUILD
    # has no socket, credentials, source checkout or access to deployment state.
    # APKBUILD recipes intentionally use unset optional abuild variables, so do
    # not impose shell nounset while sourcing a recipe.
    script = r"""set -e
mkdir -p /work/recipe /work/user/.abuild /work/distfiles
cp -a /input/. /work/recipe/
cd /work/recipe
. ./APKBUILD
printf '%s\0%s\0%s\0%s\0' "$pkgname" "$pkgver-r$pkgrel" "${subpackages:-}" "${sha512sums:-}" > /output/recipe-metadata
abuild -C /work/recipe -s /work/distfiles -P /output fetch srcpkg
"""
    container = "psst-runtime-source-" + uuid.uuid4().hex
    try:
        command(
            "docker",
            "run",
            "--rm",
            "--name",
            container,
            "--read-only",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--user",
            f"{uid}:{gid}",
            "--pids-limit",
            "128",
            "--memory",
            "1g",
            "--cpus",
            "2",
            "--tmpfs",
            f"/work:rw,size=2g,uid={uid},gid={gid},mode=0700",
            "--tmpfs",
            f"/tmp:rw,size=128m,uid={uid},gid={gid},mode=1777",
            "--env",
            "HOME=/work/user",
            "--env",
            "ABUILD_USERDIR=/work/user/.abuild",
            "--mount",
            f"type=bind,src={recipe_dir},dst=/input,readonly",
            "--mount",
            f"type=bind,src={output},dst=/output",
            "--entrypoint",
            "/bin/sh",
            helper,
            "-c",
            script,
            timeout=900,
            operation="apk-source-package-fetch",
        )
    finally:
        # A subprocess timeout does not stop a detached Docker workload by itself.
        subprocess.run(
            ["docker", "rm", "-f", container], capture_output=True, timeout=30
        )
    metadata_file = output / "recipe-metadata"
    require(
        stat.S_ISREG(metadata_file.lstat().st_mode)
        and metadata_file.stat().st_size <= RECIPE_LIMIT,
        "Invalid collected recipe metadata",
    )
    metadata = metadata_file.read_bytes().decode().split("\0")
    require(len(metadata) == 5 and metadata[-1] == "", "Invalid source recipe metadata")
    origin, version, subpackages, sums = metadata[:-1]
    require(
        origin == package["origin"] and version == package["version"],
        "Source recipe version differs from installed package",
    )
    members = {origin} | {name.split(":")[0] for name in subpackages.split()}
    require(
        package["name"] in members, "Installed package not declared by source recipe"
    )
    archives = list(output.glob("src/**/*.tar.gz"))
    require(len(archives) == 1, "Missing or ambiguous abuild source package")
    archive = archives[0]
    for path in (archive, *archive.parents):
        if path == output:
            break
        require(not path.is_symlink(), "Collected source path contains a symlink")
    require(
        stat.S_ISREG(archive.lstat().st_mode),
        "Collected source is not a regular archive",
    )
    verify_source_package(archive, sums)
    return archive, {
        "origin": origin,
        "version": version,
        "declared_packages": sorted(members),
        "source_sha512sums": sums,
    }


def verify_source_package(archive: Path, sums: str) -> None:
    require(archive.stat().st_size <= LIMIT, "Source package exceeds limit")
    # Independently check original source bytes against original APKBUILD sums.
    with tarfile.open(archive) as source:
        entries = {}
        expanded = 0
        for item in source:
            safe_member(item.name)
            expanded += item.size
            require(expanded <= EXPANDED_LIMIT, "Expanded source package exceeds limit")
            if item.isfile():
                basename = PurePosixPath(item.name).name
                require(basename not in entries, "Ambiguous source checksum input")
                entries[basename] = item
        for line in sums.splitlines():
            line = line.strip()
            if not line:
                continue
            match = re.fullmatch(r"([0-9a-f]{128})\s+([^/\s]+)", line)
            require(match is not None, "Unsupported source checksum table")
            expected, filename = match.groups()
            entry = entries.get(filename)
            require(
                entry is not None and entry.size <= LIMIT,
                "Source package missing checksum input",
            )
            with source.extractfile(entry) as stream:
                checksum = hashlib.sha512()
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    checksum.update(chunk)
            require(
                checksum.hexdigest() == expected, "Original source checksum mismatch"
            )


def image_info(image: str) -> dict:
    result = json.loads(
        command("docker", "image", "inspect", image, operation="runtime-image-inspect")
    )
    require(isinstance(result, list) and len(result) == 1, "Image must exist locally")
    info = result[0]
    matches(info.get("Id"), DIGEST, "Invalid runtime image ID")
    require(
        info.get("Architecture") in {"amd64", "arm64"}, "Unsupported image architecture"
    )
    return info


def recipe_notices(folder: Path) -> dict[str, bytes]:
    """Preserve embedded notices in Alpine's own local helper sources.

    Full local files are retained rather than attempting to cut a legal header
    at an arbitrary number of lines. Upstream archives have a separate collector.
    """
    result = {}
    for path in sorted(folder.rglob("*")):
        if path.is_file():
            data = path.read_bytes()
            if NOTICE_NAME.fullmatch(path.name) or re.search(
                rb"\bcopyright\b", data, re.I
            ):
                require(
                    len(data) <= RECIPE_LIMIT, "Embedded recipe notice exceeds limit"
                )
                result["recipe/" + path.relative_to(folder).as_posix()] = data
    return result


def layer_packages(archive: Path, image_id: str) -> tuple[list[dict], list[dict]]:
    """Include APK versions retained in lower layers after package upgrades."""
    require(
        archive.stat().st_size <= 2 * 1024 * 1024 * 1024,
        "Saved runtime image exceeds limit",
    )
    result, databases = {}, []
    with tarfile.open(archive) as saved:
        members = saved.getmembers()
        require(len(members) <= 10_000, "Saved image member count exceeds limit")
        paths = set()
        for member in members:
            safe_member(member.name)
            require(member.name not in paths, "Duplicate saved image path")
            paths.add(member.name)
        manifest = saved.getmember("manifest.json")
        require(
            manifest.isfile() and manifest.size <= RECIPE_LIMIT,
            "Invalid saved image manifest",
        )
        manifests = json.loads(saved.extractfile(manifest).read())
        require(
            isinstance(manifests, list) and len(manifests) == 1,
            "Ambiguous saved runtime image",
        )
        config = saved.getmember(str(safe_member(manifests[0]["Config"])))
        require(
            config.isfile() and config.size <= RECIPE_LIMIT,
            "Invalid saved image config",
        )
        config_bytes = saved.extractfile(config).read()
        require(
            "sha256:" + hashlib.sha256(config_bytes).hexdigest() == image_id,
            "Saved image identity differs",
        )
        config_value = json.loads(config_bytes)
        layers = manifests[0].get("Layers")
        require(
            isinstance(layers, list) and 0 < len(layers) <= 128,
            "Invalid runtime layers",
        )
        require(
            len(layers) == len(config_value["rootfs"]["diff_ids"]),
            "Saved image layer count differs",
        )
        for index, name in enumerate(layers):
            member = saved.getmember(str(safe_member(name)))
            require(member.isfile(), "Invalid saved runtime layer")
            with saved.extractfile(member) as stream:
                layer_sha256 = hashlib.file_digest(stream, "sha256").hexdigest()
            count, expanded = 0, 0
            with saved.extractfile(member) as stream, tarfile.open(
                fileobj=stream, mode="r|*"
            ) as layer:
                for entry in layer:
                    safe_member(entry.name)
                    count += 1
                    expanded += entry.size
                    require(
                        count <= 1_000_000 and expanded <= 8 * 1024 * 1024 * 1024,
                        "Runtime layer exceeds bounds",
                    )
                    if (
                        entry.isfile()
                        and entry.name.lstrip("./") == "lib/apk/db/installed"
                    ):
                        require(
                            entry.size <= RECIPE_LIMIT,
                            "Runtime APK database exceeds limit",
                        )
                        data = layer.extractfile(entry).read()
                        found = packages(data)
                        databases.append(
                            {
                                "layer": index,
                                "layer_sha256": layer_sha256,
                                "database_sha256": hashlib.sha256(data).hexdigest(),
                            }
                        )
                        for package in found:
                            key = tuple(
                                package[field]
                                for field in ("name", "version", "aports_commit")
                            )
                            result[key] = package
    require(databases, "Saved runtime layers lack APK inventory")
    return list(result.values()), databases


def collect(image: str, helper: str, output: Path) -> dict:
    require(not output.exists(), "Runtime output directory already exists")
    output.mkdir(mode=0o700, parents=True)
    info = image_info(image)
    helper_info = image_info(helper)
    image, helper = info["Id"], helper_info["Id"]
    helper_database = command(
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
        helper,
        "/lib/apk/db/installed",
        operation="helper-apk-inventory",
    )
    raw = command(
        "docker",
        "run",
        "--rm",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--security-opt",
        "no-new-privileges",
        "--entrypoint",
        "cat",
        image,
        "/lib/apk/db/installed",
        operation="runtime-apk-inventory",
    )
    entries = packages(raw)
    (output / "installed-apk-db").write_bytes(raw)
    with tempfile.TemporaryDirectory(prefix="psst-runtime-layers-") as folder:
        archive = Path(folder) / "image.tar"
        command(
            "docker",
            "image",
            "save",
            "--output",
            str(archive),
            image,
            timeout=600,
            operation="runtime-image-save",
        )
        retained, layers = layer_packages(archive, image)
    all_packages = {
        tuple(item[field] for field in ("name", "version", "aports_commit")): item
        for item in retained + entries
    }
    final_keys = {
        tuple(item[field] for field in ("name", "version", "aports_commit"))
        for item in entries
    }
    entries = list(all_packages.values())
    for package in entries:
        package["present_in_final_filesystem"] = (
            tuple(package[field] for field in ("name", "version", "aports_commit"))
            in final_keys
        )
    origins = {}
    notices = []
    for package in entries:
        key = (package["origin"], package["version"], package["aports_commit"])
        if key not in origins:
            folder = output / (package["origin"] + "-" + package["aports_commit"])
            folder.mkdir()
            repository = recipe(
                package["origin"], package["aports_commit"], folder / "recipe"
            )
            source, metadata = source_package(
                helper, package, folder / "recipe", folder / "collected"
            )
            non_archives = []
            texts = source_notices(source, non_archives=non_archives)
            texts.update(recipe_notices(folder / "recipe"))
            origins[key] = {
                **metadata,
                "aports_commit": package["aports_commit"],
                "repository": repository,
                "source_file": str(source.relative_to(output)),
                "source_sha256": file_hash(source),
                "notices": {
                    name: hashlib.sha256(content).hexdigest()
                    for name, content in texts.items()
                },
                "nested_non_archives_requiring_review": non_archives,
            }
            for name, content in texts.items():
                notices.append(f"\n{package['origin']} — {name}\n".encode() + content)
        require(
            package["name"] in origins[key]["declared_packages"],
            "Undeclared runtime subpackage",
        )
        package["source_key"] = "/".join(key)
    result = {
        "schema_version": 1,
        "image_id": info["Id"],
        "architecture": info["Architecture"],
        "helper_image_id": helper_info["Id"],
        "helper_packages": packages(helper_database),
        "layer_databases": layers,
        "packages": entries,
        "sources": list(origins.values()),
        "review_required": True,
        "missing_notice_origins": sorted(
            {item["origin"] for item in origins.values() if not item["notices"]}
        ),
    }
    (output / "runtime-inventory.json").write_bytes(json_bytes(result))
    (output / "THIRD_PARTY_NOTICES.txt").write_bytes(
        b"psst.zip runtime source/license collection\n" + b"\n".join(notices)
    )
    return result


def file_hash(path: Path) -> str:
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument(
        "--helper-image",
        required=True,
        help="Locally built unprivileged abuild collector image; never the application image",
    )
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        result = collect(args.image, args.helper_image, args.output.resolve())
        print(
            json.dumps(
                {
                    "packages": len(result["packages"]),
                    "sources": len(result["sources"]),
                    "review_required": True,
                    "missing_notice_origins": result["missing_notice_origins"],
                }
            )
        )
    except (InvalidRelease, OSError, ValueError, subprocess.TimeoutExpired) as error:
        parser.exit(1, f"Runtime collection rejected: {error}\n")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Retain exact application dependency inputs; never authorize distribution.

Go module ZIP/go.mod inputs are independently checked against authenticated sums.
Every npm lock entry is retained, including optional platforms and build inputs,
without installing packages or running their scripts. Preferred-form upstream
source review remains distinct from retaining original package distributions.
"""

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request
import zipfile

from github_release_transport import command
from publish_container_release import sha256
from release_artifacts import (
    COMMIT,
    DIGEST,
    VERSION,
    InvalidRelease,
    create_output,
    git,
    json_bytes,
    matches,
    read_json,
    repository_name,
    require,
)

MAX_PACKAGE = 512 * 1024**2
MAX_TOTAL = 2 * 1024**3
MAX_MEMBERS = 100_000
GO_VERSION = "go1.27.2"
GO_SCRIPT = r"""set -eu
export GOTOOLCHAIN=local GOPATH=/tmp/go GOMODCACHE=/reports/cache GOCACHE=/tmp/build
export GOWORK=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE= GONOPROXY= GONOSUMDB= GOFLAGS=-mod=readonly
mkdir /reports/work
cp /source/go.mod /source/go.sum /reports/work/
cd /reports/work
go version > /reports/go-version.txt
go env -json GOTOOLCHAIN GOPROXY GOSUMDB GOPRIVATE GONOPROXY GONOSUMDB GOFLAGS > /reports/go-environment.json
go mod download -json all > /reports/go-downloads.json
go list -m -json all > /reports/go-modules.json
cmp /source/go.mod go.mod
cp go.sum /reports/authenticated-go.sum
"""


def stream_json(raw: bytes) -> list[dict]:
    require(len(raw) <= 16 * 1024**2, "Dependency metadata exceeds bounds")
    text, position, result = raw.decode(), 0, []
    decoder = json.JSONDecoder()
    while position < len(text):
        if text[position].isspace():
            position += 1
            continue
        value, position = decoder.raw_decode(text, position)
        require(
            isinstance(value, dict) and len(result) < 4096, "Invalid module metadata"
        )
        require(
            not value.get("Error") and not value.get("Replace"),
            "Failed/replaced module input",
        )
        result.append(value)
    require(result, "Empty module metadata")
    return result


def safe_name(name: str) -> str:
    path = PurePosixPath(name)
    require(
        name
        and not path.is_absolute()
        and ".." not in path.parts
        and "\\" not in name
        and "\n" not in name
        and "\r" not in name
        and str(path) == name.rstrip("/"),
        "Unsafe dependency archive path",
    )
    return name


def h1(lines: list[tuple[str, str]]) -> str:
    summary = "".join(checksum + "  " + name + "\n" for name, checksum in sorted(lines))
    return "h1:" + base64.b64encode(hashlib.sha256(summary.encode()).digest()).decode()


def module_zip_sum(raw: bytes, module: str, version: str) -> str:
    prefix, names, total, lines = module + "@" + version + "/", set(), 0, []
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        for entry in archive.infolist():
            safe_name(entry.filename)
            require(
                entry.filename.startswith(prefix)
                and entry.filename not in names
                and len(names) < MAX_MEMBERS,
                "Invalid module ZIP inventory",
            )
            names.add(entry.filename)
            mode = entry.external_attr >> 16
            require(not stat.S_ISLNK(mode), "Module ZIP contains a link")
            total += entry.file_size
            require(total <= MAX_PACKAGE, "Module ZIP expansion exceeds bounds")
            with archive.open(entry) as content:
                digest = hashlib.file_digest(content, "sha256").hexdigest()
            lines.append((entry.filename, digest))
    require(lines, "Empty module source ZIP")
    return h1(lines)


def bounded(path: Path, limit=MAX_PACKAGE) -> bytes:
    require(
        path.is_file() and not path.is_symlink() and 0 < path.stat().st_size <= limit,
        "Invalid dependency input file",
    )
    with path.open("rb") as stream:
        raw = stream.read(limit + 1)
    require(len(raw) <= limit, "Dependency input exceeds bounds")
    return raw


def module_inputs(
    records: list[dict], selected: list[dict], cache: Path, sums: bytes
) -> tuple[dict[str, bytes], list[dict]]:
    expected = {}
    for line in sums.decode().splitlines():
        parts = line.split()
        require(len(parts) == 3, "Invalid committed go.sum")
        key = tuple(parts[:2])
        require(
            key not in expected or expected[key] == parts[2], "Conflicting module sum"
        )
        expected[key] = parts[2]
    required = {(r["Path"], r["Version"]) for r in selected if not r.get("Main")}
    require(
        required and len(required) == len([r for r in selected if not r.get("Main")]),
        "Missing/duplicate selected modules",
    )
    files, inventory, seen = {}, [], set()
    for record in records:
        key = (record.get("Path"), record.get("Version"))
        require(
            key in required and key not in seen,
            "Downloaded modules differ from selected graph",
        )
        seen.add(key)
        module, version = key
        require(
            isinstance(module, str) and isinstance(version, str),
            "Malformed module identity",
        )
        retained = {}
        for field, suffix in (("Zip", "zip"), ("GoMod", "mod"), ("Info", "info")):
            source = Path(record.get(field, ""))
            require(
                source.is_absolute() and source.is_relative_to(Path("/reports/cache")),
                "Module download escaped private cache",
            )
            relative = source.relative_to("/reports/cache")
            require(".." not in relative.parts, "Unsafe module cache path")
            local = cache / relative
            require(
                all(
                    not parent.is_symlink()
                    for parent in (local, *local.parents)
                    if parent.is_relative_to(cache)
                ),
                "Module cache path contains a link",
            )
            retained[suffix] = bounded(local)
        zip_sum = module_zip_sum(retained["zip"], module, version)
        mod_sum = h1([("go.mod", hashlib.sha256(retained["mod"]).hexdigest())])
        require(
            zip_sum == record.get("Sum") == expected.get(key)
            and mod_sum
            == record.get("GoModSum")
            == expected.get((module, version + "/go.mod")),
            "Module inputs differ from authenticated go.sum",
        )
        info = read_json(retained["info"])
        require(
            isinstance(info, dict) and info.get("Version") == version,
            "Module version info differs",
        )
        name = hashlib.sha256((module + "@" + version).encode()).hexdigest()
        inputs = {}
        for suffix, content in retained.items():
            path = "go/" + name + "." + suffix
            files[path] = content
            inputs[suffix] = {
                "file": path,
                "sha256": sha256(content),
                "size": len(content),
            }
        inventory.append(
            {
                "module": module,
                "version": version,
                "sum": zip_sum,
                "go_mod_sum": mod_sum,
                "inputs": inputs,
            }
        )
    require(seen == required, "Selected module source inputs are missing")
    return files, sorted(inventory, key=lambda row: (row["module"], row["version"]))


def additional_sums(original: bytes, authenticated: bytes) -> list[dict]:
    def parse(raw):
        result = {}
        for line in raw.decode().splitlines():
            parts = line.split()
            require(
                len(parts) == 3 and parts[2].startswith("h1:"), "Invalid Go sum input"
            )
            key = tuple(parts[:2])
            require(key not in result, "Duplicate Go sum input")
            result[key] = parts[2]
        return result

    committed, measured = parse(original), parse(authenticated)
    require(
        all(measured.get(key) == value for key, value in committed.items()),
        "Authenticated collection changed committed sums",
    )
    return [
        {"module": key[0], "version": key[1], "sum": measured[key]}
        for key in sorted(measured.keys() - committed.keys())
    ]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise InvalidRelease("Dependency registry redirected unexpectedly")


def download(url: str) -> bytes:
    parsed = urllib.parse.urlsplit(url)
    require(
        parsed.scheme == "https"
        and parsed.netloc == "registry.npmjs.org"
        and not parsed.query
        and not parsed.fragment
        and parsed.path.endswith(".tgz"),
        "Unsupported dependency registry URL",
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(
        urllib.request.Request(url, headers={"User-Agent": "psst.zip-release-inputs"}),
        timeout=55,
    ) as response:
        require(response.status == 200, "Dependency registry download failed")
        chunks, count, deadline = [], 0, time.monotonic() + 55
        while True:
            require(time.monotonic() < deadline, "Dependency download timed out")
            chunk = response.read1(1024**2)
            if not chunk:
                break
            count += len(chunk)
            require(count <= MAX_PACKAGE, "Dependency registry input exceeds bounds")
            chunks.append(chunk)
        raw = b"".join(chunks)
    require(0 < len(raw) <= MAX_PACKAGE, "Dependency registry input exceeds bounds")
    return raw


def npm_inputs(lock: bytes, fetch=download) -> tuple[dict[str, bytes], list[dict]]:
    value = read_json(lock)
    require(
        isinstance(value, dict)
        and value.get("lockfileVersion") == 3
        and isinstance(value.get("packages"), dict),
        "Unsupported npm lock format",
    )
    files, inventory, total = {}, [], 0
    require(0 < len(value["packages"]) <= 4096, "Missing/oversized npm lock graph")
    for path, entry in sorted(value["packages"].items()):
        if not path:
            continue
        require(
            isinstance(path, str) and isinstance(entry, dict) and not entry.get("link"),
            "Unsupported npm lock entry",
        )
        safe_name(path)
        require("node_modules/" in path, "Unexpected npm package path")
        name = path.rsplit("node_modules/", 1)[1]
        require(
            re.fullmatch(r"(?:@[a-z0-9_.-]+/)?[a-z0-9_.-]+", name) is not None,
            "Unsupported npm package name",
        )
        integrity = entry.get("integrity")
        require(
            isinstance(integrity, str) and integrity.startswith("sha512-"),
            "npm input lacks SHA512 integrity",
        )
        expected = base64.b64decode(integrity[7:], validate=True)
        require(len(expected) == 64, "Malformed npm integrity")
        url = entry.get("resolved")
        require(isinstance(url, str), "npm registry URL is missing")
        # Check the same fixed URL policy even when a fixture downloader is used.
        parsed = urllib.parse.urlsplit(url)
        require(
            parsed.scheme == "https"
            and parsed.netloc == "registry.npmjs.org"
            and not parsed.query
            and not parsed.fragment
            and parsed.path.endswith(".tgz"),
            "Unsupported dependency registry URL",
        )
        raw = fetch(url)
        total += len(raw)
        require(
            total <= MAX_TOTAL
            and len(raw) <= MAX_PACKAGE
            and hashlib.sha512(raw).digest() == expected,
            "npm input integrity/size differs",
        )
        names, expanded, package, archive_root = set(), 0, None, None
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
            for member in archive:
                safe_name(member.name)
                root = PurePosixPath(member.name).parts[0]
                if archive_root is None:
                    archive_root = root
                require(
                    root == archive_root
                    and ("/" in member.name or member.isdir())
                    and member.name not in names
                    and len(names) < MAX_MEMBERS
                    and (member.isfile() or member.isdir()),
                    "Invalid npm archive inventory",
                )
                names.add(member.name)
                expanded += member.size
                require(expanded <= MAX_PACKAGE, "npm source expansion exceeds bounds")
                if member.name == archive_root + "/package.json":
                    require(
                        member.isfile() and member.size <= 1024**2,
                        "Malformed package metadata",
                    )
                    package = read_json(archive.extractfile(member).read())
        require(
            isinstance(package, dict)
            and package.get("name") == name
            and package.get("version") == entry.get("version"),
            "npm package identity differs from lock",
        )
        file = (
            "npm/"
            + hashlib.sha256((path + "@" + entry["version"]).encode()).hexdigest()
            + ".tgz"
        )
        files[file] = raw
        inventory.append(
            {
                "path": path,
                "name": name,
                "version": entry["version"],
                "resolved": url,
                "integrity": integrity,
                "file": file,
                "sha256": sha256(raw),
                "size": len(raw),
                "optional": entry.get("optional") is True,
                "development": entry.get("dev") is True,
            }
        )
    require(inventory, "No npm dependency inputs")
    return files, inventory


def collect(
    *,
    root: Path,
    repository: str,
    version: str,
    commit: str,
    platform: str,
    go_image: str,
    output: Path,
    execute=command,
    fetch=download,
) -> dict:
    repository_name(repository)
    require(
        git(root, "remote", "get-url", "origin").decode().strip()
        in {
            f"https://github.com/{repository}.git",
            f"https://github.com/{repository}",
            f"git@github.com:{repository}.git",
            f"git@github.com:{repository}",
        },
        "Dependency checkout origin differs from selected repository",
    )
    matches(version, VERSION, "Invalid dependency release version")
    matches(commit, COMMIT, "Invalid dependency source commit")
    require(platform in {"linux/amd64", "linux/arm64"}, "Invalid dependency platform")
    require(
        re.fullmatch(r"docker.io/library/golang@sha256:[0-9a-f]{64}", go_image),
        "Go dependency collector requires immutable official builder",
    )
    require(
        not output.exists() and not output.is_symlink(),
        "Dependency output already exists",
    )
    inputs = {
        name: git(root, "show", commit + ":" + name)
        for name in ("backend/go.mod", "backend/go.sum", "web/package-lock.json")
    }
    with tempfile.TemporaryDirectory(
        prefix="psst-application-dependencies-"
    ) as temporary:
        private = Path(temporary)
        source, reports = private / "source", private / "reports"
        source.mkdir()
        reports.mkdir()
        for name in ("go.mod", "go.sum"):
            (source / name).write_bytes(inputs["backend/" + name])
        env = {
            k: v
            for k, v in os.environ.items()
            if k
            in {
                "PATH",
                "HOME",
                "DOCKER_HOST",
                "DOCKER_CONTEXT",
                "DOCKER_CONFIG",
                "DOCKER_CERT_PATH",
                "DOCKER_TLS_VERIFY",
                "XDG_RUNTIME_DIR",
            }
        }
        inspected = read_json(
            execute(
                ["docker", "image", "inspect", go_image], environment=env, timeout=55
            )
        )
        require(
            isinstance(inspected, list)
            and len(inspected) == 1
            and inspected[0].get("Os") == "linux"
            and inspected[0].get("Architecture") == platform.split("/")[1],
            "Preloaded Go builder platform differs",
        )
        config = matches(
            inspected[0].get("Id"), DIGEST, "Go builder configuration missing"
        )
        execute(
            [
                "docker",
                "run",
                "--rm",
                "--platform",
                platform,
                "--read-only",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--user",
                f"{os.getuid()}:{os.getgid()}",
                "--pids-limit",
                "128",
                "--memory",
                "2g",
                "--cpus",
                "2",
                "--tmpfs",
                "/tmp:rw,nosuid,nodev,noexec,size=256m",
                "--env",
                "HOME=/tmp",
                "--mount",
                f"type=bind,src={source},dst=/source,readonly",
                "--mount",
                f"type=bind,src={reports},dst=/reports",
                "--entrypoint",
                "/bin/sh",
                config,
                "-ec",
                GO_SCRIPT,
            ],
            environment=env,
            timeout=1200,
        )
        require(
            bounded(reports / "go-version.txt", 1024).decode().strip()
            == f"go version {GO_VERSION} {platform}",
            "Unexpected dependency collector Go version",
        )
        settings = read_json(bounded(reports / "go-environment.json", 4096))
        require(
            settings
            == {
                "GOTOOLCHAIN": "local",
                "GOPROXY": "https://proxy.golang.org",
                "GOSUMDB": "sum.golang.org",
                "GOPRIVATE": "",
                "GONOPROXY": "",
                "GONOSUMDB": "",
                "GOFLAGS": "-mod=readonly",
            },
            "Dependency authentication settings differ",
        )
        authenticated_sums = bounded(reports / "authenticated-go.sum", 1024**2)
        added_sums = additional_sums(inputs["backend/go.sum"], authenticated_sums)
        go_files, modules = module_inputs(
            stream_json(bounded(reports / "go-downloads.json", 16 * 1024**2)),
            stream_json(bounded(reports / "go-modules.json", 16 * 1024**2)),
            reports / "cache",
            authenticated_sums,
        )
        npm_files, packages = npm_inputs(inputs["web/package-lock.json"], fetch)
        files = {
            **go_files,
            **npm_files,
            **{"inputs/" + name: raw for name, raw in inputs.items()},
            "inputs/authenticated-go.sum": authenticated_sums,
            **{
                "collection/" + name: bounded(reports / name, 16 * 1024**2)
                for name in (
                    "go-downloads.json",
                    "go-modules.json",
                    "go-environment.json",
                    "go-version.txt",
                )
            },
        }
        require(
            sum(map(len, files.values())) <= MAX_TOTAL,
            "Dependency source inputs exceed archive limit",
        )
        record = {
            "schema_version": 1,
            "kind": "application-dependency-inputs",
            "repository": repository,
            "version": version,
            "source_commit": commit,
            "inputs": {name: sha256(raw) for name, raw in inputs.items()},
            "go_collector": {
                "image": go_image,
                "config": config,
                "platform": platform,
                "version": GO_VERSION,
                "environment": settings,
            },
            "go_modules": modules,
            "additional_authenticated_sums": added_sums,
            "npm_packages": packages,
            "package_inputs_verified": True,
            "preferred_source_review_required": True,
            "publication_authorized": False,
        }
        files["dependency-inputs.json"] = json_bytes(record)
        files["SOURCE.md"] = (
            f"# psst.zip application dependency inputs\n\nVersion: {version}\nSource: {commit}\n\n"
            "Original Go module ZIP/go.mod/version inputs and npm registry tarballs are retained byte-for-byte.\n"
            "Go H1 and npm lock SHA512 integrity are independently verified.\n"
            "Committed sums remain unchanged. Additional sums needed for the full selected module graph were acquired with Go's public checksum database and retained separately.\n"
            "Package scripts were not executed. All locked optional/development npm packages are included.\n"
            "These are package distribution/build inputs. Preferred-form upstream source and distribution approval remain separate release review requirements.\n"
        ).encode()
        buffer = io.BytesIO()
        timestamp = int(git(root, "show", "-s", "--format=%ct", commit))
        with gzip.GzipFile(
            fileobj=buffer, mode="wb", filename="", mtime=timestamp
        ) as zipped:
            with tarfile.open(
                fileobj=zipped, mode="w", format=tarfile.USTAR_FORMAT
            ) as archive:
                for name, raw in sorted(files.items()):
                    entry = tarfile.TarInfo(name)
                    entry.size = len(raw)
                    entry.mtime = timestamp
                    entry.mode = 0o644
                    archive.addfile(entry, io.BytesIO(raw))
        require(
            len(buffer.getvalue()) <= MAX_TOTAL, "Dependency archive exceeds bounds"
        )
        output.mkdir(mode=0o700, parents=False)
        architecture = platform.split("/")[1]
        asset = output / f"psst.zip-dependency-inputs-{version}-{architecture}.tar.gz"
        create_output(asset, buffer.getvalue())
        result = {
            "schema_version": 1,
            "kind": "application-dependency-collection",
            "repository": repository,
            "version": version,
            "source_commit": commit,
            "platform": platform,
            "asset": {
                "name": asset.name,
                "sha256": sha256(buffer.getvalue()),
                "size": asset.stat().st_size,
            },
            "go_modules": len(modules),
            "npm_packages": len(packages),
            "package_inputs_verified": True,
            "preferred_source_review_required": True,
            "publication_authorized": False,
        }
        create_output(output / "dependency-collection.json", json_bytes(result))
        return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("repository", "version", "commit", "platform", "go-image"):
        parser.add_argument("--" + name, required=True)
    for name in ("root", "output"):
        parser.add_argument("--" + name, required=True, type=Path)
    args = parser.parse_args()
    try:
        collect(**vars(args))
    except (
        InvalidRelease,
        OSError,
        ValueError,
        tarfile.TarError,
        zipfile.BadZipFile,
    ) as error:
        parser.exit(1, f"Dependency inputs rejected: {error}\n")


if __name__ == "__main__":
    main()

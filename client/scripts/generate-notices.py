#!/usr/bin/env python3
"""Retain exact upstream notices for the CLI's resolved target package graphs.

This reads verified Go module-cache and toolchain files; it does not infer license
terms from package names or include unrelated go.mod requirements as linked code.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

CLIENT = Path(__file__).resolve().parents[1]
SITE = CLIENT / "internal/lanassets/site"
TARGETS = tuple(
    f"{system}/{arch}"
    for system in ("linux", "darwin", "windows")
    for arch in ("amd64", "arm64")
)
LEGAL_PREFIXES = ("license", "licence", "copying", "copyright", "notice", "patents")


def command(args: list[str], *, env: dict[str, str] | None = None) -> bytes:
    return subprocess.check_output(
        args,
        cwd=CLIENT,
        env=os.environ if env is None else env,
        timeout=90,
    )


def values(raw: bytes):
    text = raw.decode("utf-8")
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        yield value


def legal_files(directory: Path) -> list[Path]:
    return sorted(
        path
        for path in directory.iterdir()
        if path.is_file() and path.name.lower().startswith(LEGAL_PREFIXES)
    )


def collect(
    output: Path,
    targets: tuple[str, ...] = TARGETS,
    *,
    include_web: bool = True,
    workspace: bool = False,
) -> dict:
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("notice output directory must be empty")
    goroot = Path(command(["go", "env", "GOROOT"]).decode().strip())
    goversion = command(["go", "env", "GOVERSION"]).decode().strip()
    files: dict[str, bytes] = {}
    entries: dict[str, dict] = {}
    commands = []

    def retain(path: Path, relative: str, entry: dict) -> None:
        if path.is_symlink() or not path.is_file():
            raise ValueError("legal text must be a regular file")
        content = path.read_bytes()
        previous = files.setdefault(relative, content)
        if previous != content:
            raise ValueError("conflicting dependency legal text")
        notice = {"path": relative, "sha256": hashlib.sha256(content).hexdigest()}
        if notice not in entry["notices"]:
            entry["notices"].append(notice)

    go_entry = {
        "module": "Go toolchain and standard library",
        "version": goversion,
        "packages": [],
        "targets": list(targets),
        "notices": [],
    }
    entries["go"] = go_entry
    for path in legal_files(goroot):
        retain(path, f"go/{path.name}", go_entry)
    if not (goroot / "LICENSE").is_file():
        raise ValueError("Go toolchain license is missing")

    for target in targets:
        if target not in TARGETS:
            raise ValueError("unsupported CLI release target")
        system, arch = target.split("/")
        environment = os.environ | {
            "CGO_ENABLED": "0",
            "GOOS": system,
            "GOARCH": arch,
            "GOFLAGS": "-mod=readonly",
            "GOWORK": str(CLIENT / "go.work") if workspace else "off",
        }
        args = ["go", "list", "-deps", "-json", "./cmd/psst"]
        commands.append({"target": target, "command": args})
        for package in values(command(args, env=environment)):
            if package.get("Error") or package.get("DepsErrors"):
                raise ValueError("Go package graph could not be resolved")
            module = package.get("Module")
            if module and (
                not module.get("Main")
                or module["Path"] == "github.com/endorses/psst.zip/backend"
            ):
                local_backend = bool(module.get("Main") and workspace)
                if module.get("Replace") or (
                    not local_backend and not module.get("Version")
                ):
                    raise ValueError(
                        "release dependencies must use pinned upstream modules"
                    )
                version = "workspace-source" if local_backend else module["Version"]
                identity = module["Path"] + "@" + version
                entry = entries.setdefault(
                    identity,
                    {
                        "module": module["Path"],
                        "version": version,
                        "packages": [],
                        "targets": [],
                        "notices": [],
                    },
                )
                if target not in entry["targets"]:
                    entry["targets"].append(target)
                if package["ImportPath"] not in entry["packages"]:
                    entry["packages"].append(package["ImportPath"])
                root = Path(module["Dir"])
                directory = Path(package["Dir"])
                if not directory.is_relative_to(root):
                    raise ValueError("package directory escaped its pinned module")
                # Retain root terms plus any package-specific terms along the
                # actual imported directory's ancestry (e.g. shellescape).
                while True:
                    for path in legal_files(directory):
                        relative = path.relative_to(root).as_posix()
                        retain(path, f"modules/{identity}/{relative}", entry)
                    if directory == root:
                        break
                    directory = directory.parent
                if not any(
                    Path(item["path"])
                    .name.lower()
                    .startswith(("license", "licence", "copying"))
                    for item in entry["notices"]
                ):
                    raise ValueError(f"No license text for {identity}")
            elif package.get("Standard") and package["ImportPath"].startswith(
                "vendor/"
            ):
                directory = Path(package["Dir"])
                while directory != goroot:
                    for path in legal_files(directory):
                        retain(
                            path, "go/" + path.relative_to(goroot).as_posix(), go_entry
                        )
                    directory = directory.parent

    # The executable also distributes the browser code and its exact bundled
    # dependency terms. Keep those independently identified from Go modules.
    browser = {"module": "Embedded LAN browser", "notices": []}
    if include_web:
        source_metadata = SITE / "licenses/SOURCE.json"
        if not source_metadata.is_file() or not (SITE / "index.html").is_file():
            raise ValueError(
                "embedded LAN assets are missing; prepare them before generating notices"
            )
        for path in sorted((SITE / "licenses").rglob("*")):
            if path.is_file():
                retain(
                    path,
                    "web/" + path.relative_to(SITE / "licenses").as_posix(),
                    browser,
                )
        for required in (
            "web/AGPL-3.0-only.txt",
            "web/THIRD_PARTY_NOTICES.txt",
            "web/dependency-inventory.json",
        ):
            if required not in files:
                raise ValueError("embedded browser license inventory is incomplete")

    inventory = {
        "schema_version": 1,
        "inputs": {
            name: hashlib.sha256((CLIENT / name).read_bytes()).hexdigest()
            for name in ("go.mod", "go.sum")
        },
        "commands": commands,
        "embedded_browser": browser,
        "modules": [entries[key] for key in sorted(entries)],
    }
    text = [
        "psst.zip CLI third-party notices",
        "Exact legal text from pinned modules used by the listed target package graphs.",
        "Go standard-library and imported vendored-package terms are retained too.",
        "The project itself is licensed under AGPL-3.0-only; see LICENSE.",
        "",
    ]
    for entry in inventory["modules"]:
        entry["packages"].sort()
        entry["targets"].sort()
        entry["notices"].sort(key=lambda item: item["path"])
        for item in entry["notices"]:
            text.extend(
                [
                    "=== " + item["path"] + " ===",
                    files[item["path"]].decode("utf-8"),
                    "",
                ]
            )
    for item in browser["notices"]:
        if Path(item["path"]).suffix.lower() in {".txt", ".md"}:
            text.extend(
                [
                    "=== " + item["path"] + " ===",
                    files[item["path"]].decode("utf-8"),
                    "",
                ]
            )
    files["dependency-inventory.json"] = (
        json.dumps(inventory, indent=2) + "\n"
    ).encode()
    files["THIRD_PARTY_NOTICES.txt"] = ("\n".join(text).rstrip() + "\n").encode()
    for relative, content in sorted(files.items()):
        path = output / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    return inventory


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    inventory = collect(args.output)
    print(
        f"Retained notices for {len(inventory['modules']) - 1} dependency modules and Go."
    )


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"Notice generation failed: {error}", file=sys.stderr)
        raise SystemExit(1) from None

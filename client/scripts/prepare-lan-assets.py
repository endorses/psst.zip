#!/usr/bin/env python3
"""Build and inventory the offline LAN browser UI, reusing unchanged build inputs."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[2]
WEB = ROOT / "web"
SITE = ROOT / "client/internal/lanassets/site"
METADATA = SITE / "licenses/SOURCE.json"


def digest_inputs() -> str:
    digest = hashlib.sha256()
    paths = [
        WEB / name
        for name in (
            "package.json",
            "package-lock.json",
            "svelte.config.js",
            "vite.config.ts",
        )
    ]
    for directory in ("src", "static", "scripts"):
        paths.extend(path for path in (WEB / directory).rglob("*") if path.is_file())
    for path in sorted(set(paths)):
        if not path.is_file():
            continue
        if path.is_symlink():
            raise ValueError("LAN web source must not contain symbolic links")
        digest.update(path.relative_to(ROOT).as_posix().encode() + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def inventory() -> dict[str, str]:
    files = {}
    for path in sorted(SITE.rglob("*")):
        if path == METADATA or path.name == "README.txt":
            continue
        if path.is_symlink():
            raise ValueError("LAN browser output must not contain symbolic links")
        if path.is_file():
            files[path.relative_to(SITE).as_posix()] = hashlib.sha256(
                path.read_bytes()
            ).hexdigest()
    return files


def git(*args: str) -> str:
    return subprocess.check_output(
        ["git", "-C", str(ROOT), *args], text=True, stderr=subprocess.DEVNULL
    ).strip()


def notice_inputs() -> str:
    digest = hashlib.sha256()
    digest.update(b"workspace" if use_workspace() else b"pinned-module")
    toolchain = json.loads(
        subprocess.check_output(
            ["go", "env", "-json", "GOVERSION", "GOROOT"], cwd=ROOT / "client"
        )
    )
    digest.update(toolchain["GOVERSION"].encode())
    goroot = Path(toolchain["GOROOT"])
    for path in sorted(goroot.iterdir()):
        if path.is_file() and path.name.lower().startswith(
            ("license", "copying", "notice", "patents")
        ):
            digest.update(path.name.encode() + b"\0" + path.read_bytes())
    for directory in (ROOT / "backend", ROOT / "client"):
        for path in sorted(directory.rglob("*")):
            if not path.is_file() or path.suffix not in {".go", ".mod", ".sum"}:
                continue
            if "vendor" in path.parts:
                continue
            digest.update(path.relative_to(ROOT).as_posix().encode() + b"\0")
            digest.update(hashlib.sha256(path.read_bytes()).digest())
    digest.update((ROOT / "client/scripts/generate-notices.py").read_bytes())
    return digest.hexdigest()


def use_workspace() -> bool:
    return os.environ.get("GOWORK") != "off" and (ROOT / "client/go.work").is_file()


def prepare_notices() -> None:
    script = ROOT / "client/scripts/generate-notices.py"
    spec = importlib.util.spec_from_file_location("psst_lan_go_notices", script)
    if spec is None or spec.loader is None:
        raise ValueError("Go notice generator is unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    with tempfile.TemporaryDirectory(prefix="psst-lan-notices-") as directory:
        output = Path(directory) / "notices"
        module.collect(output, include_web=False, workspace=use_workspace())
        target = SITE / "licenses/backend"
        if target.exists():
            shutil.rmtree(target)
        shutil.copytree(output, target)


def npm_dependencies_current() -> bool:
    """The hidden lock describes this platform's installed subset of the lockfile."""
    try:
        expected = json.loads((WEB / "package-lock.json").read_text())["packages"]
        installed = json.loads((WEB / "node_modules/.package-lock.json").read_text())[
            "packages"
        ]
    except (OSError, ValueError, KeyError):
        return False
    system = {"Windows": "win32", "Darwin": "darwin"}.get(
        platform.system(), platform.system().lower()
    )
    cpu = {"x86_64": "x64", "amd64": "x64", "aarch64": "arm64"}.get(
        platform.machine().lower(), platform.machine().lower()
    )
    libc = "musl" if platform.libc_ver()[0] == "musl" else "glibc"

    def matches(values: list[str], value: str) -> bool:
        positives = [item for item in values if not item.startswith("!")]
        return "!" + value not in values and (not positives or value in positives)

    if any(expected.get(name) != data for name, data in installed.items()):
        return False
    for name, data in expected.items():
        if name == "" or name in installed:
            continue
        if not data.get("optional") or (
            matches(data.get("os", []), system)
            and matches(data.get("cpu", []), cpu)
            and matches(data.get("libc", []), libc)
        ):
            return False
    return True


def prepare(check: bool = False, *, release_version: str | None = None) -> dict:
    web_inputs = digest_inputs()
    go_inputs = notice_inputs()
    inputs = hashlib.sha256((web_inputs + go_inputs).encode()).hexdigest()
    try:
        previous = json.loads(METADATA.read_text())
    except (OSError, ValueError):
        previous = {}
    files = inventory()
    fresh = (
        previous.get("input_digest") == inputs
        and files == previous.get("files")
        and "index.html" in files
    )
    if check and not fresh:
        raise ValueError("embedded LAN assets are missing or stale; run make build")
    browser_fresh = (
        previous.get("web_input_digest") == web_inputs
        and "index.html" in files
        and {k: v for k, v in files.items() if not k.startswith("licenses/backend/")}
        == {
            k: v
            for k, v in previous.get("files", {}).items()
            if not k.startswith("licenses/backend/")
        }
    )
    if not fresh and not browser_fresh:
        npm = shutil.which("npm")
        if npm is None:
            raise ValueError("source builds need npm to prepare the browser interface")
        if not npm_dependencies_current():
            subprocess.run([npm, "ci", "--ignore-scripts"], cwd=WEB, check=True)
        # The web inventory identifies stable server-style versions or development
        # builds. CLI commit-distance versions remain in the CLI/source metadata.
        version = os.environ.get("VERSION", "dev")
        if not re.fullmatch(r"v\d+\.\d+\.\d+", version):
            version = "dev"
        subprocess.run(
            [npm, "run", "build:lan"],
            cwd=WEB,
            check=True,
            env=os.environ | {"VERSION": version},
        )
        built = WEB / "build-lan"
        if not (built / "index.html").is_file():
            raise ValueError("standalone browser build did not produce index.html")
        SITE.mkdir(parents=True, exist_ok=True)
        for path in SITE.iterdir():
            if path.name == "README.txt":
                continue
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink()
        shutil.copytree(built, SITE, dirs_exist_ok=True)
    if not fresh:
        prepare_notices()
        files = inventory()
    try:
        revision = git("rev-parse", "HEAD")
        dirty = bool(
            git(
                "status",
                "--porcelain",
                "--untracked-files=all",
                "--",
                "client",
                "backend",
                "web",
            )
        )
        public_ref = git(
            "for-each-ref",
            "--format=%(refname)",
            "--contains",
            revision,
            "refs/remotes/origin",
        )
        source = (
            "https://github.com/endorses/psst.zip/tree/" + revision
            if not dirty and public_ref
            else ""
        )
    except subprocess.CalledProcessError:
        revision = previous.get("revision", "unknown")
        dirty = previous.get("source_dirty", True)
        source = previous.get("source", "")
    digest = hashlib.sha256(
        "".join(name + "\0" + value + "\n" for name, value in files.items()).encode()
    ).hexdigest()
    source_archive = ""
    if release_version is not None:
        if not re.fullmatch(
            r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", release_version
        ):
            raise ValueError(
                "release browser source metadata requires a stable CLI version"
            )
        if not dirty and source:
            source_archive = (
                "https://github.com/endorses/psst.zip/releases/download/"
                + quote("client/v" + release_version, safe="")
                + "/psst-"
                + release_version
                + "-source.tar.gz"
            )
    metadata = {
        "product": "psst.zip",
        "revision": revision,
        "asset_digest": digest,
        "source": source,
        "source_archive": source_archive,
        "source_dirty": dirty,
        "input_digest": inputs,
        "web_input_digest": web_inputs,
        "notice_input_digest": go_inputs,
        "files": files,
    }
    if not check:
        METADATA.parent.mkdir(parents=True, exist_ok=True)
        METADATA.write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    return metadata


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    arguments = parser.parse_args()
    try:
        result = prepare(arguments.check)
        print("LAN browser assets verified: " + result["asset_digest"])
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"LAN browser preparation failed: {error}\n")

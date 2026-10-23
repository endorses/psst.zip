#!/usr/bin/env python3
"""Build deterministic CLI release candidates; never create tags or publish.

Requires an exact source commit and a clean scoped checkout. --allow-dirty is an
explicit development smoke-test exception and marks every output nonpublishable.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import importlib.util
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import zipfile
from pathlib import Path

CLIENT = Path(__file__).resolve().parents[1]
ROOT = CLIENT.parent
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
REVISION = re.compile(r"[0-9a-f]{40}\Z")
TARGETS = tuple(
    f"{system}/{arch}"
    for system in ("linux", "darwin", "windows")
    for arch in ("amd64", "arm64")
)


def run(args: list[str], *, cwd: Path = ROOT, env=None) -> bytes:
    return subprocess.check_output(args, cwd=cwd, env=env, timeout=120)


def json_bytes(value) -> bytes:
    return (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()


def archive(
    path: Path, prefix: str, files: dict[str, tuple[bytes, int]], epoch: int
) -> None:
    """Sort entries and normalize timestamps, ownership, mode and gzip headers."""
    if path.suffix == ".zip":
        stamp = time.gmtime(max(315532800, epoch))[:6]
        with zipfile.ZipFile(
            path, "w", zipfile.ZIP_DEFLATED, compresslevel=9
        ) as output:
            for name, (content, mode) in sorted(files.items()):
                info = zipfile.ZipInfo(prefix + "/" + name, date_time=stamp)
                info.create_system = 3
                info.external_attr = (0o100000 | mode) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                output.writestr(info, content, compresslevel=9)
    else:
        with (
            path.open("wb") as raw,
            gzip.GzipFile(
                filename="", fileobj=raw, mode="wb", mtime=0, compresslevel=9
            ) as compressed,
            tarfile.open(
                fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT
            ) as output,
        ):
            for name, (content, mode) in sorted(files.items()):
                info = tarfile.TarInfo(prefix + "/" + name)
                info.size = len(content)
                info.mode = mode
                info.mtime = epoch
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                output.addfile(info, io.BytesIO(content))


SOURCE_SCOPE = (
    "client",
    "backend",
    "web",
    "LICENSE",
    "README.md",
    "Makefile",
    "docs/cli.md",
    "docs/plans/cli-client.md",
    "docs/plans/cli-lan-sharing.md",
)
BACKEND_MODULE = "github.com/endorses/psst.zip/backend"


def source_files(development: bool) -> list[Path]:
    commands = [["git", "ls-files", "-z", "--", *SOURCE_SCOPE]]
    if development:
        commands.append(
            [
                "git",
                "ls-files",
                "--others",
                "--exclude-standard",
                "-z",
                "--",
                *SOURCE_SCOPE,
            ]
        )
    paths = {
        ROOT / os.fsdecode(name)
        for command in commands
        for name in run(command).split(b"\x00")
        if name
    }
    # Generated browser output is intentionally ignored by Git, but is an
    # essential source-build input for offline rebuilding without Node.
    paths.update(
        path
        for path in (CLIENT / "internal/lanassets/site").rglob("*")
        if path.is_file()
    )
    selected = []
    for path in sorted(paths):
        relative = path.relative_to(ROOT)
        # Backend source is copied from the actual immutable module below,
        # never a possibly divergent development workspace checkout.
        if relative.parts[0] == "backend" or path.name in {"go.work", "go.work.sum"}:
            continue
        if any(
            part
            in {
                "node_modules",
                ".svelte-kit",
                ".svelte-kit-lan",
                "__pycache__",
                "vendor",
            }
            for part in relative.parts
        ):
            continue
        if path.suffix == ".pyc" or relative.as_posix() in {
            "client/psst",
            "client/psst.exe",
        }:
            continue
        if path.is_symlink() or not path.is_file() or not path.is_relative_to(ROOT):
            raise ValueError("release source contains a link or unavailable file")
        selected.append(path)
    return selected


def pinned_backend() -> dict:
    module = json.loads(
        run(
            ["go", "list", "-m", "-json", BACKEND_MODULE],
            cwd=CLIENT,
            env=os.environ | {"GOWORK": "off", "GOFLAGS": "-mod=readonly"},
        )
    )
    if (
        module.get("Path") != BACKEND_MODULE
        or module.get("Main")
        or module.get("Replace")
        or not module.get("Version")
        or not module.get("Sum")
        or not Path(module.get("Dir", "")).is_dir()
    ):
        raise ValueError(
            "LAN embedding requires a resolved immutable backend module without replacements"
        )
    return module


def load_script(name: str, filename: str):
    specification = importlib.util.spec_from_file_location(
        name, CLIENT / "scripts" / filename
    )
    if not specification or not specification.loader:
        raise ValueError("release build helper is unavailable")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def main() -> None:
    # The checked-in workspace is only for development. Every packaging phase,
    # including embedded notices, resolves the immutable dependency in go.mod.
    os.environ["GOWORK"] = "off"
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument(
        "--allow-dirty",
        action="store_true",
        help="development only; outputs cannot be published",
    )
    args = parser.parse_args()
    if not VERSION.fullmatch(args.version) or not REVISION.fullmatch(args.revision):
        raise ValueError("use a stable X.Y.Z version and full lowercase source commit")
    head = run(["git", "rev-parse", "HEAD"]).decode().strip()
    if args.revision != head:
        raise ValueError("source revision must equal checked-out HEAD")
    dirty = bool(
        run(
            [
                "git",
                "status",
                "--porcelain",
                "--untracked-files=all",
                "--",
                *SOURCE_SCOPE,
            ]
        )
    )
    if dirty and not args.allow_dirty:
        raise ValueError(
            "CLI source is dirty; commit it or use development-only --allow-dirty"
        )
    output = args.output.resolve()
    if any(
        output.is_relative_to(ROOT / directory)
        for directory in ("client", "backend", "web")
    ):
        raise ValueError(
            "release output must be outside the application source directories"
        )
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError(
            "release output directory must be empty; existing artifacts are never replaced"
        )
    epoch = int(run(["git", "show", "-s", "--format=%ct", head]).decode())
    go_version = run(["go", "env", "GOVERSION"], cwd=CLIENT).decode().strip()
    commands = []
    assets = load_script("psst_release_lan_assets", "prepare-lan-assets.py").prepare(
        release_version=args.version
    )
    backend = pinned_backend()
    module = load_script("psst_release_notices", "generate-notices.py")
    with tempfile.TemporaryDirectory(prefix="psst-cli-release-") as temporary:
        staging = Path(temporary)
        notice_dir = staging / "notices"
        inventory = module.collect(notice_dir)
        shared = {"LICENSE": ((ROOT / "LICENSE").read_bytes(), 0o644)}
        for path in sorted(notice_dir.rglob("*")):
            if path.is_file():
                shared["licenses/" + path.relative_to(notice_dir).as_posix()] = (
                    path.read_bytes(),
                    0o644,
                )
        install = (
            "psst.zip CLI " + args.version + "\nSource revision: " + head + "\n\n"
            "Extract this archive and place psst (psst.exe on Windows) on your PATH.\n"
            "Run psst --help. For temporary trusted-LAN sharing: psst serve send FILE --qr (no account required).\n"
            "For hosted sharing: psst auth login --server https://your-server --username your-user.\n"
            "Use a regular user account. Private keys stay on this client; pairing does not sync keys.\n"
            "Verify this archive against SHA256SUMS before installation.\n"
            "See docs/cli.md in the corresponding source archive for credentials, QR and automation usage.\n"
            "Cross-compilation does not establish an operating-system runtime pass.\n"
            "The release workflow exercises the packaged Linux amd64 version and offline QR commands.\n"
        ).encode()
        for target in TARGETS:
            system, arch = target.split("/")
            binary_name = "psst.exe" if system == "windows" else "psst"
            binary = staging / (system + "-" + arch + "-" + binary_name)
            flags = f"-s -w -X main.version={args.version} -X main.revision={head}"
            command = [
                "go",
                "build",
                "-trimpath",
                "-buildvcs=false",
                "-ldflags",
                flags,
                "-o",
                str(binary),
                "./cmd/psst",
            ]
            run(
                command,
                cwd=CLIENT,
                env=os.environ
                | {
                    "CGO_ENABLED": "0",
                    "GOOS": system,
                    "GOARCH": arch,
                    "GOFLAGS": "-mod=readonly",
                    "GOWORK": "off",
                },
            )
            commands.append(
                {
                    "target": target,
                    "command": command[: command.index("-o")]
                    + ["-o", binary_name, "./cmd/psst"],
                }
            )
            members = shared | {
                binary_name: (binary.read_bytes(), 0o755),
                "INSTALL.txt": (install, 0o644),
            }
            name = f"psst-{args.version}-{system}-{arch}"
            archive(
                output / (name + (".zip" if system == "windows" else ".tar.gz")),
                name,
                members,
                epoch,
            )
        source_dir = staging / "source"
        for path in source_files(args.allow_dirty):
            destination = source_dir / path.relative_to(ROOT)
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, destination)
        backend_root = Path(backend["Dir"])
        for path in sorted(backend_root.rglob("*")):
            if path.is_symlink():
                raise ValueError("pinned backend source contains a symbolic link")
            if path.is_file():
                destination = source_dir / "backend" / path.relative_to(backend_root)
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, destination)
        vendor_dir = source_dir / "client/vendor"
        run(
            ["go", "mod", "vendor", "-o", str(vendor_dir)],
            cwd=CLIENT,
            env=os.environ | {"GOFLAGS": "-mod=readonly", "GOWORK": "off"},
        )
        commands.append(
            {"command": ["go", "mod", "vendor", "-o", "source/client/vendor"]}
        )
        source_build = (
            "This archive includes CLI source, the exact pinned backend module source, browser source,\n"
            "embedded browser assets/licenses and vendored Go dependencies. No local workspace replacement is used.\n"
            "Install the Go version declared by client/go.mod. Offline build (no Node or network needed):\n\n"
            "  cd client\n"
            f'  CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=vendor -trimpath -buildvcs=false -ldflags "-s -w -X main.version={args.version} -X main.revision={head}" ./cmd/psst\n\n'
            "To rebuild the embedded browser assets from their preferred source, install the Node version\n"
            "declared by web/package.json and use npm ci (requires access to the locked npm registry):\n\n"
            "  cd web && npm ci --ignore-scripts && npm run build:lan && cd ..\n"
            "  python3 client/scripts/prepare-lan-assets.py\n"
            "  cd client && CGO_ENABLED=0 GOWORK=off GOPROXY=off go build -mod=vendor ./cmd/psst\n\n"
            "Browser assets are built once before cross-compiling the six release targets.\n"
            "Go's toolchain is a separate prerequisite. Cross-compilation is not a runtime pass.\n"
            "SOURCE.json records source identity, the pinned backend module and archived input hashes.\n"
        )
        (source_dir / "BUILD.txt").write_text(source_build)
        record = {
            "schema_version": 1,
            "version": args.version,
            "tag": "client/v" + args.version,
            "revision": head,
            "development_only": args.allow_dirty,
            "source_dirty": dirty,
            "source_date_epoch": epoch,
            "go_version": go_version,
            "cgo_enabled": False,
            "targets": list(TARGETS),
            "commands": commands,
            "notice_graph_commands": inventory["commands"],
            "backend_module": {
                key: backend[key]
                for key in ("Path", "Version", "Sum", "GoModSum")
                if key in backend
            },
            "embedded_browser": assets,
            "source_files": {
                path.relative_to(source_dir).as_posix(): hashlib.sha256(
                    path.read_bytes()
                ).hexdigest()
                for path in sorted(source_dir.rglob("*"))
                if path.is_file()
            },
        }
        (source_dir / "SOURCE.json").write_bytes(json_bytes(record))
        source_members = {
            path.relative_to(source_dir).as_posix(): (
                path.read_bytes(),
                0o755 if path.suffix in {".sh", ".py"} else 0o644,
            )
            for path in source_dir.rglob("*")
            if path.is_file()
        }
        archive(
            output / f"psst-{args.version}-source.tar.gz",
            f"psst-{args.version}-source",
            source_members,
            epoch,
        )
        (output / "cli-release.json").write_bytes(json_bytes(record))
        sums = "".join(
            hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n"
            for path in sorted(output.iterdir())
        )
        (output / "SHA256SUMS").write_text(sums)
    print(
        f"Built six targets, vendored source and checksums ({'development only' if args.allow_dirty else 'clean source'}). Nothing published."
    )


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"CLI packaging failed: {error}", file=sys.stderr)
        raise SystemExit(1) from None

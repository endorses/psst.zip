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


def source_files(development: bool) -> list[Path]:
    names = run(
        [
            "git",
            "ls-files",
            "-z",
            "--",
            "client",
            "LICENSE",
            "docs/cli.md",
            "docs/plans/cli-client.md",
        ]
    )
    paths = {ROOT / os.fsdecode(name) for name in names.split(b"\x00") if name}
    if development:
        paths.update(
            path
            for path in CLIENT.rglob("*")
            if path.is_file()
            and "__pycache__" not in path.parts
            and path.suffix != ".pyc"
        )
        paths.add(ROOT / "LICENSE")
        if (ROOT / "docs/cli.md").is_file():
            paths.add(ROOT / "docs/cli.md")
    for path in paths:
        if path.is_symlink() or not path.is_file() or not path.is_relative_to(ROOT):
            raise ValueError("release source contains a link or unavailable file")
    return sorted(paths)


def main() -> None:
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
                "client",
                "LICENSE",
                "docs/cli.md",
                "docs/plans/cli-client.md",
            ]
        )
    )
    if dirty and not args.allow_dirty:
        raise ValueError(
            "CLI source is dirty; commit it or use development-only --allow-dirty"
        )
    output = args.output.resolve()
    if output.is_relative_to(CLIENT):
        raise ValueError("release output must be outside the client source directory")
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError(
            "release output directory must be empty; existing artifacts are never replaced"
        )
    epoch = int(run(["git", "show", "-s", "--format=%ct", head]).decode())
    go_version = run(["go", "env", "GOVERSION"], cwd=CLIENT).decode().strip()
    commands = []
    specification = importlib.util.spec_from_file_location(
        "psst_release_notices", CLIENT / "scripts/generate-notices.py"
    )
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
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
            "Run psst --help, then psst auth login --server https://your-server --username your-user.\n"
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
        vendor_dir = source_dir / "client/vendor"
        run(
            ["go", "mod", "vendor", "-o", str(vendor_dir)],
            cwd=CLIENT,
            env=os.environ | {"GOFLAGS": "-mod=readonly"},
        )
        commands.append(
            {"command": ["go", "mod", "vendor", "-o", "source/client/vendor"]}
        )
        source_build = (
            "This archive includes the client, its public test vectors and vendored Go module source.\n"
            "Install the Go version declared by client/go.mod, then:\n\n"
            "  cd client\n"
            "  CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -trimpath -buildvcs=false ./cmd/psst\n\n"
            "Go's toolchain is a separate build prerequisite. Binaries were cross-compiled with CGO disabled.\n"
            "The complete repository at the recorded commit contains backend/browser integration fixtures.\n"
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

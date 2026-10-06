#!/usr/bin/env python3
"""Check Git's index/history without formatting, staging, or printing file contents.

Requires Python 3 and the modern Gitleaks git command. Staged Go changes also
require gofmt; staged web changes require npm ci in web for local Prettier.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tempfile

MAX_FILE_BYTES = 5 * 1024 * 1024
WEB_EXTENSIONS = {".js", ".mjs", ".cjs", ".ts", ".json", ".svelte", ".css", ".html"}
CONFLICT_MARKER = re.compile(rb"^(?:<{7}|>{7}|\|{7})(?: |$)|^={7}$", re.MULTILINE)


class CheckFailure(Exception):
    """A diagnostic that never contains source contents or credentials."""


def run(
    args: list[str], root: Path, data: bytes | None = None
) -> subprocess.CompletedProcess:
    return subprocess.run(args, cwd=root, input=data, capture_output=True, check=False)


def git(root: Path, *args: str) -> bytes:
    result = run(["git", *args], root)
    if result.returncode:
        raise CheckFailure(
            "Git could not inspect the repository; resolve index/repository errors."
        )
    return result.stdout


def index_entries(root: Path) -> dict[str, tuple[str, str]]:
    entries = {}
    for record in git(root, "ls-files", "--stage", "-z").split(b"\0"):
        if not record:
            continue
        metadata, path = record.split(b"\t", 1)
        mode, oid, stage = metadata.decode("ascii").split()
        if stage != "0":
            raise CheckFailure(
                "Resolve unmerged index entries before committing or pushing."
            )
        entries[os.fsdecode(path)] = (mode, oid)
    return entries


def sensitive_path(path: str) -> bool:
    parts = PurePosixPath(path.lower()).parts
    name = parts[-1]
    if name.startswith(".env") and name != ".env.example":
        return True
    if name in {"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}:
        return True
    if re.search(r"\.(?:db|sqlite|sqlite3)(?:-(?:wal|shm|journal))?$", name):
        return True
    if name.endswith(
        (
            ".key",
            ".p12",
            ".pfx",
            ".jks",
            ".keystore",
            ".apk",
            ".aab",
            ".ipa",
            ".mobileprovision",
        )
    ):
        return True
    if any(
        part
        in {"uploads", "payloads", "psst-data", "caddy-data", "caddy-config", ".ssh"}
        for part in parts
    ):
        return True
    return parts[0] == "data" or parts[:2] in {("backend", "data"), ("deploy", "data")}


def check_index(root: Path, entries: dict[str, tuple[str, str]]) -> None:
    failures = []
    sizes = object_sizes(
        root, {oid for mode, oid in entries.values() if mode != "160000"}
    )
    for path, (mode, oid) in entries.items():
        if sensitive_path(path):
            failures.append(f"Sensitive/generated path is tracked: {path!r}")
        if mode == "160000":
            failures.append(f"Submodules require a separate security policy: {path!r}")
            continue
        size = sizes[oid][1]
        if size > MAX_FILE_BYTES:
            failures.append(f"Tracked file exceeds the 5 MiB limit: {path!r}")
    if failures:
        raise CheckFailure("\n".join(failures))


def object_sizes(root: Path, oids: set[str]) -> dict[str, tuple[str, int]]:
    if not oids:
        return {}
    result = run(
        ["git", "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)"],
        root,
        ("\n".join(sorted(oids)) + "\n").encode("ascii"),
    )
    sizes = {}
    try:
        for line in result.stdout.decode("ascii").splitlines():
            oid, kind, size = line.split()
            sizes[oid] = (kind, int(size))
    except ValueError:
        raise CheckFailure("Git could not inspect object metadata.") from None
    if result.returncode or set(sizes) != oids:
        raise CheckFailure("Git could not inspect object metadata.")
    return sizes


def check_history_files(root: Path) -> None:
    oids = set(
        git(root, "rev-list", "--objects", "--all", "--no-object-names")
        .decode("ascii")
        .splitlines()
    )
    sizes = object_sizes(root, oids)
    oversized = {
        oid
        for oid, (kind, size) in sizes.items()
        if kind == "blob" and size > MAX_FILE_BYTES
    }
    failures = set()
    referenced_large_blobs = set()
    # Trees may appear in multiple commits; inspecting each unique root once
    # preserves historical path/blob associations without Git's quoted paths.
    trees = set(git(root, "log", "--all", "--format=%T").decode("ascii").splitlines())
    for tree in sorted(trees):
        for record in git(root, "ls-tree", "-r", "-z", tree).split(b"\0"):
            if not record:
                continue
            metadata, raw_path = record.split(b"\t", 1)
            mode, kind, oid = metadata.decode("ascii").split()
            path = os.fsdecode(raw_path)
            if sensitive_path(path):
                failures.add(f"Sensitive/generated path remains in history: {path!r}")
            if mode == "160000":
                failures.add(
                    f"Historical submodule requires a separate security policy: {path!r}"
                )
            if kind == "blob" and oid in oversized:
                failures.add(f"Historical file exceeds the 5 MiB limit: {path!r}")
                referenced_large_blobs.add(oid)
    # A tag can refer directly to a blob, without a commit tree or filename.
    for oid in sorted(oversized - referenced_large_blobs):
        failures.add(f"Historical blob exceeds the 5 MiB limit: object {oid[:12]}")
    if failures:
        raise CheckFailure("\n".join(sorted(failures)))


def staged_contents(
    root: Path, entries: dict[str, tuple[str, str]], path: str
) -> bytes:
    return git(root, "cat-file", "blob", entries[path][1])


def check_staged_format(root: Path, entries: dict[str, tuple[str, str]]) -> None:
    result = run(["git", "diff", "--cached", "--check"], root)
    if result.returncode:
        raise CheckFailure(
            "Staged whitespace/conflict errors; inspect git diff --cached --check locally."
        )
    changed = git(root, "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z")
    for raw_path in changed.split(b"\0"):
        if not raw_path:
            continue
        path = os.fsdecode(raw_path)
        if path not in entries:
            continue
        contents = staged_contents(root, entries, path)
        if CONFLICT_MARKER.search(contents):
            raise CheckFailure(f"Staged conflict marker: {path!r}")
        suffix = PurePosixPath(path).suffix
        if suffix == ".go":
            if not shutil.which("gofmt"):
                raise CheckFailure("Install Go: gofmt is required for staged Go files.")
            result = run(["gofmt", "-s"], root, contents)
        elif (
            path.startswith("web/")
            and suffix in WEB_EXTENSIONS
            and path != "web/package-lock.json"
        ):
            prettier = root / "web/node_modules/.bin/prettier"
            if not prettier.is_file():
                raise CheckFailure(
                    "Run npm ci in web: local Prettier is required for staged web files."
                )
            result = run(
                [str(prettier), "--stdin-filepath", str(root / path)],
                root / "web",
                contents,
            )
        else:
            continue
        if result.returncode or result.stdout != contents:
            raise CheckFailure(
                f"Format and restage {path!r}; staged contents are not formatted."
            )


def scan_secrets(root: Path, entries: dict[str, tuple[str, str]], staged: bool) -> None:
    scanner = shutil.which("gitleaks")
    if not scanner:
        raise CheckFailure(
            "Gitleaks is required; install it before committing or pushing."
        )
    if ".gitleaks.toml" not in entries:
        raise CheckFailure(
            "Stage the repository .gitleaks.toml policy before running secret checks."
        )
    with tempfile.TemporaryDirectory(prefix="psst-repository-check-") as directory:
        scratch = Path(directory)
        config = scratch / "config.toml"
        config.write_bytes(staged_contents(root, entries, ".gitleaks.toml"))
        ignore = scratch / ".gitleaksignore"
        ignore.write_bytes(
            staged_contents(root, entries, ".gitleaksignore")
            if ".gitleaksignore" in entries
            else b""
        )
        report = scratch / "report.json"
        args = [scanner, "git"]
        args += ["--pre-commit", "--staged"] if staged else ["--log-opts=--all"]
        args += [
            "--redact=100",
            "--no-banner",
            "--no-color",
            "--ignore-gitleaks-allow",
            "--config",
            str(config),
            "--gitleaks-ignore-path",
            str(ignore),
            "--report-format=json",
            "--report-path",
            str(report),
            str(root),
        ]
        env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith("GITLEAKS_")
        }
        result = subprocess.run(
            args, cwd=root, env=env, capture_output=True, check=False
        )
        if result.returncode:
            findings = []
            if report.is_file():
                try:
                    findings = json.loads(report.read_text())
                except (ValueError, OSError):
                    pass
            if isinstance(findings, list) and findings:
                # Never forward scanner output or excerpts, even on scanner errors.
                raise CheckFailure(
                    f"Gitleaks found {len(findings)} secret finding(s). "
                    "Review them locally with redaction and remove secrets before continuing."
                )
            raise CheckFailure(
                "Gitleaks failed. Check its installation and repository policy locally with redaction."
            )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("staged", "history", "files"))
    args = parser.parse_args()
    try:
        root_result = run(["git", "rev-parse", "--show-toplevel"], Path.cwd())
        if root_result.returncode:
            raise CheckFailure("Run this check inside a Git repository.")
        root = Path(os.fsdecode(root_result.stdout).strip())
        entries = index_entries(root)
        check_index(root, entries)
        if args.mode == "staged":
            scan_secrets(root, entries, staged=True)
            check_staged_format(root, entries)
        elif args.mode == "history":
            check_history_files(root)
            scan_secrets(root, entries, staged=False)
    except (CheckFailure, OSError) as error:
        # OS errors may include paths, but never subprocess outputs/source values.
        print(f"Repository check failed: {error}", file=sys.stderr)
        return 1
    print(f"Repository {args.mode} checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

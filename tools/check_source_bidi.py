#!/usr/bin/env python3
"""Reject bidi formatting controls without lint's Kotlin PSI traversal.

Scan tracked Kotlin, Java, Gradle and Swift sources. Optional generated directories
must explicitly select emitted source beneath a checkout build/generated directory.
No source contents, credentials, keys or private values are printed.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import selectors
import stat
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE_SUFFIXES = {".kt", ".kts", ".java", ".gradle", ".swift"}
BIDI_CONTROLS = set(range(0x202A, 0x202F)) | set(range(0x2066, 0x206A))
UNICODE_ESCAPE = re.compile(
    r"\\(?:u+([0-9a-f]{4})|u\{([0-9a-f]{1,8})\})", re.IGNORECASE | re.ASCII
)
MAX_INVENTORY_BYTES = 8 * 1024 * 1024
MAX_FILES = 10_000
MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_TOTAL_BYTES = 64 * 1024 * 1024
MAX_DIRECTORY_DEPTH = 32


class SourceCheckError(ValueError):
    """Fixed diagnostic, never arbitrary tool output or source contents."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SourceCheckError(message)


def git_inventory(root: Path) -> bytes:
    with subprocess.Popen(
        ["git", "-C", str(root), "ls-files", "--cached", "-z"],
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    ) as process:
        try:
            deadline = time.monotonic() + 15
            output = bytearray()
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline - time.monotonic()
                    require(remaining > 0, "Tracked inventory exceeded its time limit")
                    require(
                        bool(selector.select(remaining)),
                        "Tracked inventory exceeded its time limit",
                    )
                    chunk = os.read(process.stdout.fileno(), 65536)
                    if not chunk:
                        break
                    output.extend(chunk)
                    require(
                        len(output) <= MAX_INVENTORY_BYTES,
                        "Tracked inventory exceeds bounds",
                    )
            require(
                process.wait(timeout=max(0.001, deadline - time.monotonic())) == 0,
                "Tracked inventory is unavailable",
            )
            return bytes(output)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()


def checked_path(root: Path, path: Path) -> Path:
    path = path.absolute()
    require(path.is_relative_to(root), "Source path must stay within the checkout")
    relative = path.relative_to(root)
    require(".." not in relative.parts, "Source path contains traversal")
    current = root
    for part in relative.parts:
        current /= part
        require(not current.is_symlink(), "Source paths must not contain symlinks")
    return path


def tracked_sources(root: Path) -> list[Path]:
    inventory = git_inventory(root)
    require(
        not inventory or inventory.endswith(b"\0"), "Tracked inventory is truncated"
    )
    names = inventory.rstrip(b"\0").split(b"\0") if inventory else []
    require(len(names) <= MAX_FILES, "Tracked inventory contains too many files")
    result = []
    for raw in names:
        name = raw.decode("utf-8", errors="strict")
        relative = Path(name)
        require(
            not relative.is_absolute(), "Tracked inventory contains an absolute path"
        )
        if relative.suffix.lower() in SOURCE_SUFFIXES:
            result.append(checked_path(root, root / relative))
    require(bool(result), "Tracked inventory contains no applicable source files")
    return result


def generated_sources(root: Path, directory: Path) -> list[Path]:
    directory = checked_path(root, directory)
    parts = directory.relative_to(root).parts
    require(
        any(
            parts[index : index + 2] == ("build", "generated")
            for index in range(len(parts) - 1)
        )
        and directory.name in {"kotlin", "java", "swift"},
        "Select an emitted source directory beneath build/generated",
    )
    require(directory.is_dir(), "Generated source directory is unavailable")
    result = []
    pending = [(directory, 0)]
    entries = 0
    while pending:
        parent, depth = pending.pop()
        require(
            depth <= MAX_DIRECTORY_DEPTH, "Generated directory depth exceeds bounds"
        )
        with os.scandir(parent) as children:
            for child in children:
                entries += 1
                require(
                    entries <= MAX_FILES, "Generated directory inventory exceeds bounds"
                )
                require(
                    not child.is_symlink(), "Generated source must not contain symlinks"
                )
                path = Path(child.path)
                if child.is_dir(follow_symlinks=False):
                    pending.append((path, depth + 1))
                elif path.suffix.lower() in SOURCE_SUFFIXES:
                    require(
                        child.is_file(follow_symlinks=False),
                        "Generated source is not a regular file",
                    )
                    result.append(path)
    return result


def first_bidi_control(text: str) -> tuple[int, int] | None:
    for number, line in enumerate(text.splitlines(keepends=True), start=1):
        for character in line:
            if ord(character) in BIDI_CONTROLS:
                return number, ord(character)
        for match in UNICODE_ESCAPE.finditer(line):
            codepoint = int(match.group(1) or match.group(2), 16)
            if codepoint in BIDI_CONTROLS:
                return number, codepoint
    return None


def check_sources(root: Path, generated: list[Path]) -> int:
    root = root.resolve()
    sources = tracked_sources(root)
    for directory in generated:
        sources.extend(generated_sources(root, directory))
    sources = sorted(set(sources))
    require(len(sources) <= MAX_FILES, "Source file count exceeds bounds")
    total = 0
    for path in sources:
        checked_path(root, path)
        require(path.is_file(), "Tracked source is unavailable or not a regular file")
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(descriptor, "rb") as stream:
            require(
                stat.S_ISREG(os.fstat(stream.fileno()).st_mode),
                "Source is not a regular file",
            )
            data = stream.read(min(MAX_FILE_BYTES, MAX_TOTAL_BYTES - total) + 1)
        require(len(data) <= MAX_FILE_BYTES, "Source file size exceeds bounds")
        total += len(data)
        require(total <= MAX_TOTAL_BYTES, "Combined source size exceeds bounds")
        text = data.decode("utf-8", errors="strict")
        found = first_bidi_control(text)
        if found:
            line, codepoint = found
            name = json.dumps(path.relative_to(root).as_posix(), ensure_ascii=True)
            raise SourceCheckError(
                f"Bidi formatting control: {name}:{line} U+{codepoint:04X}"
            )
    return len(sources)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--generated-directory", type=Path, action="append", default=[])
    args = parser.parse_args()
    try:
        count = check_sources(ROOT, args.generated_directory)
    except SourceCheckError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
    except (OSError, UnicodeError, subprocess.SubprocessError):
        print(
            "Bidi source check failed: source/inventory is unavailable or invalid UTF-8.",
            file=sys.stderr,
        )
        raise SystemExit(1)
    print(f"Bidi source check passed: {count} tracked/generated source files.")


if __name__ == "__main__":
    main()

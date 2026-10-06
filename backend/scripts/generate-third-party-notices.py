#!/usr/bin/env python3
"""Refresh vendored notices from exact go.mod versions in the local module cache.

Run after `go mod download`. This tool performs no network requests. --check verifies
both lockfile fingerprints and notice content against the installed module cache.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


def normalize(content):
    """Preserve legal text while normalizing only line endings/edge whitespace."""
    text = content.decode().replace("\r\n", "\n").replace("\r", "\n")
    return (
        "\n".join(line.rstrip(" \t") for line in text.split("\n")).rstrip("\n") + "\n"
    ).encode()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    backend = Path(__file__).resolve().parents[1]
    output = backend / "licenses"
    module_cache = Path(
        subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip()
    )
    inputs = {
        name: hashlib.sha256((backend / name).read_bytes()).hexdigest()
        for name in ("go.mod", "go.sum")
    }
    modules = sorted(
        re.findall(r"^\s+([^\s]+)\s+(v[^\s]+)", (backend / "go.mod").read_text(), re.M)
    )
    files = {}
    inventory = []
    notices = [
        "psst.zip backend dependency notices",
        "Generated from the exact versions declared in backend/go.mod.",
        "License formatting uses LF, no trailing spaces/tabs, and one final newline.",
        "The inventory retains both original upstream and distributed text hashes.",
        "The Go standard library license is copied from the build toolchain into",
        "/app/licenses/go/LICENSE in the container. Base-image packages retain their",
        "upstream licenses; this inventory covers the application Go modules.",
        "",
    ]
    for module, version in modules:
        # Go module-cache paths escape capital letters as !lowercase.
        escaped = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), module)
        directory = module_cache / f"{escaped}@{version}"
        if not directory.is_dir():
            raise RuntimeError(
                f"Module missing; run go mod download: {module}@{version}"
            )
        licenses = sorted(
            path
            for path in directory.iterdir()
            if path.is_file()
            and path.name.lower().startswith(
                (
                    "license",
                    "licence",
                    "copying",
                    "copyright",
                    "notice",
                    "sqlite-license",
                )
            )
        )
        if not any(
            path.name.lower().startswith(("license", "licence", "copying"))
            for path in licenses
        ):
            raise RuntimeError(f"No upstream license text found: {module}@{version}")
        retained = []
        for path in licenses:
            relative = f"dependencies/{module}@{version}/{path.name}"
            upstream = path.read_bytes()
            content = normalize(upstream)
            files[relative] = content
            retained.append(
                {
                    "path": relative,
                    "sha256": hashlib.sha256(content).hexdigest(),
                    "upstream_sha256": hashlib.sha256(upstream).hexdigest(),
                }
            )
            notices.extend(
                [f"=== {module}@{version}: {path.name} ===", content.decode(), ""]
            )
        inventory.append({"module": module, "version": version, "notices": retained})
    files["dependency-inventory.json"] = (
        json.dumps({"inputs": inputs, "modules": inventory}, indent=2) + "\n"
    ).encode()
    files["THIRD_PARTY_NOTICES.txt"] = normalize("\n".join(notices).encode())
    for relative, content in files.items():
        path = output / relative
        if args.check:
            if not path.is_file() or path.read_bytes() != content:
                raise RuntimeError(
                    f"Stale dependency notice: {path}; run {Path(__file__).name}"
                )
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
    expected = {output / name for name in files if name.startswith("dependencies/")}
    existing = set((output / "dependencies").rglob("*"))
    for path in existing:
        if path.is_file() and path not in expected:
            if args.check:
                raise RuntimeError(f"Obsolete dependency notice: {path}")
            path.unlink()
    print(
        f"{'Checked' if args.check else 'Generated'} notices for {len(modules)} Go modules."
    )


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)

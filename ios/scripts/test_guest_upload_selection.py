#!/usr/bin/env python3
"""Compile exact portable guest-upload selection helper/tests in Swift, without iOS or network dependencies."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-guest-capacity-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "GuestUploadSelectionHarness"
        tests = work / "Tests" / "GuestUploadSelectionHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        shutil.copy2(ROOT / "Shared/GuestUploadSelection.swift", sources)
        (tests / "GuestUploadSelectionTests.swift").write_text(
            (ROOT / "PsstTests/GuestUploadSelectionTests.swift")
            .read_text()
            .replace(
                "@testable import Psst", "@testable import GuestUploadSelectionHarness"
            )
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "GuestUploadSelectionHarness", targets: [
    .target(name: "GuestUploadSelectionHarness"),
    .testTarget(name: "GuestUploadSelectionHarnessTests", dependencies: ["GuestUploadSelectionHarness"])
])
""")
        subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "--network=none",
                "-v",
                f"{work}:/work",
                "-w",
                "/work",
                "swift:6.0-noble",
                "bash",
                "-c",
                f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; swift test --jobs 4",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

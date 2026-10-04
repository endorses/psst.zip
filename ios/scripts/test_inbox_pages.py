#!/usr/bin/env python3
"""Compile exact portable inbox-page navigation and save-scope helper/tests in Swift, without iOS or network dependencies."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-inbox-page-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "InboxPageHarness"
        tests = work / "Tests" / "InboxPageHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        shutil.copy2(ROOT / "Shared/InboxPageWindow.swift", sources)
        (tests / "InboxPageWindowTests.swift").write_text(
            (ROOT / "PsstTests/InboxPageWindowTests.swift")
            .read_text()
            .replace("@testable import Psst", "@testable import InboxPageHarness")
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "InboxPageHarness", targets: [
    .target(name: "InboxPageHarness"),
    .testTarget(name: "InboxPageHarnessTests", dependencies: ["InboxPageHarness"])
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

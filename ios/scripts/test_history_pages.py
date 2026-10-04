#!/usr/bin/env python3
"""Compile exact portable account-history page decoder and cursor navigation tests in Swift, without iOS or network dependencies."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-history-page-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "HistoryPageHarness"
        tests = work / "Tests" / "HistoryPageHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        for name in ("HistorySnapshot", "InboxPageWindow"):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
            (tests / f"{name}Tests.swift").write_text(
                (ROOT / f"PsstTests/{name}Tests.swift")
                .read_text()
                .replace("@testable import Psst", "@testable import HistoryPageHarness")
            )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "HistoryPageHarness", targets: [
    .target(name: "HistoryPageHarness"),
    .testTarget(name: "HistoryPageHarnessTests", dependencies: ["HistoryPageHarness"])
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

#!/usr/bin/env python3
"""Run exact portable source metadata XCTest helpers; does not build iOS UI/resources."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-release-source-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "ReleaseSourceHarness"
        tests = work / "Tests" / "ReleaseSourceHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        shutil.copy2(ROOT / "Shared/ReleaseSource.swift", sources)
        (tests / "ReleaseSourceTests.swift").write_text(
            (ROOT / "PsstTests/ReleaseSourceTests.swift")
            .read_text()
            .replace("@testable import Psst", "@testable import ReleaseSourceHarness")
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "ReleaseSourceHarness", targets: [
    .target(name: "ReleaseSourceHarness"),
    .testTarget(name: "ReleaseSourceHarnessTests", dependencies: ["ReleaseSourceHarness"])
])
""")
        subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "--network=none",
                "--user",
                f"{os.getuid()}:{os.getgid()}",
                "-v",
                f"{work}:/work",
                "-w",
                "/work",
                "swift:6.2-noble",
                "swift",
                "test",
                "--jobs",
                "4",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

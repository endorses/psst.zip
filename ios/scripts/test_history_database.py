#!/usr/bin/env python3
"""Run exact portable Swift SQLite history/migration tests offline; no Apple SDK required."""

import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-history-db-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "HistoryDatabaseHarness"
        tests = work / "Tests" / "HistoryDatabaseHarnessTests"
        sqlite = work / "Sources" / "SQLite3"
        for path in (sources, tests, sqlite):
            path.mkdir(parents=True)
        for name in ("HistoryRecordDatabase", "HistoryJSONStream"):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        (tests / "HistoryRecordDatabaseTests.swift").write_text(
            (ROOT / "PsstTests/HistoryRecordDatabaseTests.swift")
            .read_text()
            .replace("@testable import Psst", "@testable import HistoryDatabaseHarness")
        )
        shutil.copy2("/usr/include/sqlite3.h", sqlite / "sqlite3.h")
        (sqlite / "module.modulemap").write_text(
            'module SQLite3 [system] {\n header "sqlite3.h"\n link "sqlite3"\n export *\n}\n'
        )
        (work / "Package.swift").write_text(
            """// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "HistoryDatabaseHarness", targets: [
    .systemLibrary(name: "SQLite3"),
    .target(name: "HistoryDatabaseHarness", dependencies: ["SQLite3"]),
    .testTarget(name: "HistoryDatabaseHarnessTests", dependencies: ["HistoryDatabaseHarness", "SQLite3"])
])
"""
        )
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
                f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; "
                "mkdir -p /work/sqlite-libs; "
                "ln -s /usr/lib/$(uname -m)-linux-gnu/libsqlite3.so.0 "
                "/work/sqlite-libs/libsqlite3.so; "
                "swift test --jobs 4 -Xlinker -L/work/sqlite-libs",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

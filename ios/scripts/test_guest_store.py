#!/usr/bin/env python3
"""Run exact guest storage logic with portable Apple/network/crypto boundary stubs."""

import os
import shutil
import subprocess
import tempfile

from localization_harness import add_localization
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-guest-store-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "GuestStoreHarness"
        tests = work / "Tests" / "GuestStoreHarnessTests"
        sqlite = work / "Sources" / "SQLite3"
        for path in (sources, tests, sqlite):
            path.mkdir(parents=True)
        for name in (
            "HistoryRecordDatabase",
            "HistoryJSONStream",
            "ReceiveHistoryStream",
        ):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        fixture = ROOT / "scripts/guest_store_harness"
        for name in ("CryptoKit", "Shared"):
            target = work / "Sources" / name
            target.mkdir()
            shutil.copy2(fixture / f"{name}.swift", target)
        shutil.copy2(fixture / "BoundaryStubs.swift", sources)
        shutil.copy2(fixture / "GuestStoreTests.swift", tests)
        # Linux requires an explicit macro import; storage source is unchanged.
        (sources / "GuestDownloadStore.swift").write_text(
            "import Observation\n"
            + (ROOT / "Psst/Services/GuestDownloadStore.swift").read_text()
        )
        (sources / "GuestHistoryPageViewModel.swift").write_text(
            "import Observation\n"
            + (ROOT / "Psst/ViewModels/GuestHistoryPageViewModel.swift").read_text()
        )
        shutil.copy2("/usr/include/sqlite3.h", sqlite / "sqlite3.h")
        (sqlite / "module.modulemap").write_text(
            'module SQLite3 [system] {\n header "sqlite3.h"\n link "sqlite3"\n export *\n}\n'
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "GuestStoreHarness", targets: [
    .systemLibrary(name: "SQLite3"),
    .target(name: "CryptoKit"),
    .target(name: "Shared"),
    .target(name: "GuestStoreHarness", dependencies: ["SQLite3", "CryptoKit", "Shared"]),
    .testTarget(name: "GuestStoreHarnessTests", dependencies: ["GuestStoreHarness", "SQLite3"])
])
""")
        add_localization(sources, work / "Package.swift", "GuestStoreHarness")
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

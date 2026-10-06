#!/usr/bin/env python3
"""Run exact indexed inbox checkpoints with portable Apple/account boundaries."""

import os
import shutil
import subprocess
import tempfile

from localization_harness import add_localization
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(
        prefix="psst-receive-checkpoint-swift-"
    ) as directory:
        work = Path(directory)
        sources = work / "Sources" / "ReceiveCheckpointHarness"
        tests = work / "Tests" / "ReceiveCheckpointHarnessTests"
        sqlite = work / "Sources" / "SQLite3"
        for path in (sources, tests, sqlite):
            path.mkdir(parents=True)
        for name in (
            "HistoryRecordDatabase",
            "HistoryJSONStream",
            "HistorySnapshot",
            "HistorySync",
            "HistoryRefresh",
            "ServerTimestamp",
            "ReceiveHistoryStream",
            "TransferRecord",
            "SharedLinkTitle",
            "TransferIncident",
            "ReceiveCheckpoint",
            "ReceiveCheckpointStorage",
        ):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        fixture = ROOT / "scripts/receive_checkpoint_harness"
        crypto = work / "Sources/CryptoKit"
        crypto.mkdir()
        shutil.copy2(ROOT / "scripts/guest_store_harness/CryptoKit.swift", crypto)
        shutil.copy2(fixture / "BoundaryStubs.swift", sources)
        (tests / "ReceiveCheckpointTests.swift").write_text(
            (ROOT / "PsstTests/ReceiveCheckpointTests.swift")
            .read_text()
            .replace(
                "@testable import Psst",
                "@testable import ReceiveCheckpointHarness",
            )
        )
        for name, path in (
            ("TransferHistoryStore.swift", "Shared/TransferHistoryStore.swift"),
            (
                "DeviceHistoryPageViewModel.swift",
                "Psst/ViewModels/DeviceHistoryPageViewModel.swift",
            ),
        ):
            (sources / name).write_text(
                "import Observation\n" + (ROOT / path).read_text()
            )
        shutil.copy2("/usr/include/sqlite3.h", sqlite / "sqlite3.h")
        (sqlite / "module.modulemap").write_text(
            'module SQLite3 [system] {\n header "sqlite3.h"\n link "sqlite3"\n export *\n}\n'
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "ReceiveCheckpointHarness", targets: [
    .systemLibrary(name: "SQLite3"),
    .target(name: "CryptoKit"),
    .target(name: "ReceiveCheckpointHarness", dependencies: ["SQLite3", "CryptoKit"]),
    .testTarget(name: "ReceiveCheckpointHarnessTests", dependencies: ["ReceiveCheckpointHarness", "SQLite3"])
])
""")
        add_localization(
            sources,
            work / "Package.swift",
            "ReceiveCheckpointHarness",
            app_constants=False,
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

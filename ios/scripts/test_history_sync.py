#!/usr/bin/env python3
"""Execute production sync/store reconciliation and model code with offline network/Keychain boundaries."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from localization_harness import add_localization

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-history-sync-") as directory:
        work = Path(directory)
        sources = work / "Sources/HistorySyncHarness"
        tests = work / "Tests/HistorySyncHarnessTests"
        sqlite = work / "Sources/SQLite3"
        for path in (sources, tests, sqlite):
            path.mkdir(parents=True)
        for name in (
            "HistoryRecordDatabase",
            "HistoryJSONStream",
            "ReceiveHistoryStream",
            "HistorySnapshot",
            "HistorySync",
            "HistoryCooldowns",
            "HistoryRefresh",
            "TransferRecord",
            "SharedLinkTitle",
            "InboxPageWindow",
            "ServerTimestamp",
        ):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        model = (ROOT / "Psst/ViewModels/HistoryPageViewModel.swift").read_text()
        model = "import Observation\n" + model
        (sources / "HistoryPageViewModel.swift").write_text(model)
        store = (ROOT / "Shared/TransferHistoryStore.swift").read_text()
        (sources / "CacheIntegration.swift").write_text(
            "import Foundation\n"
            + store[
                store.index(
                    "@MainActor\nextension TransferHistoryStore {",
                    store.index("extension Notification.Name"),
                ) :
            ]
        )
        shutil.copy2(ROOT / "scripts/history_sync_harness/Boundaries.swift", sources)
        for name in ("HistorySyncTests", "HistorySyncModelTests"):
            (tests / f"{name}.swift").write_text(
                (ROOT / f"PsstTests/{name}.swift")
                .read_text()
                .replace("@testable import Psst", "@testable import HistorySyncHarness")
            )
        shutil.copy2("/usr/include/sqlite3.h", sqlite / "sqlite3.h")
        (sqlite / "module.modulemap").write_text(
            'module SQLite3 [system] {\n header "sqlite3.h"\n link "sqlite3"\n export *\n}\n'
        )
        shutil.copy2(ROOT.parent / "docs/testing/fixtures/history-sync-v1.json", tests)
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "HistorySyncHarness", targets: [
 .systemLibrary(name: "SQLite3"),
 .target(name: "HistorySyncHarness", dependencies: ["SQLite3"]),
 .testTarget(name: "HistorySyncHarnessTests", dependencies: ["HistorySyncHarness"], resources: [.copy("history-sync-v1.json")])
])
""")
        add_localization(
            sources, work / "Package.swift", "HistorySyncHarness", app_constants=True
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
                f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; mkdir -p /work/sqlite-libs; ln -s /usr/lib/$(uname -m)-linux-gnu/libsqlite3.so.0 /work/sqlite-libs/libsqlite3.so; swift test --jobs 2 -Xlinker -L/work/sqlite-libs",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

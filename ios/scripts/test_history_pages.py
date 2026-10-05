#!/usr/bin/env python3
"""Compile exact portable account-history page decoder and cursor navigation tests in Swift, without iOS or network dependencies."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from localization_harness import add_localization

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-history-page-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "HistoryPageHarness"
        tests = work / "Tests" / "HistoryPageHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        for name in ("HistorySnapshot", "InboxPageWindow", "HistorySync"):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
            (tests / f"{name}Tests.swift").write_text(
                (ROOT / f"PsstTests/{name}Tests.swift")
                .read_text()
                .replace("@testable import Psst", "@testable import HistoryPageHarness")
            )
        for name in ("SharedLinkTitle", "BoundedHistoryMerge", "TransferRecord"):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        (sources / "Boundaries.swift").write_text("""import Foundation
extension String { init(localized value: String) { self = value } }
struct DeviceSession: Equatable { var serverURL: String; var userID: String; var canTransfer = true }
enum SecretStore {
 static func read(_ key: String) -> Data? { nil }
 static func write(_ data: Data, name: String) throws {}
}
""")
        for path in (
            "Psst/Services/UnifiedHistory.swift",
            "Psst/ViewModels/MergedDeviceHistoryViewModel.swift",
        ):
            shutil.copy2(ROOT / path, sources)
        shutil.copy2(ROOT / "scripts/workflow_harness/HistoryStores.swift", sources)
        shutil.copy2(ROOT / "scripts/workflow_harness/MergedHistoryTests.swift", tests)
        (tests / "WorkflowPresentationTests.swift").write_text(
            (ROOT / "PsstTests/WorkflowPresentationTests.swift")
            .read_text()
            .replace("@testable import Psst", "@testable import HistoryPageHarness")
        )
        shutil.copy2(ROOT.parent / "docs/testing/fixtures/history-sync-v1.json", tests)
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "HistoryPageHarness", targets: [
    .target(name: "HistoryPageHarness"),
    .testTarget(name: "HistoryPageHarnessTests", dependencies: ["HistoryPageHarness"], resources: [.copy("history-sync-v1.json")])
])
""")
        add_localization(
            sources, work / "Package.swift", "HistoryPageHarness", app_constants=True
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
                f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; swift test --jobs 4",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Run production Foundation presenters with a narrow fake Shared bridge.

The Shared target models the exported error types so canImport(Shared) branches
compile on Linux. It does not run Kotlin/Native or verify its exception bridge;
native app, extension and XCTest verification still require macOS/Xcode.
"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="psst-localization-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "LocalizationHarness"
        shared = work / "Sources" / "Shared"
        tests = work / "Tests" / "LocalizationHarnessTests"
        sources.mkdir(parents=True)
        shared.mkdir(parents=True)
        tests.mkdir(parents=True)
        bridge_fixtures = ROOT / "scripts" / "localization_harness"
        shutil.copy2(bridge_fixtures / "SharedBoundary.swift", shared)
        shutil.copy2(bridge_fixtures / "SharedFailureBridgeTests.swift", tests)
        for name in (
            "Localization",
            "AppConstants",
            "TransferIncident",
            "ClientErrorPresentation",
            "LocalUploadFailure",
            "AbuseReport",
            "TransferRecord",
        ):
            shutil.copy2(ROOT / f"Shared/{name}.swift", sources)
        for language in ("en", "de"):
            shutil.copytree(
                ROOT / "Shared" / f"{language}.lproj", sources / f"{language}.lproj"
            )
        (sources / "BoundaryStubs.swift").write_text("""import Foundation
struct DeviceSession { var serverURL: String; var userID: String; var canTransfer = true }
enum SecretStore {
 static func read(_ key: String) -> Data? { nil }
 static func write(_ data: Data, name: String) throws {}
}
// Platform-only boundaries; their behavior is not exercised by this harness.
enum AccountError: LocalizedError { case request; var errorDescription: String? { nil } }
enum LinkLimitError: LocalizedError { case unsupportedServer; var errorDescription: String? { nil } }
enum SharedLinkTitle { enum Failure: LocalizedError { case invalid; var errorDescription: String? { nil } } }
enum ShareSelectionError: Error { case tooLarge; var message: String { "" } }
enum GuestUploadSelectionError: LocalizedError { case invalidFiles; var errorDescription: String? { nil } }
enum ReceiveSafetyError: LocalizedError { case storage; var errorDescription: String? { nil } }
""")
        for name in ("LocalizationTests", "TransferIncidentTests"):
            (tests / f"{name}.swift").write_text(
                (ROOT / f"PsstTests/{name}.swift")
                .read_text()
                .replace(
                    "@testable import Psst", "@testable import LocalizationHarness"
                )
            )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "LocalizationHarness", defaultLocalization: "en", targets: [
 .target(name: "Shared"),
 .target(name: "LocalizationHarness", dependencies: ["Shared"], resources: [.copy("en.lproj"), .copy("de.lproj")]),
 .testTarget(name: "LocalizationHarnessTests", dependencies: ["LocalizationHarness", "Shared"])
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

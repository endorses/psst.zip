#!/usr/bin/env python3
"""Run production scanner request lifetime against pending, non-cooperative server replies.

The Foundation harness verifies cancellation/result ownership, not SwiftUI or camera rendering.
"""

import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    scanner = (ROOT / "Psst/Views/ScanReceiveView.swift").read_text()
    assert re.search(r'Button\(L10n.text\("Back"\)\)\s*\{\s*stopPreflight\(\)', scanner)
    assert ".onChange(of: isSelected)" in scanner and re.search(
        r"if\s+!selected\s*\{\s*stopPreflight\(\)", scanner
    )
    assert "capacityRequest.attachTransport(request: request)" in scanner
    assert "pairingRequest.isCurrent(request)" in scanner
    settings = (ROOT / "Psst/Views/ServerConfigView.swift").read_text()
    assert settings.count(".pickerStyle(.menu)") == 2
    with tempfile.TemporaryDirectory(prefix="psst-scanner-navigation-") as directory:
        work = Path(directory)
        sources = work / "Sources/ScannerHarness"
        tests = work / "Tests/ScannerHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        shutil.copy2(ROOT / "Shared/ScannerRequestLifetime.swift", sources)
        (tests / "ScannerRequestLifetimeTests.swift").write_text(
            (ROOT / "PsstTests/ScannerRequestLifetimeTests.swift")
            .read_text()
            .replace("@testable import Psst", "@testable import ScannerHarness")
        )
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "ScannerHarness", targets: [
 .target(name: "ScannerHarness"),
 .testTarget(name: "ScannerHarnessTests", dependencies: ["ScannerHarness"])
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

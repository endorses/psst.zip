#!/usr/bin/env python3
"""Run exact portable Swift sources/tests with Apple's pinned Linux crypto provider.

Requires Docker. This is not an iOS build or a substitute for CryptoKit/device tests.
The temporary SwiftPM workspace and dependency caches are removed on exit.
"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULES = (
    "ReceiveCrypto",
    "ReceiveSafety",
    "LinkLimit",
    "HistorySnapshot",
    "TransferIncident",
)


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="psst-receive-swift-") as directory:
        work = Path(directory)
        sources = work / "Sources" / "ReceiveCryptoHarness"
        tests = work / "Tests" / "ReceiveCryptoHarnessTests"
        sources.mkdir(parents=True)
        tests.mkdir(parents=True)
        for name in MODULES:
            shutil.copy2(ROOT / "ios" / "Shared" / f"{name}.swift", sources)
            test = ROOT / "ios" / "PsstTests" / f"{name}Tests.swift"
            (tests / test.name).write_text(
                test.read_text().replace(
                    "@testable import Psst", "@testable import ReceiveCryptoHarness"
                )
            )
        shutil.copy2(ROOT / "docs/security/fixtures/hpke-receive-v2.json", tests)
        (work / "Package.swift").write_text("""// swift-tools-version:5.9
import PackageDescription
let package = Package(
    name: "ReceiveCryptoHarness",
    dependencies: [.package(url: "https://github.com/apple/swift-crypto.git", exact: "3.12.3"),
                   .package(url: "https://github.com/apple/swift-asn1.git", exact: "1.6.0")],
    targets: [
        .target(name: "ReceiveCryptoHarness", dependencies: [.product(name: "Crypto", package: "swift-crypto")]),
        .testTarget(name: "ReceiveCryptoHarnessTests", dependencies: ["ReceiveCryptoHarness"],
                    resources: [.copy("hpke-receive-v2.json")])
    ])
""")
        # Restore host ownership even on test failure, so TemporaryDirectory can
        # remove all caches made by the container without elevated host cleanup.
        script = (
            f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; "
            "swift test --jobs 4"
        )
        subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "-v",
                f"{work}:/work",
                "-w",
                "/work",
                "swift:6.0-noble",
                "bash",
                "-c",
                script,
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

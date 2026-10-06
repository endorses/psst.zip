#!/usr/bin/env python3
"""Compile actual observer ownership with Swift 6 strict concurrency and test cleanup."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(
        prefix="psst-notification-observers-"
    ) as directory:
        work = Path(directory)
        shutil.copy2(ROOT / "Shared/NotificationObservers.swift", work)
        shutil.copy2(
            ROOT / "scripts/notification_observer_harness/LifetimeChecks.swift", work
        )
        # Keep a failing control so this test cannot silently lose its actor check.
        (work / "UnsafeOwner.swift").write_text("""import Foundation
@MainActor final class UnsafeOwner {
    private var token: NSObjectProtocol?
    deinit {
        if let token { NotificationCenter.default.removeObserver(token) }
    }
}
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
                f"trap 'chown -R {os.getuid()}:{os.getgid()} /work' EXIT; "
                "set -eu; "
                "if swiftc -swift-version 6 -strict-concurrency=complete "
                "-emit-module UnsafeOwner.swift >negative-control.log 2>&1; then "
                "echo 'Expected unsafe actor deinit to fail compilation'; exit 1; fi; "
                "grep -q deinit negative-control.log; "
                "grep -q non-sendable negative-control.log; "
                "swiftc -swift-version 6 -strict-concurrency=complete -parse-as-library "
                "NotificationObservers.swift LifetimeChecks.swift -o lifetime-checks; "
                "./lifetime-checks",
            ],
            check=True,
        )


if __name__ == "__main__":
    main()

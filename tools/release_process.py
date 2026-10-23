"""Temporary bounded process adapter for unmigrated Python measurements.

No publisher lives here. Remove when the last native measurement/recovery
consumer moves to the Go release tool; historical immutable bundles retain their
original captured helpers.
"""

from __future__ import annotations

import os
import signal
import subprocess
import tempfile
import time

from release_artifacts import require

MAX_JSON = 16 * 1024**2


def command(
    args: list[str], *, environment: dict[str, str], timeout: int = 1200
) -> bytes:
    """Run fixed argv with bounded captured output; never echo output or secrets."""
    with tempfile.TemporaryFile() as output:
        child = subprocess.Popen(
            args,
            stdout=output,
            stderr=subprocess.DEVNULL,
            env=environment,
            start_new_session=True,
        )
        try:
            started = time.monotonic()
            while child.poll() is None:
                require(
                    time.monotonic() - started <= timeout, "Image command timed out"
                )
                require(
                    os.fstat(output.fileno()).st_size <= MAX_JSON,
                    "Image command output exceeds limit",
                )
                time.sleep(0.05)
            require(
                child.returncode == 0,
                "Image command failed; reconcile uncertain mutation",
            )
            require(
                os.fstat(output.fileno()).st_size <= MAX_JSON,
                "Image command output exceeds limit",
            )
            output.seek(0)
            return output.read(MAX_JSON + 1)
        finally:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            child.wait()

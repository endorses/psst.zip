#!/usr/bin/env python3
"""Sample macOS build resources without changing the build or its exit status."""

import os
import re
import signal
import subprocess
import sys
import threading
import time

INTERVAL_SECONDS = 30
MAX_SAMPLES = 50  # The native job already has a 25-minute limit.
PROCESS_NAMES = frozenset(
    {
        "xcodebuild",
        "XCBBuildService",
        "swift-frontend",
        "swiftc",
        "swift-driver",
        "actool",
        "ibtool",
        "clang",
        "ld",
        "java",
        "konanc",
        "CoreSimulatorService",
        "com.apple.CoreSimulator.CoreSimulatorService",
        "Simulator",
        "simdiskimaged",
        "simd",
    }
)
VM_FIELDS = frozenset(
    {
        "Pages free",
        "Pages active",
        "Pages inactive",
        "Pages wired down",
        "Pages occupied by compressor",
        "Pageouts",
        "Swapins",
        "Swapouts",
    }
)


def read_diagnostic(command):
    try:
        return subprocess.run(
            command,
            capture_output=True,
            text=True,
            timeout=2,
            check=False,
        ).stdout
    except (OSError, subprocess.TimeoutExpired):
        return ""


def snapshot():
    # Query executable names, never process arguments or environment variables.
    rows = []
    for line in read_diagnostic(
        ["ps", "-axo", "pid=,ppid=,stat=,%cpu=,rss=,time=,comm="]
    ).splitlines():
        fields = line.split(maxsplit=6)
        if len(fields) != 7 or os.path.basename(fields[6]) not in PROCESS_NAMES:
            continue
        try:
            pid, parent, state, cpu, rss, cpu_time, executable = fields
            row = (
                float(cpu),
                f"{os.path.basename(executable)} pid={int(pid)} parent={int(parent)} "
                f"state={state} cpu={float(cpu):.1f}% rss={int(rss)}KiB "
                f"cpu_time={cpu_time}",
            )
            rows.append(row)
        except ValueError:
            continue
    # CPU is ps's lifetime average, not an instantaneous utilization sample.
    lines = ["Processes (CPU lifetime average):"]
    lines.extend(row[1] for row in sorted(rows, reverse=True)[:12])
    if not rows:
        lines.append("No matching process sample available")
    memory = read_diagnostic(["vm_stat"])
    page_size = re.search(r"page size of (\d+) bytes", memory)
    if page_size:
        lines.append(f"VM page_size={page_size[1]} bytes")
    for line in memory.splitlines():
        name, separator, value = line.partition(":")
        if separator and name in VM_FIELDS and re.fullmatch(r"\s*\d+\.?\s*", value):
            lines.append(f"{name}={value.strip().rstrip('.')}")
    swap = read_diagnostic(["sysctl", "-n", "vm.swapusage"])
    fields = re.findall(r"\b(total|used|free) = (\d+(?:\.\d+)?[MG])", swap)
    if fields:
        lines.append("Swap " + " ".join(f"{key}={value}" for key, value in fields))
    return lines


def monitor(stop, started):
    for sample in range(1, MAX_SAMPLES + 1):
        if stop.is_set():
            return
        try:
            load = "/".join(f"{value:.2f}" for value in os.getloadavg())
            lines = snapshot()
            print(
                f"Build resources sample={sample} elapsed={time.monotonic() - started:.1f}s "
                f"processors={os.cpu_count()} load_1m/5m/15m={load}\n"
                + "\n".join(lines),
                flush=True,
            )
        except Exception as error:
            # Diagnostics are best effort and must never fail the build.
            print(f"Build resources unavailable: {type(error).__name__}", flush=True)
        if stop.wait(INTERVAL_SECONDS):
            return


def main():
    if len(sys.argv) < 2:
        raise SystemExit("Usage: diagnose_build.py xcodebuild [arguments...]")
    started = time.monotonic()
    print("Starting native build with bounded resource sampling", flush=True)
    child = subprocess.Popen(sys.argv[1:], start_new_session=True)
    interrupted = None
    cancellation = None

    def force_stop():
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    def forward_signal(number, _frame):
        nonlocal interrupted, cancellation
        if interrupted is not None:
            return
        interrupted = number
        try:
            os.killpg(child.pid, number)
        except ProcessLookupError:
            pass
        # A cancelled build must not leave a subprocess that ignores the signal.
        cancellation = threading.Timer(5, force_stop)
        cancellation.daemon = True
        cancellation.start()

    for number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(number, forward_signal)
    stop = threading.Event()
    sampler = threading.Thread(target=monitor, args=(stop, started), daemon=True)
    sampler.start()
    try:
        status = child.wait()
    finally:
        if cancellation is not None:
            cancellation.cancel()
            force_stop()
        stop.set()
        # No temporary logs are created; any outstanding diagnostic has a 2s limit.
        sampler.join(timeout=7)
    print(
        f"Native build finished elapsed={time.monotonic() - started:.1f}s status={status}",
        flush=True,
    )
    number = interrupted or (-status if status < 0 else None)
    if number is not None:
        signal.signal(number, signal.SIG_DFL)
        os.kill(os.getpid(), number)
    return status


if __name__ == "__main__":
    sys.exit(main())

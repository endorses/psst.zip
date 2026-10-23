#!/usr/bin/env bash
# Internal runner shared by host and Docker execution; the caller owns scratch cleanup.
set -euo pipefail
work=$1
jobs=$2
shift 2
package=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# Ubuntu's runtime-only Swift image has the SQLite runtime but not its linker name.
link_flags=()
if [[ $(uname -s) == Linux ]]; then
  if [[ ! -e /usr/lib/$(uname -m)-linux-gnu/libsqlite3.so ]]; then
    mkdir -p "$work/sqlite-libs"
    ln -s /usr/lib/"$(uname -m)"-linux-gnu/libsqlite3.so.0 "$work/sqlite-libs/libsqlite3.so"
    link_flags=(-Xlinker -L"$work/sqlite-libs")
  fi
fi
swift --version
swift test --package-path "$package" --scratch-path "$work/build" \
  --cache-path "$work/cache" --config-path "$work/config" --security-path "$work/security" \
  --disable-automatic-resolution --jobs "$jobs" "${link_flags[@]}" "$@"
# Keep a failing control so actor lifetime safety cannot silently lose its check.
if swiftc -swift-version 6 -strict-concurrency=complete -emit-module \
  "$package/Checks/UnsafeOwner.swift" -o "$work/UnsafeOwner.swiftmodule" >"$work/negative-control.log" 2>&1; then
  echo 'Expected unsafe actor deinit to fail compilation' >&2
  exit 1
fi
grep -qi 'deinit' "$work/negative-control.log"
grep -qi 'non-sendable' "$work/negative-control.log"
swiftc -swift-version 6 -strict-concurrency=complete -parse-as-library \
  "$package/Checks/NotificationObservers.swift" "$package/Checks/LifetimeChecks.swift" -o "$work/lifetime-checks"
"$work/lifetime-checks"

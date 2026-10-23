#!/bin/sh
# Build a native development tool once per exact source/toolchain input set.
set -eu
tooling_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
go_command=${GO:-go}
build_dir="$tooling_dir/.build"
mkdir -p "$build_dir"
host_os=$("$go_command" env GOHOSTOS)
host_arch=$("$go_command" env GOHOSTARCH)
toolchain=$("$go_command" env GOVERSION)
suffix=
if [ "$host_os" = windows ]; then suffix=.exe; fi
output="$build_dir/psst-release$suffix"
fingerprint=$(
    {
        printf '%s\n' "$host_os/$host_arch/$toolchain"
        find "$tooling_dir" -type f \( -name '*.go' -o -name go.mod -o -name go.sum \) -exec cksum {} \; | sort
    } | cksum
)
previous=
if [ -f "$output.inputs" ]; then previous=$(cat "$output.inputs"); fi
if [ ! -x "$output" ] || [ "$fingerprint" != "$previous" ]; then
    temporary="$output.tmp.$$"
    trap 'rm -f "$temporary" "$temporary.inputs"' EXIT HUP INT TERM
    GOOS="$host_os" GOARCH="$host_arch" CGO_ENABLED=0 GOWORK=off \
        "$go_command" -C "$tooling_dir" build -trimpath -buildvcs=false -o "$temporary" ./cmd/psst-release
    printf '%s\n' "$fingerprint" > "$temporary.inputs"
    mv "$temporary" "$output"
    mv "$temporary.inputs" "$output.inputs"
fi
printf '%s\n' "$output"

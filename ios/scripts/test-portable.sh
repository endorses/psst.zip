#!/usr/bin/env bash
# Run all portable Swift targets once; use Docker only when requested or Swift is absent.
set -euo pipefail
repository=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
mode=auto
case "${1:-}" in
  --native) mode=native; shift ;;
  --docker) mode=docker; shift ;;
  --help)
    printf '%s\n' 'Usage: ios/scripts/test-portable.sh [--native|--docker] [swift test arguments...]' \
      'Runs exact Swift production sources and XCTest plus a strict-concurrency negative control.' \
      'Docker requires a local SQLite development header (PSST_SQLITE_HEADER overrides /usr/include/sqlite3.h).' \
      'PSST_SWIFT_IMAGE defaults to the stable swift:6.4.0-noble image; PSST_SWIFT_JOBS defaults to 4.'
    exit 0 ;;
esac
if [[ $mode == auto ]]; then
  if command -v swift >/dev/null 2>&1; then mode=native; else mode=docker; fi
fi
work=$(mktemp -d "${TMPDIR:-/tmp}/psst-swift-portable.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
# SwiftPM rejects shared physical Swift paths across semantic harness modules.
# Stage exact complete files from an explicit reviewed map, with no rewriting.
cp -R "$repository/ios/portable" "$work/package"
while IFS=$'\t' read -r destination source; do
  [[ -z $destination || $destination == \#* ]] && continue
  for path in "$destination" "$source"; do
    case $path in
      /*|../*|*/../*|*/..|*\\*|*//*|./*|*/./*) echo 'Unsafe portable source mapping' >&2; exit 2 ;;
    esac
  done
  [[ -n $source && ! -L $repository/$source ]] || { echo 'Invalid portable source mapping' >&2; exit 2; }
  [[ ! -e $work/package/$destination ]] || { echo 'Duplicate portable source destination' >&2; exit 2; }
  mkdir -p "$(dirname "$work/package/$destination")"
  cp -R "$repository/$source" "$work/package/$destination"
done < "$repository/ios/portable/Sources.tsv"
jobs=${PSST_SWIFT_JOBS:-4}
[[ $jobs =~ ^[1-9][0-9]*$ ]] || { echo 'PSST_SWIFT_JOBS must be a positive integer' >&2; exit 2; }
if [[ $mode == docker ]]; then
  header=${PSST_SQLITE_HEADER:-/usr/include/sqlite3.h}
  [[ -f $header ]] || { echo 'Install the SQLite development header or set PSST_SQLITE_HEADER.' >&2; exit 2; }
  image=${PSST_SWIFT_IMAGE:-swift:6.4.0-noble}
  docker_args=(--rm --user "$(id -u):$(id -g)" -e HOME=/work -e XDG_CACHE_HOME=/work/cache
    -v "$repository:/src:ro" -v "$work:/work" -v "$header:/usr/include/sqlite3.h:ro" -w /work/package)
  docker run "${docker_args[@]}" "$image" swift package --scratch-path /work/build \
    --cache-path /work/cache --config-path /work/config --security-path /work/security resolve
  docker run "${docker_args[@]}" --network none "$image" bash /work/package/run-checks.sh /work "$jobs" "$@"
else
  swift package --package-path "$work/package" --scratch-path "$work/build" \
    --cache-path "$work/cache" --config-path "$work/config" --security-path "$work/security" resolve
  bash "$work/package/run-checks.sh" "$work" "$jobs" "$@"
fi

#!/usr/bin/env bash
# Opt-in Linux test. The sole outer container owns an isolated nested daemon.
set -euo pipefail

module_root=$(cd "$(dirname "$0")/../../.." && pwd)
repository_root=$(cd "$module_root/../.." && pwd)
fixture_id="psst-go-updater-$(date -u +%Y%m%d%H%M%S)-${RANDOM}"
fixture_temp=$(mktemp -d "/tmp/${fixture_id}.XXXXXX")
daemon_base='docker:29-dind@sha256:7dcdfc4a20246236f558175182ccace1eb15a41bd3eb119dd2284f393498b7c1'
fixture_images=("${fixture_id}-runner:local")
docker_local() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    docker --host=unix:///var/run/docker.sock "$@"
}
cleanup() {
  local failed=0 selected
  selected=$(docker_local container ls -aq --filter "name=^/${fixture_id}$") || failed=1
  if [[ -n "$selected" ]]; then
    docker_local rm -fv "$fixture_id" >/dev/null || failed=1
  fi
  for image in "${fixture_images[@]}"; do
    selected=$(docker_local image ls -q --filter "reference=$image") || failed=1
    if [[ -n "$selected" ]]; then
      docker_local image rm "$image" >/dev/null || failed=1
    fi
    selected=$(docker_local image ls -q --filter "reference=$image") || failed=1
    [[ -z "$selected" ]] || failed=1
  done
  selected=$(docker_local container ls -aq --filter "name=^/${fixture_id}$") || failed=1
  [[ -z "$selected" ]] || failed=1
  rm -rf -- "$fixture_temp"
  if ((failed)); then
    printf 'Owned cleanup failed; inspect only scope %s\n' "$fixture_id" >&2
    return 1
  fi
}
trap cleanup EXIT

docker_local info --format 'Outer Docker {{.ServerVersion}} (local socket)'
# The runner deliberately reuses a pinned cached daemon, rather than upgrading
# the test environment or accessing a registry during this bounded experiment.
docker_local image inspect "$daemon_base" >/dev/null
mkdir "$fixture_temp/runner"
PSST_TEST_CACHE=${GOCACHE:-"$fixture_temp/go-build"}
(
  cd "$module_root"
  CGO_ENABLED=0 GOCACHE="$PSST_TEST_CACHE" "${PSST_GO:-go}" test -c -o "$fixture_temp/runner/deployment.test" ./internal/deployment
)
cp "$repository_root/deploy/compose.release.yml" "$fixture_temp/runner/compose.release.yml"
# Reuse the installed official CLIs and their installed dynamic libraries. This
# needs no package installation and does not copy credentials or configuration.
python3 - "$fixture_temp/runner" <<'PY'
from pathlib import Path
import re
import shutil
import subprocess
import sys

root = Path(sys.argv[1])
library_root = root / "host-tools"
library_root.mkdir()
libraries = set()
for name in ("sqlite3", "curl", "tar"):
    binary = Path("/usr/bin") / name
    shutil.copy2(binary, library_root / name)
    output = subprocess.check_output(["ldd", str(binary)], text=True)
    libraries.update(re.findall(r"(?:=>\s+|^\s*)(/\S+)", output, re.M))
    (root / (name + ".sh")).write_text(
        "#!/bin/sh\nexec /opt/fixture/host-tools/ld-linux-x86-64.so.2 "
        "--library-path /opt/fixture/host-tools /opt/fixture/host-tools/"
        + name + ' "$@"\n'
    )
for library in sorted(libraries):
    shutil.copy2(library, library_root / Path(library).name)
PY
cat >"$fixture_temp/runner/Dockerfile" <<EOF
FROM $daemon_base
COPY . /opt/fixture/
RUN chmod 755 /opt/fixture/deployment.test /opt/fixture/*.sh && \
    ln -s /usr/local/bin/docker /usr/bin/docker && \
    ln -s /opt/fixture/sqlite3.sh /usr/bin/sqlite3 && \
    ln -s /opt/fixture/curl.sh /usr/bin/curl && \
    ln -sf /opt/fixture/tar.sh /usr/bin/tar
EOF
docker_local build --pull=false --network=none -q -t "${fixture_images[0]}" "$fixture_temp/runner" >/dev/null

for pair in prior-backend prior-web candidate-backend candidate-web; do
  case "$pair" in
    prior-backend) source_image=${PSST_PRIOR_BACKEND:-psst-runtime-pack-backend-overlay:local} ;;
    prior-web) source_image=${PSST_PRIOR_WEB:-psst-runtime-pack-web-overlay:local} ;;
    candidate-backend) source_image=${PSST_CANDIDATE_BACKEND:-psst-patched-review-backend-overlay:750f440} ;;
    candidate-web) source_image=${PSST_CANDIDATE_WEB:-psst-patched-review-web-overlay:750f440} ;;
  esac
  alias="${fixture_id}-${pair}:local"
  fixture_images+=("$alias")
  docker_local image tag "$source_image" "$alias"
done
mkdir "$fixture_temp/retention"
cat >"$fixture_temp/retention/Dockerfile" <<'EOF'
FROM alpine:3.24.2
ARG FIXTURE_ID
LABEL zip.psst.disposable=$FIXTURE_ID
EOF
for i in 1 2 3 4; do
  alias="${fixture_id}-retention-${i}:local"
  fixture_images+=("$alias")
  docker_local build --pull=false --network=none -q --build-arg "FIXTURE_ID=${fixture_id}-${i}" -t "$alias" "$fixture_temp/retention" >/dev/null
done
docker_local run -d --name "$fixture_id" --privileged --network none \
  --env DOCKER_TLS_CERTDIR= "${fixture_images[0]}" --storage-driver vfs >/dev/null
ready=0
for attempt in {1..60}; do
  if docker_local exec "$fixture_id" docker info >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if ((ready == 0)); then
  printf 'Disposable nested Docker did not start (%s)\n' "$fixture_id" >&2
  exit 1
fi
for i in "${fixture_images[@]:1}"; do
  docker_local image save "$i" | docker_local exec -i "$fixture_id" docker image load >/dev/null
  local_alias="${i/#$fixture_id/psst-go-fixture}"
  docker_local exec "$fixture_id" docker image tag "$i" "$local_alias"
  docker_local exec "$fixture_id" docker image rm --no-prune "$i" >/dev/null
done
docker_local exec "$fixture_id" docker version --format 'Nested Docker {{.Server.Version}}'
docker_local exec "$fixture_id" docker compose version --short
docker_local exec --env PSST_DISPOSABLE_DOCKER=1 "$fixture_id" \
  /opt/fixture/deployment.test -test.v -test.timeout=10m -test.run '^TestDisposableDockerUpdateRecoveryRetention$'

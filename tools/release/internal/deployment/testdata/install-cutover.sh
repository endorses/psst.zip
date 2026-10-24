#!/usr/bin/env bash
# Opt-in local root/SSH/sudo test. No pulls, mounts, ports or host config writes.
set -euo pipefail
module_root=$(cd "$(dirname "$0")/../../.." && pwd)
repository_root=$(cd "$module_root/../.." && pwd)
fixture_id="psst-go-cutover-$(date -u +%Y%m%d%H%M%S)-${RANDOM}"
fixture_temp=$(mktemp -d "/tmp/${fixture_id}.XXXXXX")
docker_local() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    docker --host=unix:///var/run/docker.sock "$@"
}
cleanup() {
  local selected failed=0
  selected=$(docker_local container ls -aq --filter "name=^/${fixture_id}$") || failed=1
  if [[ -n "$selected" ]]; then docker_local rm -f "$fixture_id" >/dev/null || failed=1; fi
  selected=$(docker_local image ls -q --filter "reference=${fixture_id}:local") || failed=1
  if [[ -n "$selected" ]]; then docker_local image rm "${fixture_id}:local" >/dev/null || failed=1; fi
  selected=$(docker_local container ls -aq --filter "name=^/${fixture_id}$") || failed=1
  [[ -z "$selected" ]] || failed=1
  selected=$(docker_local image ls -q --filter "reference=${fixture_id}:local") || failed=1
  [[ -z "$selected" ]] || failed=1
  rm -rf -- "$fixture_temp"
  if ((failed)); then
    printf 'Inspect only owned cutover scope %s\n' "$fixture_id" >&2
    return 1
  fi
}
trap cleanup EXIT
mkdir "$fixture_temp/rootfs"
(
  cd "$module_root"
  CGO_ENABLED=0 GOCACHE="${GOCACHE:-$fixture_temp/go-build}" "${PSST_GO:-go}" build \
    -trimpath -o "$fixture_temp/psst-deploy" ./cmd/psst-deploy
)
# Assemble a small scratch root from installed executables and their public
# runtime libraries. No host account, sudo, SSH, certificate or key config is copied.
python3 - "$repository_root" "$fixture_temp" <<'PY'
from pathlib import Path
import re
import shutil
import subprocess
import sys

repo, work = map(Path, sys.argv[1:])
root = work / "rootfs"
copied = set()

def copy_binary(path):
    path = Path(path)
    if str(path) in copied:
        return
    copied.add(str(path))
    destination = root / path.relative_to("/")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(path, destination)
    result = subprocess.run(["ldd", str(path)], capture_output=True, text=True, check=False)
    for library in re.findall(r"(?:=>\s+|^\s*)(/\S+)", result.stdout, re.M):
        copy_binary(library)

for name in ("bash", "cat", "chmod", "cmp", "cp", "cut", "dirname", "env", "find", "flock", "grep", "head", "id", "install", "kill", "ln", "mkdir", "mktemp", "mv", "readlink", "rm", "sha256sum", "sleep", "ssh", "sshd", "ssh-keygen", "stat", "sudo", "sync", "timeout", "visudo", "python3", "false"):
    copy_binary(Path("/usr/bin") / name)
for path in Path("/usr/lib/sudo").iterdir():
    if path.is_file():
        copy_binary(path)
for name in ("sshd-auth", "sshd-session"):
    copy_binary(Path("/usr/lib/ssh") / name)
copy_binary("/usr/lib/security/pam_permit.so")
stdlib = Path(sys.base_prefix) / "lib" / ("python" + str(sys.version_info.major) + "." + str(sys.version_info.minor))
shutil.copytree(stdlib, root / stdlib.relative_to("/"), ignore=shutil.ignore_patterns("site-packages", "__pycache__", "test", "tests", "tkinter", "idlelib", "ensurepip", "turtledemo"))
for path in (root / stdlib.relative_to("/") / "lib-dynload").glob("*.so"):
    copy_binary(stdlib / "lib-dynload" / path.name)

def write(name, text, mode=0o644):
    path = root / name.lstrip("/")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    path.chmod(mode)

base = root / "usr/local/lib/psst.zip"
for source, destination in ((work / "psst-deploy", base / "psst-deploy.next"), (repo / "deploy/install-go-updater.sh", base / "install-go-updater.sh"), (repo / "deploy/update.py", base / "deploy/update.py"), (repo / "tools/release_artifacts.py", base / "tools/release_artifacts.py")):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, destination)
    destination.chmod(0o644 if destination.suffix == ".py" and destination.name != "update.py" else 0o755)
write("/etc/ssh/sshd_config.d/70-psst-deploy.conf", (repo / "deploy/ssh/psst-deploy.conf").read_text())
write("/etc/sudoers.d/psst-deploy", (repo / "deploy/ssh/psst-deploy.sudoers").read_text(), 0o440)
write("/etc/sudoers", "Defaults !requiretty\nroot ALL=(ALL:ALL) ALL\n@includedir /etc/sudoers.d\n", 0o440)
write("/etc/pam.d/sudo", "auth required pam_permit.so\naccount required pam_permit.so\nsession required pam_permit.so\n")
write("/etc/passwd", "root:x:0:0:root:/root:/usr/bin/bash\npsst-deploy:x:1001:1001:Fixture:/home/psst-deploy:/usr/bin/bash\nsshd:x:74:74:SSH:/usr/share/empty.sshd:/usr/bin/false\nnobody:x:65534:65534:Nobody:/usr/share/empty.sshd:/usr/bin/false\n")
write("/etc/group", "root:x:0:\npsst-deploy:x:1001:\nsshd:x:74:\nnobody:x:65534:\n")
write("/etc/shadow", "root:x:19000:0:99999:7:::\npsst-deploy:x:19000:0:99999:7:::\n", 0o600)
write("/etc/nsswitch.conf", "passwd: files\ngroup: files\nshadow: files\nhosts: files\n")
write("/etc/hosts", "127.0.0.1 localhost\n")
write("/etc/ssh/sshd_config", "Port 2222\nListenAddress 127.0.0.1\nPidFile /run/sshd.pid\nHostKey /etc/ssh/ssh_host_ed25519_key\nUsePAM no\nPermitRootLogin no\nStrictModes yes\nAllowUsers psst-deploy\nInclude /etc/ssh/sshd_config.d/*.conf\n")
for name, mode in (("root", 0o700), ("tmp", 0o1777), ("run/sshd", 0o755), ("usr/share/empty.sshd", 0o755), ("home/psst-deploy", 0o755), ("var/lib/psst.zip-deploy/transactions", 0o700), ("var/backups/psst.zip", 0o700), ("opt/psst-fixture", 0o755), ("etc/ssh/psst-deploy", 0o755), ("fixture", 0o700)):
    directory = root / name
    directory.mkdir(parents=True, exist_ok=True)
    directory.chmod(mode)
(root / "var/lib/psst.zip-deploy").chmod(0o700)
write("/etc/psst.zip/deployment.json", '''{
"repository":"endorses/psst.zip","signer_workflow":"endorses/psst.zip/.github/workflows/release.yml",
"installation":"/opt/psst-fixture","project":"psst-fixture","environment":".env","compose_files":["compose.yml"],
"operator_files":["Caddyfile"],"release_overrides":[],"external_proxy":false,"domain":"fixture.example.test",
"db_path":"/app/data/fixture.db","volume_names":{"backend:/app/data":"fixture-backend","caddy:/data":"fixture-data","caddy:/config":"fixture-config"},
"disk_reserve_bytes":1073741824,"image_reserve_bytes":1073741824,"verification_hook":null,"checkpoint_hook":null,
"github_token_file":null,"retention_count":2,"checkpoint_protection":"local-only","verification_profile":"runtime-v1"}
''', 0o600)
shutil.copy2(repo / "tools/release/internal/deployment/testdata/install-scenario.sh", root / "fixture/run.sh")
(root / "fixture/run.sh").chmod(0o755)
PY
# Import the owned root directly so this test creates no shared builder cache.
tar --owner=0 --group=0 --numeric-owner -C "$fixture_temp/rootfs" -cf - . |
  docker_local image import --change 'ENV PATH=/usr/sbin:/usr/bin:/sbin:/bin' \
    --change 'CMD ["/usr/bin/bash", "/fixture/run.sh"]' - "${fixture_id}:local" >/dev/null
if ! docker_local run --name "$fixture_id" --network none --pids-limit 96 --memory 768m --cpus 1 \
  "${fixture_id}:local"; then
  for log in sshd.log rejected.stderr; do
    if docker_local cp "$fixture_id:/fixture/$log" "$fixture_temp/$log" >/dev/null 2>&1; then
      head -c 16384 "$fixture_temp/$log" >&2
    fi
  done
  exit 1
fi

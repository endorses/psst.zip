#!/usr/bin/bash
set -euo pipefail
trap 'printf "Fixture failed at line %s\n" "$LINENO" >&2' ERR
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
umask 077
readonly base=/usr/local/lib/psst.zip
readonly stage=$base/updater-cutover
readonly installer=$base/install-go-updater.sh
readonly candidate=$base/psst-deploy.next
readonly state=/var/lib/psst.zip-deploy
expected=$(sha256sum "$candidate" | cut -d ' ' -f 1)
reject() { if "$@" >/fixture/rejected.stdout 2>/fixture/rejected.stderr; then
  printf 'Unexpected success: %s\n' "$1" >&2
  exit 1
fi; }
ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key
ssh-keygen -q -t ed25519 -N '' -f /fixture/client
cp /fixture/client.pub /etc/ssh/psst-deploy/authorized_keys
chmod 644 /etc/ssh/psst-deploy/authorized_keys
printf '[127.0.0.1]:2222 ' >/fixture/known_hosts
ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key >>/fixture/known_hosts
ssh_status() {
  timeout 15 ssh -F /dev/null -o BatchMode=yes -o StrictHostKeyChecking=yes \
    -o UserKnownHostsFile=/fixture/known_hosts -i /fixture/client -p 2222 \
    psst-deploy@127.0.0.1 "$@"
}
sshd -V 2>&1
sudo --version >/fixture/sudo-version.txt
head -n 1 /fixture/sudo-version.txt
sshd -t
visudo -c
printf '{"schema_version":1,"id":"20261024T000000Z-aaaaaaaaaaaa","phase":"completed","version":"v0.1.6","active_version":"v0.1.6","mutation_started":false}\n' >"$state/transaction.json"
cp "$state/transaction.json" /fixture/previous-transaction.json
/usr/bin/sshd -E /fixture/sshd.log
ssh_status status >/fixture/python-status.json
sha256sum "$base/deploy/update.py" "$base/tools/release_artifacts.py" \
  /etc/psst.zip/deployment.json "$state/transaction.json" >/fixture/preserved.sha256
reject "$installer" prepare "$(printf '%064d' 0)"
reject "$installer" prepare /tmp/another-helper
chmod 777 "$candidate"
reject "$installer" prepare "$expected"
chmod 755 "$candidate"
mv "$candidate" /fixture/candidate
ln -s /fixture/candidate "$candidate"
reject "$installer" prepare "$expected"
rm "$candidate"
mv /fixture/candidate "$candidate"
printf '{"schema_version":1,"phase":"awaiting-verification"}\n' >"$state/transaction.json"
reject "$installer" prepare "$expected"
[[ ! -e $stage/retained.sha256 ]]
rm -rf -- "$stage"
cp /fixture/previous-transaction.json "$state/transaction.json"
reject flock -n "$state/deployment.lock" "$installer" prepare "$expected"
"$installer" prepare "$expected"
[[ ! -e $base/psst-deploy ]]
cmp /etc/ssh/sshd_config.d/70-psst-deploy.conf "$stage/previous-ssh.conf"
cmp /etc/sudoers.d/psst-deploy "$stage/previous-sudoers"
# Exercise Ubuntu's supported layout as well: preparation above used the Arch
# /usr/bin tools; all activation checks below have only /usr/sbin available.
old_pid=$(cat /run/sshd.pid)
kill "$old_pid"
for attempt in {1..50}; do
  if [[ ! -e /run/sshd.pid ]]; then break; fi
  sleep .1
done
[[ ! -e /run/sshd.pid ]]
mkdir -p /usr/sbin
mv /usr/bin/sshd /usr/sbin/sshd
mv /usr/bin/visudo /usr/sbin/visudo
[[ ! -e /usr/bin/sshd && ! -e /usr/bin/visudo ]]
/usr/sbin/sshd -E /fixture/sshd.log
printf 'Match User psst-deploy\n ForceCommand /usr/bin/false\nMatch all\n' >/etc/ssh/sshd_config.d/00-fixture.conf
reject "$installer" activate "$expected"
cmp /etc/ssh/sshd_config.d/70-psst-deploy.conf "$stage/previous-ssh.conf"
cmp /etc/sudoers.d/psst-deploy "$stage/previous-sudoers"
[[ ! -e $base/psst-deploy ]]
rm /etc/ssh/sshd_config.d/00-fixture.conf
"$installer" activate "$expected"
passed=0
for attempt in {1..10}; do
  if ssh_status status >/fixture/go-status.json; then
    passed=1
    break
  fi
  sleep 1
done
[[ $passed == 1 ]]
python3 -I -B -c 'import json; assert json.load(open("/fixture/python-status.json")) == json.load(open("/fixture/go-status.json"))'
for original in '' 'status; id' 'update latest' 'update v01.2.3' 'update /tmp/image' \
  'update v1.2.3 --force' 'verify' 'restore' 'retention' 'cutover-check'; do
  reject ssh_status "$original"
done
sha256sum -c /fixture/preserved.sha256
[[ $(sha256sum "$base/psst-deploy" | cut -d ' ' -f 1) == "$expected" ]]
[[ $(stat -c %u:%a "$base/psst-deploy") == 0:755 ]]
[[ $(stat -c %u:%a /etc/sudoers.d/psst-deploy) == 0:440 ]]
cat "$stage/installed-check.json"
printf '%s\n' 'PASS: root ownership, fixed hash/path, lock, pending-state refusal, rollback, sudo restrictions, SSH reload and actual restricted status/rejections.'

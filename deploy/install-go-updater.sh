#!/usr/bin/bash
# Root-only local installation. No remote host, binary path or config is accepted.
set -eEuo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C
umask 077

readonly base=/usr/local/lib/psst.zip
readonly stage=$base/updater-cutover
readonly state=/var/lib/psst.zip-deploy
readonly ssh_config=/etc/ssh/sshd_config.d/70-psst-deploy.conf
readonly sudo_config=/etc/sudoers.d/psst-deploy
readonly helper=$base/psst-deploy
readonly account=psst-deploy
readonly match_context=user=psst-deploy,host=localhost,addr=127.0.0.1

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}
[[ $(id -u) == 0 ]] || fail 'Run the installed cutover tool as root.'
[[ $# == 2 && $1 =~ ^(prepare|activate)$ && $2 =~ ^[a-f0-9]{64}$ ]] ||
  fail 'Usage: install-go-updater.sh prepare|activate EXPECTED_SHA256'
readonly action=$1 expected=$2
[[ ${BASH_SOURCE[0]} == "$base/install-go-updater.sh" ]] ||
  fail 'Use the fixed root-owned installed cutover tool.'

# Every executable, input and parent is root-owned without linked components or
# group/other writers. This also protects the fixed destination directories.
protected() {
  local current=$1 mode owner
  [[ $current == /* && -e $current && ! -L $current ]] || return 1
  while :; do
    [[ ! -L $current ]] || return 1
    owner=$(stat -c %u -- "$current")
    mode=$(stat -c %a -- "$current")
    [[ $owner == 0 && $mode =~ ^[0-7]+$ ]] || return 1
    (((8#$mode & 0022) == 0)) || return 1
    [[ $current == / ]] && break
    current=$(dirname -- "$current")
  done
}
regular() { protected "$1" && [[ -f $1 ]]; }
system_tool() {
  local name=$1 candidate resolved
  for candidate in "/usr/sbin/$name" "/usr/bin/$name"; do
    resolved=$(readlink -e -- "$candidate") || continue
    [[ $resolved == /usr/sbin/"$name" || $resolved == /usr/bin/"$name" ]] || continue
    if regular "$resolved" && [[ -x $resolved ]]; then
      printf '%s\n' "$resolved"
      return 0
    fi
  done
  return 1
}
sshd=$(system_tool sshd) || fail 'A protected sshd is required in a supported system location.'
visudo=$(system_tool visudo) || fail 'A protected visudo is required in a supported system location.'
readonly sshd visudo
checksum() { sha256sum -- "$1" | cut -d ' ' -f 1; }
sync_file() { sync -f -- "$1"; }
atomic_copy() {
  local source=$1 destination=$2 mode=$3 temporary
  protected "$(dirname -- "$destination")" || return 1
  [[ ! -L $destination ]] || return 1
  temporary=$(mktemp -- "${destination}.psst-new.XXXXXX") || return 1
  install -m "$mode" -- "$source" "$temporary" || return 1
  sync_file "$temporary" || return 1
  mv -T -- "$temporary" "$destination" || return 1
  sync_file "$(dirname -- "$destination")" || return 1
}
ssh_restrictions() {
  local config=$1 output key value
  "$sshd" -t -f "$config"
  output=$("$sshd" -T -f "$config" -C "$match_context")
  # New OpenSSH dumps canonical directive case; older releases use lowercase.
  # Normalize directive names while retaining command/path values exactly.
  output=$(while read -r key value; do printf '%s %s\n' "${key,,}" "$value"; done <<<"$output")
  for line in \
    'authenticationmethods publickey' 'pubkeyauthentication yes' \
    'passwordauthentication no' 'kbdinteractiveauthentication no' \
    'authorizedkeysfile /etc/ssh/psst-deploy/authorized_keys' \
    'forcecommand /usr/bin/sudo -n /usr/local/lib/psst.zip/psst-deploy ssh' \
    'disableforwarding yes' 'permittty no' 'permittunnel no' 'x11forwarding no'; do
    if ! grep -Fxq -- "$line" <<<"$output"; then
      printf 'Effective SSH restriction differs: %s\n' "$line" >&2
      grep -F -- "${line%% *} " <<<"$output" >&2 || true
      return 1
    fi
  done
}
reload_sshd() {
  local pid executable
  regular /run/sshd.pid || return 1
  read -r pid </run/sshd.pid || return 1
  [[ $pid =~ ^[1-9][0-9]*$ && $pid -gt 1 ]] || return 1
  executable=$(readlink -- "/proc/$pid/exe") || return 1
  [[ $executable == /usr/bin/sshd || $executable == /usr/sbin/sshd ]] || return 1
  kill -HUP "$pid"
}
sudo_restrictions() {
  /usr/bin/sudo -n -u "$account" /usr/bin/env SSH_ORIGINAL_COMMAND=status \
    /usr/bin/sudo -n "$helper" ssh >/dev/null
  if /usr/bin/sudo -n -u "$account" /usr/bin/sudo -n /usr/bin/id >/dev/null 2>&1; then
    return 1
  fi
  if /usr/bin/sudo -n -u "$account" /usr/bin/sudo -n "$helper" status >/dev/null 2>&1; then
    return 1
  fi
  if /usr/bin/sudo -n -u "$account" /usr/bin/env SSH_ORIGINAL_COMMAND='update v01.2.3' \
    /usr/bin/sudo -n "$helper" ssh >/dev/null 2>&1; then
    return 1
  fi
}

for path in "$base/install-go-updater.sh" "$base/deploy/update.py" \
  "$base/tools/release_artifacts.py" "$ssh_config" "$sudo_config" \
  /etc/ssh/sshd_config /etc/ssh/psst-deploy/authorized_keys; do
  regular "$path" || fail 'Cutover input ownership or type is unsafe.'
done
for directory in "$state" "$state/transactions" /var/backups/psst.zip; do
  protected "$directory" && [[ -d $directory ]] &&
    (((8#$(stat -c %a -- "$directory") & 0077) == 0)) ||
    fail 'Existing private deployment state is required.'
done
if [[ -e $state/deployment.lock || -L $state/deployment.lock ]]; then
  regular "$state/deployment.lock" || fail 'Unsafe shared deployment lock.'
  (((8#$(stat -c %a -- "$state/deployment.lock") & 0077) == 0)) ||
    fail 'Unsafe shared deployment lock mode.'
fi
exec 9<>"$state/deployment.lock"
flock -n 9 || fail 'Another updater or installer owns the deployment lock.'

if [[ $action == prepare ]]; then
  regular "$base/psst-deploy.next" && [[ -x $base/psst-deploy.next ]] ||
    fail 'Stage the candidate at the fixed root-owned psst-deploy.next path.'
  [[ $(checksum "$base/psst-deploy.next") == "$expected" ]] ||
    fail 'Candidate checksum differs from the reviewed build.'
  [[ ! -e $stage && ! -L $stage ]] || fail 'A retained cutover already exists; inspect it locally.'
  check_tmp=$(mktemp -- "$base/.cutover-check.XXXXXX")
  trap 'rm -f -- "$check_tmp"' EXIT
  "$base/psst-deploy.next" cutover-check >"$check_tmp"
  mkdir -m 700 -- "$stage"
  install -m 755 -- "$base/psst-deploy.next" "$stage/psst-deploy"
  [[ $(checksum "$stage/psst-deploy") == "$expected" ]]
  mv -T -- "$check_tmp" "$stage/prepared-check.json"
  install -m 755 -- "$base/deploy/update.py" "$stage/legacy-update.py"
  install -m 644 -- "$base/tools/release_artifacts.py" "$stage/legacy-release_artifacts.py"
  install -m 600 -- "$ssh_config" "$stage/previous-ssh.conf"
  install -m 600 -- "$sudo_config" "$stage/previous-sudoers"
  if [[ -e $helper || -L $helper ]]; then
    regular "$helper" || fail 'Existing Go entrypoint is unsafe.'
    install -m 755 -- "$helper" "$stage/previous-go-helper"
  fi
  cat >"$stage/go-ssh.conf" <<'SSH'
Match User psst-deploy
    AuthenticationMethods publickey
    PubkeyAuthentication yes
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AuthorizedKeysFile /etc/ssh/psst-deploy/authorized_keys
    ForceCommand /usr/bin/sudo -n /usr/local/lib/psst.zip/psst-deploy ssh
    DisableForwarding yes
    PermitTTY no
    PermitTunnel no
    X11Forwarding no
Match all
SSH
  cat >"$stage/go-sudoers" <<'SUDO'
Defaults:psst-deploy env_keep += "SSH_ORIGINAL_COMMAND"
psst-deploy ALL=(root) NOPASSWD: /usr/local/lib/psst.zip/psst-deploy ssh
SUDO
  "$visudo" -cf "$stage/go-sudoers"
  ssh_restrictions "$stage/go-ssh.conf"
  printf '%s\n' "$expected" >"$stage/expected.sha256"
  (
    cd "$stage"
    sha256sum -- legacy-update.py legacy-release_artifacts.py previous-ssh.conf \
      previous-sudoers go-ssh.conf go-sudoers psst-deploy prepared-check.json expected.sha256 \
      >retained.sha256
    if [[ -f previous-go-helper ]]; then sha256sum previous-go-helper >>retained.sha256; fi
  )
  find "$stage" -maxdepth 1 -type f -exec sync -f -- {} +
  sync_file "$stage"
  printf '%s\n' 'Prepared root-owned candidate and retained legacy checksums. No active entrypoint changed.'
  exit 0
fi

protected "$stage" && [[ -d $stage ]] || fail 'Prepare the fixed candidate first.'
(((8#$(stat -c %a -- "$stage") & 0077) == 0)) || fail 'Retained cutover directory must be private.'
for path in "$stage"/*; do regular "$path" || fail 'Unsafe retained cutover input.'; done
[[ $(cat "$stage/expected.sha256") == "$expected" ]] || fail 'Prepared candidate checksum differs.'
(cd "$stage" && sha256sum -c --status retained.sha256) || fail 'Retained cutover bytes changed.'
[[ $(checksum "$stage/psst-deploy") == "$expected" ]]
cmp -s -- "$ssh_config" "$stage/previous-ssh.conf" || fail 'Installed SSH restriction changed after preparation.'
cmp -s -- "$sudo_config" "$stage/previous-sudoers" || fail 'Installed sudo restriction changed after preparation.'
cmp -s -- "$base/deploy/update.py" "$stage/legacy-update.py" || fail 'Retained Python helper changed.'
cmp -s -- "$base/tools/release_artifacts.py" "$stage/legacy-release_artifacts.py" || fail 'Retained Python parser changed.'
if [[ -f $stage/previous-go-helper ]]; then
  regular "$helper" && cmp -s -- "$helper" "$stage/previous-go-helper" || fail 'Previous Go helper changed after preparation.'
else
  [[ ! -e $helper && ! -L $helper ]] || fail 'An entrypoint appeared after preparation.'
fi
"$stage/psst-deploy" cutover-check >"$stage/activation-check.json"
"$visudo" -cf "$stage/go-sudoers"
ssh_restrictions "$stage/go-ssh.conf"

changed=0
reload_attempted=0
rollback() {
  local result=${1:-$?}
  trap - ERR HUP INT TERM
  if ((changed)); then
    atomic_copy "$stage/previous-sudoers" "$sudo_config" 440 || result=1
    atomic_copy "$stage/previous-ssh.conf" "$ssh_config" 644 || result=1
    if [[ -f $stage/previous-go-helper ]]; then
      atomic_copy "$stage/previous-go-helper" "$helper" 755 || result=1
    elif [[ -f $helper && $(checksum "$helper") == "$expected" ]]; then
      rm -- "$helper" || result=1
      sync_file "$base" || result=1
    fi
    if ((reload_attempted)); then reload_sshd || result=1; fi
    printf '%s\n' 'Cutover failed; previous restrictions restored where possible. Inspect retained root-only records.' >&2
  fi
  exit "$result"
}
trap rollback ERR
trap 'rollback 1' HUP INT TERM
changed=1
# There are separate atomic file replacements, not one multi-file transaction.
# The shared updater lock covers the temporary deny/reload boundary throughout.
atomic_copy "$stage/psst-deploy" "$helper" 755
atomic_copy "$stage/go-sudoers" "$sudo_config" 440
atomic_copy "$stage/go-ssh.conf" "$ssh_config" 644
"$visudo" -c
ssh_restrictions /etc/ssh/sshd_config
sudo_restrictions
"$helper" cutover-check >"$stage/installed-check.json"
reload_attempted=1
reload_sshd
"$helper" status >"$stage/installed-status.json"
sync_file "$stage/installed-check.json"
sync_file "$stage/installed-status.json"
sync_file "$stage"
trap - ERR HUP INT TERM
printf '%s\n' 'Go entrypoint installed; SSH reload signalled. Verify status through the actual restricted account before closing cutover.'

# Verified container updates and controlled recovery

The shared updater is `deploy/update.py`. SSH and GitHub Actions select a strict
`vMAJOR.MINOR.PATCH` release through the same installed helper. They do not build
on the VPS, pass image URLs, execute downloaded scripts or choose arbitrary paths.
A commit on `main` does not update production.

The helper is implemented with disposable transaction and input-boundary tests.
These tests replace Docker and GitHub operations; they do **not** establish a live
upgrade, public provenance verification, an actual encrypted off-host restore or
ACME renewal. The first production adoption and complete disposable Docker
upgrade/recovery exercise remain separate release gates in the
[deployment plan](../plans/container-releases-and-production-deployment.md).

## Trusted installation and initial adoption

Install the entry point and its parser into a root-owned tree, separate from the
operator checkout. Install only code from an authenticated reviewed release or
reviewed administrator-controlled checkout. For future helper updates, first
verify the detached manifest, bundle and selected source against the existing
verification policy; then have the local administrator install the authenticated
helper/parser pair. An application update never silently replaces running
privileged tooling with bundle code.

For example, after verifying the source tree locally, run as root:

```sh
install -d -m 0755 /usr/local/lib/psst.zip/deploy /usr/local/lib/psst.zip/tools
install -m 0755 deploy/update.py /usr/local/lib/psst.zip/deploy/update.py
install -m 0644 tools/release_artifacts.py /usr/local/lib/psst.zip/tools/release_artifacts.py
install -d -m 0700 /etc/psst.zip /var/lib/psst.zip-deploy /var/backups/psst.zip
```

Use `/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py` as the installed
command. Python's isolated mode avoids importing user-supplied modules through
`PYTHONPATH` or the working directory. The helper also refuses privileged imports
or configuration through symlinks, non-root-owned ancestors or directories writable
by other identities. All privileged updates require root; the deployment identity
must receive only the specific forced helper command, not general sudo or Docker
group access. Interactive maintenance/recovery keeps its separate SSH identity.

Before adoption, record the running installation's actual project, physical named
volumes, database path, public ports, network addresses, proxy trust, limits and
operator overrides. Keep `/opt/psst.zip` and its original images/configuration as
the first baseline. Set `BACKEND_DATA_VOLUME` and `DB_PATH` explicitly in its
protected `.env` when retaining legacy storage. Remove **both** administrator
bootstrap variables from the existing runtime configuration before adoption;
updates require the initialized account and never bootstrap another administrator.

This updater supports the supplied two-service deployment and its separate Caddy
TLS gateway, using existing local named volumes. Bind-mounted backend data,
remote volume drivers, symlinked payload trees, additional services, service user
overrides and ownership changes require a reviewed adaptation; preflight rejects them. The
[cold backup/restore runbook](backup-restore.md) remains authoritative for such
installations. A fresh installation should use the
[container guide](container-releases.md) first; this updater refuses an empty or
uninitialized store.

Create `/etc/psst.zip/deployment.json` as root, mode `0600`. The following is an
example schema, **not** the VPS's discovered mapping:

```json
{
  "repository": "endorses/psst.zip",
  "signer_workflow": "endorses/psst.zip/.github/workflows/release.yml",
  "installation": "/opt/psst.zip",
  "project": "psst-zip",
  "environment": ".env",
  "compose_files": ["docker-compose.yml"],
  "operator_files": ["Caddyfile"],
  "release_overrides": [],
  "external_proxy": false,
  "domain": "transfer.example.com",
  "db_path": "/app/data/existing.db",
  "volume_names": {
    "backend:/app/data": "actual-backend-volume",
    "caddy:/data": "actual-caddy-data",
    "caddy:/config": "actual-caddy-config"
  },
  "disk_reserve_bytes": 1073741824,
  "image_reserve_bytes": 1073741824,
  "verification_hook": null,
  "checkpoint_hook": "/usr/local/libexec/psst-checkpoint-export",
  "github_token_file": null,
  "retention_count": 3
}
```

The installation and its parents must be root-owned without group/other write
permission. The `.env` is private mode `0600`; public Compose/Caddy configuration
may remain readable by its service account but must be root-owned and protected
against edits by other identities. Include **every** active protected bind file in
`operator_files`, and include reviewed release override files in
`release_overrides`. Each override must retain image pairing, private origins,
HTTPS, forwarding policy, storage, limits and read-only mounts.

An empty `release_overrides` list deliberately selects the new bundled Caddyfile
and removes a source-era host Caddyfile mount. Preserve that old file in
`operator_files` for recovery. To retain a custom Caddyfile, provide a protected
release override that mounts it explicitly read-only. Preflight validates its
configuration against the authenticated selected web image using disposable
`/data` and `/config` tmpfs, never the active certificate volumes. Syntax validation
does not prove an operator-specific forwarding/access policy; review it first.

For the supplied external gateway, set `external_proxy: true`, include the current
source overlay in `compose_files` and its active bind files in `operator_files`,
and add distinct `external-proxy:/data` and `external-proxy:/config` entries to
`volume_names`. Both new proxy hops select the same authenticated web digest.

Install current Docker, Compose, Buildx, GitHub CLI, curl, tar and Python 3 on the
host. The helper enforces the manifest's Docker/Compose minimums and supports Linux
AMD64/ARM64. GitHub CLI must support the complete
[attestation verification policy](https://cli.github.com/manual/gh_attestation_verify).
Use its normal protected root-owned CLI configuration, or place a narrowly scoped
GitHub read token in the protected `github_token_file`; never put a token in a
command argument, repository file or Actions deployment input. Public GHCR image
pulls need no publishing credential. If anonymous attestation access is unavailable,
configure only the required read access; it does not grant package write permission.

## Update transaction and provenance

Run the installed helper as root, selecting a published ready version:

```sh
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py update v1.2.3
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py status
```

The restricted SSH entry point is the same command with `ssh`. It accepts exactly
`update vMAJOR.MINOR.PATCH` or `status` through `SSH_ORIGINAL_COMMAND`; it rejects
extra arguments, shell operators, paths, prerelease versions and recovery commands.
Configure the forced-command/sudo wrapper so only these original commands reach
the root-owned helper. Actions cannot supply an activation approval or verification
flag. Local `verify`, `restore`, `recover-safe` and `retention` commands require
separate administrator access.

A nonblocking host lock serializes mutation. Atomic mode-`0600` records are synced
along with their parent directory under `/var/lib/psst.zip-deploy`. The transaction
stores selected/previous versions, exact image IDs/digests, resolved configuration,
physical storage, checkpoint location, prior pause state, verification evidence
and the migration boundary. Status emits only non-secret identity/version/phase
fields. Interrupted or unresolved records prevent another update.

Before any release code is executed, the installed parser bounds both download
size and archive expansion, rejects ambiguous JSON and unsafe archive entries,
and constrains the trusted repository, version, source commit, registry names,
image indexes, architecture children and bundle checksum. The updater accepts
only `deployment-ready`, non-draft, non-prerelease assets from the fixed repository.

Checksums establish consistency. Cryptographic verification uses
`gh attestation verify` for the detached manifest, deployment bundle, both image
indexes and their four platform-specific children. Every subject must match the
configured repository, exact release workflow, source digest, version-tag source
ref and GitHub-hosted runner identity. The updater then checks the actual index's
AMD64/ARM64 child descriptors against the authenticated manifest. Attestation or
registry failure stops preparation; no bundle updater is imported or executed.

Preflight resolves and checks the running containers against adopted physical
volume names, labels, database location, actual environment and published ports.
It rejects a second possible container writer, missing storage, dropped settings,
resource/logging changes, insecure public authentication, unexpected/writable
binds and incompatible users. Both selected digests are pulled and their platform,
non-root user and release/source/license labels checked **before** stopping the
original. Capacity/mapping checks repeat after pulls; headroom covers retained
images, the full checkpoint, an isolated restore and a reserved free margin.
An image user-ID change needs a separate reviewed ownership migration.

The helper records and persists the prior pause, requests pause with the matching
old binary, then stops every service and checks for surviving volume writers and
occupied ports. Pause alone does not stop cleanup, title changes, deletes,
administrative edits or authentication writes.

## Complete stopped checkpoint and off-host protection

Every checkpoint is a new mode-`0700` directory in `/var/backups/psst.zip`, outside
web/storage paths. It contains read-only snapshots of the entire backend volume
(database, existing sidecars and ciphertext), both Caddy state volumes, gateway
volumes when present, exact image archives and protected configuration. It retains
old image tags too. UID/GID and file permissions are preserved; unreadable files,
unsupported archive entries, truncated content, failed checksums or SQLite
integrity/foreign-key failures abort before candidate startup. No journal is
deleted to force validation.

Local archives contain plaintext credentials/certificates. Configure and exercise
the root-owned `checkpoint_hook` **before** an update. A missing hook fails
preflight. The hook runs with only `--checkpoint` and the protected path, must
complete the site's encrypted authenticated off-host export, verify its stored
objects/receipt, and provide evidence of the protected restore exercise. It may
use the site's backup provider and keys; the updater does not select a provider
or write an unencrypted remote copy.

The hook exits nonzero on failure. Its stdout must be a JSON object with exactly:

```json
{
  "checkpoint_sha256": "sha256 of checkpoint.json",
  "encrypted_off_host_receipt": "non-secret description of checked ciphertext objects/receipt",
  "restore_exercise": "non-secret description of the verified protected restore exercise",
  "verified_at": "current timezone-aware ISO timestamp"
}
```

The checkpoint digest must match the current stopped set; timestamps older than
15 minutes and boolean/empty success assertions are rejected. The hook and its
ancestors must be root-owned and unwritable by other identities. Output and
credentials are withheld from deployment logs; the validated receipt is retained
privately. A trusted hook's claims are only as good as its actual encryption,
remote verification and restore implementation. This integration does **not**
claim that those operator systems have been configured or tested on the VPS.

Use the root-local `retention` command after a completed update. It removes only
older verified checkpoints from successful transactions beyond the configured
count (minimum two). It keeps the current and most recent known-good checkpoints;
failed/partial checkpoints and every original/restored volume require explicit
administrator review and are never pruned. It never runs `down -v` or Docker
volume pruning. Off-host retention must independently retain a usable known-good
checkpoint and recovery keys.

## Isolated candidate and verification gates

Only after the complete checkpoint and off-host receipt pass does the helper
persist `mutation_started: true` and `migration-starting`. This happens **before**
the first ordinary candidate backend launch because startup automatically runs
schema migrations and recovery workers. A launch failure is therefore treated as
potentially migrated data, even when Docker does not report a running backend.

The paired new configuration preserves the project, volume identities, database,
operator environment, policies and accounts. Runtime bootstrap values are blank.
All candidate listeners, including an external gateway, bind exclusively to
`127.0.0.1`; the backend publishes no port. Automatic restart is disabled while
isolated. Certificates/configuration come from the adopted Caddy state, so HTTPS
checks require a valid trusted certificate for the actual hostname; curl never
uses `-k` or turns off certificate verification.

Automatic checks validate trusted HTTPS, API health, initialized-account status,
persisted public policy/limits, compiled HTML/JavaScript, selected release/source
metadata and transfer pause. These establish their named coverage; they do not
prove authentication, ciphertext decryption, private settings or complete upload,
receive, budget/revocation and recovery flows.

With no `verification_hook`, the helper records `awaiting-verification`, keeps
loopback-only routing and returns **20**. Actions/SSH must report this as pending
operator verification, not deployment success. Inspect status after an interrupted
connection instead of starting another update.

For an interactive local gate, use:

```sh
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py verify
```

Connect a browser privately with a tunnel and retained client keys. Preserve the
configured HTTPS origin: forwarding server port 443 to a different local port
changes the browser's Origin and may fail same-origin mutation checks. A local
443 tunnel plus a temporary local hostname override preserves the production
origin; ensure the domain resolves to the private tunnel on that machine during
the exercise. Keep external routing closed throughout.

The gate asks privately for the candidate administrator Cookie header, then
actually checks `/auth/me` and the admin storage/counter/orphan endpoints over
verified loopback HTTPS. It requires `checked`, `scan_pending: false`, completed
scan timestamps, no scan error and no unresolved busy, missing, failed, unsupported,
pending or saturated state. The cookie remains in memory, appears in neither
process arguments nor transaction output, and is discarded after repeated checks.

Using the isolated candidate, observe existing account/session/factor behavior,
persisted private settings/quotas, an existing encrypted download with retained
client-key authentication/decryption, new upload/receive, expiration/budget and
revocation behavior, cleanup and restart. Temporarily resume only the isolated
instance when testing transfers; pause again afterward. Record concise descriptions
of the actual observations without tokens, credentials, complete links or keys.
The helper separately reruns its automatic/authenticated checks after the browser
exercise. Browser observations are administrator attestations, not automated
assertions, and are retained privately with the transaction/version/source identity.

For fully automated updates, configure a protected `verification_hook` that
performs those same real authenticated flows against the isolated candidate with
the site's protected credentials and retained fixture keys. It receives only
`--transaction PATH --compose PATH`, exits nonzero on any failure, restores pause
and outputs:

```json
{
  "version": "v1.2.3",
  "source_commit": "full selected commit",
  "observed_at": "current timezone-aware ISO timestamp",
  "checks": {
    "existing_account_and_session": "description of tested existing authenticated state",
    "administrator_second_factor": "description of real factor/recovery verification",
    "persisted_settings_and_quotas": "description of compared policies and private settings",
    "existing_encrypted_download": "description of verified retained ciphertext",
    "new_upload_and_receive": "description of completed fixture upload and receive",
    "expiry_budget_and_revocation": "description of checked exhausted and revoked capabilities",
    "cleanup_and_restart": "description of cleanup and post-restart verification",
    "client_authenticated_decryption": "description of exact authenticated decrypted bytes"
  }
}
```

Every named check is required, reports are bound to the candidate identity and
expire after ten minutes, and truthy booleans do not constitute evidence. The
hook is trusted local verification code, not an externally supplied approval
flag; implement and exercise it against disposable data before enabling it on
production. Its existence alone is not proof that the site's flows passed.

Once verified, the helper changes proxy listeners as a pair, checks HTTPS again,
restores only the **prior** pause state and records completion. The backend remains
on the selected image. Future updates read the protected active configuration;
restored volume identities remain explicit. Any activation error stops services,
keeps original/failed state and records `failed-closed`.

## Failure handling and explicit isolated rollback

| Durable state                                    | Meaning and allowed action                                                                                                                                            |
| ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `preparing` with `mutation_started: false`       | Original has not been stopped/migrated; local `recover-safe` abandons interrupted preparation.                                                                        |
| Pause/stop/backup with `mutation_started: false` | Automatic error handling restores the matching original configuration/images and pause, then checks its health/settings. Local `recover-safe` handles a lost process. |
| `migration-starting` or any later mutation       | Old binaries must **never** restart against original storage. Keep traffic closed and preserve the failed store/checkpoint.                                           |
| `awaiting-verification`                          | Candidate is isolated/paused; complete the real automatic hook or local administrator gate.                                                                           |
| `failed-closed`                                  | Inspect the protected record, backup and containers. A migration flag means only isolated checkpoint recovery is safe.                                                |
| `restored-awaiting-verification`                 | Old matching images run against **new** restored volumes, original/backup unchanged; security reconciliation is still required.                                       |
| `completed`                                      | Successful gate/cutover and restored prior pause. Actual deployed version is also recorded as `active_version`.                                                       |

If a process is lost before mutation, inspect status and use local `recover-safe`.
The helper rejects that command after any candidate startup boundary. Never erase
an ambiguous record merely to make another update run. Before-mutation recovery
that cannot restore a healthy original is itself fail-closed and needs inspection.

For explicit rollback/recovery after mutation, run local `restore`:

```sh
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py restore
```

This verifies the selected checkpoint/receipt, stops the candidate, loads the
matching retained images and restores every state archive into newly named empty
physical volumes. Configuration binds use the protected backed-up copies. It
validates SQLite before running the matching old binary's pause command, then
launches the restored pair isolated with no bootstrap or public listener. It does
not overlay original data, discard sidecars, run two writers on a store or delete
the backup. A failed/partial restore is preserved; retry cannot overwrite it.

Complete the [restore runbook's security reconciliation](backup-restore.md#validate-and-reconcile-security-state)
while isolated. Post-checkpoint changes may be lost, and sessions, factors,
recovery codes, pairing grants, spent allowances or revoked links may reappear.
Use independent records to reconcile traffic and security; when unavailable,
invalidate affected authorities and keep routing/transfers closed. Do not adjust
time/expiry or clear counters to make tests pass.

The restored flow gate additionally requires observations named
`post_checkpoint_changes_reviewed`,
`restored_sessions_links_and_factors_reconciled` and
`independent_traffic_allowances_reconciled`. Only after all automatic/authenticated
and these operator/hook checks pass can its restored configuration be activated.
The requested candidate version and actually restored active version remain
separate in protected state. Original and failed candidate volumes survive.

## Repository verification and remaining operator evidence

Run the fast disposable transaction/input suite from the repository:

```sh
PYTHONPATH=tools python3 -m unittest tools.test_release_updater -v
```

The fast updater suite includes 33 disposable tests. The suite covers authenticated
input rejection, actual mapping/port/account/config
and capacity drift, stopped-backup corruption, root/path and restricted-SSH
boundaries, lock contention, durable interruption, before/after-startup faults,
absence of old-binary automatic rollback, honest pending verification, successful
protected-hook activation, private authentication/reconciliation gates, isolated
restore and required security reconciliation. SQLite tests use only their own
disposable databases and verify read-only integrity checking.

The disposable root/Docker integration gate uses a nested Docker daemon without
a host Docker socket, host root bind, or outer published port. It builds committed
application source and loads immutable local image IDs; only the remote release
acquisition boundary is replaced. Production permission, adoption, ownership,
Compose, TLS, stopped-backup, SQLite, candidate and isolated-restore checks remain
active. The fixture uses an explicit private CA without altering global trust.

```sh
python3 tools/test_release_updater_integration.py --source c1c1ea9
python3 tools/test_release_updater_integration.py --source c1c1ea9 --failure-after-start
```

The candidate/restore variant passed locally in 52.6 seconds and the post-startup
fault variant in 50.1 seconds, including real authenticated storage/counter/orphan
reconciliation probes. They
exercise actual initialized admin/member accounts and
sessions, TOTP enrollment and required-factor login, persisted file limits, a
member-owned TUS encrypted transfer and manifest, independent AES-GCM client
framing, complete stopped state/configuration/image archives, encrypted export
and authenticated decrypt of a fixture checkpoint, corrupt-checkpoint refusal,
private candidate startup, and restore onto new volumes. Restore preserves
original data and checkpoint hashes, matching account sessions/factor/settings,
ciphertext, incident pause, and Caddy's certificate authority. Fault injection
occurs only after the real candidate startup checks pass and verifies that all
services stop, the durable mutation marker remains, and the old binary is never
automatically restarted on the original data.

These live exercises found two defects that the earlier mocked adapter did not:
Caddy's legitimate sticky state-directory modes were rejected, and UTC timestamp
uppercase letters made retained-image repository names invalid. The updater now
preserves sticky/setgid directory modes while rejecting privileged file/setuid
bits; checkpoint image repository names are lowercase while transaction IDs stay
unchanged. A focused archive regression covers the permission distinction. A further saved
configuration regression ensures later updates continue to accept declared,
protected installation Caddy overrides while rejecting undeclared binds.

The fixture intentionally ends at `awaiting-verification` or
`restored-awaiting-verification`. It supplies no invented flow report or activation
approval. Its separate encrypted export directory simulates an off-host provider;
it does not establish a real independent off-host recovery service. Public
attestations/registry indexes, both architectures, anonymous pulls, public ACME,
full authenticated upload/receive/expiry/budget/revocation/cleanup browser flows,
security reconciliation approval and public activation, operator-configured
external backup restore, installed restricted SSH, and the current VPS migration
remain separate acceptance gates. Both sides use the same committed application
source, so the fixture exercises the startup mutation boundary without proving a
historical schema-changing migration. Passing the fixture does not close those
gates. Use
`tools/test_external_proxy.py --certificate-state` for additional gateway controls
alongside the full deployment-plan acceptance checks.

# Verified container updates and controlled recovery

The shared updater is `deploy/update.py`. SSH and GitHub Actions select a strict
`vMAJOR.MINOR.PATCH` release through the same installed helper. They do not build
on the VPS, pass image URLs, execute downloaded scripts or choose arbitrary paths.
A commit on `main` does not update production.

The helper has fast transaction/input tests and a real disposable Docker
upgrade, activation and isolated-recovery exercise. The Docker gate uses actual
application images and authenticated encrypted transfer flows, including a real
historical schema migration. It replaces remote release acquisition and simulates
an encrypted external backup store. Public provenance, an independent off-host
provider, public ACME and first production adoption remain separate gates in the
[deployment plan](../plans/container-releases-and-production-deployment.md).

## Trusted installation and initial adoption

Install the entry point and its parser into a root-owned tree, separate from the
operator checkout. Install only code from an authenticated reviewed release or
reviewed administrator-controlled checkout. For future helper updates, first
verify the detached manifest, bundle and selected source against the existing
verification policy; then have the local administrator review and install the
authenticated helper/parser pair. A changed certificate/commit verification policy
requires that separate trusted installation step; updating this repository does
not upgrade an already installed helper. An application update never silently
replaces running privileged tooling with bundle code.

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
  "checkpoint_protection": "off-host",
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
pulls need no publishing credential. The helper fixes GitHub CLI's `GH_HOST` to
`github.com` so public release and attestation reads can run anonymously without
an interactive login. It never imports credentials from the invoking environment.
If anonymous attestation access is unavailable,
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
configured repository on explicit `github.com`, the exact certificate SAN
`https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/tags/VERSION`,
both signer and source commit digests, the version-tag source ref, GitHub OIDC
issuer, the SLSA provenance-v1 predicate and GitHub-hosted runner identity. The
helper uses `--cert-identity` rather than combining it with the CLI's mutually
exclusive `--signer-workflow` selector. Workflow configuration is restricted to
that repository's reviewed `.github/workflows/release.yml`; version and commit
bindings are validated before calling the CLI. The updater then checks the actual index's
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

Local archives contain plaintext credentials/certificates. The default
`checkpoint_protection` is `off-host`, including configurations that omit the
field. In this mode, configure and exercise the root-owned `checkpoint_hook`
**before** an update. A missing hook fails preflight. The hook runs with only
`--checkpoint` and the protected path, must
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

After verified activation is durably completed, the updater automatically applies
bounded retention. The root-local `retention` command repeats that cleanup when
needed. The configured count retains at least two successful recovery checkpoints,
as well as the current transaction. Older successful checkpoints are verified
before removal. Failed, incomplete and restored transactions remain protected and
require administrator review.

Cleanup removes obsolete checkpoint image tags and retired images only when they
are explicitly tracked by those deployment transactions. It protects the active
release, retained recovery image pairs, unresolved recovery state, images used by
any running or stopped container, and unrelated image references. Image removal
is targeted and non-forced. It never runs host-wide Docker image/system pruning,
`down -v` or volume pruning; original and restored volumes remain untouched.

Cleanup failure after activation leaves the healthy active deployment completed
and records incomplete cleanup for a later `retention` retry. Before and after
pulling a candidate, preflight still requires space for its images, a complete
stopped checkpoint, an isolated restore and the configured reserve. Automatic
cleanup bounds successful release history; operator review is still required if
protected failed checkpoints or application data exhaust the 40 GB disk. Off-host
retention must independently retain a usable known-good checkpoint and recovery keys.

An operator can explicitly defer off-host backups in the protected configuration:

```json
{
  "checkpoint_protection": "local-only",
  "checkpoint_hook": null
}
```

These two fields replace their entries in the full configuration above; this
fragment is not a complete configuration. Local-only mode still requires the
complete stopped checkpoint, archive/checksum validation and SQLite integrity
checks before candidate startup. It records the actual protection mode in the
checkpoint and transaction/status and does not create an off-host receipt. This
allows recovery from a failed update while the VPS disk remains available; it
does not protect against losing the VPS or its disk. Restore and retention use
the checkpoint's recorded protection mode. Legacy checkpoints retain the off-host
requirement, and enabling off-host protection later does not upgrade old local
checkpoints. The restricted SSH request cannot change this administrator setting.

## Isolated candidate and verification gates

Only after the complete checkpoint and the configured protection checks pass does the helper
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
metadata and transfer pause. The documented `history_sync_version` capability is
validated separately from persisted operator settings, so a supported protocol
capability upgrade cannot conceal a file-limit or traffic-policy change. All other
public configuration fields remain subject to exact comparison. These checks establish their named coverage; they do not
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

When independent usage records require conservative traffic reconciliation, the
helper permits the authenticated restore gate to decrease only positive server
and default-account byte budgets. It independently reads the paused candidate
configuration before accepting the report. Enforcement, accounting basis, rates,
stream limits and every unrelated setting must match the checkpoint; increased
budgets, disabled enforcement and cleared counters are not accepted. The original
checkpoint policy remains in the protected transaction alongside the reconciled
baseline used for repeated activation checks.

## Repository verification and remaining operator evidence

Run the fast disposable transaction/input suite from the repository:

```sh
PYTHONPATH=tools python3 -m unittest tools.test_release_updater -v
```

The fast updater suite includes 37 disposable tests. It covers authenticated
input rejection, actual mapping/port/account/config and capacity drift,
stopped-backup corruption, root/path and restricted-SSH boundaries, lock
contention, durable interruption, before/after-startup faults, absence of old-binary
automatic rollback, honest pending verification, protected-hook activation,
private authentication/reconciliation gates, isolated restore, conservative
traffic reconciliation and protocol-capability versus operator-policy changes.
SQLite tests use only their own disposable databases and read-only integrity checks.
The identity-policy regression covers manifest, bundle and image subjects and
rejects malformed source/ref bindings and weak workflow selectors before invoking
the CLI. An actual GitHub CLI 2.101.0 parser check accepts the generated identity policy
for file and OCI subjects, then fails on deliberately missing test-local trust
material before network verification. Those trust overrides exist only in tests.
This check validates argument compatibility, not a successful attestation; the
earlier historical Docker exercises replaced acquisition and do not establish live
verification under the strengthened policy. Genuine matching release attestations
still need their separate acceptance gate.

The root/Docker integration gate uses a nested Docker daemon without a host Docker
socket, host root bind or outer published port. It builds committed application
source and loads immutable local image IDs; remote release acquisition is replaced
explicitly. Production permission, adoption, ownership, Compose, TLS,
stopped-backup, SQLite, candidate, hook invocation, activation and isolated-restore
checks remain active. An explicit private CA provides verified fixture HTTPS without
altering global trust. These commands require privileged nested-Docker support:

```sh
python3 tools/test_release_updater_integration.py --source HEAD --previous-source 4210414
python3 tools/test_release_updater_integration.py --source HEAD --previous-source 2ed02af --require-schema-change
python3 tools/test_release_updater_integration.py --source HEAD --previous-source 2ed02af --require-schema-change --failure-after-start
```

The public-source `4210414` upgrade, repeat update and activated restore passed
locally in 137.7 seconds. Its application schema is unchanged. The separate real
historical `2ed02af` source exercise passed in 153.1 seconds and observed one actual
migration through normal candidate startup; the historical exact-schema incident
CLI refuses the migrated original database. Sources are resolved to immutable
commits at test startup. Fixture tags are local `v1.2.2`, `v1.2.3` and `v1.2.4`, not
published releases.

The protected independent Python client hook makes actual API requests and
records assertions, response statuses and ciphertext/plaintext fingerprints. It
verifies initialized administrator/member accounts and retained sessions, required
TOTP login and fresh reauthentication, credential/recovery-code fingerprints,
persisted limits and enforced quotas, incident pause, existing authenticated
AES-GCM download, new TUS upload and protocol-v2 HPKE receive, recipient/context
binding and rejection of altered ciphertext. It tests real expiry, exhausted
download and traffic allowances, revocation, oversized uploads, payload cleanup,
backend restart and authenticated storage/counter/orphan reconciliation. All
transfers run with candidate ingress restricted to loopback. The hook re-pauses
before returning transaction-bound evidence, and only then does the actual updater
activate public fixture listeners and restore the original pause choice.

The gate verifies complete stopped volume/configuration/image checkpoints, real
AES-GCM export and complete authenticated decrypt, corrupt-checkpoint refusal,
saved protected Caddy override adoption on a second update, and restored matching
images on new physical volumes. Independent records outside the checkpoint retain
accepted TOTP counters and spent traffic allowance. Restore advances factor proof,
checks unchanged session/factor/recovery credentials, reviews absent post-checkpoint
links, revokes restored download authority and conservatively reduces budgets to
avoid allowance resurrection. It preserves original payload and checkpoint hashes,
account sessions, operator settings and Caddy CA state while activating the isolated
restored pair only after the complete flow/security gate.

The injected post-startup fault runs after real candidate checks. It requires all
services stopped and the durable mutation marker retained. The historical variant
restores the old schema only into new volumes; the migrated original and checkpoint
remain intact, and no old normal startup is attempted on the migrated original.
The final historical fault-and-restore variant passed locally in 79.6 seconds.

These exercises found defects missed by the mocked adapter: legitimate sticky
Caddy state-directory modes, invalid uppercase retained-image repository names,
saved protected installation bind adoption, capability-versus-policy comparison,
and conservative restore-budget reconciliation. Focused regressions protect those
boundaries; permissive file modes, undeclared binds, unrelated setting changes and
budget increases remain rejected.

The encrypted export directory is an isolated external-store simulation in the
fixture container, not a real independent off-host recovery provider. The client is
an independent Python protocol implementation, not a browser/mobile execution.
Public attestations/registry indexes, both architectures, anonymous pulls, public
ACME, operator-configured external backup restore, installed restricted SSH and
the current VPS migration remain separate acceptance gates. Passing this fixture
does not establish them. Use `tools/test_external_proxy.py --certificate-state`
for additional gateway controls alongside deployment-plan acceptance checks.

## Post-assembly native recovery measurements

`tools/measure_release_recovery.py` measures the actual prepared candidate after
both native architectures and release-input assembly are available. Supply both
retained native descriptors, the assembled manifest and deployment bundle, and
the genuine historical source commit. Only the selected native platform runs the
isolated Docker experiment; the other platform's input bytes are validated without
manufacturing an execution result.

```sh
python3 tools/measure_release_recovery.py \
  --root . --repository endorses/psst.zip \
  --version "$PSST_RELEASE_VERSION" --commit "$PSST_SOURCE_COMMIT" \
  --platform linux/amd64 \
  --native "linux/amd64=$AMD64_OUTPUT/native-artifacts.json" \
  --native "linux/arm64=$ARM64_OUTPUT/native-artifacts.json" \
  --manifest "$PREPARED_OUTPUT/release-manifest.json" \
  --bundle "$PREPARED_BUNDLE" \
  --previous-source 2ed02af8a062bb3cc21a80a1503d31775dd6e5d8 \
  --output "$NEW_RECOVERY_MEASUREMENT"
```

The producer validates four actual OCI graphs, tested configurations, exact index
children, build records, saved-layer hashes, source-pack/replay bindings and the
entire source-owned bundle against the candidate Git tree before privileged
execution. It also pins every fixture helper to that exact commit. A rehashed
substituted updater, parser, deployment configuration or notice is rejected.

The experiments cover ordinary upgrade/reapply/isolated restore, the same sequence
with the original incident pause enabled, and an injected post-migration startup
failure followed by isolated restore. Reapplication uses the exact same version,
manifest and candidate bytes, rather than pretending to build a second release.
Structured durable transaction, authenticated flow, schema, checkpoint, storage
and certificate observations drive the measurement; printed PASS messages are not
evidence. Scoped Docker removal and absence checks must complete successfully
before a result can return. Cleanup failure prevents a terminal measurement.

The historical AMD64 compatibility check of the shared harness ran on candidate
`c161836caf66631faa1eecdc901029464aa25e93` and the baseline above in 130.8 seconds.
It preserved the original pause at all three public activations, observed schema
47 advancing to 48, rejected the historical CLI against the migrated original,
and completed an isolated restore with schema 48. Authenticated receipts contained
8/8/11 checks, and the stopped checkpoint retained two images, three volumes and
five protected configuration records. This legacy harness check uses subsequent
local fixture versions and does not replace the new producer's same-version,
final-overlaid, two-architecture exercise.

The producer retains explicit `public_provenance_verified: false`,
`off_host_provider_verified: false`, `browser_mobile_flows_verified: false` and
`upgrade_recovery_gate_pending: true`. Local registry acquisition and encrypted
external-store simulation do not establish public attestations or independent
remote recovery. Eleven producer/cleanup boundary tests and thirty-seven updater
tests passed after the source-pinning and cleanup fixes. Genuine prepared inputs
for both architectures, complete execution, authentication and workflow gate
aggregation remain pending.

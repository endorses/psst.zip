# Backing up and restoring an instance

Use a stopped, complete backup of the database and encrypted payload tree. Restore
into a separate working copy, pause that copy before starting the server, and keep
it isolated until its data and security state have been checked. Retain the original
backup unchanged: starting the server runs recovery and cleanup even while public
transfers are paused.

This procedure supports one backend process per database/payload store. It does
not provide online snapshots, a backup scheduler, automatic historical accounting
reconstruction or disaster recovery for client-held encryption keys.

## What belongs together

| Backup component         | Contents and purpose                                                                                                                                                                         |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backend data             | The database, any SQLite WAL/journal/SHM sidecars, and the complete configured payload directory from the same stopped point. The bundled deployment keeps these in its backend data volume. |
| Proxy state              | Both Caddy data and configuration volumes, including certificate/account material.                                                                                                           |
| Protected configuration  | Compose files and overrides, Caddyfile, environment/secret files, actual volume mappings, hostname/proxy trust settings and resource limits.                                                 |
| Application version      | Exact backend/web image IDs or immutable digests, plus the source revision and locally modified deployment files. Retain access to those exact artifacts.                                    |
| Separate client recovery | Original devices/browser profiles holding receive private keys and send-link keys. These are not part of a server backup; account login or pairing cannot reconstruct them.                  |

Database and proxy backups contain credentials. Use restricted storage and encrypted,
authenticated backup protection, including off-host copies and their keys. Protect
backup manifests too; do not paste environment output, database contents, complete
share links or credentials into issue reports. Checksums detect accidental damage;
a checksum stored beside an altered archive does not prove authenticity.

SQLite requires a consistent database/sidecar set. Copying the main file during
writes, discarding a hot journal or mixing journals from another snapshot can
invalidate recovery. See [SQLite's backup cautions](https://www.sqlite.org/howtocorrupt.html#_backup_or_restore_while_a_transaction_is_active).
A supported [SQLite online backup](https://www.sqlite.org/backup.html) only snapshots
the database; this application also needs a coordinated payload snapshot. The cold
procedure below avoids that additional coordination requirement.

## Create a cold backup

Use the same Compose project, files and environment arguments as the installation.
Do not guess a volume name from another project's defaults. The example assumes
the supplied named-volume deployment; adapt explicit paths for bind mounts or
separately mounted payload/database storage.

- [ ] Arrange downtime and stop incoming traffic. Stop the backend and proxy;
      confirm neither is restarting and no second process or maintenance tool
      writes to the same storage. A transfer pause alone is insufficient: cleanup
      and reconciliation still modify data.
- [ ] Record the application version, volume mappings and backup time. Create a
      new private backup directory outside the managed payload tree and outside
      any web root. Never overwrite the last known good backup.
- [ ] Copy all components from this stopped point, preserving owner IDs and file
      permissions. Include sidecars that exist; do not delete them to simplify
      copying. If a copy fails, leave that backup incomplete and retry from a new
      stopped snapshot rather than mixing successful parts from different times.
- [ ] Check archive readability, checksums and protected off-host storage before
      resuming the original instance. Restart only the original project/volumes.
      Verify health, authentication and the intended transfer-pause state.

For a standalone service whose entire backend data is under one directory, after
stopping its supervisor and confirming that the process has exited:

```sh
(
  set -eu
  umask 077
  psst_source=/actual/stopped/backend-data
  psst_backup=/actual/private-backups/new-snapshot
  mkdir -m 700 "$psst_backup"
  tar -C "$psst_source" -cpf "$psst_backup/backend.tar" .
  tar -tf "$psst_backup/backend.tar" >/dev/null
  (cd "$psst_backup" && sha256sum backend.tar > SHA256SUMS)
)
```

Run as an account able to read every source file; do not loosen database
permissions to make a backup succeed. Capture protected configuration and proxy
state separately in this same snapshot before restarting. If the database and
payload tree have different parents, archive both explicitly while still stopped.
The example archive is plaintext until protected by your backup encryption system.

For the bundled Compose deployment, the following captures the backend volume
without mounting the original for writing. Run it from the configured deployment
directory, carrying through any `-p`, `-f` and `--env-file` options on **every**
Compose command:

```sh
set -eu
umask 077
psst_backend=$(docker compose ps -aq backend)
psst_proxy=$(docker compose ps -aq caddy)
test -n "$psst_backend"
test -n "$psst_proxy"
psst_backend_image=$(docker inspect --format '{{.Image}}' "$psst_backend")
psst_proxy_image=$(docker inspect --format '{{.Image}}' "$psst_proxy")
psst_data_volume=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Name}}{{end}}{{end}}' "$psst_backend")
psst_caddy_data=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$psst_proxy")
psst_caddy_config=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/config"}}{{.Name}}{{end}}{{end}}' "$psst_proxy")
test -n "$psst_data_volume"
test -n "$psst_caddy_data"
test -n "$psst_caddy_config"
docker compose stop
test "$(docker inspect --format '{{.State.Running}}' "$psst_backend")" = false
test "$(docker inspect --format '{{.State.Running}}' "$psst_proxy")" = false

psst_backup=/actual/private-backups/new-snapshot
mkdir -m 700 "$psst_backup"
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --entrypoint tar \
  --mount "type=volume,src=$psst_data_volume,dst=/snapshot,readonly" \
  "$psst_backend_image" -C /snapshot -cpf - . > "$psst_backup/backend.tar"
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --entrypoint tar \
  --mount "type=volume,src=$psst_caddy_data,dst=/snapshot,readonly" \
  "$psst_proxy_image" -C /snapshot -cpf - . > "$psst_backup/caddy-data.tar"
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --entrypoint tar \
  --mount "type=volume,src=$psst_caddy_config,dst=/snapshot,readonly" \
  "$psst_proxy_image" -C /snapshot -cpf - . > "$psst_backup/caddy-config.tar"
printf '%s\n%s\n' "$psst_backend_image" "$psst_proxy_image" > "$psst_backup/image-ids.txt"
```

These helper containers use each image's normal unprivileged account. A permissions
failure is a failed backup; review the deployment's ownership rather than ignoring
missing files. Named-volume mounting and backup mechanics are described in the
[Docker volume documentation](https://docs.docker.com/engine/storage/volumes/#back-up-restore-or-migrate-data-volumes).

Copy the protected deployment files into the backup, archive or otherwise retain
the exact images if they may become unavailable, then create checksums covering
all retained artifacts. Do not print `docker compose config` with live secrets.
Do not use `docker compose down -v`: it removes storage rather than backing it up.

For the external-proxy override, also identify the `external-proxy` container and
its actual `/data` and `/config` volumes. Stop that gateway along with the backend
and inner proxy; snapshot both gateway volumes with the matching gateway image
and the same read-only helper pattern. Backing up only the inner `caddy` service
omits the externally served certificate/account state. Preserve each proxy's
distinct volume mapping and restore every component into its own new working
volume with UID/GID and private-key permissions intact. Do not share a writable
certificate volume between the original gateway and a restored copy.

The repository's isolated managed-certificate exercise is
`python3 tools/test_external_proxy.py --certificate-state`. It verifies stopped
gateway data/config copies, unchanged originals/backups, private-key ownership
and permissions, and continued certificate trust/authenticated access after
restore and restart. It uses an internal test issuer and loopback-only ingress;
it does not verify a publicly trusted ACME account's issuance/renewal or substitute
for validating the operator's actual protected backup.

## Restore a separate working copy

- [ ] Keep the original service and its data safe. Verify the backup's authenticity
      and checksums before extracting trusted archives into a **new empty**
      directory or new explicitly named volumes. Never overlay a database onto
      another database's WAL/journal files or restore over an active volume.
- [ ] Use the backed-up application/schema version first. Restore permissions and
      ownership for that image/service account, including private parent
      directories. Preserve the original archive unchanged. Do upgrades later,
      after a successful same-version recovery and a new backup.
- [ ] Disable automatic restart and public routing for the restored deployment.
      Use loopback-only listeners or an isolated host/network. Do not restore
      Caddy into public service yet: its certificates and hostname configuration
      can make a second copy reachable unintentionally.
- [ ] Persist the transfer pause **before the first server startup** using the
      restored database and the matching binary. Confirm the output reports
      `public_transfers_paused: true`. The CLI refuses absent/unsupported databases;
      resolve the error without starting an empty replacement instance.

For a standalone working copy, using the service's OS account:

```sh
(
  set -eu
  umask 077
  psst_backup=/actual/private-backups/new-snapshot
  psst_restore=/actual/new-private-working-copy
  mkdir -m 700 "$psst_restore"
  tar -C "$psst_restore" -xpf "$psst_backup/backend.tar"
  DB_PATH="$psst_restore/psst.db" /actual/matching-version/server pause
  DB_PATH="$psst_restore/psst.db" /actual/matching-version/server incident-status
)
```

Adjust `psst.db` to the actual database path inside the archive. Extract only a
trusted backup; the example does not sanitize arbitrary third-party archives.
For Compose, create a separate restore project with newly restored volumes and
private networking. Verify its explicit volume mapping, then run the existing
CLI without starting dependent services:

```sh
docker compose -p psst-restore -f /actual/restore-compose.yml \
  run --rm --no-deps backend pause
docker compose -p psst-restore -f /actual/restore-compose.yml \
  run --rm --no-deps backend incident-status
```

A different project name alone does not isolate explicitly named or external
volumes. The restore Compose file must point to the **new restored volumes**, use
the matching images, and expose no public ports. `run` does not publish the
service's ports by default, but the restored service's later startup configuration
must also be isolated. Never use the original project's command by accident.

Pause does not freeze cleanup, expiry, orphan deletion, pending-offset repair or
summary reconstruction. Those workers begin at startup and can modify a restored
working copy. Files already expired at restore time remain expired; do not change
timestamps or the server clock to make old public links valid again.

## Validate and reconcile security state

Complete these checks while isolated, preserving any evidence needed from the
untouched backup. A green recovery panel establishes only its stated coverage.
It does not establish that the backup is current, decryptable or safe to expose.

- [ ] Run SQLite integrity and foreign-key checks on the stopped working copy
      using a compatible SQLite tool. Require `PRAGMA integrity_check` to report
      `ok` and `PRAGMA foreign_key_check` to return no rows. Do not discard journals
      to force a successful check. Inspect malformed canonical metadata separately.
- [ ] Start only the restored backend in isolation and verify health, sign-in,
      administrator second factor/recovery, and persisted operator settings.
      Public payload requests must remain denied while paused; administrative
      inspection/revocation must work.
- [ ] Check Resources → Stored file checks, Counter checks and Orphan file checks.
      Restart must invalidate prior scan coverage. Wait for bounded scans and
      resolve unavailable payloads, failed checks and incomplete inventory; retain
      unexpected entries for inspection. Compare canonical resource counts and
      reserved bytes with the snapshot inventory and actual volume headroom.
- [ ] Reconcile security changes after the backup time using independent records.
      If the record is unavailable, keep transfers paused while invalidating the
      affected authorities. Review all administrators as well as ordinary users.
      Password reset or account disabling revokes that account's sessions and
      pairing grants. Disabling sign-in alone does not revoke its public links;
      use account shutdown or targeted resource revocation for those.
- [ ] Re-enroll an administrator factor or replace recovery codes when rollback
      could restore a compromised secret or a consumed code. If locked out, use
      the documented named `admin-factor-reset` CLI while stopped, then set up a
      new factor privately. That CLI does not change the password or invalidate
      other accounts. Change compromised passwords separately.
- [ ] Establish conservative remaining traffic allowances from provider or other
      independent records. Do not clear restored counters or interpret absent
      historical usage as zero. Revoke/replace affected send and receive links
      when their spent allowances or revocation history cannot be reconstructed.
- [ ] Only in the isolated test environment, deliberately resume transfers and
      test an intact download with a separately retained client key, a new
      upload/receive submission, exhausted link behavior, revocation, cleanup and
      another restart. Verify exact bytes and client authentication/decryption.
      Test actions spend real allowances in that restored database. Pause it
      again before changing routing or security settings.
- [ ] Before cutover, confirm one writer, correct hostname and trusted HTTPS,
      intended proxy trust, private storage permissions, current credentials,
      operator quotas/traffic policy and all remaining release gates. Take a new
      protected checkpoint. Reopen routing and resume transfers deliberately;
      neither action should happen merely because a scan finished.

### What rollback can and cannot preserve

| Situation                               | Expected behavior and operator action                                                                                                                                                                                                  |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Matching stopped database and files     | Stored policies, credentials, reservations and historical counters reflect that checkpoint. Later events are absent.                                                                                                                   |
| Older database with newer files         | Extra files can be classified as orphans; pending bytes beyond the old committed offset can be truncated. Preserve the untouched backup before running workers. File presence cannot recreate newer link, revocation or usage history. |
| Newer database with older/missing files | Pending offsets can rewind while reservations stay charged. Published missing/short files are unavailable; they are not recreated or refunded. Restore the matching ciphertext or revoke and clean up.                                 |
| Same-size changed ciphertext            | Server length checks may pass; only the client key and authenticated decryption detect altered encrypted content.                                                                                                                      |
| Lost download/receive/traffic history   | Scans do not reconstruct or reset it. A restore can resurrect consumed allowances, old sessions, pairing grants, recovery codes and revoked links. Reconcile independently or invalidate affected capabilities before exposure.        |
| Outstanding traffic leases              | Startup conservatively charges unresolved reservations once. This bounds crash uncertainty recorded in that database; it cannot recover leases/traffic missing from an older backup.                                                   |
| Server backup without client keys       | Ciphertext may be intact but unusable. No password reset, account pairing or administrator access recovers the missing decryption material.                                                                                            |

## Repeatable repository exercise

The tests use newly created temporary directories and copy only their own stopped
test databases and payloads. They never accept an operator data path or discover
running Compose volumes. From `backend/`:

```sh
GOCACHE=/tmp/psst-restore-gocache go test -race ./internal/database -run Restore
GOCACHE=/tmp/psst-restore-gocache go test -race ./internal/reconcile -run Restore
GOCACHE=/tmp/psst-restore-gocache go test -race ./cmd/server -run Restore
```

The process test needs local loopback sockets. Both suites clean their disposable
data; remove the dedicated Go cache after the run. Their committed assertions and
recorded results in the security plan define coverage. They do not validate an
operator's actual backup, remote backup encryption, Caddy/ACME restoration, external
proxy routing, storage hardware durability or native client key recovery. Run the
operator checks above against your deployment before relying on a backup.

The separate [external-proxy deployment gate](deployment.md#external-tls-gateway-example),
`python3 tools/test_external_proxy.py`, also copies its own stopped backend volume
to a backup and then to a new restored working volume. Through verified disposable
TLS and both supplied proxies, it checks persisted policy, pause, sessions,
receive-slot revocation and deliberate resume/logout. It never copies operator
volumes. This control-plane check does not restore encrypted payload fixtures,
gateway certificate/ACME state or client-held keys, and does not certify your
actual backup.

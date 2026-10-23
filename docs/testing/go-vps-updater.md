# Go VPS updater verification and cutover

`tools/release/cmd/psst-deploy` is the standalone Linux replacement for the
installed Python updater. It uses the existing `deployment.json`, `transaction.json`,
`active.json`, per-transaction Compose records and stopped-checkpoint schema.
Updates never invoke Python or import code downloaded inside a release bundle.
The explicit local `cutover-check` migration command compares status with the
retained, protected Python helper; it is unavailable through forced SSH.

## Available verification

From `tools/release/` with the selected Go toolchain:

```sh
go test -race ./internal/deployment ./internal/contract ./cmd/psst-deploy
go vet ./internal/deployment ./internal/contract ./cmd/psst-deploy
CGO_ENABLED=0 go build -trimpath -o ./psst-deploy ./cmd/psst-deploy
```

These bounded tests cover the actual v1 parser and archive allowlist, the
migration/public-routing boundary, injected failures before and after startup,
legacy state loading, runtime/full verification separation, read-only SQLite
integrity and foreign keys, configuration/storage/port drift, checkpoint checksums
and off-host receipts, creation-time local-only policy, and cleanup protection for
foreign aliases and every container (including stopped ones). SQLite checks use
real disposable databases; ordinary unit tests use deterministic Docker adapters.
A passing unit suite does **not** prove a live Docker update or installed-host
cutover. The opt-in Docker scenario below is excluded from ordinary unit loops.

The executable requires the root-owned standard Docker CLI with Compose/buildx,
GitHub CLI attestation verification, `curl`, `tar`, and the SQLite CLI with JSON
support. Preflight checks SQLite capability before pausing production. The
read-only integrity check opens the actual database with `sqlite3 -readonly`, uses
one transaction for `integrity_check` and `foreign_key_check`, and never migrates,
repairs or vacuums it. No SQLite driver is linked into the helper. Runtime checks
and activation share a 120-second budget; each SQLite command additionally has a
30-second cap. Recovery runs outside that exhausted validation budget.

Both helper implementations use the **same** host lock and state paths. Never
run them concurrently. Ordinary `runtime-v1` updates remain automatic; exceptional
adoption and restore can retain full protected verification. Neither the local-only
checkpoint exception nor its retained creation-time policy is changed.

## Disposable Docker evidence

From `tools/release/` on Linux, with the Docker socket available:

```sh
PSST_GO=/path/to/go bash internal/deployment/testdata/docker-smoke.sh
```

The runner requires the already cached pinned daemon image named in the script,
Alpine 3.24.2, and the cached application image pairs below. It installs no
packages, copies only the existing host `curl`, `tar`, SQLite CLI and their shared
libraries, and compiles a static Go test executable. Its one privileged outer
container runs a private Docker daemon with networking disabled, no host socket
or host-root bind, and no published outer ports. All state, accounts, volumes,
checkpoints, listeners and unrelated-container fixtures belong to that private
daemon. Cleanup removes its exact outer container, anonymous daemon volume,
image aliases and temporary directory; it does not prune shared Docker caches.
An existing caller-selected `GOCACHE` is preserved.

On 2026-10-24 the scenario passed on x86_64 with Go 1.27.3, outer Docker 29.8.1,
nested Docker 29.8.2 and Compose 5.5.1. It used the cached real application pairs
`psst-runtime-pack-{backend,web}-overlay:local` at source `dffeac44c0913c6dbf0dee4e4156e918f2f4d9c5`
and `psst-patched-review-{backend,web}-overlay:750f440` at source
`750f440d143d6766d83b13e1ce789436471ae029`. Both pairs advertise fixture version
`v0.0.0`; these are local integration inputs, not published release candidates.

- [x] Imported the legacy v1 active-record shape without invoking the Python
      updater. The concrete Go host passed root/path protection, adoption,
      resource/environment/storage checks, image UID/OCI metadata checks,
      Caddy validation, stopped-volume/image archives and checkpoint checksums.
- [x] Updated the prior image pair to the candidate pair through real Compose,
      trusted local TLS, served release/static assets, initialized account state,
      settings, runtime bindings and read-only SQLite checks; automatic
      `runtime-v1` verification reached public activation.
- [x] Repeated the candidate with a fault after real startup checks. All project
      services stopped, durable state was `failed-closed`, and `recover-safe`
      rejected old-binary restart after the migration boundary.
- [x] Restored the stopped checkpoint using the retained matching image pair on
      three new volumes. A deliberately injected post-checkpoint SQLite table
      remained on original storage and was absent from restored storage. The
      restore stopped at `restored-awaiting-verification`; full administrator
      flow/reconciliation evidence was not fabricated or bypassed.
- [x] Exercised the real Docker inventory/removal adapter with separate imported
      historical v1 retention records. Expired updater-owned images/checkpoints
      disappeared; an operator alias, a stopped unrelated container image, a
      retained checkpoint image and the active image survived. Historical expired
      records used the persisted `checkpoint_prune_started` resume marker;
      actual complete checkpoint validation was exercised by update and restore.

The test substitutes only remote acquisition/pull with already loaded local
image IDs and supplies its private CA explicitly to `curl`; TLS verification
remains enabled. No GitHub attestation, remote registry index, release publication,
off-host provider or production SSH/sudo installation is exercised. Local image
IDs deliberately differ from the registry digest manifest contract, so the
automatic post-activation retention attempt reports incomplete cleanup; the
separate historical retention fixture above exercises successful scoped cleanup.
The injected failure is an idempotent repeat after activation. It proves the
storage/restart boundary and separate restore, and does not prove a historical
incompatible schema migration or old CLI refusal against that newer schema.

The executable tested for mutation is the compiled Go integration test, running
the production `Updater` and `Host` methods. The installed `psst-deploy` command's
fixed paths and production cutover still require the host checks below. Unit
tests additionally cover restricted SSH syntax,
secret-safe diagnostics and legacy transaction/checkpoint formats.

## Root installation and restricted entry point

`deploy/install-go-updater.sh` is a local root tool with exactly two operations:
`prepare EXPECTED_SHA256` and `activate EXPECTED_SHA256`. Install the reviewed
script at `/usr/local/lib/psst.zip/install-go-updater.sh` and the reviewed native
Go executable at `/usr/local/lib/psst.zip/psst-deploy.next`, root-owned mode 0755.
Pass the independently reviewed build's full 64-character SHA-256; the installer
accepts no binary, configuration, host, executable or state path argument.

Preparation leaves the Python entry point active. Under the existing
`/var/lib/psst.zip-deploy/deployment.lock`, it verifies protected real paths,
private existing state directories, native executable identity, terminal
transaction state, and exact semantic agreement of Python/Go v1 status. It
retains the old helper, parser, SSH/sudo restrictions and their checksums in
`/usr/local/lib/psst.zip/updater-cutover/`. A previous Go binary is retained if
one exists. The original Python files remain at their existing paths.

Activation rechecks the retained hashes, unchanged original helper/restrictions
and resolved state. It validates the candidate sudoers with `visudo -cf` and the
candidate's effective SSH policy with `sshd -t` and `sshd -T -C`. It atomically
replaces the regular executable at `/usr/local/lib/psst.zip/psst-deploy`, then
separately replaces the two restriction files. This is **not** one atomic
multi-file transaction: a short deny/reload boundary is covered by the shared
lock, so neither updater can mutate state during the switch. Failed validation
or reload restores the retained restriction files and previous binary where
possible and leaves protected records for local inspection. It never starts an
application update or rewrites manifest/bundle requirements.

Before signalling the fixed root-owned `/run/sshd.pid`, the installer rechecks
the complete installed SSH/sudo configuration, allows only the helper's `ssh`
sudo argument, and checks that direct `status`, arbitrary `id` and malformed
release requests fail for the restricted account. It verifies the PID's actual
sshd executable before sending SIGHUP. The Go restriction examples are
`deploy/ssh/psst-deploy.go.conf` and `psst-deploy.go.sudoers`; the existing Python
examples remain unchanged. Both profiles accept only `status` or
`update vMAJOR.MINOR.PATCH` remotely. The local installer also requires Bash,
GNU coreutils, `flock`, and protected `sshd`/`visudo` executables in `/usr/sbin` or
`/usr/bin`, plus the retained Python runtime for its one migration comparison.

The protected check records include helper SHA-256, native platform, Go version,
embedded source revision/dirty flag, and matched v1 status. A reload signal alone
does not establish successful login through the operator's actual SSH route.
Inspect the complete effective sudo policy and check `sshd -T -C` using the
actual deployment source address before production approval; the automated
policy context uses `psst-deploy` from loopback.

The opt-in local installation scenario runs from `tools/release/`:

```sh
PSST_GO=/path/to/go bash internal/deployment/testdata/install-cutover.sh
```

It compiles the real helper and imports an owned scratch root made from installed
public executables, Python standard-library files and their runtime libraries.
It downloads nothing, creates no shared builder cache, mounts no host directory
or socket, publishes no port and uses a disposable account and generated SSH
keys with networking disabled except the container's loopback. Cleanup removes
its exact container, image and temporary root; an existing caller-selected Go
cache is preserved. The test uses the real OpenSSH daemon/client and sudoers
plugin, including a live restricted SSH status request. Setup failures and mocks
are not installation passes.

On 2026-10-24 the disposable installation scenario passed with Go 1.27.3 on
linux/amd64, OpenSSH 10.5p1/OpenSSL 3.6.4, and sudo 1.9.17p2. It used a synthetic
completed v1 transaction and generated fixture keys. The real Python restricted
status request matched the real Go restricted status request after activation.
Preparation used the installed tools at `/usr/bin`; activation and the daemon
reload ran with only `/usr/sbin/sshd` and `/usr/sbin/visudo` present, exercising
the Ubuntu-style layout with the same real installed executables.
Bad hash/path/ownership/symlink inputs, a held shared lock and pending verification
were refused. An earlier conflicting effective SSH Match rule caused rollback to
the previous SSH/sudo files with no Go entrypoint left active. Real sudo rejected
direct `status`, arbitrary `id` and malformed versions; real SSH rejected empty,
shell-syntax, arbitrary-version/path, extra-argument and administrator-only
requests. The actual daemon accepted SIGHUP and a subsequent public-key session
used the Go helper. The retained helper/parser, private configuration and v1
transaction hashes stayed unchanged. All exact fixture containers, images and
temporary roots were removed; the shared caller Go cache was preserved.

This was a local development build with `source_modified: true`, not a reviewed
production binary. The scratch scenario tests installation and restrictions; the
separate Docker update/recovery/retention scenario above provides application
integration evidence. Neither scenario proves the production account, network
route, complete effective host sudo policy, backup provider or operator cutover.

## Remaining integration and installation evidence

- [x] Run the bounded disposable Go/Docker update, post-start failure, separate
      restore, legacy v1 state import and retention scenario documented above.
- [x] Run the disposable root installation, v1 status comparison, rollback,
      effective SSH/sudo configuration and live restricted-account scenario.
- [ ] Check the actual VPS has SQLite JSON support, compatible official CLIs,
      adequate disk reserves and no unresolved deployment transaction. Preserve
      existing operator configuration and volume identities.
- [ ] Build and checksum the Go helper for the actual Linux architecture. Stage it
      root-owned without replacing the current helper; retain the old Python
      helper/parser and their checksums as the pre-cutover implementation.
- [ ] Validate candidate sudoers with `visudo -cf`, and SSH configuration with
      `sshd -t`. The proposed restricted command is
      `/usr/local/lib/psst.zip/psst-deploy ssh`; it still accepts exactly `status` or
      `update vMAJOR.MINOR.PATCH` from `SSH_ORIGINAL_COMMAND`. Do not grant `verify`,
      `restore`, arbitrary paths/flags or an unrestricted shell to the deploy user.
- [ ] Under the shared deployment lock, confirm v1 status/read compatibility and
      atomically switch the forced command and sudoers rule to the staged binary.
      Validate status through the actual restricted account, then record the
      installed helper checksum and architecture. This step needs the authorized
      production rollout process.
- [ ] Preserve the existing manifest/bundle and provenance contract until every
      supported installation has migrated. Existing immutable releases still
      include `deploy/update.py` and `tools/release_artifacts.py`; the Go v1 reader
      accepts the optional updater and requires the inert parser payload. Retire the
      Python code and adjust future bundle requirements only together with the
      final supported consumer switch; do not rewrite published assets.

This document records preparation and test commands. No production installation,
SSH/sudo modification, release publication or VPS update is performed by these
unit tests.

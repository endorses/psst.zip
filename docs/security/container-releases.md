# Container release installation

The release stack runs published backend and web/Caddy images. Main-branch pushes
run CI; publishing a version makes it available to install. Production updates
are a separate manual action. The release publishing and updater tooling are
implemented, with first publication and production adoption still pending. No
tagged container release has been published through this release process yet.

Routine pushes and pull requests retain security/history checks and select
application steps from their source, build and fixture dependencies. Prose-only
documentation and reviewed release-tooling changes can omit unrelated app builds.
The five existing check names remain present; each scope step explains its
selection. An omitted application suite has not executed. Unknown changes or
unavailable Git baselines run everything. Manual CI requests the full application
suite. The container release caller requires complete security/backend/web
validation and skips separate mobile builds.

## Release candidate checks

The current `.github/workflows/release.yml` verifies candidates and contains an
explicitly enabled, protected publication job. Publication is disabled by default.
It accepts strict `vMAJOR.MINOR.PATCH` tags, requires
the tag's full source commit to match the triggering event and be reachable from
`main`, and calls the reusable CI workflow from that same commit. Container
publication requires successful repository-security, backend and web checks.
Android/shared and iOS validation remain in routine application CI and do not
block server container publication.

A main-only manual dispatch accepts an unused planned version without creating a
tag or release. Always supply an unused version: the workflow's `v0.1.0` default
is now reserved by an existing tag. A dispatch can exercise preparation before
publication is enabled. Its successful checks cannot satisfy the exact-tag
publication gate.

After source identity validation, the workflow resolves the application base-image indexes and
BuildKit builder once and checks their AMD64 and ARM64 coverage. Separate native
runners build the paired images and retain genuine build records and original
Docker saves. Each runner then collects runtime sources, verifies signed Caddy
sources, applies source/notice overlays, smoke-tests the final configurations,
exports OCI archives without changing tested configuration bytes and independently
replays runtime source correspondence. It also scans the exact archived
application sources and final OCI images, measures compiler/dependency graphs,
and retains selected Go/npm package inputs against committed locks.

The workflow retains unsigned native image/source archives, raw scanner/compiler
receipts and explicit file/hash inventories as one-day candidate artifacts. A
separate job independently checks same-run, same-attempt inputs from both native
runners and the common upstream source collection before preparing the paired
indexes, detached manifest, deployment bundle and source offerings. Both native
recovery jobs then exercise those exact prepared inputs. Downloaded artifacts
still require independent authentication and release gates; neither summaries
nor successful structural assembly approve publication. Private operator state,
credentials, disposable recovery stores, tools and caches are excluded. Runner
cleanup removes only that job's working inputs after artifact retention.

Complete AMD64 preparation and transfer replay were exercised locally. Planned
run `37778271584` also passed hosted preparation, cross-job assembly and recovery
on both architectures. Tagged run `37782470022` passed their signed gates but
failed publisher preflight. These candidate outputs are not installable releases.

Docker's local `--load` exporter can report a configuration digest in its
`containerimage.digest` field. Candidate records retain that raw metadata, but
those values must not be used as registry manifest or index digests. The future
publisher must resolve and verify the digests actually stored in the registry.

The workflow defaults to read-only repository permissions. Tagged evidence jobs
receive scoped OIDC/attestation permissions; only the guarded publication job can
write packages or releases after exact signed gates and protected review. Planned
dispatches remain unsigned, read-only candidates. No release job has production
access. Main-branch pushes run CI only. This repository has enabled guarded
publication and configured dedicated inspection credentials and first-package
initialization. Actual credential use, package initialization and public retrieval
must still pass as described in the
[publication guide](container-publication.md). Production deployment remains a
separate manual operation. Tag ancestry checks do not replace branch/tag policy;
the configured policies for this instance are recorded in the plan and operator
guides.

## Maintainer path to a ready release

Choose an unused patch version for each changed publication candidate. Preserve
`v0.0.0` and the failed `v0.1.0` and `v0.1.1` tags. A main push runs source CI.
The guarded publication
transport and signing bridge are enabled in the tag workflow; each candidate
still needs its exact signed gates and the configured human approvals.

- [x] Review and commit the complete release source and workflow. Push the
      reviewed checkpoint only with authorization, then verify routine CI on
      that exact commit. The planned candidate below repeats full source CI,
      including application suites selected out by routine checks.
- [x] Exercise the main-only planned-version candidate. For this repository, the
      historical successful dispatch command was:

      ```sh
      gh workflow run release.yml --repo endorses/psst.zip --ref main \
        -f planned_version=v0.1.0
      gh run list --repo endorses/psst.zip --workflow release.yml --branch main
      ```

      Record the selected run ID, attempt and full source SHA. Confirm both
      native preparation jobs, upstream retention, assembly and both recovery
      jobs succeed. Rerun the complete workflow when needed: retrying only failed
      jobs can leave same-attempt input artifacts unavailable. This dispatch
      creates no version tag and does not satisfy tagged publication checks.

      Planned run `37778271584`, attempt 1, at
      `77c6cebc238c37830ab5421da02392f860b7aba5` passed all three server CI
      jobs, both native preparations, strict assembly and both native recovery
      jobs with zero annotations. Recovery covered upgrade/repeat/restore,
      preservation of a prior pause and an injected startup failure. Signed
      authentication, public retrieval and live production recovery remain
      separate pending checks.

- [x] Complete the [source review](application-package-source-review.md),
      actual native scanner/smoke/recovery gates and their authentication. Review
      exact image configurations and all corresponding-source offerings; a green
      candidate summary alone is insufficient.

      Tagged run `37782470022`, attempt 1, at the same reviewed source completed
      the authenticated source/scan/smoke/notice reports and both tagged native
      recovery measurements. Their signed aggregate passed independent
      verification. The operator's separate human distribution review was
      authenticated, attested and independently verified. Its exact subjects and
      approval comment remain in artifact
      `candidate-distribution-presentation-37782470022-1`. All pre-publication
      gate jobs have zero annotations. The publisher then failed its hosted
      Node24 lookup preflight before reaching any release or registry write.
      Preserve the existing tag; repair and verify that preflight before a new
      immutable patch-version candidate. Public retrieval and production
      recovery remain pending.

- [ ] Configure reviewed branch/tag protection, immutable-release policy and
      narrowly scoped publication credentials. Review the
      wired publisher and complete its prerequisites in the
      [publication guide](container-publication.md) before creating a release tag.
      When both packages are absent, configure its explicit first-package mode;
      that same publishing attempt creates the reviewed pair and verifies public
      repository linkage before exposing version tags or a ready release. This
      instance's branch/tag and immutable-release policies are configured, and
      the operator supplied the scoped inspection credential. Its authenticated
      use and actual first-package initialization remain pending. GHCR and
      GitHub Releases provide publication storage. Independent VPS backups and
      their restore verification are deferred prerequisites for production
      migration, separate from container publication.
- [x] Once publishing is enabled, create an unused version tag at the reviewed
      source SHA and push that tag. Its workflow must rerun CI for that exact
      tagged commit, authenticate the complete image/source/bundle subjects and
      publish only after every gate passes. Never move an existing release tag
      or replace a partially published version to hide an interruption.

      `v0.1.0` points to `77c6cebc238c37830ab5421da02392f860b7aba5` and
      `v0.0.0` is preserved. Publication enablement and first-package
      initialization are configured. The protected distribution review remains
      required; the tag does not itself authorize publication or deploy the VPS.

      The corrected `v0.1.1` tag points to
      `84313f4d74e9f624a62327af180b1f7399e2097b`. Run `37790269573`, attempt 1,
      passed exact server CI, both native image preparations, signed source
      assembly and authenticated recovery on both architectures. Its fresh
      packet is `candidate-distribution-presentation-37790269573-1`. The operator's
      fresh distribution review was authenticated and the publisher separately
      approved. The publisher passed its repaired hosted runtime preflight but
      stopped in the publication command before retaining a journal or receipt;
      its exact exception is not exposed. Diagnose that failure before any retry.
      Preserve both existing version tags. Publication and anonymous retrieval
      remain pending.

      Read-only checks subsequently authenticated all eight retained gate reports
      and validated the real smoke-configuration mapping. They did not reproduce
      the hosted failure. The publisher now emits fixed preparation-stage names
      and fixed failure categories without exposing exception text or credentials.
      These diagnostics identify a future failure boundary; they do not establish
      successful publication or authorize retrying old mutation state.

      The diagnostic `v0.1.2` tag points to
      `57c64eafba191a0621f9ca452c79557507c7389b`. Run `37799619020`, attempt 1,
      passed exact server CI, both native image preparations, signed source
      assembly and authenticated recovery on both architectures. Its fresh
      packet is `candidate-distribution-presentation-37799619020-1`; the configured
      environment is waiting for the operator's new distribution review.
      Publication and anonymous retrieval remain pending. Preserve all existing
      version tags; this candidate does not update the VPS.

- [ ] Confirm the immutable ready GitHub Release, authenticated manifest/bundle,
      both multi-platform image indexes, all four native children, source assets
      and fresh anonymous retrieval. Follow the publication guide's protected
      journal recovery if publication is interrupted. Publishing makes a release
      available; it does not update the VPS.

The detailed [native preparation guide](container-native-preparation.md) records
the measured inputs, replay commands and remaining hosted gates. These steps
describe the full maintainer path; they do not establish live publication or a
first ready release.

## Operator path for updates and recovery

After a ready release and the separately reviewed installed updater are available,
manual SSH and Actions use the same version-only update command. Complete actual
volume/configuration adoption, an encrypted off-host checkpoint export and an
independent restore exercise before the first production migration. The
[update/recovery guide](release-update-recovery.md) specifies the protected host
configuration, backup hook and authenticated application checks. The
[backup runbook](backup-restore.md) covers maintenance copies and security
reconciliation after restore.

From the existing maintenance SSH connection, run the installed root-owned
helper as the administrator:

```sh
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py status
/usr/bin/python3 -I /usr/local/lib/psst.zip/deploy/update.py update v0.1.0
```

Check the protected transaction, active version and application state. Exit zero
with phase `completed` confirms activation. Exit 20 means the isolated candidate
is awaiting local authenticated verification; follow the update guide before
running its `verify` command. Keep client-held encryption keys available for the
existing download and new upload checks. Preserve the checkpoint, original data
and matching images until retention policy permits removal.

For subsequent Actions updates, first provision and validate the dedicated
restricted SSH key and `production` environment using the
[Actions deployment guide](actions-production-deployment.md), then select a
published ready version:

```sh
gh workflow run deploy.yml --repo endorses/psst.zip --ref main -f version=v0.1.0
```

Use maintenance access to inspect durable host status after any disconnect,
timeout or failed workflow. Before migration starts, `recover-safe` can restore
the checked original state. After the migration/startup boundary, use the update
guide's explicit isolated `restore` procedure with the matching stopped
checkpoint; complete security/traffic reconciliation before activation. An image
tag change alone cannot roll back migrated storage. Rotation, maintenance backup
and retention commands remain operator actions through the protected helper.

The first manual VPS migration and end-to-end Actions update remain unperformed.

## Compose requirements and image selection

Use Docker Compose **2.24.4 or later**. The external gateway overlay uses Compose's
`!reset` and `!override` merge tags. The release manifest records the supported
Docker Engine version and architectures for the selected release.

`deploy/compose.release.yml` has no `build:` directives. Set `BACKEND_IMAGE` and
`WEB_IMAGE` to the full registry references from the independently verified release
manifest, including `@sha256:` and its 64 hexadecimal digits. These refer to the
multi-platform image index, rather than a floating tag or an architecture-specific
child image. Compose requires both variables to be nonempty; the release tooling
must also validate their syntax and agreement with the selected manifest before
pulling. A checksum from an untrusted source does not authenticate the manifest.

The backend retains its private network, has no host port, and trusts only the
web/Caddy container's exact socket address. Images supply the non-root application
users. The release configuration preserves read-only roots, dropped capabilities,
`no-new-privileges`, bounded temporary filesystems, resource limits, rotating logs,
and the source stack's public 80/443 mappings. The Caddy administration endpoint
remains unpublished.

## Operator configuration and first installation

Keep the Compose files and their `external-proxy/` configuration directory together.
Paths in the overlay resolve relative to the base Compose file's directory. In a
repository checkout, use `deploy/` as shown below; a release bundle retains that
relative layout. Do not override Compose's project directory with another path.

Copy `deploy/release.env.example` to an operator-owned `deploy/release.env`, restrict
its permissions to the administrator, and set the verified image references and
public address. The example hostname is a placeholder. Set `PSST_DOMAIN` to your
hostname and `PUBLIC_URL` to its canonical HTTPS origin; leave
`AUTH_ALLOW_INSECURE_HTTP=false`. DNS and inbound TCP 80/443 must reach this server.
The administrator password is entered directly on the VPS for first startup;
sign in successfully before removing **both** `ADMIN_USERNAME` and
`ADMIN_PASSWORD` from the environment file. Existing installations use their
existing accounts and leave both bootstrap credentials unset.

```sh
chmod 600 deploy/release.env
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml config --quiet
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml pull
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml up -d
```

After first sign-in succeeds, edit the private environment file on the VPS to
remove both bootstrap variables, then recreate only the backend with the same
verified image and existing named volume:

```sh
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml up -d --no-deps --force-recreate --pull never backend
```

Sign in again and confirm your original administrator account and settings are
present. This removes the bootstrap values from the running container, rather
than only from the file. Keep the password in your password manager; do not put
it in a command argument, repository or chat.

The public website uses inbound TCP 80/443. Keep administration on SSH with
key-only authentication; SSH can remain reachable on port 22 when a dynamic
client address prevents a fixed source-IP rule. Allow outbound DNS, certificate
issuance and image retrieval. Point the domain's A record to the server's IPv4;
add an AAAA record only for a working server IPv6 address. Caddy
[obtains and renews TLS certificates](https://caddyserver.com/docs/automatic-https);
its named state volumes preserve them across updates.

Use these direct commands only for a verified fresh installation. Upgrades need
preflight, a stopped complete backup, and post-update verification through the
shared updater described in the deployment plan. Do not use this quick start to
replace an existing source installation without inspecting its storage first.
Avoid displaying rendered Compose output containing live credentials.

## Persistent identities and existing installations

The default project is `psst-zip`. Explicit physical volume names prevent changing
the Compose filename or directory from accidentally selecting new storage:

| State                                         | Default physical volume          | Override                       |
| --------------------------------------------- | -------------------------------- | ------------------------------ |
| Backend database and encrypted files          | `psst-zip_psst-data`             | `BACKEND_DATA_VOLUME`          |
| Web/Caddy certificate state                   | `psst-zip_caddy-data`            | `CADDY_DATA_VOLUME`            |
| Web/Caddy configuration state                 | `psst-zip_caddy-config`          | `CADDY_CONFIG_VOLUME`          |
| Optional external gateway certificate state   | `psst-zip_external-proxy-data`   | `EXTERNAL_PROXY_DATA_VOLUME`   |
| Optional external gateway configuration state | `psst-zip_external-proxy-config` | `EXTERNAL_PROXY_CONFIG_VOLUME` |

If `COMPOSE_PROJECT_NAME` is set, that value replaces `psst-zip` in defaults. Keep
this name stable across invocations. The logical service names remain `backend`,
`caddy`, and, for the optional gateway, `external-proxy`.

Before switching an existing installation, inspect actual container volume
mappings, project identity, and its database path. Preserve `BACKEND_DATA_VOLUME`
and `DB_PATH` explicitly when the actual storage differs from fresh-install
`/app/data/psst.db`. Preserve both Caddy volumes, gateway volumes when used, public
ports, private address overrides, operator limits, and trusted proxy configuration.
Compose creates missing named volumes; a successful `config` check cannot prove
that an existing installation's data was selected. The updater must reject missing
or incompatible storage instead of starting an empty replacement installation.

Follow the [cold backup and restore runbook](backup-restore.md) for the entire
backend volume, both Caddy volumes, gateway state when used, and protected operator
configuration. Stop the sole writers before taking the backup. Keep original
storage and matching images for controlled restore; never use `down -v` or prune
installation volumes. An older binary running against a newer database is not a
rollback procedure.

## Bundled Caddy configuration and deliberate overrides

The base release stack uses the selected web image's bundled
`/etc/caddy/Caddyfile`; it does not mount the checkout's root `Caddyfile`. A source
installation's host mount must be removed or explicitly adopted when migrating,
so stale host configuration cannot silently override the released proxy policy.

An operator who needs a custom Caddyfile can add a separate override file beside
`compose.release.yml`, with a deliberate read-only mount:

```yaml
services:
  caddy:
    volumes:
      - ./operator/Caddyfile:/etc/caddy/Caddyfile:ro
```

Pass that override after the release Compose file on every relevant command.
Retain the private backend origin, internal ports 8080/8443, exact proxy trust,
forwarding and response-header policy, and static content path. Validate the
custom configuration against the selected web image before use. Keep it protected
with operator configuration and include it in the stopped backup. Updater preflight
must identify active mounts, check their compatibility, and retain a deliberate
operator override; it must not silently discard or adopt a source-era mount.

## Optional external TLS gateway

Use the release overlay after the base file:

```sh
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml \
  -f deploy/external-proxy.release.compose.yml config --quiet
```

Use the same file list for pull, startup, inspection, backup, and update. The
`external-proxy` service owns public 80/443 and joins the edge network for automatic
HTTPS. The inner `caddy` service has no published ports and uses HTTP only on the
internal proxy network. Both services use exactly the selected `WEB_IMAGE` digest.
The overlay mounts its matching `external-proxy/Caddyfile` and
`external-proxy/trusted-proxy.caddy` read-only from the release bundle.

The external gateway trusts no upstream forwarding headers. The inner web proxy
trusts only `${PSST_EXTERNAL_PROXY_IP:-172.30.194.2}/32` on the separate internal
network, then supplies its resolved client address to the backend. Backend trust
remains limited to `${PSST_PROXY_IP:-172.30.193.3}/32`; adding a gateway does not
broaden that trust. Retain private subnet/address overrides consistently.

Gateway certificate and configuration volumes are separate from the inner Caddy
volumes and use the explicit names listed above. Do not combine this overlay with
another listener on the same public ports. A different external proxy requires an
operator-specific reviewed configuration; this overlay is a complete separate
Caddy TLS gateway, not a universal proxy integration.

Exercise this release configuration with an already-loaded backend/web image pair
from the same source checkout. Supply full local `sha256:` image IDs or
`repository@sha256:` digest references; floating tags are rejected. Resolve the
prepared images and Python helper to local IDs, then run both existing flows:

```sh
BACKEND_ID=$(docker image inspect --format '{{.Id}}' "$BACKEND_IMAGE")
WEB_ID=$(docker image inspect --format '{{.Id}}' "$WEB_IMAGE")
CLIENT_ID=$(docker image inspect --format '{{.Id}}' python:3.13-alpine)
python3 tools/test_external_proxy.py \
  --backend-image "$BACKEND_ID" --web-image "$WEB_ID" --client-image "$CLIENT_ID"
python3 tools/test_external_proxy.py \
  --backend-image "$BACKEND_ID" --web-image "$WEB_ID" --client-image "$CLIENT_ID" \
  --certificate-state
```

Release mode uses `compose.release.yml` and `external-proxy.release.compose.yml`,
without building or pulling the application pair. Both proxy hops must run the
selected web image; the bundled inner Caddyfile and mounted proxy configurations
must match this checkout, including after restore/restart. The disposable fixture
adds test credentials, isolated addresses and loopback ports. Caller-supplied
application/helper images remain intact during cleanup. A tagged Python helper
argument retains the original pull behavior; use its already-loaded immutable ID
to avoid that pull. The managed certificate flow retains the original CA trust and
leaf certificate while restoring gateway data/config into new volumes. These
flows test local configuration and storage behavior; public ACME, anonymous
release acquisition, native clients and current-candidate release evidence require
their separate checks. Keep this expensive harness outside routine CI.

## Preparing the detached manifest and bundle

`tools/release_artifacts.py` builds a deterministic archive from the current Git
commit's allowlisted deployment inputs. Release inputs must be committed and
unchanged; unrelated untracked files, including local research, are excluded.
Credentials, operator `.env` files, private history artifacts, database files and
uploads are never archive inputs. The detached manifest records the matching
commit and source archive URL, both multi-platform index digests and their two
architecture-specific child digests, resolved base images, toolchain versions,
Docker/Compose requirements, bundle checksum and migration/rollback notes.

For a reviewed committed checkout, prepare the archive in an operator-selected
output directory:

```sh
python3 tools/release_artifacts.py build-bundle \
  --root . --output /private/release-output --version v0.1.0
python3 tools/release_artifacts.py create-manifest --help
python3 tools/release_artifacts.py validate \
  --manifest /private/release-output/release-manifest.json \
  --bundle /private/release-output/psst.zip-deployment-v0.1.0.tar.gz
```

The example version does not select or publish the first release. Manifest
creation requires the publisher's actual image digests and build records; do not
substitute example hashes. The archive contains `release-bundle.json`, while the
manifest remains outside it to avoid a self-referential checksum. Existing output
files are never overwritten. Validation rejects unexpected repositories/images,
missing platforms, inconsistent source/version metadata, unsafe archive paths or
entry types, duplicate JSON keys and oversized compressed or expanded input.

Bundles default to `payload_profile: artifact-foundation`, including when a
tracked updater is present. The updater has passed isolated actual Docker
migration/update/recovery exercises; authenticated public release acquisition,
independent off-host provider recovery and production adoption remain unverified.
Only the gated publisher may explicitly select `--payload-profile deployment-ready` after the distribution
and recovery requirements pass. That profile requires the tracked updater, and
the manifest profile must match the archive. Do not execute
bundle tooling as root merely because checksum validation succeeded. Publication
must authenticate the detached manifest, archive and image digests with provenance
from the expected repository, release workflow and source commit. See
[GitHub artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations).
The [updater and recovery guide](release-update-recovery.md) documents the
implemented privileged verification policy and its remaining live checks. The
[publication guide](container-publication.md) covers exact inputs, signed evidence,
reservation and partial-publication recovery. The
[Actions deployment guide](actions-production-deployment.md) covers the separate
manual version dispatch, environment protection and restricted SSH installation.
Publishing a release makes images available; deploying it remains a separate
operator action through that installed helper.

## Image metadata and dependency notices

Both Dockerfiles accept `SOURCE_URL`, `VERSION` and `REVISION`. Released images
must use the complete source commit for `REVISION`. OCI labels identify source,
version, revision and `AGPL-3.0-only`, plus the matching source archive locator.
The backend accepts digest-pinned `GO_IMAGE` and `RUNTIME_IMAGE`; web accepts
`NODE_IMAGE` and `CADDY_IMAGE`. The publishing workflow must resolve and record
those four bases and actual toolchain versions in the release manifest. Tags
remain convenient source-build defaults, not release provenance.

The backend carries `/app/licenses/` with application licenses, dependency
inventories and notices, and the actual Go builder's standard library license.
Web serves `/licenses/AGPL-3.0-only.txt`, `/licenses/dependency-inventory.json`,
`/licenses/THIRD_PARTY_NOTICES.txt` and `/licenses/release.json`. Dependency notice
checks run in CI against exact committed locks, including browser runtime code
that the build project classifies as development dependencies.

Source archive locators and application inventories do not by themselves establish
complete distribution compliance. Hosted source/legal discovery and native
notices are implemented. [macOS CI run 37669775085](https://github.com/endorses/psst.zip/actions/runs/37669775085)
passed all 176 XCTest cases on `b697119`, including actual app and embedded share
extension license, notice, inventory and source-resource packaging assertions.
Corresponding-source publication and the selected Caddy/Alpine runtime
distribution's obligations remain release gates. The patched private
AMD64 source pack, served runtime overlays and final-image scan review have
passed for their recorded subjects; [the dependency review](container-dependency-review.md)
records exact images and bounded findings. ARM64 and actual public source delivery
remain pending. Native store terms are
tracked separately and are outside server deployment automation.

GoReleaser retains its binary/archive definitions but no longer publishes images;
the paired container publisher will own GHCR releases. Standalone binary releases
have not been verified by this container work. In particular, the existing
root-level `go mod tidy` pre-hook must be reviewed before enabling binary
publication in this multi-module repository.

## Assemble the two native build results

After source identity validation, native preparation and upstream source retention
run alongside exact-commit CI. Assembly waits for security/backend/web CI and both native
results; failed or incomplete CI cannot reach source-CI approval or publication.
The workflow keeps its existing serialized publication group. Preparation may
consume runner time when CI fails, but does not authorize a release.

Ordinary PR/main CI treats changes to this reviewed release workflow as release
tooling and runs repository-security checks. CI/unknown workflow changes and
selector-policy changes still require all application checks. Container tags and
planned release dispatches always run security/backend/web validation regardless
of change selection. Other full-validation callers retain mobile checks by default.

After CI and both native jobs finish, `tools/prepare_release_inputs.py` assembles their
actual final OCI exports, measurements, source packs and build records. Each job
can complete its local measurements before the release manifest exists. The
assembly step checks both native platforms, shared immutable bases, matching
actual Go/Node versions, exact smoke-tested configurations and OCI bytes, and
source asset hashes. Both architecture-specific dependency collections are
independently replayed against committed locks and complete source-scanner
receipts. Assembly also requires the common upstream source collection, replayed
against its catalog and lock from the exact Git tree. It copies both runtime,
both dependency and the common upstream archives into its output, pinning retained
bytes to the verified hashes. It creates deterministic paired indexes, the
deployment bundle, detached manifest and an application source archive from the
exact tagged Git tree: eight release assets and fourteen bound subjects in total. Untracked operator files and research are excluded by
`git archive`. Package integrity does not establish that every upstream package
contains complete preferred-form source; that review remains required.

Use a new private output directory and the actual selected version/commit:

```sh
python3 tools/prepare_release_inputs.py \
  --root . --repository endorses/psst.zip \
  --ref "refs/tags/$PSST_RELEASE_VERSION" --event-sha "$PSST_SOURCE_COMMIT" \
  --reviewed-commit "$PSST_SOURCE_COMMIT" \
  --candidate "$REVIEW_DIR/candidate-bases.json" \
  --build "linux/amd64=$REVIEW_DIR/amd64/build-record.json" \
  --build "linux/arm64=$REVIEW_DIR/arm64/build-record.json" \
  --measurement "linux/amd64=$REVIEW_DIR/amd64/native-measurement.json" \
  --measurement "linux/arm64=$REVIEW_DIR/arm64/native-measurement.json" \
  --pack "linux/amd64=$REVIEW_DIR/amd64/pack" \
  --pack "linux/arm64=$REVIEW_DIR/arm64/pack" \
  --archive "backend-amd64=$REVIEW_DIR/amd64/export/backend-amd64.oci.tar" \
  --archive "web-amd64=$REVIEW_DIR/amd64/export/web-amd64.oci.tar" \
  --archive "backend-arm64=$REVIEW_DIR/arm64/export/backend-arm64.oci.tar" \
  --archive "web-arm64=$REVIEW_DIR/arm64/export/web-arm64.oci.tar" \
  --dependencies "linux/amd64=$REVIEW_DIR/amd64/application-dependencies" \
  --dependencies "linux/arm64=$REVIEW_DIR/arm64/application-dependencies" \
  --source-scan "linux/amd64=$REVIEW_DIR/amd64/source-scans/source-scan-measurement.json" \
  --source-scan "linux/arm64=$REVIEW_DIR/arm64/source-scans/source-scan-measurement.json" \
  --upstream "$REVIEW_DIR/upstream-sources" \
  --migration-notes "$PSST_MIGRATION_NOTES" --rollback-notes "$PSST_ROLLBACK_NOTES" \
  --output "$REVIEW_DIR/prepared"
```

For a read-only planned main candidate, use the actual main ref/event SHA and add
`--planned-version "$PSST_RELEASE_VERSION" --event-name workflow_dispatch`.
The planned version must be unused. This mode creates no tag and writes
`planned-candidate-inputs`; it cannot satisfy tagged source CI or signer identity.
The actual publishing entry point still requires a reviewed tag. The hosted
candidate workflow now assembles both native results and runs native recovery;
see [retained inputs across jobs](container-native-preparation.md).

`release-inputs.json` records every image/file/source subject for subsequent
authenticated aggregation. Its `publication_authorized` remains false and
`measurement_authentication_required` remains true. The `deployment-ready`
payload profile means the bundle contains the updater; publication still requires
the authenticated completed checks and reviews in the
[publication guide](container-publication.md). A failed partial preparation is
kept for inspection and cannot be resumed by overwriting the directory.

## Disposable final-image smoke check

After pulling the exact selected images, run the paired-image check with the
version and complete revision from the manifest:

```sh
python3 tools/verify_release_images.py \
  --backend-image "$BACKEND_IMAGE" --web-image "$WEB_IMAGE" \
  --platform linux/amd64 --version "$PSST_RELEASE_VERSION" \
  --revision "$PSST_SOURCE_COMMIT"
```

Repeat for `linux/arm64` with that platform's images loaded and either a native
runner or explicitly configured emulation. The tool reports native versus emulated
execution. Publication requires native execution on both architectures; an
emulated local operator check cannot satisfy that gate.

The tool uses a randomly named disposable project, an unused private subnet,
a generated administrator password, loopback-only HTTP and dedicated volumes.
It checks metadata, hardening, built HTML/JavaScript, licenses/source metadata,
API/config, authentication, and account/session persistence after removing
bootstrap credentials and recreating the backend. Captured command output is
withheld to protect generated credentials. Cleanup removes only that tool-owned
project’s resources and temporary files.

The tool uses locally available images and does not pull them itself. It verifies
an HTTP fixture, not public ACME or a production TLS/account/storage flow. Passing
this check does not replace release provenance verification, ARM64 verification,
complete upgrade/recovery tests, or inspection of an existing installation.

# Container release installation

The release stack runs published backend and web/Caddy images. Main-branch pushes
run CI; publishing a version makes it available to install. Production updates
are a separate manual action. The release publishing and updater tooling are
being implemented; these templates alone do not establish a verified release. No
tagged container release has been published through this release process yet.

## Release candidate checks

The current `.github/workflows/release.yml` verifies candidates without publishing
images or a GitHub Release. It accepts strict `vMAJOR.MINOR.PATCH` tags, requires
the tag's full source commit to match the triggering event and be reachable from
`main`, and calls the reusable CI workflow from that same commit. All five CI
jobs remain required, including native iOS and Android/shared checks.

A main-only manual dispatch accepts an unused planned version, defaulting to
`v0.1.0`, without creating a tag or release. It can exercise preparation before
publication is enabled. Its successful checks cannot satisfy the exact-tag
publication gate.

After CI succeeds, the workflow resolves the application base-image indexes and
BuildKit builder once and checks their AMD64 and ARM64 coverage. Separate native
runners build the paired images and retain genuine build records and original
Docker saves. Each runner then collects runtime sources, verifies signed Caddy
sources, applies source/notice overlays, smoke-tests the final configurations,
exports OCI archives without changing tested configuration bytes and independently
replays runtime source correspondence. It also scans the exact archived
application sources and final OCI images, measures compiler/dependency graphs,
and retains selected Go/npm package inputs against committed locks.

Only bounded unsigned records and complete scanner findings are uploaded as
short-lived candidate artifacts. Image archives, source archives and raw compiler
receipts remain private to each runner and are removed after checking; uploaded
summaries cannot replace the full retained inputs required by release assembly
and authenticated gates. The complete AMD64 preparation was exercised locally;
the integrated hosted run and native ARM64 verification remain pending. These
outputs are neither installable bundles nor publication approval.

Docker's local `--load` exporter can report a configuration digest in its
`containerimage.digest` field. Candidate records retain that raw metadata, but
those values must not be used as registry manifest or index digests. The future
publisher must resolve and verify the digests actually stored in the registry.

The workflow has read-only repository permissions and no registry publishing,
attestation or production credentials. Main-branch pushes still run CI only.
Publication will be added after its provenance, version-reservation, dependency
review and source/license gates are implemented; production deployment will
remain a separate manual operation. Tag ancestry checks do not configure branch
or tag protection; those repository settings are a separate maintainer task.

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

After both native jobs finish, `tools/prepare_release_inputs.py` assembles their
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

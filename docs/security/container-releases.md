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

After CI succeeds, it resolves the application base-image indexes and BuildKit
builder once and checks their AMD64 and ARM64 coverage. Separate native runners
build and smoke-test the paired images for each architecture, recording the
pinned bases and actual toolchains. Only candidate metadata is uploaded as
temporary Actions artifacts. Image archives remain private to each runner and
are removed after checking. These outputs are not authenticated release manifests
or installable deployment bundles.

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
remove `ADMIN_PASSWORD` from the environment file after account creation. Existing
installations use their existing accounts and leave bootstrap credentials unset.

```sh
chmod 600 deploy/release.env
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml config --quiet
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml pull
docker compose --env-file deploy/release.env \
  -f deploy/compose.release.yml up -d
```

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

Current bundles have `payload_profile: artifact-foundation`: the production
updater is not implemented. A future deployment-ready bundle must contain its
tracked updater, and the manifest profile must match the archive. Do not execute
bundle tooling as root merely because checksum validation succeeded. Publication
must authenticate the detached manifest, archive and image digests with provenance
from the expected repository, release workflow and source commit. See
[GitHub artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations).
The privileged updater and its verification policy remain pending.

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
complete distribution compliance. Corresponding-source publication, discoverable
hosted source/legal links, Android/iOS notices, and the selected Caddy/Alpine
runtime distribution's obligations remain release gates. Native store terms are
tracked separately and are outside server deployment automation.

GoReleaser retains its binary/archive definitions but no longer publishes images;
the paired container publisher will own GHCR releases. Standalone binary releases
have not been verified by this container work. In particular, the existing
root-level `go mod tidy` pre-hook must be reviewed before enabling binary
publication in this multi-module repository.

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
execution. It uses a randomly named disposable project, an unused private subnet,
a generated administrator password, loopback-only HTTP and dedicated volumes.
It checks metadata, hardening, built HTML/JavaScript, licenses/source metadata,
API/config, authentication, and account/session persistence after removing
bootstrap credentials and recreating the backend. Captured command output is
withheld to protect generated credentials. Cleanup removes only that tool-owned
project's resources and temporary files.

The tool uses locally available images and does not pull them itself. It verifies
an HTTP fixture, not public ACME or a production TLS/account/storage flow. Passing
this check does not replace release provenance verification, ARM64 verification,
complete upgrade/recovery tests, or inspection of an existing installation.

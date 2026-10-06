# Container releases and production deployment

## Objective

Publish psst.zip as an open-source GitHub project with ready-to-run container
images, and make updates to the existing VPS a manually triggered GitHub Actions
deployment. Operators should also be able to install and update over SSH without
connecting their server to GitHub Actions.

The initial target is the existing Debian VPS running Docker Compose and Caddy at
`psst.zip`. Brief maintenance during an update is acceptable for this personal
installation. Kubernetes, automatic updates on every push, and zero-downtime
database migrations are outside this plan.

## Agreed approach

GitHub Container Registry (GHCR) is the primary registry. Docker Hub is an optional
mirror of the same released images; Compose files, deployment scripts and
installation documentation live on GitHub.

```text
Pull request / main → CI
Version tag → CI for that exact commit → publish release images and bundle
Select a release → Deploy production → pull → stopped backup → update → verify
```

Build two images for `linux/amd64` and `linux/arm64`:

| Component                  | Example release image                    | Compose service |
| -------------------------- | ---------------------------------------- | --------------- |
| Backend                    | `ghcr.io/<owner>/psst-zip-backend:1.0.0` | `backend`       |
| Compiled website and Caddy | `ghcr.io/<owner>/psst-zip-web:1.0.0`     | `caddy`         |

Each release records both image digests, its source commit, and its matching
deployment configuration. Production uses those digests rather than floating
tags. A `latest` convenience tag may exist for discovery, but is not the production
update mechanism. Published version tags must not be overwritten.

Public packages can be pulled anonymously; the VPS will not need a registry
credential. GHCR packages initially default to private, so explicitly making both
packages public is a setup step. Publishing from Actions uses `GITHUB_TOKEN`.
See [GitHub's container registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

## Existing deployment to preserve

The VPS currently builds committed sources in `/opt/psst.zip`, uses Compose
project name `psst-zip`, and stores operator configuration in its local `.env`.
The backend database and encrypted files, Caddy certificates, and Caddy
configuration occupy persistent named volumes. The administrator already exists;
updates must not run administrator bootstrap again.

The source-build Compose file mounts the host `Caddyfile` over the image's bundled
file. The release-based setup must explicitly resolve this mount so an old host
file cannot silently override a new release's proxy configuration.

Existing deployment and recovery requirements are documented in
[deployment hardening](../security/deployment.md) and the
[cold backup and restore runbook](../security/backup-restore.md). Reuse these
requirements rather than introducing a competing restore procedure.

## Implementation

### Repository publication checks

- [x] Finalize AGPL-3.0-only licensing before the first push. Rewrite project
      license files and project README declarations throughout the existing
      history as requested, preserving commit metadata and third-party licenses.
      Keep a private pre-rewrite backup outside the published repository.
- [x] Add and install checked-in pre-commit and pre-push hooks with staged secret
      checks, redacted output, sensitive-file restrictions, and staged formatting.
      Use lippycat's hook structure as inspiration while retaining default secret
      detectors and failing when required checking tools are unavailable.
- [x] Repeat repository security checks in CI, using a pinned scanner and full Git
      history; retain both Android and native iOS validation jobs.
- [x] Review existing history with the scanner before the first public push.
      Document only precise verified fixture exceptions, and verify hook behavior
      with disposable regression fixtures including partially staged changes.

Local verification: 16 disposable hook/checker regression tests passed with the
CI-pinned Gitleaks v8.30.1 build. Staged and complete-history scans are required
again after the license rewrite. Repository-security CI is configured; its first
GitHub execution remains pending until publication. Native Android/iOS jobs are
preserved and have not been rerun for these repository-tooling changes.

Publication setup: the public repository is `endorses/psst.zip`. The project
license history was rewritten to AGPL-3.0-only before publication. All 81
pre-publication commits were compared: only the project license and project README
license declaration changed; authors, dates, messages, parent structure, and all
other file contents/modes were preserved. The complete original history and
old/new commit mapping are retained privately outside the published repository.

### Release artifacts and image-based installation

- [ ] Add `deploy/compose.release.yml` using `image:` references for both services,
      with explicit release digests. Retain the source-build `docker-compose.yml`
      for development and operators who build from source.
- [ ] Preserve current non-root users, read-only roots, capability restrictions,
      resource limits, rotating logs, private backend networking, exact proxy
      trust, public ports, and persistent paths. Keep service and volume identities
      compatible with the existing installation.
- [ ] Use the image's bundled Caddy configuration by default. Document deliberate
      operator overrides and include their compatibility in preflight checks.
      Keep hostname and public URL operator-configurable; psst.zip branding must
      not hardcode the hosted instance into reusable deployment files.
- [ ] Supply a release-compatible external TLS gateway overlay. Both proxy hops
      must use the selected web image digest and matching configuration; preserve
      the existing source-build overlay and its private network protections.
- [ ] Define a detached release manifest containing version, source commit, architecture
      coverage, backend/web image digests, deployment bundle checksum, minimum
      Docker/Compose requirements, and migration/rollback notes. Clearly identify
      manifest-index digests versus architecture-specific image digests.
- [ ] Package Compose templates, necessary configuration, and update tooling as
      one versioned release bundle. Publish its authenticated manifest alongside
      it, outside the archive, so the bundle checksum is not self-referential.
      Exclude `.env`, passwords, private keys, database files, uploads, and all
      live installation state.
- [ ] Attach OCI source, version, revision, and AGPL-3.0-only license metadata to both
      images. Record resolved base-image digests and build toolchain versions.
- [ ] Include the license and required dependency notices in distributed images
      and bundles. Publish matching corresponding source, including build/install
      scripts, and provide a source-access link for each hosted version. Account
      for source offers and legal notices in web, Android, and iOS; review native
      store distribution terms separately before any store release.
- [ ] Reconcile `.goreleaser.yml`, whose existing Docker configuration publishes
      only a backend under an older image name. Establish one owner for container
      publication while preserving any intended binary releases and existing
      internal binary/protocol identities.

### CI and publication

- [ ] Refactor `.github/workflows/ci.yml` as needed so a release workflow can gate
      publication on tests for the exact tagged commit, not an unrelated previous
      successful run on `main`.
- [ ] Preserve backend race tests, web checks/unit/browser tests, Android build
      and shared tests, and native iOS app, embedded share extension, and XCTest
      checks. Protocol changes require equivalent Android and iOS work and
      interoperability checks before release.
- [ ] Add `.github/workflows/release.yml` for reviewed `vMAJOR.MINOR.PATCH` tags
      reachable from the protected release branch. Build both architectures with
      Buildx and publish the paired images only after the release gates pass.
- [ ] Smoke-test the final backend and web images together before declaring the
      release deployable. Exercise each advertised architecture using native
      runners or documented emulation, and report which validation was used.
- [ ] Perform the existing dependency and final-image vulnerability review, record
      scanner versions and findings, and resolve or document applicability before
      approving the release. A scanner failure is not a passing result.
- [ ] Reject attempts to overwrite an existing version. Serialize publication and
      make incomplete publication recoverable without advertising a half-built
      image pair as a ready release or advancing convenience tags.
- [ ] Publish verifiable provenance for image digests and the release bundle.
      Define and implement verification of the expected repository, workflow, and
      source commit. Checksums alone are insufficient for authenticity.
- [ ] Limit package write permissions to the publishing job and keep credentials
      out of pull-request jobs. Pin third-party Actions to reviewed commit SHAs;
      do not execute fork code with publishing or production credentials.
- [ ] Make both GHCR packages public, link them to the repository, and test a
      fresh anonymous pull of the complete image pair.
- [ ] Document optional Docker Hub mirroring with a dedicated publishing token.
      Copy the already built release rather than independently rebuilding it;
      record and verify destination digests. A mirror failure must not invalidate
      an otherwise complete GHCR release or silently select a different build.

### VPS update tooling

- [ ] Add an update command shared by manual SSH operation and Actions. Accept a
      validated release identifier from the configured trusted repository;
      constrain registry/image names and reject arbitrary shell arguments,
      filesystem paths, and unverified bundles.
- [ ] Verify release provenance and manifest consistency before executing release
      tooling. Install the privileged entry point as a root-owned helper; any
      update of that helper must follow the same trusted-release boundary.
- [ ] Preflight Docker/Compose compatibility, architecture, actual volume
      mappings/ownership, configuration, required ports, disk headroom for both
      images and the complete backup, and the current deployment state. Pull both
      image digests before stopping services.
- [ ] Acquire a host-side deployment lock and maintain a protected transaction
      record containing current/previous versions, image digests, configuration
      references, and backup location. Refuse a second update or an ambiguous
      interrupted transaction until recovery is explicitly resolved.
- [ ] Pause transfers where appropriate and stop every writer before taking the
      complete cold backup. Capture database/journal and encrypted payloads
      together, both Caddy state volumes, and protected deployment configuration.
      Pause alone is insufficient because cleanup and other writers can continue.
- [ ] Check backup readability, checksums, ownership and permissions before
      proceeding. Keep backups outside the web root, encrypt off-host copies, and
      provide bounded retention without deleting the last known-good checkpoint.
- [ ] Switch the image pair and matching release configuration together, preserving
      `.env`, Compose project identity, existing accounts, storage, limits and
      operator proxy settings. Never use `down -v`, volume pruning, or bootstrap
      passwords as an update step.
- [ ] Start the candidate with public mutations held until checks succeed. Verify
      HTTPS, API/config endpoints, initialized account state, website assets,
      authentication behavior, public download/receive routes, and persisted
      settings. Document which checks are automatic and which require an
      authenticated operator/browser; a metadata health response does not prove
      storage or complete user flows work.
- [ ] Restore the prior pause state only after successful verification. Record
      deployment outcome and versions without logging secret-bearing Compose
      output, session tokens, or private configuration.
- [ ] Implement explicit failure handling: failures before mutation keep or
      restart the previous deployment; failures after backend startup may involve
      automatic schema migration and must not blindly start an older backend.
      Preserve failed state and stop public writes for controlled recovery.
- [ ] Provide an explicit rollback command/runbook using the matching stopped
      checkpoint, configuration and image pair, first restored into isolated
      volumes. Account for lost post-checkpoint changes, potentially restored
      sessions/links and budgets before reopening traffic. Do not overwrite the
      only surviving copy of production data or run two writers on one store.

### GitHub production deployment

- [ ] Add `.github/workflows/deploy.yml` with `workflow_dispatch`, taking a
      published release version. Execute the trusted workflow from the protected
      branch and resolve the selected version to its verified manifest/digests;
      deploying arbitrary branch builds is outside this workflow.
- [ ] Configure a `production` environment with an allowed deployment branch,
      VPS host/user variables, a separate deployment SSH private-key secret, and
      an independently verified pinned SSH host key. Required reviewers are
      optional for this personal instance; manual dispatch is the normal gate.
      See [GitHub deployment environments](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments).
- [ ] Provision a dedicated deployment SSH identity constrained to the approved
      update command, with no general shell access. If sudo is necessary, allow
      only the root-owned helper. Docker group membership grants broad host
      control and is not a substitute for this restriction. Retain the user's
      separate interactive SSH key for maintenance and recovery.
- [ ] Verify the pinned host key on every CI connection. Do not disable host-key
      checking or establish trust from an unverified `ssh-keyscan` during a run.
- [ ] Use GitHub-hosted runners, minimum token permissions, and per-production
      concurrency with no cancellation halfway through a deployment. Handle
      connection loss using the durable host transaction record. Do not install
      a public-project self-hosted runner on the production VPS.
- [ ] Ensure fork/PR workflows cannot obtain environment secrets or invoke the
      deployment helper. Document key rotation/revocation and recovery access.
      Follow [GitHub's secure Actions guidance](https://docs.github.com/en/actions/reference/security/secure-use).

### Documentation and current VPS migration

- [ ] Add a self-hosting quick start covering DNS, ports, Docker/Compose, public
      image pulls, persistent storage, first-admin creation directly on the host,
      and removal of bootstrap credentials after initialization.
- [ ] Document the maintainer path from reviewed commit to version tag to ready
      release, and the operator path for manual SSH updates, Actions deployment,
      maintenance, backup, failure recovery, and rollback.
- [ ] Choose the GitHub owner/repository, package namespace, and initial release
      version; create the remote and configure branch/tag protection. Review the
      repository and release inputs for accidental private deployment state
      before making the project public.
- [ ] Inspect the live installation's actual volume mappings and settings before
      migration. Preserve `/opt/psst.zip`, project name `psst-zip`, and the original
      local images/configuration as the first migration recovery baseline.
- [ ] Configure and verify protected encrypted off-host backups, including a
      restore exercise, before enabling production deployment automation.
- [ ] Publish and test the first release, perform a manual migration using the
      shared update command, and verify existing accounts, settings, files, links,
      and TLS state survive. Do not recreate the administrator.
- [ ] Configure the environment and restricted deployment key, then exercise the
      Actions deployment path with a subsequent tested release. Never upload the
      VPS `.env`, data volumes, or personal maintenance key to GitHub.

## Verification and completion criteria

Implementation and live rollout are separate gates. Mark tasks complete only
when their work and applicable checks have actually finished. GitHub account
setup, registry publication, production secrets, and live migration remain
pending until performed in those environments.

- [ ] Validate workflow syntax, release manifest parsing, Compose configuration,
      bundle contents, and image metadata without exposing live secrets.
- [ ] Exercise fresh installation and a repeatable upgrade in disposable stacks
      using fixture accounts and encrypted payloads. Verify preserved settings,
      quota/expiry state, sessions, revoked capabilities, upload/download/receive,
      cleanup, and restart behavior across the update.
- [ ] Exercise the release-compatible external-proxy configuration and managed
      certificate persistence. Adapt the existing disposable proxy/restore tests;
      test certificate storage separately from actual public ACME renewal.
- [ ] Inject failed pulls, invalid provenance/manifests, insufficient backup space,
      backup/configuration failures, migration/startup failures, failed health
      checks, simultaneous deployments, and interrupted connections. Confirm the
      documented recovery state and absence of destructive automatic rollback.
- [ ] Complete a matching-checkpoint isolated restore and rollback exercise,
      preserving the original state and checking security reconciliation before
      accepting traffic.
- [ ] Run the existing backend/web/Android/iOS release gates for the candidate.
      Keep native iOS validation pending until macOS/Xcode builds both the app and
      embedded share extension and runs XCTest. Record exact commands and runner
      requirements in the release documentation; Linux checks do not replace it.
- [ ] Demonstrate anonymous installation from the published release bundle and
      images, followed by a successful manually selected production update and a
      verified recovery path. Document any remaining operator-only checks.

This plan does not include Play Store/App Store distribution or native signing
automation. It preserves both mobile platforms' existing release validation and
shared protocol compatibility while adding server image publication/deployment.

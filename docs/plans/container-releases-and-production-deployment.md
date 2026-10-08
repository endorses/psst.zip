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

Publication does not require a private storage bucket. On 2026-10-08 the operator
chose GHCR for images and GitHub Releases for durable public release files,
superseding the private S3 publication-retention prerequisite below. Publishing
keeps its local transaction journal and immutable inputs; bounded Actions
diagnostics are temporary, best-effort records. VPS backups remain a separate
recovery requirement and can use a suitable off-host destination without S3.

```text
Pull request / main → CI
Version tag → exact-commit CI + native preparation → reviewed publication
Select a release → Deploy production → pull → stopped backup → update → verify
```

After source identity validation, CI and native preparation can overlap.
Assembly requires both to succeed; a failed or incomplete CI cannot produce the
source-CI approval report or reach publication. Preparation may consume runner
time after a CI failure. Whole-workflow publication serialization remains in place.

On 2026-10-08 the operator agreed to separate mobile validation from server
publication. Container releases require repository security, backend and web CI
on the exact source commit. Routine application CI retains Android/shared and
iOS checks; no mobile app is published by this plan. Earlier five-job release
gate checkpoints below describe the previous contract and are superseded by the
server-only release validation checkpoint.

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

The identity retcon changes fresh-install storage defaults. Before the next VPS
update, configure `BACKEND_DATA_VOLUME` and `DB_PATH` from the running installation
to retain its physical volume and database. The release setup must carry these
overrides forward and verify existing data, not create a new empty store.

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
CI-pinned Gitleaks v8.30.1 build. Staged and complete-history scans passed again
after the license rewrite. The first GitHub repository-security job passed.
Existing web and native SDK setup failures were discovered during that initial
run and are tracked below.

Publication setup: the public repository is `endorses/psst.zip`. The project
license history was rewritten to AGPL-3.0-only before publication. All 81
pre-publication commits were compared: only the project license and project README
license declaration changed; authors, dates, messages, parent structure, and all
other file contents/modes were preserved. The complete original history and
old/new commit mapping are retained privately outside the published repository.

### Initial CI repairs

- [x] Configure both Android and iOS SDK setup to request `platform-tools`
      explicitly, avoiding the action's obsolete `tools` default. Pin the verified
      SDK setup action revision.
- [x] Pass complete fixture key pairs to independent WebCrypto HPKE tests so they
      work on Node 22 without exporting non-extractable private keys. Preserve the
      RFC9180, shared mobile interoperability fixtures, and authentication checks.
- [x] Verify the repaired web job on GitHub, including its browser checks.
- [x] Verify Android assembly/shared tests and native iOS app, embedded share
      extension, and XCTest checks on GitHub after the SDK setup fix.

Local repair verification: all 110 web integration tests passed on Node 22.23.3;
all six receive-crypto tests also passed on Node 26.10.0. `npm run check` reported
zero errors and warnings. Workflow YAML and both explicit mobile SDK requests
were checked. Native builds/tests remain pending until the GitHub jobs run;
changing SDK setup does not establish native build success.

### Backend race-test timing repairs

- [x] Give the complete CI race suite an explicit bounded package timeout and
      limit simultaneous package runs to reduce database-heavy contention.
      Preserve every test, real migrations, and retention boundary workloads.
- [x] Separate reconciliation batch-contract validation from the production
      two-second time budget. Retain the 64-row, restart, busy-resource and cursor
      assertions, plus production-entry-point cancellation/deadline checks.
- [x] Verify backend lint and the complete race suite locally, and repeat the
      affected reconciliation checks.
- [x] Verify the backend GitHub job after publication.

Diagnosis: the initial GitHub API and database test binaries each exceeded Go's
default ten-minute package limit. The named tests had only just started and were
running migrations. The two large database history retention checks passed but
took 119 and 63 seconds. The reconciliation contract test also assumed all 64
rows would finish within the production two-second sweep budget under race
instrumentation. These repairs do not change production time limits, schema
migrations, retention thresholds, or the mobile protocol.

The workflow now uses `-race -p 2 -timeout 30m`. Local lint passed using CI's Go
1.26.8 toolchain. The affected reconciliation checks passed three times with race
detection. A prepared-statement experiment did not improve the count-retention
test because the SQLite driver reparses SQL on execution; that experiment was
removed, preserving all 10,010 update mutations and the 100,010-event global
retention test. The complete local race suite passed on Go 1.27.1 with the same
`-race -p 2 -timeout 30m` flags (API package: 379 seconds; database package: 527
seconds). GitHub's backend job also passed on CI's Go 1.26.8 in run `37423344109`.

The fixes are published in `d1e4268`; GitHub run `37423344109` passed repository
security, Android/shared and the complete backend lint/race/build checks.
Broader CI remains blocked by a separate native iOS compilation failure:
`CryptoProvider.ios.kt` cannot resolve `CCCryptorGCMOneshotEncrypt` and
`CCCryptorGCMOneshotDecrypt`. Per the project's unexpected-issues instruction,
that separate failure was recorded without changing mobile cryptography in this
backend timing repair.

### Performance and mobile CI repairs (initially held locally)

The performance/mobile repair `3a05f71` is now published. GitHub run
[`37433228153`](https://github.com/endorses/psst.zip/actions/runs/37433228153)
passed repository security, backend, Android/shared and the complete main
browser suite. The separate administrator lifecycle stopped before test setup,
and the native iOS build failed while compiling the share-extension storyboard.
At that checkpoint, further fixes were held locally and native validation was
pending. Subsequent authorized pushes and successful macOS runs resolved these
gates, as recorded below.

- [x] Replace unresolved iOS CommonCrypto GCM functions with the pinned maintained
      CryptoKit provider and selected-Xcode Swift linker configuration. Preserve
      AES-256 keys, 12-byte nonces, appended 16-byte tags, empty-message
      authentication, and both app/share-extension use of the shared framework.
- [x] Add independent AES-GCM vectors and modified-input rejection checks for
      Android/shared tests and native iOS XCTest through the exported provider.
- [x] Rebuild Android and run all shared tests after the dependency change.
      Android assembly and 175 shared tests passed, including four new AES-GCM
      compatibility tests with no failures or errors.
- [x] Repair missing portable Swift harness dependencies and verify the affected
      suites, source gates, localization and Swift syntax parsing. All 171 tests
      across 12 portable harnesses passed; the crypto harness uses official Swift
      6.2.4 to resolve the older image's Observation runtime linker failure.
- [x] Diagnose and fix reproducible browser-suite failures, retaining test
      coverage, real authentication limits and a disposable backend.
- [x] Verify complete browser checks, the opt-in administrator lifecycle, web
      type checks and production build locally. Type checks and production build
      passed; the initial full browser rerun had 187 passes, five existing opt-in
      skips and a page-loading failure. Tracing identified Chromium host network
      changes cancelling local module requests. The final unchanged browser
      suite passed in a temporary loopback-only namespace: 188 tests, five
      existing opt-in skips, zero failures, four minutes. The separate
      administrator lifecycle passed all three tests in 34.131 seconds. Final
      web type checks reported zero errors/warnings and the static build passed.
- [x] On macOS/Xcode, build the app and embedded share extension and run native
      XCTest, including the new exported-provider compatibility tests. Use the
      commands in `ios/README.md` and CI's selected Xcode 26.0.1 toolchain.
- [x] Publish the complete repair following the user's later authorization to
      push verified CI fixes; verify all five jobs on the exact pushed commit.

The complete Go 1.26.8 backend race suite passed after all fixture changes:
698 cases in 216.919 seconds (3 minutes 37 seconds), versus 371.634 seconds
before the bulk-fixture pass with the same toolchain and flags. Database took
186.463 seconds versus 350.531 seconds. Lint reported zero issues and the
production backend build passed.

Performance work and exact local measurements are recorded in
[`test-suite-performance.md`](test-suite-performance.md). The user approved
expanded localhost test execution and local Git writes, resolving the earlier
sandbox restrictions. Native iOS validation required macOS/Xcode and subsequently passed on GitHub.
The performance repair was originally committed as `3a05f71`; the follow-up fixes
described below were subsequently published with the user's authorization.

### Follow-up CI fixes after the performance run

- [x] Diagnose the failed steps in run `37433228153`. The main browser suite
      passed; the administrator lifecycle was rejected because its state file
      used GitHub's `runner.temp` while Node used its default temporary root.
      iOS `ibtool` rejected the storyboard's `AppleSDK` target runtime.
- [x] Set `TMPDIR` to `runner.temp` for the administrator lifecycle step so the
      disposable database, state marker and safety checks share the same root.
      Retain the private marker and harness-owned database restrictions.
- [x] Verify the three real administrator lifecycle tests using the exact
      workflow environment with a separate temporary runner directory. All
      three passed in 33.748 seconds, with marker/database cleanup verified.
- [x] Correct the share-extension storyboard runtime while preserving its
      controller and extension entry wiring. XML parsing and iOS source gates
      passed; the gate now rejects invalid runtime/controller declarations.
- [x] On macOS/Xcode, verify the repaired app/extension build and native XCTest
      through the existing CI commands. Linux source checks do not replace this.
- [x] Commit verified follow-up fixes locally. Installed security/format hooks
      passed, temporary diagnostic files were removed, and nothing was pushed.

### Native error bridge after the second CI run

Run [`37435387491`](https://github.com/endorses/psst.zip/actions/runs/37435387491)
on `eb635d3` passed web (including administrator lifecycle), backend,
Android/shared and repository security. The corrected share-extension storyboard
compiled. Kotlin framework generation completed, but native Swift compilation
stopped in `ClientErrorPresentation.swift`: `NSError.kotlinException` is exported
as `Any`, while `FailureDescriptions.describe(error:)` requires `KotlinThrowable`.

- [x] Identify the exact native compiler error and check neighboring bridge uses.
      `TransferTrafficRecovery` already uses a conditional `KotlinThrowable` cast.
- [x] Cast only genuine Kotlin throwables before invoking the shared error
      classifier; preserve fallback behavior for unclassified platform failures.
- [x] Compile the actual Foundation presenter with a minimal portable bridge
      target exposing the observed types. Check classified and unknown failures;
      do not present this as a generated Kotlin/Native or native Xcode build.
      All 18 presenter/localization tests passed, including four bridge regressions.
- [x] Verify iOS source/localization gates and formatting. Both gates passed;
      Swift/Python/Markdown formatting and whitespace checks passed.
- [x] Verify the app, embedded share extension and native XCTest on macOS/Xcode.
      Native execution remains pending until the corresponding job passes.
- [x] Commit verified bridge repairs locally. Installed security/format hooks
      passed. Clean task-owned temporary artifacts and keep the commit unpushed.

### Native client creation and observer cleanup

Run [`37437714861`](https://github.com/endorses/psst.zip/actions/runs/37437714861)
on `9b8db47` passed all four Linux jobs. Native compilation progressed past the
error presenter and reported actor-isolated observer access from
`ServerConfigManager.deinit`, plus an unavailable `HttpClientFactoryKt` facade.

- [x] Move notification registration cleanup into a Foundation-only owner whose
      destructor can run on any executor. Retain token/server-scoped session
      guards and the scanner's serial capture dispatch. Apply the same ownership
      repair to the scanner's equivalent notification cleanup.
- [x] Add a shared `ApiClient(config, sessionToken)` constructor backed by the
      platform HTTP client and replace all four obsolete Swift factory references.
      Keep explicit test-client construction and anonymous client scope intact.
- [x] Verify Android assembly and shared tests after the common API change.
      Assembly and all 175 shared tests passed, with zero failures/errors.
- [x] Verify observer cleanup under Swift 6 strict concurrency. All four lifetime
      checks passed, including final release off the actor and sibling isolation.
      A negative control confirms unsafe actor-owned-token cleanup is rejected.
- [x] Run iOS source/localization gates and format the changed Kotlin, Swift and
      Python files. These checks do not constitute native UIKit compilation.
- [x] Verify the native app, embedded extension and XCTest with macOS/Xcode.
      The Kotlin-to-Swift constructor export and UIKit compile passed in the
      successful native run recorded below.
- [x] Commit the verified client/lifetime repairs locally. Installed security/format
      hooks passed. Clean task-owned temporary artifacts and keep the commit unpushed.

### Scanner callback and image importer follow-up

Run [`37440453480`](https://github.com/endorses/psst.zip/actions/runs/37440453480)
on `c18b364` passed all four Linux jobs. The native compiler accepted the observer
owner and platform-client constructor, then reported three implicit captures in
the scanner's nested main-actor notification callback.

- [x] Qualify the scanner's active state, serial queue and error presentation with
      explicit `self` references. Preserve weak capture and existing camera behavior.
- [x] Format the scanner and run iOS source/localization checks. Both gates passed.
- [x] Commit and push the scanner capture repair, as authorized on October 6.
      Commit `f22959b` passed installed security/format hooks and was pushed.
- [x] Fix the single-file QR image importer's URL result handling. Run
      [`37441541438`](https://github.com/endorses/psst.zip/actions/runs/37441541438)
      compiled the scanner and then reported an array-only `.first` access on
      the main app's single `URL` result. Use the same direct URL handling as
      the existing login image picker.
- [x] Format the image picker and run iOS source/localization checks. Both gates passed.
- [x] Commit and push the verified image importer repair. Commit `6831afc` passed
      the installed security/format hooks and was pushed.
- [x] Verify the app, embedded extension and XCTest on the next macOS CI run.

### Native XCTest numeric assertion follow-up

Run [`37442272581`](https://github.com/endorses/psst.zip/actions/runs/37442272581)
on `6831afc` compiled the app and embedded extension. The test build then reported
an `Int` expected value in a streamed-file assertion comparing `[Int64]` sizes.

- [x] Give the streamed-file expected size an explicit `Int64` type; preserve the
      sparse-file, raised-limit and processing-ceiling assertions.
- [x] Format the XCTest source and run the iOS source/localization gates. Both passed.
- [x] Commit and push the verified XCTest compile repair. Commit `9993fb7` passed
      installed security/format hooks and was pushed.
- [x] Verify the full build and native XCTest execution on macOS CI.

### Native XCTest runtime fixtures and signing

Run [`37443155590`](https://github.com/endorses/psst.zip/actions/runs/37443155590)
on `9993fb7` passed all four Linux jobs and the complete native build, including
the embedded extension. Native XCTest ran 171 tests in approximately 96 seconds
and reported seven failures across five cases: three QR payload assertions,
two history fixture expectations, and two secure-storage errors.

- [x] Correct the history fixtures: expect the catalog's singular file count and
      persist the concurrent local rename using coordinated mutation before a
      stale incoming update. Strengthen unloaded-record and label assertions.
- [x] Enable ad hoc simulator signing and verify app/extension signatures before
      testing. The unsigned run failed at shared Keychain writes; missing access
      entitlements are the suspected cause. Keep production secure storage intact
      and clean the oversized-inbox fixture's vault key after the test.
- [x] Keep exact Vision QR decoding assertions and add plain/branded decoder
      diagnostics, pixel metadata and retained images for the next native run.
- [x] Format changes and run source/localization/YAML checks. All passed.
- [x] Commit and push the verified runtime fixtures/signing diagnostics. Commit
      `007e4e2` passed security/format hooks and was pushed.
- [x] Verify shared Keychain tests, history fixtures and all QR payload decoding
      on macOS; finish any repair indicated by the diagnostics.
- [x] Verify the complete GitHub workflow succeeds for the final pushed commit.

### Native QR decoder fallback and simulator startup

Run [`37445042420`](https://github.com/endorses/psst.zip/actions/runs/37445042420)
on `007e4e2` passed all four Linux jobs, signed native compilation and signature
verification. The history and real shared Keychain tests passed. Only the three
QR payload assertions failed among 171 tests (approximately 120 seconds).
Vision also failed to decode plain control QRs; Core Image decoded every branded
payload exactly. The long native test step spent nearly six minutes starting
the simulator before the actual suite began; oversized migration took 31 seconds.

- [x] Use a Core Image fallback for still-image QR decoding when Vision produces
      no payloads or cannot process the image. Preserve ambiguity rejection,
      payload/file/pixel limits and security-scoped URL handling.
- [x] Add native tests of complete branded download/upload/pairing payloads through the actual
      production image reader, plus rejection of blank/multiple-code images.
      Implementation is complete; native execution remains pending below.
- [x] Start simulator boot after selection so it overlaps Kotlin/Swift compilation.
      Preserve the same installed runtime, destination and full XCTest target.
- [x] Format changes and run source/localization/YAML gates. All passed.
- [x] Commit and push the verified QR fallback/startup changes.
- [x] Verify the complete native suite and all five jobs on the final commit.

### Successful GitHub CI verification

Run [`37447273776`](https://github.com/endorses/psst.zip/actions/runs/37447273776)
completed successfully on `c6b322240edc5f62b416610f5316a8761765870c`.
All five jobs completed with `success`: Repository security, Backend, Web,
Android/shared, and native iOS. The native job built the app, embedded extension
and test products, verified both simulator signatures, and executed all
173 XCTest cases with zero failures in 127.346 seconds. Exact branded QR
payloads, blank/multiple-code rejection, real shared Keychain storage and
history migration/concurrent-label cases passed. The result bundle was retained.

This run resolves the earlier pending CI build/XCTest gates above; historical
failure descriptions retain the evidence that led to each repair. Simulator
boot overlapped compilation; startup from the test command to the suite fell
from about 352 seconds in the preceding run to about 52 seconds. Total native
job duration remained about 15 minutes, so this does not establish a comparable
overall build-time improvement. Backend completed in 6m38s, Web in 6m19s,
Android/shared in 2m49s, and security in 40s.

- [x] Verify the complete public workflow against its exact pushed commit and
      inspect the native test totals and critical regression results.
- [x] Update verified CI task status without claiming separate language-launch,
      physical-device, App Store signing or production deployment checks passed.
- [x] Commit this documentation-only completion record locally; installed security/format
      hooks passed. At that checkpoint, the final pushed CI repair was `c6b3222`.

### Current CI checkpoint after identity cleanup

The identity rewrite and subsequent test synchronization repair are published.
Run [`37501740872`](https://github.com/endorses/psst.zip/actions/runs/37501740872)
passed all five jobs on `7e61e6073bfa3782fa78f1f3f1f492fa21c699b9`:
Repository security (41s), Android/shared (3m1s), Web (5m36s), Backend (6m44s),
and native iOS app, extension and XCTest (12m40s). The Web test now waits for a
completed title save before reloading; a controlled delayed save reproduced the
old failure and passed with the repair. This resolves the earlier CI/no-push
status notes; historical run descriptions above retain their original evidence.

Container publication, registry visibility, protected release/deployment setup,
and production migration remain separate, unperformed gates. Passing source CI
does not establish that release images or an updater have been verified.

### Release artifacts and image-based installation

- [x] Add `deploy/compose.release.yml` using `image:` references for both services,
      with explicit release digests. Retain the source-build `docker-compose.yml`
      for development and operators who build from source.
- [x] Preserve current non-root users, read-only roots, capability restrictions,
      resource limits, rotating logs, private backend networking, exact proxy
      trust, public ports, and persistent paths. Keep service and volume identities
      compatible with the existing installation.
- [x] Use the image's bundled Caddy configuration by default and document deliberate
      operator overrides. Keep hostname and public URL operator-configurable.
- [ ] Include active operator Caddy overrides and their compatibility in updater
      preflight checks before migrating an installation.
- [x] Supply a release-compatible external TLS gateway overlay. Both proxy hops
      must use the selected web image digest and matching configuration; preserve
      the existing source-build overlay and its private network protections.
- [x] Define a detached release manifest containing version, source commit, architecture
      coverage, backend/web image digests, deployment bundle checksum, minimum
      Docker/Compose requirements, and migration/rollback notes. Clearly identify
      manifest-index digests versus architecture-specific image digests.
- [ ] Package Compose templates, necessary configuration, and update tooling as
      one versioned release bundle. Publish its authenticated manifest alongside
      it, outside the archive, so the bundle checksum is not self-referential.
      Exclude `.env`, passwords, private keys, database files, uploads, and all
      live installation state.
- [x] Attach OCI source, version, revision, and AGPL-3.0-only license metadata to both
      images; allow digest-pinned build bases and define build records in the manifest.
- [ ] Record actual resolved bases and toolchain versions for each published release,
      and verify both advertised architectures before publication.
- [ ] Include the license and required dependency notices in distributed images
      and bundles. Publish matching corresponding source, including build/install
      scripts, and provide a source-access link for each hosted version. Account
      for source offers and legal notices in web, Android, and iOS; review native
      store distribution terms separately before any store release.
- [x] Remove GoReleaser's backend-only Docker publication while retaining binary
      builds and archives with license notices. The paired release workflow will
      own container publication. Standalone binary release execution remains unverified.

### Release artifact foundation checkpoint

The image-based Compose templates, external gateway overlay, strict detached
manifest contract and deterministic allowlisted bundle builder are implemented.
Bundles currently declare `artifact-foundation`; the shared production updater
and authenticated publication remain unfinished, so these are not deployable
released bundles. Preparation does not modify a production installation.

Application dependency inventories and full available license/notice texts are
included in both images and the bundle. CI checks inventory freshness against
locked dependencies, and checks named operator environment files as sensitive
inputs. Generated notices normalize whitespace without changing legal words;
inventories retain upstream and distributed-text hashes. Hosted legal/source
discovery, both native clients' notices and selected
runtime/base distribution obligations still require the separate licensing gate.

GoReleaser no longer competes for image publication. Its existing root-level
`go mod tidy` hook is not verified for standalone binary releases; review that
pre-hook before enabling binary publication. It is not used by container builds.

Verification completed: 18 manifest/archive boundary tests and seven Compose
regression tests passed in 2.902s; all 17 repository security tests passed in
2.328s. Both images built from resolved official base-image index digests. The
native `linux/amd64` smoke exercised image labels/non-root users, hardening,
loopback-only ingress, compiled HTML and JavaScript, application and dependency
license hashes, served source metadata, API/config, administrator authentication
and persistence of the account/session after removing bootstrap credentials and
recreating the backend. All fixture containers, volumes, networks and temporary
files were removed. Dependency notice checks and formatting passed.

This checkpoint is committed as `6c384e4`. The complete-history security scan
passed. Building a bundle twice from that committed tree produced identical
bytes with 42 allowlisted members and the exact source commit; both temporary
archives were removed. The two local smoke image tags were removed as well.

ARM64 execution, public registry pulls, provenance, public ACME renewal, complete
license/source-offer review, update/rollback fault injection, and live migration
remain pending. The previous successful GitHub run covers its recorded source
commit; the new CI steps are not claimed to have run on GitHub yet.

### CI and publication

- [x] Refactor `.github/workflows/ci.yml` as needed so a release workflow can gate
      publication on tests for the exact tagged commit, not an unrelated previous
      successful run on `main`.
- [x] Preserve backend race tests, web checks/unit/browser tests, Android build
      and shared tests, and native iOS app, embedded share extension, and XCTest
      checks. Protocol changes require equivalent Android and iOS work and
      interoperability checks before release.
- [x] Implement a read-only release-candidate workflow for strict version tags
      reachable from `main`, gated by the reusable CI at the exact tagged commit.
      Resolve immutable application and BuildKit bases once, verify both platform
      descriptors, and define native AMD64/ARM64 paired image builds and smoke
      checks. Upload metadata only while distribution gates remain unfinished.
- [x] Run the new reusable CI and complete candidate workflow on GitHub, including
      the native ARM64 image pair. Local structural checks do not establish that
      this new workflow passed on hosted runners.
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
- [x] Document optional Docker Hub mirroring with a dedicated publishing token.
      Copy the already built release rather than independently rebuilding it;
      record and verify destination digests. A mirror failure must not invalidate
      an otherwise complete GHCR release or silently select a different build.

The [optional mirror guide](../security/docker-hub-mirroring.md) defines a
serialized paired copy with preserved index/child digests, independent credentials,
anonymous complete readback, and explicit partial-copy recovery. It records that
Docker Hub setup and live verification have not happened; the documentation
checkbox does not assert a working mirror or a published primary release.

### Read-only release candidate checkpoint

At this initial checkpoint, the release workflow verified candidates without
publishing images, releases, attestations or deployment bundles. It had
`contents: read` permissions and no registry, production or signing credentials.
All Actions in CI and the candidate workflow were pinned to verified upstream
commits, and checkouts disabled
persisted credentials. All five source CI jobs and their commands were preserved.

Strict tag/event/ancestry checks precede reusable CI. At this initial checkpoint,
the two native build jobs were gated on all CI jobs and shared resolved index digests for Go, Alpine, Node,
Caddy and BuildKit. Build records include actual toolchain output and local image
metadata, explicitly separated from future registry manifest/index digests.
At this initial checkpoint, candidate metadata had one-day artifact retention
and image archives stayed on the runner. The subsequent local cross-job assembly
implementation retains explicitly inventoried native image/source inputs for one
day, bound to the same run and attempt; its complete hosted execution is still
pending. No candidate is advertised as deployment-ready. Branch/tag protection
remains an unperformed repository setup.

Verification: eight candidate boundary regressions passed. Real resolution of all
five official multi-platform indexes passed, including immutable reinspection and
AMD64/ARM64 descriptor coverage. A disposable scratch image confirmed actual
Buildx `--load` metadata fields and was cleaned up. The source CI refactor passed
parsed-YAML preservation checks; both workflows passed actionlint v1.7.12 and
formatting. All 33 release/candidate/Compose tests passed in 3.09s. Complete hosted CI,
the native candidate matrix, publication and production deployment remain pending.

### Source discovery, runtime collection and updater implementation checkpoint

- [x] Implement hosted source discovery and bundled license access on the web,
      Android settings, iOS settings and iOS share extension, preserving operator
      URLs and identifying exact clean build revisions.
- [x] Generate native inventories from actual Android release-runtime and all
      three iOS klib artifact graphs, preserving full available notices and pinned
      Kotlin/Native and SKIE runtime notice sources. Add freshness checks to CI.
- [x] Verify web source metadata boundaries and browser discovery, Android build,
      focused JVM tests and packaged APK assets, portable Swift metadata tests,
      localization/source checks and resolved native notice freshness locally.
- [x] Verify the new iOS views compile and actual app/share-extension legal
      resource packaging assertions pass on macOS CI. Run
      [37669775085](https://github.com/endorses/psst.zip/actions/runs/37669775085)
      passed all five jobs on `b697119ba81c3aaf18725f38869b778f2e59df9d`.
      All 176 native XCTest cases passed in 132.951 seconds, including the
      real app and embedded extension license/notices/inventory/source-resource
      test and source metadata boundaries. This does not establish physical-device
      navigation, App Store signing or store distribution approval.
- [x] Implement isolated APK source collection bound to exact installed origin,
      version and aports commit, including lower-layer package versions, original
      source checksum verification and local helper copyright notices.
- [x] Verify all-package collection on the pinned Alpine image and exact official
      Caddy source/binary/module binding. Outputs remain review inputs; do not
      publish them or declare distribution compliance from collection alone.
- [ ] Complete final backend/web runtime notice review, upstream source/signature
      review, corresponding-source publication and served runtime source offers.
- [x] Implement the root-owned version-only updater, durable transaction/lock,
      fixed provenance policy, stopped checkpoints, isolated candidates and
      controlled restore, with a protected automatic verification hook or an
      explicit pending local verification gate.
- [x] Resolve the four bounded independent updater review findings and pass their
      focused regressions: recovery health failure, active-version identity,
      verified-candidate failure handling and effective service user overrides.
- [ ] Exercise actual disposable Docker upgrade/restore flows, public attestations,
      encrypted off-host checkpoint hooks, restricted SSH installation and live
      adoption before closing the updater or production deployment requirements.

The authorized push published `6c384e4` and `4210414` plus the verification-only
tag `v0.0.0`. Main CI run
[`37574199055`](https://github.com/endorses/psst.zip/actions/runs/37574199055)
passed all five jobs at `4210414`, including native iOS. The separate tagged
candidate run
[`37574199465`](https://github.com/endorses/psst.zip/actions/runs/37574199465)
timed out during XCTest launch after the iOS job's 45-minute limit on its first
attempt. GitHub's annotation confirms the limit; the same source commit's main
CI passed. Attempt two completed successfully, including iOS and both native
AMD64/ARM64 image pair builds and disposable smoke verification.
The first attempt's diagnostic session connected to testmanagerd and installed
the app, then stalled at the debugger-assisted application launch before any
XCTest output. This success verifies the exact `4210414` candidate; it does not
verify subsequent local commits or establish publication readiness.

Native notice snapshots cover 115 Android and 111 iOS artifact variants, with
twenty pinned upstream notice files. Linux verification includes actual Android
packaging and the portable Swift helper, not iOS UI execution. The web advertises
backend/runtime notice links only when the deployment metadata declares those
files; the web image copies the backend notices it declares.

The final private AMD64 runtime collection covers seventeen retained package
versions from eleven backend origins and thirty-two from twenty web origins,
including lower layers. A checksum-bound evidence pack retains twenty-five
upstream legal documents and exact recipe revisions for the previously missing
notice origins. Two malformed upstream BusyBox test fixtures remain byte-exact
and explicitly scoped. No blanket archive or APK signature waiver was added.
The original MPL/BSD license whitespace is preserved through narrow
`.gitattributes` exceptions; CI verifies hashes and secret detectors remain enabled.

Actual Caddy source signatures, workflow/ref/commit identity, checksum bindings,
and executable/source correspondence passed with pinned cosign 2.6.5. The
complete runtime source pack binds installed/retained APK versions, exact source
recipes, Caddy source and all served notices. Both final AMD64 overlays passed
native HTTP smoke checks and exact OCI config/blob/decompressed layer checks.
The `/legal` page exposes runtime notices through preserved release metadata;
two browser tests passed. Forty focused legal/collector/signature/packaging tests
passed. Source packs remain private, with publication and distribution review
explicitly pending. ARM64 and actual verified public source delivery are unrun.

The authenticated final AMD64 image scan found fixable OpenSSL/zlib runtime
findings. See the [dependency review](../security/container-dependency-review.md)
for exact subjects, scanner/database identities and package findings. These
candidates are not approved for distribution; refresh the runtime dependencies,
rebuild and repeat source correspondence and final scans before publishing.

All 31 updater boundary/fault tests passed, including real filesystem/SQLite
checks and simulated Docker/GitHub failures. The complete 64-test release suite
passed in 3.642 seconds. These establish implementation boundaries; actual Docker
and production behavior remain pending. Bundles default to `artifact-foundation`
even with a committed updater; readiness is an explicit gated publisher decision.
The privileged helper is never silently replaced by code from an application
update. Store strategy research remains uncommitted as requested.

The implementation checkpoint is committed locally as `c1c1ea9`. Its disposable
AMD64 image pair passed `tools/verify_release_images.py`, including served backend
notices matching the actual backend image, exact source metadata and initialized
account/session persistence after removing bootstrap credentials. This verifies
that local pair; it does not replace ARM64, public provenance or upgrade checks.

Dependency review found `source-map-js` 1.2.1 affected by
[GHSA-68fv-2mgg-jv7q](https://github.com/advisories/GHSA-68fv-2mgg-jv7q).
The lockfile now selects the upstream
[1.2.2 patch](https://github.com/7rulnik/source-map-js/releases/tag/v1.2.2), and its
distributed notices/inventory were regenerated. On 2026-10-07, npm 12.1.0 with
Node 26.10.0 reported zero vulnerabilities across 173 dependencies; all 114 web
tests passed in 1.38 seconds, Svelte checking reported no errors or warnings, and
the production build passed. The transfer fixture requires the already-approved
localhost access outside the sandbox. Release Node 22 CI, Go analysis and final
image review remain separate pending checks.

### VPS update tooling requirements

- [x] Add an update command shared by manual SSH operation and Actions. Accept a
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

The disposable root/Docker integration gate now completes public activation,
repeat updates, isolated restoration and independently checked encrypted transfer
flows. It verifies authentication/TOTP, TUS, RFC 9180 HPKE receive decryption,
quota/expiry/revocation, cleanup, budget denial, persisted settings and restart.
The normal activation/update/restore gate passed in 137.7 seconds. A genuine
historical source baseline (`2ed02af`, before the history-sync migration) passed
migration, activation, repeat update and restoration in 153.1 seconds; the actual
SQLite migration count increased by one and the old CLI rejected the migrated
original. The corresponding injected post-startup failure/recovery gate passed
in 79.6 seconds, preserving the failed original and checkpoint and activating
only fresh restored volumes after verification.

Two production corrections preserve operator settings while excluding the
validated history-sync protocol capability, and allow only authenticated,
restore-gated paused budget decreases needed for security reconciliation. The
fixture compares independently recorded HTTP statuses and ciphertext hashes.
The complete release suite has 123 passing tests, including 35 updater tests.
Local acquisition substitutes immutable fixture image IDs for public release
verification. These results establish isolated Docker behavior; public release
provenance, independent off-host provider recovery, full browser/mobile flows,
installed restricted SSH and VPS adoption remain pending.

Publication preparation now has a separate core and sixteen boundary tests. It
binds reviewed source, exact registry indexes/children and corresponding-source
assets; requires all eight evidence gates; enumerates the updater's eight
mandatory provenance subjects plus source assets; and defines exclusive draft
reservation, complete readback readiness and manual partial-publication recovery.
Its CLI explicitly reports `publication_authorized: false`. Trusted evidence
verification, live GitHub/GHCR transport, durable mutation receipts, signatures
and registry/package visibility integration still require implementation. The
[publication guide](../security/container-publication.md) records the exact
boundaries. Adding this core does not complete the publishing tasks above.

### Authenticated report verifier checkpoint

- [x] Implement `tools/github_release_evidence.py` as the publication core's
      authenticated evidence adapter. Verify a private snapshot of report bytes
      with GitHub CLI, requiring the exact repository, release workflow and signer
      commit, tagged source commit/ref, certificate identity, hosted Actions
      issuer, and hosted runners. Accept only a verified SHA-256 subject and
      witnessed timestamp; do not accept unsigned caller approval JSON.
- [x] Verify failed/stale reports, missing attestations, substituted files,
      process failures, output limits, and timeouts with eight focused regression
      tests. These use verification fixtures and real bounded subprocess checks;
      they do not claim a live signed GitHub report passed.
      A real offline CLI check also catches unsupported flag combinations: use
      exact `--cert-identity` rather than combining it with the mutually exclusive
      `--signer-workflow`, while retaining the signer/source commit and ref policy.
- [ ] Integrate actual check/report generation and signing into the reviewed
      release workflow, then exercise the verifier against its authenticated
      reports. Source/legal review approval policy and live publication remain
      pending. Tagged runs now have narrowly scoped attestation permissions;
      package/release publishing and production access remain unavailable.

The verifier follows the documented [GitHub CLI attestation policy
flags](https://cli.github.com/manual/gh_attestation_verify). Its isolated process
has a 55-second deadline and a combined 4 MiB output bound. Tokens are supplied
explicitly, never through command arguments; caller CLI configuration and trust
overrides are not inherited. An attestation authenticates the reviewed workflow's
report, so that workflow must derive reports from actual completed checks before
signing them. The adapter alone does not establish distribution readiness.

### Installed updater verification policy

- [x] Require the exact release workflow certificate identity and version-tag
      ref, matching signer and source commit, GitHub Actions issuer, hosted
      runner, and SLSA provenance-v1 predicate for every updater subject. Reject
      weak workflow selectors or malformed bindings before invoking GitHub CLI.
- [x] Check generated arguments with GitHub CLI 2.101.0 using a deliberately
      missing test-local trust root, and run all 37 updater tests and the complete
      141-test release suite. The offline CLI check proves parser compatibility;
      it does not prove a valid live release attestation.
- [ ] Review and install the strengthened helper through the separate trusted
      administrator path, and verify matching live release attestations before
      adopting a production release. Updating the checkout does not upgrade an
      already installed privileged helper.

### Patched runtime inputs

- [x] Require OpenSSL 3.3.7-r2 or newer in the backend Alpine 3.21 runtime
      and zlib 1.3.2-r1 or newer in both final runtimes. Keep pinned official
      multi-platform base indexes; their current tags still resolve to the
      vulnerable original layers. Record and collect sources for retained and
      actually installed package versions instead of flattening the image.
- [x] Rebuild and smoke the patched AMD64 pair from exact source `750f440`;
      repeat complete retained-layer source collection, Caddy signatures, legal
      overlays and exact served-byte checks. Verify OCI config/blob/diff-ID bytes
      against the actually tested configuration IDs, then scan those archives.
      Both final installed Alpine graphs have zero findings. Retain and match all
      21 backend/one Caddy module findings to bounded absent-package evidence.
      See the [exact patched scan review](../security/container-dependency-review.md).
- [ ] Repeat these checks for the patched ARM64 native pair and bind both
      architectures into authenticated release workflow evidence. The previous
      vulnerable image scan remains historical evidence.

Package minimums select compatible newer revisions from the configured Alpine
branch. A base digest plus live APK repositories does not promise byte-for-byte
rebuild reproducibility. Release images remain immutable, and actual APK package
versions, checksums, recipe commits and retained sources must be captured for each
native build. Repeatable package bytes additionally require retained APK inputs.

### Native smoke measurement checkpoint

- [x] Emit a bounded, secret-free `image-smoke.json` only after actual checks
      and owned-resource cleanup succeed. Record exact image configuration IDs,
      source version/revision/platform, native/emulated execution, check coverage,
      and runtime pack hash when present. Lock Compose to checked immutable
      configuration IDs and compare the started containers against them.
- [x] Verify real AMD64 report generation against the private source-overlay
      image pair; the disposable HTTP stack passed and all owned resources were
      removed. The report covers the recorded `dffeac44` application source and
      is not distribution approval for that vulnerable candidate.
- [x] Reuse the bounded GitHub attestation verifier for native measurement bytes
      without returning a gate approval receipt. Nine authenticity regressions
      and five served-offer/measurement regressions passed.
- [ ] Authenticate both native matrix measurement records and aggregate the full
      release smoke/notices evidence against the exact OCI child descriptors.
      Tagged native measurements are now signed; actual two-platform hosted
      aggregation remains unverified. A missing runtime pack cannot satisfy
      the final publication gate.

### Completed-check report producers

- [x] Separate validated immutable publication inputs from authenticated gate
      verification using one shared `prepare_inputs` contract. Preparing a
      binding does not authorize publication or create approval receipts.
- [x] Generate source-CI evidence from the selected version-tag release run
      attempt's fully paginated API response, requiring every exact reusable CI
      job to be completed and successful at the selected source SHA. Reject old
      main runs, skipped/missing/unfinished jobs, altered attempts and pagination.
- [x] Run the real final smoke harness with runtime overlays and validate native
      OCI child/configuration correspondence before and after execution. Recheck
      exact runtime pack/source bytes and complete check coverage.
- [x] Authenticate both native measurement records before generating full smoke
      and runtime-notice gate reports. Unauthenticated single-platform records,
      stale source/configuration/pack identities and generic completion JSON do
      not satisfy publication. These producers cannot sign or publish.
- [x] Verify the report producers and shared input boundary with thirteen focused
      regressions; the complete release suite passed 139 checks in 8.120 seconds.
- [x] Require substantive exact source/distribution records in publication
      verification, even for authenticated reports. Reject empty or incomplete
      source coverage, source/image/notice/policy substitutions and distribution
      approval detached from its corresponding-source report. The focused
      publication group passed 23 checks in 2.9 seconds.
- [x] Implement authorized distribution-review production from exact committed
      policy, an authenticated complete source report and read-only GitHub review
      history. Require the configured protected-environment reviewer and exact
      artifact/source/attempt comment, retain raw API evidence, and recheck for
      changes. Fixture verification passed in 1.7 seconds; no live reviewer
      approval or signing is claimed.
- [x] Provision the `container-release` review environment and wire the strict
      distribution producer into the final trusted workflow after complete source
      production. Present concrete final artifacts before approval; attest both
      the gate and retained review evidence.
- [ ] Verify a real approval and rejection against completed hosted artifacts
      and the exact attempt. Environment provisioning alone is not distribution
      approval or hosted execution verification.
- [ ] Validate the wired producers and signed native records against actual
      two-platform hosted aggregation. Planned main dispatches still emit
      unsigned measurements and cannot satisfy a tagged publication gate.
- [ ] Implement actual source/final scanner, corresponding-source completeness,
      authorized distribution review, upgrade/recovery and readback/provenance
      producers. Source-asset hash measurements explicitly leave completeness
      and review pending; they cannot mint a corresponding-source approval.

### Post-matrix release input assembly

- [x] Remove the native-job/full-binding dependency cycle. Native producers use
      validated repository/version/commit/platform context and schema-2 local
      measurements; authenticated aggregation binds their image configurations,
      OCI children and source hashes after both jobs finish. Emulated smoke cannot
      satisfy native release evidence. Fourteen producer regressions passed.
- [x] Enforce native execution in the final publication verifier as well as the
      producer, and reject an authenticated record substituting an emulated ARM64
      check. The publication boundary regression suite passed.

- [x] Add a concrete preparation command consuming both native build records,
      measurements, source packs and all four OCI exports. Verify shared resolved
      bases, actual Go/Node versions, exact local image/source facts, and native
      execution before assembling indexes, bundle, manifest and application
      source archive from the selected tagged Git tree.
- [x] Verify assembly with real disposable Git repositories and OCI graphs,
      including platform omissions, stale/emulated/substituted measurements,
      changed bases/toolchains/source bytes, dirty tracked inputs, untracked
      exclusion and output overwrite rejection. Five focused tests passed.
- [ ] Run this command against real final AMD64 and ARM64 artifacts, authenticate
      native measurements after the complete binding exists, and integrate the
      command in the release workflow. Preparation does not approve publication.

### Runtime source replay

- [x] Implement a verifier that replays an externally hash-bound runtime source
      archive against final native OCI exports and smoke evidence. Verify source
      SHA512 tables, retained recipe/helper inputs, every origin revision,
      reconstructed lower-layer and installed package graphs, exact executable
      and notice bytes, and fresh Caddy source signatures/module correspondence.
      Rehash archive, pack, smoke and OCI inputs before returning measurements.
- [x] Exercise the unchanged patched AMD64 asset and both final native exports.
      All 34 origin revisions and 53 retained package versions passed; the final
      private report SHA256 is
      `14bd03673034bdeaff0c2178cbfd0ffa691cb7f4efed20dc8221723984fde519`.
      Eleven new regression tests and 51 related checks passed.
- [ ] Run the replay for ARM64 in the release workflow and authenticate both
      outputs against the final binding. Application source, public anonymous
      source delivery and authorized distribution review remain separate gates;
      technical runtime replay cannot clear those approvals.

### Native image scanner measurements

- [x] Implement a native scanner command over exact OCI exports and tested image
      configurations. Authenticate pinned Trivy/Cosign bytes and upstream signing
      identity before execution; snapshot the scanner database and unchanged OCI
      layout, record tool/database/date identities, and retain complete findings
      across every severity and unfixed package without ignores or approval flags.
- [x] Verify the actual patched AMD64 backend/web pair through this producer.
      Both installed Alpine graphs have zero findings; all 21 backend and one
      Caddy module findings remain in the raw JSON. Ten boundary/CLI regressions
      passed, including native ARM tool authentication fixtures. Those fixtures
      do not establish ARM64 execution.
- [ ] Execute final native ARM64 scans, authenticate all four image measurements,
      and derive each finding's disposition from exact binary/source/package
      graphs and official advisory evidence. A completed scan remains unapproved
      until that release gate succeeds.

### Source scanner and compiler graph measurements

- [x] Implement source scanner measurements from exact archived Git source with
      pinned native Go/Node builders, checksum-authenticated govulncheck 1.8.0,
      full Go dependency graphs and lock-only npm audit. Preserve all findings,
      raw report hashes and actual tool/database identities; measurement
      completion does not authorize a scanner gate or publication.
- [x] Add compiler graph measurements for the exact final OCI executables.
      Require a byte-identical backend rebuild with matching embedded modules;
      bind Caddy's graph to freshly verified upstream signed source/checksums
      and the exact signed executable. Verify seventeen focused producer/CLI
      tests, including native-daemon and unsupported build-setting rejection.
      The private `750f440` AMD64 measurements retained 21 Go findings and no npm
      findings; backend bytes matched and Caddy's 147 embedded module pairs
      matched its 970-package graph. See the
      [dependency review](../security/container-dependency-review.md).
- [ ] Run the final producers against the selected release source on both native
      runners, authenticate every measurement, and derive scanner gates from
      exact findings and authoritative advisory evidence. The new daemon guard
      was exercised in the final Caddy measurement; source/backend runs preceded
      that final guard and need repetition in the integrated workflow.
- [x] Repair the resolver-to-scanner builder contract using the exact canonical
      untagged official repository digest references emitted by `resolve-bases`.
      Verify a resolver-derived regression and repeat actual source scanning on
      `c73a5da` with the final native-daemon guard: 21 Go module findings retained,
      no package/symbol findings, and zero npm findings. Signed scanner gates
      and native ARM64 measurements remain pending.

### Native preparation orchestration

- [x] Add an individual native preparation command consuming an authentic
      candidate build record and its exact original Docker save. Retain source
      collection, overlays, final native smoke, OCI exports and independent
      runtime source replay in an unsigned, hash-bound artifact descriptor.
      Write the completion descriptor only after every check passes.
- [x] Preserve original Docker configuration and layer bytes during OCI export.
      A real AMD64 scratch-image build/save/export check passed for both
      components; twelve focused orchestration tests passed with explicitly
      substituted collection/smoke fixtures. Add these tests and runtime source
      replay tests to regular CI. See the
      [native preparation guide](../security/container-native-preparation.md).
- [ ] Run complete preparation from authentic application build records on
      both native runners, wire retained outputs into post-matrix assembly,
      authenticate the measurements, and complete publication gates. The scratch
      export check does not verify the complete application pipeline.
- [x] Run the complete individual AMD64 command on exact committed source
      `c73a5da`, genuine Buildx records and its original saved application pair.
      Collection, signed Caddy sources, overlays, all ten actual native smoke
      checks, byte-preserving OCI exports and independent runtime source replay
      passed. Retain the private descriptor and measured artifacts; the planned
      `v0.1.0` version has not been tagged or published.

### Authenticated scanner inputs and typed finding aggregation

- [x] Acquire the final-image scanner's fresh official database using the
      authenticated native Trivy executable in an empty private configuration
      and cache. Record acquisition, database and metadata hashes; retained
      snapshots remain measurement-only. Actual patched AMD64 backend/web CLI
      runs retained all 21/one Go module findings and found zero OS findings.
- [x] Implement typed aggregation of authenticated native/image measurements,
      exact raw findings and compiler evidence. Require byte reproduction for
      the backend and distinct upstream signed source/binary correspondence for
      Caddy. Derive package absence from complete authoritative Go advisories
      and exact module/import graphs; reject caller approval flags, missing
      proofs, unhandled findings, source/config substitution and unsupported
      compiler settings. Twelve scanner and eight gate tests passed with
      explicit authentication fixtures.
- [x] Derive the source-scanner gate from both authenticated native reports,
      exact Git and lock bytes, every hashed raw receipt, canonical resolved
      builders, complete Go import graphs and official advisory ranges. Retain
      every finding and reject unsupported dispositions; twelve focused gate
      tests passed. Actual AMD64 replay retained all 21 module findings; hosted
      ARM64 and live authentication remain pending.
- [ ] Run both native architectures, authenticate actual scanner/compiler
      measurements, and derive the final full-binding gate in the release
      workflow. Historical compiler measurements using tagged builder names
      cannot substitute for corrected canonical-reference measurements.

### Integrated read-only native candidate verification

- [x] Wire genuine paired build records and exact saves into native preparation,
      final image smoke, actual source/image scanning, backend/Caddy compiler
      correspondence and application dependency retention on both native matrix
      runners. Keep publication, signing and production permissions absent.
- [x] Add a main-only pre-tag dispatch for unused planned `v0.1.0`, preserving
      strict tag-push validation. Verify exact HEAD/main ancestry and reject
      existing version tags without creating or moving them. Main dispatch
      records explicitly cannot establish the final tagged source-CI gate.
- [x] Validate actionlint, embedded shell/Python syntax, thirteen real-Git
      candidate tests, and bounded report copying against thirteen actual new
      AMD64 records. Preserve all 21 backend/one Caddy findings; omit image/source
      payloads and private diagnostics. Correct the Cosign download size bound
      against its actual pinned 137,225,264-byte executable.
- [x] Repeat corrected exact-image compiler and fresh official database scan
      commands on `c73a5da`: backend reproduction matched its 236-package graph,
      Caddy signed correspondence matched 970 packages/147 module pairs, and both
      final OS graphs had zero findings with all module findings retained.
      The complete local release test discovery passed 199 tests in 9.7 seconds.
- [ ] Push this verified checkpoint with scoped authorization, run the new
      read-only hosted dispatch on the exact pushed source, and resolve actual
      ARM64/integration failures. Complete authenticated full gate assembly,
      corresponding-source/distribution review, registry/release publication
      and production migration separately. See the
      [native preparation guide](../security/container-native-preparation.md).

### Cross-job candidate assembly and native recovery

- [x] Retain complete native inputs with an explicit file/hash inventory, preserving
      descriptor paths and full scanner/compiler receipts. Exclude private
      diagnostics, tool/cache files and privileged recovery state; revalidate every
      retained byte and reference after same-run/attempt artifact download. Ten
      transfer fixtures passed; actual AMD64 stage/relocation replay verified 112
      files and 939,932,806 bytes without changing original measured inputs.
- [x] Add a distinct unsigned planned-main assembly path with exact source, origin,
      tracked-checkout, ancestry, event and unused-version guards. Keep the
      publisher's reviewed tag boundary mandatory; never create a pretend tag.
      Fifteen assembly and twenty-one publication boundary tests passed.
- [x] Wire common upstream collection, both native transfers, post-matrix assembly
      and two actual native recovery jobs into the read-only candidate workflow.
      Preserve all five source-CI jobs and upload only terminal recovery facts.
      Actionlint 1.7.12, Bash syntax, all five embedded Python blocks and the bounded
      workflow review passed; source permissions remain read-only.
- [ ] Run the integrated workflow on the exact pushed checkpoint and fix hosted
      transfer, assembly or native recovery failures. Complete source/distribution
      review, authenticated gates, publication and production adoption separately.

The combined release-tooling suite passed all 290 tests in 22.9 seconds after
this integration. The actual hosted two-architecture pipeline remains unrun.

### Application dependency input retention

- [x] Emit a production client module inventory tied to the npm lock and build
      identity. Verify original input, installed manifest and final static output
      hashes after the adapter runs, retaining tree-shaken modules separately.
      Record copied scripts and worker coverage limits without approving complete
      source coverage or publication; see the
      [source review](../security/application-package-source-review.md).
      Ten hook tests and twelve verifier fixtures passed. The local `v0.1.0`
      working-tree build verified 34 served outputs and 4,380 module records in
      under one second. Hosted CI and final OCI correspondence remain unrun.

- [x] Include the imported browser inventory script in the restricted Docker
      context. A real pinned-base native AMD64 web image build passed in 21.6
      seconds and retained the generated browser inventory and both new legal
      notices. Remove its owned image/container. This diagnostic working-tree
      build used `dev`/`main`; it does not establish release source binding.
- [x] Implement retention of the real web builder's observed source/generated/package inputs and
      Vite metadata, replay them against each final tested web configuration and
      exact committed inputs, and preserve this evidence through native transfer.
      Native preparation, transfer and recovery now require schema-2 evidence.
      Small real Git/npm/OCI fixtures reject missing inputs and substituted source
      or final image bytes. A real native builder diagnostic retained 4,401 members
      and checked 98 Git inputs: build 21.4 seconds, capture/static/Git replay 1.7
      seconds. Its owned image/container were removed. This diagnostic does not
      establish the complete final OCI/dependency/transfer path for a release.
- [ ] Run the complete schema-2 native preparation, transfer and recovery on both
      hosted architectures for the exact candidate commit. Full browser source
      closure, measurement authentication and publication remain separate gates.
- [x] Verify the initial schema-2 path on real local AMD64 images at `f90be19`.
      Complete preparation passed in 368.9 seconds. Independent replay using the
      exact committed verifier passed in 13.7 seconds with 98 Git inputs, 16 npm
      archives and 99 final static files. Retain private measurements and remove
      owned tool caches/image tags. This predates the expanded recipe profile and
      does not verify hosted transfer/recovery or native ARM64.
- [x] Bind copied browser scripts and the application template to exact Git
      originals. Retain a committed recipe catalog plus Vite/Kit generator bytes
      and manifests, checked against integrity-bound npm archives. Associate all
      23 current virtual modules and 11 generated files (five rendered, six
      excluded) through finite reviewed grammars; keep unfamiliar origins explicit.
      Five focused regressions passed in 4.0 seconds and actual retained npm
      replay passed in 1.5 seconds. These are source associations, not generated
      byte reproduction or complete preferred-source approval.
- [x] Retain all copied static originals, including brand SVGs, favicons and
      notices, and compare them to unchanged built copies and the exact Git tree.
      Include the locked adapter-static 3.0.10 generator and manifest in retained
      npm replay, with its preferred originals from the pinned Kit tree. Keep
      fallback/generated outputs distinct from copied originals. Eight focused
      fixture checks passed in 0.7 seconds after replacing production watchdog
      polling with direct bounded Git calls in the tiny fixtures.
- [x] Verify the expanded static/adapter profile on a real Docker web builder
      at committed `e3a5c83`. The build passed in 20.9 seconds and captured-input
      replay in 1.8 seconds: 122 Git inputs, 18 integrity-bound npm archives and
      all 21 copied static originals. The exact generated metadata matched its
      native identity. Both adapter implementation files matched their preferred
      Kit originals. Reuse unchanged retained archives; remove the owned image
      tag/container and temporary replay directory. Retain private capture and
      receipt; final OCI, ARM64 and complete closure remain separate pending gates.
- [x] Include the original Vite and bundled Rollup generator license terms in
      browser notices and serve their exact original license bytes. Check lock,
      generator and notice hashes and reject stale inputs. Existing focused notice
      checks passed in 0.123 seconds; type checking reported no errors or warnings.
- [x] Retain and pin full original Vite 6.4.3 and Rollup CommonJS 28.0.3 source
      archives and build locks. The exact Vite lock selects the CommonJS version;
      its 1,337-byte helper template literal matches the installed Vite generator.
      Preserve ten explicitly reviewed package-self test links as inert archive
      metadata only; default link restrictions remain in force. Eighteen source
      retention checks passed in 0.7 seconds.
- [x] Validate the actual checked-in upstream catalog and lock in a small offline
      regression as well as synthetic archive fixtures. The first real expanded
      replay caught an incorrect Rollup archive filename; the corrected name
      follows its `rollup/plugins` repository. All nineteen affected checks passed
      in 0.85 seconds, including the actual-catalog check.
- [x] Package and independently replay the fourteen-original offering against
      committed catalog `3bd44a3`. Reuse the twelve unchanged hash-bound official
      archives and two newly retained official originals without downloading them
      again. Collection passed in 4.1 seconds and replay in 4.3 seconds, covering
      31,033 members and 102,971,136 original bytes. The 103,751,003-byte offering
      has SHA256 `56f2addf264c5bc2c918ba17c5027bf911e2e434db1c17f32299508d10c2b88a`.
      Retain the private receipt; completeness and publication flags remain false.
- [x] Replay the updated fourteen-original source catalog with the added adapter
      inspection inputs at `e3a5c83`. Collection passed in 4.1 seconds and replay
      in 4.2 seconds without downloads. The 103,751,391-byte offering retains the
      same 31,033 original members and has SHA256
      `418ececca767da6944f05091713266d865820d8c49e0b9d5f84c69efb4da4967`.
      Source completeness, authentication and publication remain unapproved.
- [ ] Verify the new final native recipe/notice profile on both hosted
      architectures. Complete source/distribution gates and public delivery remain
      separate requirements.

- [x] Retain every selected Go module ZIP/module/version input and all npm locked
      archives, including development and optional platforms, without executing
      package scripts. Independently verify H1/SHA512 integrity, preserve exact
      committed locks, and distinguish additional authenticated graph sums.
      A real collection from `2cc2f72` retained 35 Go modules and 173 npm packages;
      nine focused boundary tests passed. Document the exact asset checksum in
      the [native preparation guide](../security/container-native-preparation.md).
- [x] Independently replay retained dependency archives against committed locks
      and actual source-scanner receipts. Recheck archive boundaries, selected
      Go module H1 checksums, npm SHA512 integrity and complete payload coverage.
      The actual `c73a5da` AMD64 archive replay verified 35 Go modules, 173 npm
      packages and nine additional sums; preferred-source review remains pending.
- [x] Require both architecture-specific dependency collections in release-input
      assembly and retain both archives alongside runtime and application source
      payloads. Bind thirteen subjects and seven assets, pin copied bytes to the
      verified digests, and reject collection substitution before output. Actual
      publication and public retrieval remain pending.
- [x] Update the operator guide's assembly command with both required dependency
      and source-scanner inputs, and describe the implemented read-only main
      dispatch. Keep uploaded candidate summaries distinct from the complete
      retained inputs needed for authenticated assembly.
- [x] Inspect the actual retained web package inputs for original source and
      build materials. Record the generated Lucide and fflate input gaps,
      hpke build-script gap, and embedded QR/tus source-map contents in the
      [application package inspection](../security/application-package-source-review.md).
      This ten-package subset does not establish complete source coverage.
- [x] Retain immutable upstream Lucide, fflate and hpke source archives for
      the identified missing build inputs. Verify hpke's original TypeScript
      against the locked npm archive. Record source/archive hashes and Lucide's
      version-setting discrepancy; complete reproduction, offering assembly and
      source-completeness review remain pending.
- [ ] Review package inputs for complete preferred-form upstream source, include
      all required sources in the final corresponding-source offering, bind the
      archive to authenticated release inputs, and verify public delivery.
      Retaining original package distributions alone does not complete this gate.

### Retained upstream application source inputs

- [x] Add a collector and independent replay for immutable full-commit Lucide,
      fflate and hpke archives, deriving package versions and archive policy from
      the exact committed catalog and npm lock. Preserve original archives and
      measured inventories without extraction or package-script execution. Twelve
      collector/replay tests passed; all three actual retained archives parsed
      successfully. No correspondence or source-completeness approval is inferred.
- [x] Require the replayed common upstream archive during release-input assembly,
      copy only the verified digest, and include it in every release subject and
      asset binding. Twelve assembly checks passed, including missing/tampered
      inputs and changes after replay. The signing fixture verifies all fourteen
      subjects, including the common source asset.
- [x] Exercise the collector and independent replay through fresh official HTTPS
      downloads for exact source `2a6d2a5d65ad50b2565aa09624e5681c0976c262` and planned
      `v0.1.0`: all three original archives and 6,238 members verified in 4.3 seconds.
      Retain the 7,910,216-byte common offering and private replay evidence. Source
      completeness, build reproduction, authenticated offering and public delivery
      remain pending; see the [source review](../security/application-package-source-review.md).

The combined release-tooling suite passed all 272 tests in 19.9 seconds after
this integration. Formatting, CLI help, Python/shell syntax and the bounded source
retention review passed.

### Browser source and embedded-license follow-up

- [x] Match all fourteen observed rendered-package manifests against actual
      retained npm archive hashes/sizes and locked versions/integrity. Inspect
      original transitive implementations and their concrete missing build inputs.
- [x] Pin six additional immutable source originals for noble-curves, noble-hashes,
      clsx, node-qrcode, qr-scanner and its embedded jsQR decoder. Verify all nine
      original archives: 83,412,855 bytes and 7,513 members. Preserve three internal
      noble test links as metadata without extraction/following; reject escapes,
      chains, recursive/dangling targets, hard links and linked outer offerings.
      Seventeen small collector fixtures passed.
- [x] Add the decoder's missing full Apache 2.0 license and factual attribution
      to web notices. Bind the original license, exact parent lock and four
      scanner/worker file hashes; retain a separate embedded-component inventory.
      Three focused fixtures and actual 173-package/one-component notice checks
      passed. No native mobile implementation bundles this JavaScript component.
- [x] Collect and independently replay the expanded common source offering from
      exact committed catalog `224d95042349e5cd37421a992810eef1c58ec14d` for
      planned `v0.1.0`. Fresh official downloads of all nine originals took
      31.2 seconds; independent replay took 2.4 seconds. The retained offering is
      83,381,770 bytes. This is an explicit release check, not a routine unit test.
- [x] Retain three further exact noble-ciphers, SvelteKit and Svelte originals,
      including omitted TypeScript/framework generators, message inputs and
      monorepo locks. Compare 541 original source files to retained npm inputs;
      run both original version generators in disposable directories and verify
      byte equality. All twelve archives passed existing bounds unchanged.
- [x] Preserve dijkstrajs's abbreviated upstream notice and add explicitly
      supplementary full MIT terms from its referenced official page. Bind exact
      version/integrity and both notice hashes; three tiny failure/preservation
      fixtures and notice freshness/type checks passed. The complete web suite
      passed 130 tests in 1.4 seconds, the production build passed in 7.2 seconds,
      and final served terms/static inventory verification passed.
- [x] Collect and independently replay the twelve-original offering from exact
      source `f62d35364fdc2cbb3540176d39899448d5ec4835` for planned `v0.1.0`.
      Fresh official acquisition/packaging took 37.2 seconds and independent
      replay took 3.7 seconds. Retain the 91,545,944-byte offering and private
      replay receipt; final hosted assembly and publication remain pending.
- [ ] Authenticate the expanded offering and verify final hosted image
      correspondence and public retrieval before publication.
- [x] Keep new regressions limited to actual notice/input/archive failures with
      small local fixtures and no network calls. The complete release-tooling
      suite passed 307 tests in 23.3 seconds; web passed 127 tests in 1.4 seconds.
- [ ] Measure the next complete hosted workflow and investigate slow test steps
      before accepting it. Keep heavyweight native preparation/recovery in the
      explicit release workflow and do not extend timeouts to conceal slow tests.

### Post-assembly recovery and hosted publication commands

- [x] Add an actual native recovery measurement producer requiring both retained
      native descriptors and the assembled manifest/bundle. Validate four OCI
      graphs, saved layers, source/runtime/replay inputs and every source-owned
      bundle file against the exact candidate Git tree before execution.
- [x] Measure ordinary and initially paused upgrade/reapply/restore sequences
      and injected startup failure using structured transaction, API, schema,
      checkpoint and isolated-storage facts. Require successful bounded cleanup;
      caller PASS flags do not create evidence. Eleven producer/cleanup boundary
      tests and thirty-seven updater tests passed. The historical paused AMD64
      compatibility run passed in 130.8 seconds; full prepared-candidate execution
      remains pending. See the [recovery guide](../security/release-update-recovery.md).
- [x] Add the hosted-only signing bridge using exact pinned official GitHub action
      bytes and the runner's Node24. Sign actual file/image subjects and completed
      bound reports, verify the freshly generated bundles cryptographically, and
      independently verify GitHub API retrieval under the exact workflow/tag,
      signer/source commit and hosted policy. Sixteen offline signing fixtures
      passed; actual hosted Node24/OIDC execution remains unverified.
- [x] Add the real publication command around authenticated gates and four exact
      OCI exports. Require public repository-linked packages before reservation,
      preserve the lease through digest pushes/signatures/assets/tags/readbacks,
      and sync all snapshot files/directories before remote mutation. Ten command
      fixtures passed, including interrupted writes/signing and retained inputs.
      No local test published a release or changed package visibility.
- [ ] Execute the recovery producer against both actual native artifact sets,
      authenticate its measurements, complete source/distribution gates and wire
      the real signing/publication commands into the reviewed version-tag workflow.
      Package bootstrap/visibility, immutable policy and hosted OIDC/live API
      verification remain separate prerequisites; candidate dispatch stays read-only.

The combined release-tooling suite passed all 257 tests in 19.5 seconds after
these changes. Python formatting, syntax, documented shell commands and Markdown
formatting passed. This local result does not replace the pending hosted checks.

### Authenticated native recovery aggregation

- [x] Derive an upgrade-recovery gate from both signed terminal native records,
      checking exact prepared inputs, committed helpers, final configurations and
      all upgrade/reapply/pause/failure/isolated-restore facts without rerunning
      experiments or replaying image/source archives.
- [x] Wire tagged recovery attestation and post-matrix aggregation using small
      descriptor/binding artifacts. Authenticate the complete source gate before
      using its full binding without downloading offered source payloads again.
- [ ] Execute and verify this integrated recovery gate on both actual native
      hosted architectures. Independent off-host provider recovery, public
      acquisition/provenance and browser/mobile flows remain separate checks.

The publication consumer now requires complete recovery coverage and both native
measurement identities; an authenticated report with empty recovery details is
insufficient. The gate preserves explicit false scope flags for the separate
public-delivery, provider and browser/mobile checks. Retained experiments are
validated within their signed measurement interval; immediate experiment
execution keeps its existing freshness requirement.

`tools/aggregate_release_recovery.py` authenticates both native terminal records
and validates their structured scenarios, exact manifest/bundle/configurations,
committed execution helpers, historical ancestry and measurement time interval.
Its `--source-report` mode requires the current attempt's authenticated complete
source report before accepting the full binding from the three small retained
prepared files. It checks exact source-asset metadata coverage and rechecks
retained small bytes. `--verify-only` authenticates the resulting canonical gate
without another experiment or archive replay. Host runner and isolated daemon
tool versions are validated independently, since they can legitimately differ.

The tagged workflow now signs native recovery measurements and the joined gate.
Small native-descriptor and prepared-binding artifacts avoid downloading all
images/source payloads in the aggregation job. Planned main dispatches still
cannot satisfy these authenticated tagged gates. No registry publication or
production permission was added.

Nine small aggregation/command cases and eleven existing measurement cases
passed in 2.83 seconds; the final asset-metadata refinement passed all nine
aggregation cases in 0.49 seconds. Existing publication/command cases, including
one incomplete-recovery rejection case, passed 34 checks in 3.45 seconds. These
fixtures use local bytes, Git and mocked experiment results, with no Docker
execution. Workflow YAML parsed, all 25 shell steps passed `bash -n` and three
inline Python blocks parsed. The selected historical commit remains an ancestor
of the current source. Actual hosted execution and artifact-layout validation
remain pending, as do the separate public/provider/mobile checks.

### OCI archive and publishing transport checkpoint

- [x] Add `tools/assemble_release_oci.py` to verify all four local OCI exports
      against the exact configurations of the tested native image pair. Verify
      archive entries, all blob hashes and descriptor sizes, platform/source
      labels, and each decompressed layer's `diff_id`; checking only compressed
      blobs would allow a substituted layer beside an unchanged tested config.
      Construct deterministic index bytes with the exact two runnable children.
- [x] Run six focused OCI corruption, substitution, archive-boundary and index
      regressions. These fixtures cover gzip and raw layers; actual final native
      exports and ARM64 execution remain distinct verification requirements.
- [x] Implement `tools/github_release_transport.py` and seventeen transport
      fixtures for fixed-host bounded GitHub/GHCR API calls, exact OCI pushes,
      complete anonymous child pulls, assets and readbacks, draft reservation,
      immutable release publication and interruption reconciliation. Require
      authenticated reports and fresh remote state before publishing, and record
      durable mutation intent/results without automatically resuming partial work.
- [ ] Wire the transport and check/report generators into the exact reviewed tag
      workflow, preserve receipts beyond a hosted runner's lifetime, configure
      public packages and policy inspection, then verify actual publication and
      recovery. Fixtures do not establish these live behaviors.

The transport requires the literal repository-wide workflow concurrency group
`container-release-publication`, `cancel-in-progress: false`, and publishing job
`publish`, plus a local held lock. At this checkpoint the candidate-only workflow
had not been changed to publish; subsequent guarded integration is recorded below.
Immutable policy inspection may require a separate
Administration-read credential; the package/content publishing token must not
silently gain repository administration permission. Errors are not absence.
The [publication guide](../security/container-publication.md) describes the
implemented adapters and the still-pending source/legal review and live gates.

### GitHub production deployment

- [x] Add `.github/workflows/deploy.yml` with `workflow_dispatch`, taking a
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

The manual workflow and bounded SSH client are implemented, with fixed
`psst-deploy` identity, exact host-key pinning, no forwarding/agent/user-config
inheritance and private temporary credential files. Only a strict version is sent;
the installed host helper authenticates the ready release. Success requires its
response confirming that exact version active and completed. Pending local
verification and connection loss do not produce a success result. Five focused
boundary tests passed in 0.087 seconds, including real key parsing, credential
file cleanup, output/timeout bounds and rejection of a mismatched active version.
The workflow passes actionlint v1.7.12. SSH/sudo templates and setup/rotation
instructions are in
[the production Actions guide](../security/actions-production-deployment.md).
Standalone template syntax and effective Match restrictions passed locally on
2026-10-08 using OpenSSH 10.5p1 with a disposable host key and isolated config.
The deployment user received the exact forced command, public-key-only
authentication, protected key-file path and disabled forwarding/TTY/tunnels;
the nonmatching maintenance user did not inherit those restrictions. Sudoers
syntax passed with visudo 1.9.17p2. The local visudo also warned about unrelated
ownership of `/etc/sudo.conf`; this check did not modify that file or validate
the local machine's installed sudo policy. All temporary keys/configs were
removed. Installed VPS Includes, account/PAM policy, sudo grants and actual
connection/denial behavior remain pending, as do production secrets and rollout.

Source dependency review is recorded in
[the dependency review](../security/container-dependency-review.md). Pinned
govulncheck 1.8.0 using the actual release Go 1.26.8 found zero reachable symbols
and zero imported-package findings. All 21 module-level advisories are documented
with applicability to the actual command import graph. Final image and native
reviews remain pending; this source result does not approve publication.

### Documentation and current VPS migration

- [x] Add a self-hosting quick start covering DNS, ports, Docker/Compose, public
      image pulls, persistent storage, first-admin creation directly on the host,
      and removal of both bootstrap credentials from the file and running
      backend after initialization. The guide explicitly requires verified
      published inputs; public installation and production adoption are unrun.
- [x] Document the maintainer path from reviewed commit to version tag to ready
      release, and the operator path for manual SSH updates, Actions deployment,
      maintenance, backup, failure recovery, and rollback.
      The [operations path](../security/container-releases.md) distinguishes the
      current read-only candidate workflow from pending tagged publication,
      same-run/attempt artifact retention, version-only update commands and
      before/after-migration recovery. Documentation does not establish a ready
      release or a completed production migration.
- [x] Choose and publish the GitHub repository `endorses/psst.zip`, following
      reviewed licensing and complete-history secret checks.
- [ ] Confirm the GHCR package namespace and initial release version, and configure
      branch/tag protection before publication. Review release inputs for
      accidental private deployment state.
- [x] Select `v0.1.0` as the first container release, as confirmed by the operator.
      Preserve the existing `v0.0.0` tag; create the new tag only after the reviewed
      workflow and release inputs are ready.
- [x] Compare the active VPS Caddyfile with the committed bundled configuration.
      Confirm byte equality and absence of imported custom policy; select the
      bundled file for eventual adoption while retaining the old protected file
      for recovery. Live adoption remains pending.
- [x] Inspect the live installation's actual volume mappings and settings before
      migration. Preserve `/opt/psst.zip`, project name `psst-zip`, and the original
      local images/configuration as the first migration recovery baseline.
      Read-only SSH inspection on 2026-10-08 used the existing maintenance key
      and strict known-host verification. Both local image IDs, Compose service
      labels, mount mappings and non-secret database/storage/public-URL settings
      were read. The backend's original volume and database basename differ from
      fresh-install defaults: carry their explicit `BACKEND_DATA_VOLUME` and
      `DB_PATH` overrides into migration. Caddy's existing data/config volumes
      and read-only host Caddyfile mount remain in place. Configuration hashes
      were inspected without printing `.env`; it is root-owned with mode 0600.
      No server files, containers or data were changed; final stopped-backup and
      migration-time reinspection remain pending.
- [ ] Configure and verify protected encrypted off-host backups, including a
      restore exercise, before enabling production deployment automation.
- [ ] Publish and test the first release, perform a manual migration using the
      shared update command, and verify existing accounts, settings, files, links,
      and TLS state survive. Do not recreate the administrator.
- [ ] Configure the environment and restricted deployment key, then exercise the
      Actions deployment path with a subsequent tested release. Never upload the
      VPS `.env`, data volumes, or personal maintenance key to GitHub.

Read-only live inspection on 2026-10-07 confirmed `/opt/psst.zip`, project
`psst-zip`, two source-built services, a private backend and the existing named
backend/Caddy volumes. The backend still uses its original physical volume and
database identifiers; adoption must set protected explicit overrides rather than
select fresh defaults. No legacy identifier is being republished as a new
project identity. Backend ownership is UID/GID 1000; Caddy is 10001. Both services
retain resource policies, with no explicit Compose user override.

A further read-only comparison found that the active read-only Caddyfile bind is
byte-identical to the committed file copied into the web image: SHA256
`86fc3a748f5d9994ffca700953ca357e2c8637d1ca97f94cc978099de57a266d`.
There are no imported custom proxy policies or operator authentication, manual
TLS or logging directives. For this installation, adoption should deliberately
select the bundled Caddyfile with an empty `release_overrides` list and retain the
old host file in the stopped checkpoint. This preserves the observed proxy,
headers, static routing and operator-configured domain policy. Actual activation,
port/storage equivalence and TLS persistence still need verification; no mount
or host configuration was changed.

The host is AMD64, Docker 29.8.2 and Compose 5.6.0, with approximately 34.6 GB free.
HTTPS authentication status returned 200 and `setup_required: false`; no nonempty
administrator bootstrap variables remain. The private environment file is
root-owned mode 0600. The Compose and Caddy files are root-owned mode 0664 and
must lose group write access before the protected helper can adopt them. No
production state or permissions were changed. Baseline image/configuration
checkpoint retention and operator-setting equivalence still require verification,
so the complete migration inspection/adoption task remains pending.

### Application archive replay and generated backend originals

- [x] Independently replay the publication-bound application source archive from
      the selected Git commit and release gzip recipe, ignoring working-tree
      edits. Keep this partial fact separate from full corresponding-source
      completeness and publication authorization. A focused real Git fixture
      covers digest substitution, malformed retained bytes and another commit;
      it passed in 0.115 seconds.
- [x] Add exact official SQLite C and musl originals to the common upstream
      offering for the locked `modernc.org/sqlite v1.37.0` and
      `modernc.org/libc v1.65.0` modules. Verify actual archive roots, all inspected
      build/source/notice paths and SQLite's generated-source Fossil identity.
      Reuse the existing bounded archive parser; do not execute upstream builds.
- [x] Bind backend associations to exact committed Go requirements and retain
      `go.mod`/`go.sum` bytes and hashes. Refuse module replacements, missing or
      substituted locks, alternate hosts and redirects. Two focused regression
      methods extend the existing upstream suite; all 21 checks passed in
      0.794 seconds with tiny local inputs and no network calls.
- [x] Collect and independently replay the expanded sixteen-original offering
      from the committed source, reusing unchanged hash-bound originals.
- [ ] Complete final-image compiler binding, all preferred source/generator
      relationships and both native architecture reviews before producing the
      passed corresponding-source gate. Original retention and application
      archive replay alone do not establish those requirements.

Actual collection from source `036d9470f22d0938c3fab8c4e4589ad1037c6b0d` for
planned `v0.1.0` retained 16 originals and 36,237 archive members. Collection took
4.7 seconds and independent replay took 5.2 seconds. The 117,942,945-byte offering
has SHA256 `f4f28bd13622f486fdc9e53a9ba2d49e338e1769f2ad022b7e26b0e55737249f`.
Both committed backend lock hashes were independently checked. Fourteen unchanged
originals were reused with their pinned hashes; only the two newly identified
official source inputs were downloaded, outside routine tests.

The actual application archive from that same source was created and independently
replayed in a combined 1.1 seconds: 2,650,450 bytes, SHA256
`c0d13831d63c13af0173ea79fddbba642c1fbf88a3144d0e59a0465edaa69b0c`.
Private archive/collection/replay receipts are retained. An additional bounded
comparison against the retained dependency asset confirmed SQLite's source ID in
both generated Linux architecture files and musl's snapshot commit in both libc
generator inputs. This establishes those source identities, not full generation
or final-image completeness. All 53 affected gate, upstream and assembly
regression checks passed in 5.8 seconds; formatting and staged checks passed.

### Full Go runtime source retention and backend compiler binding

- [x] Retain the complete official Go source tree for the actual current runtime
      version, Go 1.26.8, pinned to commit
      `c293dd49cbe25e1fe8d97d94a5cb618e7b6d831e` and original archive checksum in
      `tools/go-runtime-sources.json`. Preserve runtime, standard-library,
      compiler, build files and original notices; keep nested test archives
      unchanged rather than interpreting them as dependency archives.
- [x] Extend runtime packaging and independent replay to require both actual
      executables' Go versions to match their retained original. Read metadata
      without running the programs, retain the exact Go policy in the source
      pack, and supply the backend's original Go notices as well as the web's.
- [x] Bind both authenticated backend compiler graphs to the exact committed
      application snapshot and H1-verified module originals. Require all compiled
      dependency versions/checksums and publication asset subjects to match;
      permit additional verified build-only modules. Reuse existing native
      compiler evidence without adding another build or scanner execution.
- [x] Verify the actual pinned Go original and both previously tested native
      AMD64 executable versions, retaining exact archive/configuration/binary
      identities. This check is separate from preparing a new final runtime pack.
- [ ] Prepare and verify new final runtime packs and overlaid image pairs on
      both native architectures, with the complete Go source and notice changes.
- [ ] Finish remaining preferred-form package/generator relationships and emit
      the complete corresponding-source gate before hosted authorization and
      publication. Backend compiler binding and Go originals do not finish all
      source categories.

The exact original Go archive is 34,443,123 bytes, SHA256
`061b4e784db7ce97cd9ae99ea71a857a2ff8455c6400495e5d1a98b8accd2542`.
Its bounded inspection covered 16,693 members and 145,163,055 expanded source
bytes in 0.9 seconds, finding 38 original notices (70,348 bytes) and an exact
Go 1.26.8 VERSION file. Download occurred only during explicit source acquisition;
routine test fixtures are small local archives. Original source/runtime
inspection against the retained `f90be19b543b31ef7738187a61ceb45aec518680`
AMD64 pair took 3.0 seconds. Both actual executable versions matched. Private
receipts remain retained; this does not relabel that older pair as a new build,
validate ARM64 or approve full corresponding sources.

All 386 affected release, native-preparation, browser-input, runtime source/notice
and signature regressions passed in 26.4 seconds, with one existing opt-in Docker
case skipped. The real original-source/executable inspection above ran separately.
New cases target source substitution, missing preferred inputs, mismatched binary
versions, module checksum mismatch and authentication refusal; no network calls,
compiler builds or coverage-only tests were added to the routine suite. The
updated source-scanner caller was included in those checks after the runtime
replay API change.

### Native Go source packaging and compiler input capture

- [x] Prepare and independently verify the updated native AMD64 pair from
      `4ae5954223e6ec93f191a6578722551f60fdb54f`, including complete Go sources,
      both original executable versions, final notice overlays, OCI exports,
      actual image smoke checks and the existing browser input replay.
- [x] Retain the installed Svelte compiler and preprocessor inputs in the
      browser builder capture: 235 Svelte files and all 21 plugin JavaScript
      files. Require the reviewed package identities and entrypoints and reuse
      the existing npm archive integrity checks.
- [x] Add one small regression for omitted or substituted compiler/preprocessor
      bytes. Use seven representative files in fixtures; all 34 affected browser
      and native-preparation checks passed in 1.2 seconds, with one opt-in Docker
      case skipped. The actual native Docker check above ran separately.
- [x] Verify the complete new compiler/plugin capture against the actual builder
      and retained locked npm archives.
- [ ] Finish preferred-source/generator relationships and the native ARM64
      preparation before emitting the complete corresponding-source gate.

The AMD64 preparation took 348.6 seconds. Its 190,781,748-byte runtime source
asset has SHA256
`cecccf06bc91a10a3377ddc966d4f5306db0312935fdd263e698101d2340aa53`.
The independently checked final configurations are
`72268e1334d2eb6aa2e9580aca617072936b86c048d16914d9b801e19f497688`
(backend) and
`a348f835af1e0009bda5b8252096b35c13bd006149548b4fced9863f0dd79ce3`
(web). Private schema-2 artifact and replay receipts are retained. Both executable
Go versions are 1.26.8 and match the retained original. This establishes the
runtime packaging check for that exact AMD64 source, not full application-source
completeness, ARM64 validation or publication approval.

The first attempt stopped before creating output because a prior temporary
source-helper image was absent. Recreating the helper and restoring the saved
application images reused completed builds and dependency originals. No image
was published or deployed. The compiler capture is a subsequent implementation;
the earlier AMD64 receipt does not validate its additional inputs.

The subsequent real builder capture from
`7d0f42a8ffb2b44a38ae25c635c4ddefe4eaf1df` verified every new compiler/plugin
member against its locked npm original. All 233 retained Svelte source files also
matched their exact paths in the pinned upstream source tree; the two CommonJS
files remain npm-bound inputs without a reproduction claim. The builder took
21.4 seconds, capture 0.7 seconds and independent input replay 1.9 seconds.
The 20,325,888-byte capture has SHA256
`0088a4019bb088c727c163ee09d3bc60d86a2a389d602c602cbeb3e62aefa3c1`.
The replay bound 122 Git inputs and 19 npm originals. Its private receipt preserves
all 233 file hashes and the exact builder, capture and dependency asset identities.
Temporary extraction caches and disposable containers/image aliases were cleaned.
This additional check establishes builder input correspondence, not a new final
OCI pair, complete preferred-source coverage or publication authority.

### Preferred browser relationships and complete modernc project originals

- [x] Encode explicit original-source and build-recipe relationships for every
      actually rendered package module, including Noble TypeScript, HPKE import
      rewriting, fflate browser-worker rewriting, clsx minification and Lucide
      SVG/metadata generation. Unknown packages, missing originals and changed
      captured bytes refuse correspondence.
- [x] Compare QR scanner source-map contents against every pinned decoder/worker
      original and map the captured Svelte compiler/plugin inputs separately.
      Actual offline replay resolved 168 rendered package modules and 256 captured
      compiler/plugin inputs in 3.3 seconds from the existing `7d0f42a` evidence.
      Three tiny regression methods cover substitution, missing originals,
      unclassified rendered inputs and changed icon data.
- [x] Acquire and pin the complete official modernc SQLite/libc project archives
      at the exact retained Go origin commits. Preserve nested generator modules
      omitted from proxy ZIPs, including SQLite's vendor tool and its module locks.
- [x] Independently verify all 1,323 SQLite and 4,153 libc proxy files against
      those complete project trees, with H1 replay and exact origin/version pins.
      The actual comparison took 3.5 seconds and retained generator input hashes.
- [x] Restrict acquisition to the two canonical full-commit GitLab archive routes,
      refusing other hosts/repositories, mismatched paths, tags and redirects.
      The 25 affected upstream/browser checks passed in 1.0 seconds without
      network requests or compiler execution.
- [x] Collect and independently replay the expanded eighteen-original offering
      from a committed source, reusing unchanged original archive bytes.
- [x] Implement the browser source producer joining both authenticated native
      reports to independently replayed Git/npm/final OCI bytes and the exact
      publication-bound upstream offering. Replay the common offering once
      and reuse existing native input checks for package, compiler and generator
      associations.
- [x] Verify the offering reader against retained real originals and builder
      capture inputs, and exercise authentication, image/replay substitution,
      missing architecture, publication binding and post-replay mutation refusal
      with two small regression methods.
- [x] Pin and retain the complete JSBT 0.7.1 project at its exact npm Git origin;
      verify that its TypeScript configuration is byte-identical to the original
      npm member whose SHA512 integrity is present in all three Noble locks.
- [x] Bind Noble's external configuration to the offered source, package identity,
      configuration hash and upstream lock integrity. Reject missing or changed
      inputs and unsupported additional configuration inheritance.
- [x] Collect and independently replay the nineteen-original source offering
      from the updated committed catalog before hosted release preparation.
- [x] Bind rendered Vite virtual helpers and SvelteKit generated application
      inputs to retained preferred generator sources, exact npm bytes and project
      Git inputs. Reject unknown rendered inputs, changed helper templates, wrong
      workspace lock selections and unattributed final JavaScript.
- [ ] Complete remaining generator/configuration relationships and authenticate
      the exact final-image inputs on both native architectures before producing
      the corresponding-source gate and requesting distribution review.

The SQLite module origin is `dc8212054b608339e80d7e986e530fa24bc5e369`
in `cznic/sqlite`; its full 39,232,665-byte archive has SHA256
`492752855dd34e06c4798019c6708e1f89e7b9444ef80ddd9e94c54a1322e167`.
The libc origin is `d6b3f0f238e49c751b8ace5b5595ccc93db8b536`
in `cznic/libc`; its 15,742,379-byte archive has SHA256
`8bfc54fb0cd88402f0fcd902a3117e785cf30de3574216fe95ba22685f67e81f`.
Both entire source trees are retained unchanged. The SQLite tree expands to
244,731,639 bytes, so the bounded expanded-source limit is now 512 MiB and the
combined offering limit is 256 MiB. Per-original archive and member-count limits
remain bounded; routine fixtures remain small.

The browser relationship helper consumes already verified archive/capture/npm
facts; it does not authenticate or authorize publication itself. Noble's upstream
locks identify `@paulmillr/jsbt@0.7.1`; its full original project is now pinned at
`c138efca67d90dce86a7cc22c01d6b66d193ea7a`. The 224,261-byte source archive has
SHA256 `0c0f435d7945837d6473279d73da94582bea474fcc80edc127997a8d9172c94b`.
The configuration is byte-identical to the integrity-checked npm member, SHA256
`be47d0e8938ae5896bbd69348b22fc5e756e5a820480229035d3ac06861875ac`.
The source relationship now requires that retained configuration and its exact
locked integrity. Two tiny regressions cover configuration/identity substitution
and missing or mismatched external lock inputs. Actual replay of the retained
`7d0f42a` capture verified all 15 Noble external-configuration inputs in 0.03
seconds. The complete release/notice suite passed 400 tests in 24.3 seconds
(one existing opt-in Docker skip); the two new cases took 0.001 and 0.034 seconds.
Vite virtual helpers and Kit generated application outputs are now associated
with retained preferred generator sources. Backend generator relationships were
subsequently implemented below. The combined corresponding-source producer is
implemented; authenticated execution for the final current two-platform
candidate remains pending.
These facts do not establish offline or byte-identical dependency regeneration,
complete corresponding-source coverage or publication authority.

Actual collection and replay from `5da2acec232fa10cfaf7ad60cdf362bfa5c7b928`
retained all 18 originals and 41,908 members. Collection took 7.1 seconds and
independent replay 7.4 seconds, without repeating downloads. The 172,577,635-byte
offering has SHA256
`db40608f19d5d0f7244e3208dde113f9c425560ff4dc27202b4e230bcaa3ff59`.
Private committed-source collection and replay receipts are retained. The archive
includes the two full modernc projects alongside the sixteen unchanged originals;
this does not finish the remaining complete-source producer or authorize release.

The subsequent nineteen-original offering from
`8bed82dc8d4e198808f8c286ec392f54a09c3a10` includes the pinned JSBT project and
42,434 original members. Collection took 7.2 seconds and independent replay 8.9
seconds using retained originals, without additional network requests. Its
172,822,312-byte asset has SHA256
`d9562099d25343b2e1c220812f59f6b6e6e0993a41cb3f61b6c7366768ee94f8`.
The replay also checked the configuration and all three Noble lock integrities;
private collection/replay receipts are retained. Full source-gate approval and
hosted final-image verification remain pending.

The browser producer now authenticates both image observations before consuming
large source inputs and checks their full independent replay results. It reuses
retained compiler/npm inputs without rebuilding, downloading or rescanning.
The real offering reader replayed all eighteen originals in 8.5 seconds; fourteen
browser source maps resolved the retained 168 rendered package inputs and 256
compiler/plugin inputs in 0.2 seconds. This check combines the exact `5da2ace`
offering and `7d0f42a` capture identified above; it does not represent authenticated
final-image execution for a new source commit or either architecture. Temporary
extraction caches were cleaned and the private receipt retained. The two new
regression methods took 0.23 and 0.03 seconds, including fixture setup/cleanup;
the complete routine-suite result is recorded below.

The subsequent generator replay used that nineteen-original offering and the
retained `7d0f42a` capture. It mapped all 23 rendered virtual inputs, five rendered
generated application inputs (eleven including excluded modules), seventeen
generator/recipe inputs and 100 project source/build inputs in 0.011 seconds.
The exact Vite workspace importer selects Rollup CommonJS 28.0.3; its helper
body matches the authenticated Vite bundle, and the preload template matches
with the documented two identifier substitutions. All sixteen captured Kit
recipe files match the preferred originals. No downloads or rebuilds were
needed; temporary extraction caches were cleaned and the private replay receipt
retained. These are source associations, not byte-identical regeneration or
authenticated final-image checks for the current commit on both architectures.

### Preferred backend project and generator relationships

- [x] Expose selected module members from the existing independent H1 replay,
      reusing verified original ZIP/info bytes without rereading the large asset
      or creating another module cache. Keep the original hash-only API intact.
- [x] Bind both authenticated backend compiler observations to the exact
      publication-bound upstream offering. Authenticate the complete small
      observation set before reading large originals, and check offering mutation
      after the preferred-source comparison.
- [x] Encode exact SQLite/libc module origin/version and every proxy member's
      equality to the preferred full project. Associate both Linux SQLite outputs
      with the original C version and Fossil source ID; use active libc generator
      recipes, musl archive pins, platform overlays/headers and exact retained
      CC/CCGo/fileutil source/version/H1 inputs.
- [x] Pin and boundedly inspect the complete matching sibling libsqlite3 v1.9.0
      project at its immutable official Go/GitLab origin. Restrict its auxiliary
      association to the locked SQLite module and its acquisition to the exact
      full-commit canonical GitLab route.
- [x] Collect and independently replay the twenty-original offering from the
      committed catalog, reusing existing original archives.
- [x] Encode and independently replay the sibling vendoring transformation
      against both generated Linux outputs; keep byte regeneration separate.
- [x] Finish the complete corresponding-source producer, including final image
      notices and independent original dependency notice associations.
- [ ] Authenticate and run the final current candidate source inputs on both
      native architectures, retaining and signing the complete gate evidence.

Actual offline replay used the retained nineteen-original `8bed82d` offering and
SHA-bound `4ae5954` dependency asset. Five selected module ZIPs passed independent
H1 verification in 1.5 seconds; the offering reader took 9.0 seconds and preferred
source association 0.44 seconds. All 1,323 SQLite and 4,153 libc proxy members
match their pinned full projects; 622 libc recipe/overlay/header inputs are mapped.
This verifies retained source relationships, not authenticated native execution
for the current commit or a complete corresponding-source gate. Private receipts
remain retained; no source generators were executed or temporary caches left.

The missing SQLite sibling is `cznic/libsqlite3` commit
`489e7b6027e7cc723ec60b34a00be46236c94cc7` (`modernc.org/libsqlite3 v1.9.0`).
Official Go proxy Origin and GitLab tag metadata agree. Its complete
70,672,238-byte source archive has SHA256
`2fe319a41b9026fdcf10fa1cc7b40983d6209ac06f816045882e03776987a16f`;
bounded inspection checked 1,367 members and 399,686,031 member bytes in 1.2
seconds. The per-original limit is now 80 MiB; aggregate 256 MiB and expanded
512 MiB limits remain unchanged. Both source outputs match the vendoring recipe
under comment/whitespace token normalization, package rename, removal of
`SQLITE_TRANSIENT` and 797 added aliases per platform. This research receipt
identified the inputs; the standard-library AST verifier now checks the same
transformation, with semantic syntax and platform constraints preserved.
The sibling's translator pins are CC/CCGo v4.25.2 and fileutil v1.3.0, distinct
from libc's retained v4.26.0/v1.3.1 tools. Their declared recipes/locks are retained;
independent regeneration would require its old tool environment and is not
claimed by these source associations.

The twenty-original offering from committed source
`06b1425a4f5f0c1b8c552a51fcf56df5a2b00833` contains 43,801 original members.
Collection took 9.5 seconds and independent replay 10.0 seconds, reusing the
nineteen previous archive bytes and the pinned sibling archive without further
network requests. Its 243,327,758-byte asset fits the unchanged 256 MiB aggregate
bound and has SHA256
`0550c863f7c3dcc460402f4c64fa2d381de635c8ccbcf9b767f29451ee3b8197`.
Private collection/replay receipts are retained; full source-gate approval,
current native final-image checks and publication remain pending.

The SQLite verifier is bound to the selected application's committed checker
source and the reviewed original vendoring recipe SHA256
`619a55071e22cac99a8583858379a2c52f6504c38bec049ccb6f6f138ed8d223`.
It applies the package/constant/type-alias changes and compares ordered Go AST
structure. Type aliases, variadic parameters/calls, literal bytes, imports,
declaration order and significant semicolons remain distinguishable. Exactly
matching reviewed Linux build constraints are bound into the comparison;
unsupported executable comment directives refuse verification. Ordinary comments
and formatting are ignored. Inputs are bounded to 40 MiB, compilation to 60
seconds and each comparison to 45 seconds, with no package downloads. One checker
is compiled per backend review; identical independently H1-replayed module ZIP
and origin facts reuse the preferred source result across architectures. Private
checker/input directories are removed after the review.

Actual replay used the twenty-original `06b1425` offering and the retained
`4ae5954` dependency asset, together with the working checker source (SHA256
`8fca230e66f00994e756e7b15ad6f83b1d5394239e1fd473b49cb68b788a393b`).
Five module H1 checks took 1.6 seconds, the offering reader 12.6 seconds and
preferred association including checker compilation and both real platform
comparisons 4.8 seconds. Each platform requires 797 added aliases and has equal
expected/target AST hashes. The private receipt records exact raw input and
structural hashes. The two Go regressions passed in 0.004 seconds (warm-cache
command wall time 0.044 seconds); CI runs these tiny fixtures explicitly. The
Python protocol fixture checks reuse, source/receipt substitutions, timeouts and
cleanup without another compilation. Neither upstream C translation nor the
original GC-based vendoring tool was executed. Native final-image authentication
and execution of the complete corresponding-source producer for the current
candidate remain pending.

### Complete corresponding-source report composition

- [x] Compose the substantive committed application archive, backend module and
      browser preferred/compiler/generator replays. Require all four final image
      identities and every offered source asset; derive canonical category
      evidence hashes instead of accepting caller-provided completion flags.
- [x] Authenticate both existing runtime-source completeness records and bind
      their exact source asset, pack, native smoke, final image and Go/Caddy
      coverage to the complete publication binding. Reuse these retained original
      source replays without repeating collectors, image builds or signature tools.
- [x] Project the complete final backend `/app/licenses` tree with OCI layer and
      whiteout semantics. Check committed application/dependency notices, locks,
      the generated source locator, original Go license and runtime overlays.
      Derive web notice inventory hashes from the existing independent final
      static-tree replay, including copied backend and runtime notices.
- [x] Bind every committed backend dependency notice to the already H1-verified
      original Go ZIP bytes, checking the reviewed normalization recipe and exact
      original/local hashes. Reuse the existing ZIP pass and include these facts
      in the backend category evidence.
- [x] Wire the complete producer and retained evidence into version-tag native
      authentication and signing. Bind verification to the exact workflow run and
      attempt; planned main dispatches remain unsigned.
- [ ] Execute this wiring on both hosted native architectures for the current
      selected source. Local verification does not establish an authenticated
      current-candidate release gate.

The complete producer returns a `corresponding-source` report plus retained
canonical coverage evidence. Individual source helpers retain their partial
completeness/publication flags. The joined report establishes only corresponding
source coverage; protected distribution approval and source delivery/readback
remain separate gates. The selected committed policy, all original source assets,
measurements, runtime pack metadata and final image archives are rechecked before
returning the report.

- [x] Wire source-CI, final-image-smoke, runtime-notices, source-scanners and
      final-image-scanners reports into the same
      tagged assembly and authenticated verification path. Reuse the prepared
      binding and measurement authenticator without another source archive pass.
- [ ] Verify these five gates against actual two-platform hosted evidence for
      the selected tag attempt; local command fixtures do not establish this.

The four new small regression methods cover substituted or missing runtime/source
evidence, incomplete categories, changed final notice bytes and OCI removals or
links. Together they took approximately 0.05 seconds. They use local bytes and
tiny layers, with no image builds, downloads or arbitrary sleeps. The notice
helper also replayed the retained `4ae5954` AMD64 backend archive in 0.23 seconds:
44 final notice files and 22 dependency notices, inventory digest
`sha256:8413cd8478ee12ad88d3acaa3828c13b49d2416262d048e668214bc6bfb85494`.
That earlier archive is development evidence, not authentication of the current
candidate or publication approval.

The dependency notice comparison also passed against the retained `4ae5954`
originals in 0.54 seconds: all fifteen declared modules and twenty-two legal
members match the exact committed inventory and local bytes. Only OTP `NOTICE`
and memory `LICENSE-MMAP-GO` require the reviewed whitespace normalization;
SQLite `SQLITE-LICENSE` is an original ZIP member. The reviewed generator has
SHA256 `1c670e2f028fd422cf5948887946e3da26db7e43f5a49e55695035eb546317ca`;
a changed recipe requires review rather than executing selected-source Python.
The same streaming read rechecked the retained source asset and selected original
ZIP hashes without another H1 replay, extraction or cache. Its private receipt
remains separate from authenticated current-candidate release evidence.

Tagged native jobs attest eight explicit small measurement files after transfer
verification. Assembly authenticates these records, runs the complete source
producer and attests its two canonical output files. The official action is pinned
to `1e69f48acb82d1966a394da916b4c1698aa569d6`; its reviewed distribution uses
the exact run and attempt invocation identity required by the verifier. Signing
permissions do not grant registry publication, release writes or production access.

The source command consumes the prepared release directory, both retained native
transfer directories and the upstream input directory, with explicit repository,
version, commit, run ID and attempt. Its `--verify-only` mode independently
authenticates the generated report and retained evidence without repeating source
replay or image builds. Successful small-record authentication is cached only
within one verifier, keyed by binding, content hash and invocation, with a
64-entry bound; failed or changed records are never reused. Later publication
must independently rebind the selected assets.

Two tiny command fixtures cover exact paths/output binding, stale or partial
outputs and substituted report evidence. A focused authentication fixture checks
attempt substitution and safe cache reuse. These extend existing substantive
replay tests instead of repeating large source fixtures. Workflow YAML parsed,
all 22 shell steps passed `bash -n` and three inline Python blocks parsed; actual
hosted signing and execution remain pending.

The optional `--checks-output` directory retains exactly the source-CI,
final-image-smoke, runtime-notices, source-scanners and final-image-scanners reports
alongside the separate two-file
source output. It shares the existing binding and verifier, checks current CI
before expensive source replay, and reuses authenticated small native records.
Neither generation nor post-attestation verification adds an image build or
source archive pass. Assembly has explicit Actions read permission for the CI
API; the two source files and five check reports are attested together and independently verified before
artifact retention. Planned main dispatches cannot execute this tagged path.

The five command fixtures passed in 0.022 seconds, including three new paired
output cases. They catch stale/substituted reports, output mutation, partial or
overlapping directories and failed CI that must leave complete outputs absent.
The existing eighteen gate-producer fixtures passed in 1.93 seconds and ten
authentication fixtures in 0.123 seconds. These targeted checks followed the
26.7-second full-suite baseline below; another broad suite was unnecessary.

Scanner aggregation reuses the retained raw native reports and all four compiler
graphs. `--resolved-bases` is required with `--checks-output`; exact tagged base
resolution is now attested and authenticated before the substantive source replay.
Aggregation independently derives finding dispositions from retained evidence,
and verification rejects unresolved findings or missing target coverage. It adds
no collector, scanner, container build or upstream signature execution. The
source scanner aggregate creates a local committed Git snapshot and reads/hashes
retained raw reports; image aggregation reuses runtime packs and compiler facts.
Post-attestation verification authenticates completed reports without invoking
these producers or reopening bases.

The existing five command fixtures were extended with native-path mapping,
missing/substituted scanner reports, unresolved findings and early bases
authentication rejection; they passed in 0.028 seconds. Twelve existing source
scanner boundary cases passed in 2.03 seconds and nine image scanner cases in
1.05 seconds. No new test methods, downloads or builds were added to routine CI.
Existing native compiler collection remains release-only and does perform real
source extraction, compiler analysis and backend correspondence compilation;
these small aggregation timings do not promise a faster full release pipeline.

Read-only GitHub inspection on 2026-10-08 confirmed public `endorses/psst.zip`,
remote main `b697119ba81c3aaf18725f38869b778f2e59df9d` and successful CI run
`37669775085`. No protected environments, rulesets or published releases were
configured. The only version tag is preserved `v0.0.0`, pointing to
`998fd83fb8b2915c576547d70c3a472b4291bd7f`; `v0.1.0` has not been created.
This inspection changed no remote configuration, refs or publication state.

### Protected distribution presentation and signing

- [x] Add `present`, `produce` and `verify` commands around the existing strict
      distribution-review producer. Share the authenticated small binding and
      source report; present exact candidate artifact names, final image/notice
      identities, complete source coverage and attempt-bound approval text.
- [x] Present the artifact packet after native recovery succeeds and before the
      protected job waits. Produce and attest the authorized report plus retained
      GitHub review evidence, then independently verify both without another
      source replay or mutable review-history query.
- [x] Provision and independently read back the live `container-release`
      environment for the committed reviewer policy and `v*` tags only.
- [ ] Run the protected workflow on the exact pushed source and verify actual
      reviewer approval/rejection, report signing and artifact layout.

Live provisioning on 2026-10-08 created environment `23740086979`, with exactly
the required user reviewer `endorses` (`172757685`). Self-review is allowed so
the sole personal-project maintainer can review their own release. Custom
deployment policies contain only tag rule `62318446`, pattern `v*`; branches
cannot enter this environment. Both API readbacks matched the selected policy.
No environment secrets, publication permission, registry package or release
were created. A job bypass without an exact authorized recorded review cannot
satisfy the producer. These settings follow GitHub's
[environment API](https://docs.github.com/en/rest/deployments/environments#create-or-update-an-environment)
and [tag deployment policy API](https://docs.github.com/en/rest/deployments/branch-policies#create-a-deployment-branch-policy).

The review packet and tagged native/prepared/source inputs are retained for
seven days, so a pending review does not lose its inputs after one day. Planned
main candidates keep one-day payload retention. Review must finish while its
exact artifacts are retained; expired artifacts fail closed rather than fetching
another attempt or recreating an approval. Published release source retention
and durable interrupted-publication receipts remain separate requirements.

Two tiny CLI fixtures passed in 0.019 seconds, covering presentation references,
production/verification separation, exact authorization and output substitutions.
The two existing producer authorization cases passed in 0.257 seconds. These
tests use local bytes and mocked GitHub evidence; no real approval or publication
is claimed. Workflow YAML parsed, all 29 shell steps passed `bash -n` and three
inline Python blocks parsed. The new local workflow remains unpushed; live
review execution and later publication/VPS rollout are pending.

### Publication attempt identity and first-release prerequisites

- [x] Map the exact downloaded prepared, native and signed-gate artifact paths
      into the publication command's input JSON without rereading source or OCI
      payloads. Reuse the source command's bounded metadata inventory contract.
- [x] Restrict the publication command's default evidence verifier to the exact
      hosted run ID and attempt, matching the gate producers. Reject absent or
      malformed identifiers before creating snapshots or contacting remote APIs.
- [x] Inspect and enable the live repository's immutable-release policy before
      the first release. Independently verify the setting after the change.
- [ ] Provision public, repository-linked `psst-zip-backend` and `psst-zip-web`
      GHCR packages and the separate Administration-read inspection credential.
- [x] Verify the bucket-free driver, local intent-before-write persistence and
      bounded diagnostics with focused offline regressions. Preserve explicit reconciliation/no automatic
      retries after interrupted writes. Actions artifacts expire and uploads can
      fail; local fsync does not protect against runner loss.

On 2026-10-08, the authenticated repository API reported no existing releases,
immutable releases disabled, and HTTP 404 for both expected package names.
The planned immutable-release setting was enabled through the repository API;
an independent read returned `enabled: true`, `enforced_by_owner: false`.
No release, package, image or tag was created by this setup. Package provisioning,
branch/tag protection and the least-privilege workflow inspection credential
remain pending. The [official repository API](https://docs.github.com/en/rest/repos/repos#enable-immutable-releases)
documents the setting and its separate Administration permissions.

The eleven publication command fixtures passed in 1.23 seconds. The new case
checks current-attempt wiring and malformed-identity refusal before snapshots
or transport calls; existing evidence tests cover cryptographic rejection of
stale provenance. It adds no network request, Docker build or arbitrary wait.
`tools/prepare_publication_inputs.py` checks the declared source context, manifest,
subject inventory, exact eight gate names/binding and four regular final archive
paths. Its output is a path map, not authenticated approval; the publication
command still snapshots, hashes and authenticates the selected bytes. Two small
mapping fixtures and the five existing source-command cases passed in 0.036
seconds, including a check that mapping never opens large source/bundle/OCI
payloads. The dependent distribution and recovery command fixtures passed in
0.018 and 0.481 seconds after the shared metadata refactor.
The publication command retains the exact packet and eight authenticated gate
checks. The operator subsequently chose bucket-free publication; the previous
private S3 adapter and provisioning prerequisite are superseded.

### Guarded publication using GitHub and GHCR

- [x] Wire the exact `publish` job after assembled source, signed native recovery
      and authorized distribution review. Keep planned dispatches read-only and
      require explicit publication enablement plus the protected environment.
- [x] Serialize the whole release workflow using the literal
      `container-release-publication` group, with cancellation disabled. Confine
      Contents/package write scopes to the publishing job.
- [x] Remove mandatory S3 configuration, AWS dependencies and the retired
      publication-retention adapter/tests. Preserve independent inspection,
      package initialization and the protected publication gate.
- [x] Preserve immutable input snapshots, fsynced intent-before-write journal,
      failure-state preservation, lease guards and read-only reconciliation.
      An interrupted attempt must not automatically replay remote writes.
- [x] Implement a bounded, explicitly whitelisted diagnostic projection and the
      verified successful receipt as named Actions artifacts. Never upload the
      private journal directory, arbitrary details/errors or credentials; reuse
      existing prepared/native/source artifacts rather than copying large OCI
      archives into another packet.
- [x] Verify bucket-free default publication, substitution refusal, snapshots
      before mutation, interrupted-write behavior and safe diagnostic projection
      with existing focused regressions. Validate workflow syntax and formatting.
- [ ] Configure public repository-linked packages, the least-privilege inspection
      credential, branch/tag protection and explicit enablement; verify hosted
      OIDC, real reviewer decisions and public publication.

GHCR stores the released images; GitHub Releases stores the public source,
deployment files, manifests and provenance. No private bucket or storage
credentials are required for publication. Local journal writes and immutable
snapshots protect the live process; they do not survive runner loss by themselves.
Diagnostic uploads are best effort and expire. Existing tagged preparation,
native/source packets and receipts use bounded Actions retention. Missing or
expired diagnostic material requires fresh read-only inspection of release,
package and tag state; an artifact is neither publication approval nor permission
to retry an uncertain mutation.

The opt-in publishing job checks configuration before downloading large inputs,
uses the existing exact artifact layout, and invokes the shared publication
command. It performs no additional build, source replay, scanner or recovery run.
Its 30-minute deadline bounds publication transfers and readback, separately from
routine CI tests. Publication remains disabled until
`CONTAINER_RELEASE_PUBLICATION_ENABLED=true`. Protected approval after distribution
review remains required; enabling the variable cannot satisfy missing gates.

VPS backups remain separately required before production migration. Their
location, encryption and restore verification are addressed in the existing
backup runbook, without making S3 a prerequisite for releasing containers.

The complete existing release regression suite passed 369 cases in 24.376
seconds; the focused publication suite passed 43 cases in 4.22 seconds. Review
identified a possible FIFO block before the journal file-type check. A
nonblocking open and direct FIFO refusal fixture fixed it; five diagnostic cases
then passed in 0.002 seconds. Projection has a one-minute step bound. Actionlint
and formatting/diff checks passed. The old S3-only adapter and fixtures were
retired; no additional application build, sleeps or large OCI copies were added.
Actual hosted artifact retention and first publication remain unverified.

Exact-head run
[`37737075656`](https://github.com/endorses/psst.zip/actions/runs/37737075656)
passed all five checks at `8e7a6e61ff8ce4b4b4357223a9e4fc8237c077ae` with
no GitHub annotations. Protected PR #4 merged the bucket-free publisher as
`a3280162390f0a3ea9c679160a05ef2986d17ccd`. This verifies integrated CI;
it does not claim that publishing, new diagnostic artifact uploads or
production migration have run.

### Live repository and production environment protections

- [x] Configure and independently verify the active main ruleset using the five
      existing successful GitHub Actions contexts. Require a PR and latest-base
      checks, block deletion/force pushes, and allow the sole maintainer to merge
      their own passing PR without another reviewer.
- [x] Configure and independently read back the `refs/tags/v*` ruleset, allowing
      new tags while blocking updates/deletion. Verify `v0.0.0` remains unchanged.
- [x] Create and verify the main-only `production` environment policy without
      installing SSH credentials, configuring a host or dispatching deployment.
- [x] Track the applied non-secret ruleset/environment payloads and operator
      guidance, preserving separate pending live publication and VPS checks.
- [x] Document the independent immutable-release inspection credential setup:
      a fine-grained token scoped to this repository with Administration read,
      stored directly in the protected `container-release` environment. Record
      expiration/renewal and keep candidate checks independent of this token.
- [x] Confirm the operator-created `PSST_IMMUTABLE_INSPECTION_TOKEN` secret is
      present in `container-release`. The operator selected only this repository,
      Administration read and required Metadata read, with expiration on
      2027-01-06. API readback verified the secret name and update metadata only;
      it did not expose the value or prove successful authenticated use.
- [ ] Verify the inspection credential's actual immutable-policy API access in
      the protected publishing job after the candidate and distribution gates.
- [ ] Configure the remaining environment variables, independent inspection
      credentials, public package namespaces and restricted deployment
      identity; verify hosted release and production behavior against them.

On 2026-10-08, live inspection found no rulesets or legacy main protection and no
production environment. Active `main-ci` ruleset `24694051` now requires exactly
the five successful contexts from GitHub Actions integration `15368`, matched to
remote main `b697119ba81c3aaf18725f38869b778f2e59df9d`. It requires latest-base
checks, PRs and resolved review threads, with no bypass actors. Required reviewer
count is zero; code-owner, last-push and extra unattributed-change approval are
explicitly disabled. This permits self-merge after CI and changes subsequent
publication of code to branch/PR pushes instead of direct pushes to main.

Active `version-tags` ruleset `24694056` covers only `refs/tags/v*`; creation is
allowed, update/deletion prohibited, and there are no bypass actors. An independent
read confirmed `v0.0.0` still points to
`998fd83fb8b2915c576547d70c3a472b4291bd7f`. Stable version syntax remains enforced
by the existing release workflow. GitHub rejected an optional metadata-name rule
with HTTP 422; it was removed from the prepared configuration and the unchanged
active update/delete policy was independently verified. No protected-ref mutation
probe or new tag was attempted.

The `production` environment is `23741939516`, with custom deployment policies
and only branch `main` (`62320707`). It has no additional reviewer requirement,
secrets or variables; manual dispatch remains deliberate deployment authorization
for this personal instance. Separate reads verified its settings, policy count
and empty credential/variable lists. The release environment had no secrets or
variables at that initial checkpoint. On 2026-10-08, the operator subsequently
added `PSST_IMMUTABLE_INSPECTION_TOKEN`; API readback confirmed the exact name and
update timestamp. Its value and successful API use remain unverified. Publication
enablement and package-initialization variables remain unset. No VPS account/key,
host trust, public registry artifact or production transaction was created.

The operator explicitly deferred off-host backup setup on 2026-10-08. Continue
container candidate verification and publication independently. Keep encrypted
off-host protection, its real restore exercise, the first VPS migration and
production Actions deployment pending; deferral does not satisfy or remove the
updater's existing checkpoint gate. Do not provision backup storage meanwhile.

Applied request payloads live under `.github/rulesets/` and
`.github/environments/`; the publication and production operator guides record
the effective settings. JSON parsed, formatter/diff checks passed, and independent
API projections matched the tracked policy; no application tests or builds were
repeated for these reversible configuration changes. Remaining package/publication
and real hosted deployment checks remain pending; off-host backup selection is
separate.

### Release-compatible external gateway verification

- [x] Adapt the existing external-proxy harness to accept an already-loaded,
      immutable backend/web pair and use both release Compose files. Preserve
      source mode, share the selected web image across both proxy hops, and keep
      caller images out of cleanup. Reuse a loaded immutable client image too.
- [x] Verify the existing proxy and managed certificate-state restore flows with
      the retained local pair. Check selected image/configuration identities at
      startup and after restoration/restart; perform no new builds or pulls.
- [ ] Repeat against the final exact tagged candidate and complete public ACME,
      anonymous release acquisition and production adoption verification.

On 2026-10-08, the release-mode proxy flow passed in 20.250 seconds: verified TLS,
isolated ingress, hardening, canonical Origin/authentication/cookies, live SSE,
public revocation, pause/restart/logout persistence, stopped backend restoration
into a new volume, and spoofed-IP rate limits with independent client peers.
The managed certificate flow passed in 16.662 seconds: both gateway state volumes
were copied through backups into new volumes, original/backup contents and key
permissions were preserved, and the original CA trust and leaf certificate
survived restore and restart. Both runs checked the exact selected images and
matching inner/gateway/trusted-proxy configuration and cleaned their disposable
containers, networks, volumes and private temporary files.

These runs reused source `4ae5954223e6ec93f191a6578722551f60fdb54f`, with backend
configuration ID `sha256:70ae03fb2348deafc079ddcd24700c144abc908af360becc92ed100fb83ede69`
and web configuration ID `sha256:e79a61c39d68234e0be066093fd2262c93b99e27d53f280616b9cadc5cc4821e`.
No backend/web application or proxy configuration source changed between that
revision and this checkpoint. The pair remained intact after harness cleanup;
only the diagnostic tags loaded for these checks were subsequently removed.
Its original saved archive remains retained. This verifies current release
Compose/harness behavior, not new hosted provenance or final-candidate approval.
The invocation is documented in the container release guide. No routine CI job
or new test suite was added; syntax, formatting and diff checks passed.

### First-package publication ordering

- [x] Remove the first-release package-creation circular dependency with an
      explicit protected-environment initialization option in the existing
      publication run. Preserve strict existing-public-package behavior by default.
- [x] Admit only paired missing package lookups for initialization; reject mixed
      state, existing private/unlinked packages and permission/server failures.
      Reuse the exact reviewed native archives, held lease and retained journal.
- [x] Require independent public package readback after the complete digest pair
      push and before anonymous pulls, registry version tags or ready publication.
      Bound operator visibility setup to ten minutes without retrying writes,
      automatically resuming a journal or adopting prior seeded images.
- [x] Exercise the actual driver-to-transport initialization path and failure
      boundaries with tiny fake API/clock fixtures; validate malformed settings
      before snapshots and record the operator setup instructions.
- [ ] Verify first-package creation, repository linkage, operator visibility
      changes and anonymous retrieval on the actual GHCR service.

The previous driver required existing public packages before draft reservation,
while its only image transfer required that draft. Separate seeding would also
conflict with deliberate existing-child/index refusal. The opt-in
`PSST_INITIALIZE_GHCR_PACKAGES=true` path instead operates within one authenticated
tag attempt: verify every existing gate and retention prerequisite, admit both
missing package lookups, reserve the draft, push the exact reviewed digests, then
wait for the owner to make the new linked packages public. It changes no package
visibility itself and creates no registry release tags during the wait. Existing
public packages proceed without waiting; preexisting private packages remain a
refusal. Failure preserves the original draft, inputs and journal for explicit
reconciliation. No namespace marker, extra build, seeding receipt or resume mode
was added.

Twenty transport checks passed in 2.014 seconds and fourteen publication driver
checks in 1.564 seconds. Three new transport cases cover strict/default opt-in,
created-private-to-public transition, identity/API failures and deadline expiry
using fake time; one driver case covers the same-run complete release path.
The existing early-refusal case also rejects malformed initialization settings.
No real sleeps, service calls or application builds occur in these fixtures.
Workflow YAML, all 35 shell steps and seven embedded Python blocks parsed;
formatting and diff checks passed. Actual initialization and publication remain
disabled and unrun.

The reviewed deployment checkpoint `bcc4871a4b8d8f17e4f06db8bbb4301cc2b40aad` was
pushed to `container-release-deployment` and opened as
[draft PR #1](https://github.com/endorses/psst.zip/pull/1).
[CI run 37721962715](https://github.com/endorses/psst.zip/actions/runs/37721962715)
passed all five jobs: repository security 1m32s (release regression step 38s),
Android/shared 1m37s, backend 5m23s (race tests 4m54s), web 8m02s (browser tests
5m59s), and iOS 12m32s (build 7m37s, XCTest 2m33s). This proves that checkpoint,
not the subsequent package-ordering correction; its exact hosted verification
remains pending. Main, release tags, enablement and VPS state were unchanged by
the draft PR.

## Verification and completion criteria

Implementation and live rollout are separate gates. Mark tasks complete only
when their work and applicable checks have actually finished. GitHub account
setup, registry publication, production secrets, and live migration remain
pending until performed in those environments.

Keep routine checks focused on observable failures rather than coverage totals.
Use small local fixtures for release parsing, tamper rejection and preservation
rules; keep full image/source/recovery experiments in release verification. The
combined local release/notice regression suite ran 437 tests in 26.729 seconds
on 2026-10-08 after publication wiring, including native preparation, browser input and runtime source
replay fixtures. One existing opt-in Docker case was skipped; actual container
builds remain separate release checks. Per-test timing included fixture setup
and cleanup; the slowest case took 0.753 seconds. All executable cases passed.
The three backend source
regressions took 0.020 seconds; they reject substituted project origins/members,
wrong C source identities, changed generator pins and missing platform/tool inputs.
The six focused browser-source
checks, including the two new generator methods, took 0.005 seconds. These catch
wrong workspace lock selection, substituted helper/configuration bytes and
unclassified rendered source inputs. The source-archive regression
exercises recursive-link rejection and the exact metadata-only exception;
browser fixtures check source and notice substitutions.

For every new test, identify the observable failure it catches and whether an
existing case already covers that contract. Reuse fixtures and retained inputs;
do not add redundant assertions merely to increase coverage. Keep routine
release-tooling fixtures offline, without container builds or arbitrary sleeps.
Investigate if this combined suite exceeds one minute on a comparable runner or
if a routine backend/web job approaches its existing 15-minute limit. These are
investigation thresholds, not reasons to increase timeouts, weaken assertions or
skip meaningful checks. Measure the next hosted run before claiming CI timing.
Do not repeat broad suites without changes, failures or unresolved risks that
justify them.

- [x] Bound native XCTest execution independently of the whole build job. The
      test step now has a ten-minute limit, and XCTest enforces a maximum
      two-minute allowance per case. The existing unconditional result-bundle
      upload remains after the step. These limits fail hangs rather than claim
      faster successful execution. Run `37721962715` passed all 176 native cases;
      the slowest large-file streaming case took 52.873 seconds and the oversized
      inbox case took 27.282 seconds. Both meaningful size-boundary checks remain.
      The iOS build and tests were unchanged in the subsequent `de0721b` source;
      its prolonged live XCTest step prompted this bounded-execution change.
- [x] Verify the native execution limits on macOS/Xcode at `8e18de8`, run
      `37725494465`. All 176 cases passed in 113.668 seconds. XCTest accepted the
      enabled two-minute allowances; its step took 4m23s including startup. The
      downloaded 287,402-byte result artifact matched its recorded SHA256
      `9314828446b27dde518fe792ce521282175ffe46575e902e4b75abc619f99315`;
      its 379 entries included result metadata with no external locations.
      This inspected archive structure and logs, not native xcresulttool decoding.
- [x] Reduce construction costs in the two measured slow native fixtures without
      changing their failure contracts. The 101 MiB streaming test now constructs
      one bridged nonzero full plaintext chunk instead of constructing it for
      every frame, retaining actual nonce/frame encryption, authentication, file
      writing and digest verification. Encryption copies header plus plaintext,
      so the cached input cannot be mutated by its provider. The oversized inbox
      fixture uses 4,800 paths at the original width instead of 5,000 rows, still asserts
      the original exceeds 16 MiB, and retains staged-state reopen/resume, late
      identity, secrets and both JSON/legacy SQLite recovery. The existing
      portable checkpoint harness passed all 17 cases at the initial 4,200-row
      checkpoint in 41.768 seconds, with its oversized case taking 21.913 seconds. This validates migration
      behavior with portable boundaries; it does not prove an iOS timing gain.
      After retaining the original path width, only the changed migration case
      was repeated: it passed in 22.982 seconds, with 5.99 seconds of portable
      compilation. These final refinements still require native verification.
- [x] Measure the initial fixture checkpoint in complete hosted native XCTest.
      Run `37725494465` passed all 176 cases: streaming took 45.733 seconds and
      oversized inbox recovery 33.229 seconds. These different-run measurements
      do not demonstrate an inbox timing improvement over its earlier 27.282
      seconds. Keep that limitation explicit instead of claiming faster tests.
- [x] Verify and measure the final cached nonzero frame and original-width inbox
      fixtures with real Kotlin/Apple boundaries. Exact-head run `37728045919`
      at `14dc7c2` passed all 176 native cases with zero failures in 105.210
      seconds. Streaming took 41.222 seconds and oversized inbox recovery
      32.181 seconds; retain the different-run comparison limitation. The iOS
      job took 13m22s, including an 8m50s build, six-second simulator readiness
      and 2m29s XCTest step. Backend took 6m27s, web 8m02s, Android 2m39s and
      security 1m33s; its release/Compose regressions took 42 seconds. All five
      jobs executed and passed, rather than being selected out.
- [x] Diagnose terminal run `37723307243` before superseding it: backend, web,
      Android and security passed; iOS exhausted the job budget after its build
      succeeded. Its completed log contains no XCTest case or suite start, so
      there is no logged evidence linking the stall to fixture execution. The result upload completed,
      but the partial bundle alone does not identify the startup cause. Retain
      overlapping simulator boot and add an explicit `simctl bootstatus -b`
      readiness check bounded to 120 seconds before XCTest. The step has a
      three-minute cap; the XCTest step's separate cap also covers app/test launch.
- [x] Verify simulator readiness and native test startup on exact `8e18de8`:
      readiness completed in 13 seconds, native build/embedded extension passed,
      and the full XCTest suite executed successfully. The whole iOS job took
      15m13s, including an 8m29s build; web took 8m02s and release fixtures 29s.
      This successful runner check does not prove every future startup stall fixed.

- [x] Scope routine application checks to their actual source/build/fixture
      dependencies. Keep repository history/security checks unconditional and
      preserve all five required job names. Select inside existing jobs so release
      evidence retains its exact five-job contract. Backend changes also run web;
      shared/native toolchain inputs run both mobile jobs. Protocol/shared fixture
      or workflow changes and unknown inputs run the full suite. Missing Git
      baselines or failed classification must never produce a silent skip.
- [x] Require full validation for reusable release calls and manual CI runs,
      regardless of routine path selection. Keep release evidence strict about
      completed successful checks; an unrelated routine green run cannot replace
      the exact tagged source-CI gate.
- [x] Verify scope selection with small offline Git fixtures covering prose-only
      edits versus forced full release checks, dependency crosspaths, renames,
      deletions, complete push ranges and unavailable/unknown inputs. Do not add
      repeated application builds or a second large test suite for this selector.
      Five focused selector cases passed in 1.275 seconds, including subprocess
      execution and Git fixtures; all temporary repositories/event files were
      removed. Both workflows passed actionlint 1.7.12, exact five-job/full-release
      wiring checks, shell parsing and embedded Python syntax checks. No release
      evidence-parser relaxation or additional reusable job was introduced.
- [x] Verify the changed workflow on GitHub before claiming its routine time
      saving or complete native execution. Exact-head run `37728045919` executed
      and passed all five jobs for workflow/native changes. Documentation-only
      run `37729387662` at `0d6a5b7` also completed successfully: security took
      1m25s, while web/backend/Android/iOS jobs took 5/6/9/11 seconds. Step
      metadata confirms all application toolchain setup, compilation and tests
      were selected out; security checks ran unconditionally. Application skips
      mean unaffected source was selected out, not that its tests executed. The
      separate planned release run explicitly requests full validation.

PR #1 merged the fully passing `14dc7c2` into main as
`c2576f8132c5b790e2672543893b52ace3001272` on 2026-10-08; an independent diff
confirmed identical file contents. Planned main-only candidate run
[`37729240625`](https://github.com/endorses/psst.zip/actions/runs/37729240625)
uses that exact merge commit and planned version `v0.1.0`. Its current native
CI failed after the app/extension/test build succeeded: `simctl bootstatus -b`
timed out after 120 seconds, before any XCTest case started. The other four CI
jobs passed, and image/source/recovery preparation was skipped. The separate
main integration run `37729196108` failed at the same readiness boundary on the
same image version as the preceding passing PR. Image/source/recovery checks
remain pending; this dispatch created no release tag and cannot authorize
publication. Provider credentials, tagged review and
the first published release/VPS migration remain separate incomplete tasks.

- [x] Inspect the two terminal readiness failures before changing or repeating
      native CI. Both used runner image `20260907.0337.1`; neither log establishes
      a slow XCTest fixture or a runner-image version change as the cause.
- [x] Modernize deprecated workflow actions using upstream-verified stable
      Node 24 manifests and immutable commit pins. Preserve ZIP artifact names
      and roots explicitly with `archive: true`, keep digest-mismatch rejection,
      and select Gradle's open-source `basic` cache provider. Attest, Buildx and
      golangci action pins already match their maintained stable releases.
      Pin routine Linux jobs to Ubuntu 24.04 instead of a moving `latest` label.
- [x] Retire the old native CI pair in favor of the installed stable Xcode 27.0
      and iOS 27.0 pair on GitHub's `xcode-27` macOS 27 runner. Apple documents simulator cache creation and
      `simctl` hang fixes in [Xcode 26.4](https://developer.apple.com/documentation/xcode-release-notes/xcode-26_4-release-notes).
      The runner infrastructure is still marked preview, approved explicitly by
      the operator on 2026-10-08; select its
      stable compiler, excluding the installed betas. That is a reason to use a
      maintained toolchain, not proof of the internal
      cause of these failures. Retain the existing test/readiness budgets and
      complete native suite; bound listing/boot commands and collect limited
      failure diagnostics. Native Kotlin/SKIE/cryptography compatibility still
      requires hosted compilation and XCTest, beyond the local syntax checks.
      Actionlint 1.7.12 passed with the newly documented hosted runner label
      allowlisted explicitly. All 69 shell steps and eight inline Python scripts
      parsed; immutable action pins, 16 ZIP uploads, 22 named downloads, unchanged
      native budgets and full-release/unconditional-security wiring were checked.
      The iOS source/configuration gate passed; it is not a Swift build.
- [x] Verify the updated actions, simulator startup, full native suite and
      preserved artifact layout on GitHub before checking off hosted execution
      or restarting the planned release candidate from the integrated repair.
      Run `37733269430` passed current-toolchain app/framework/share-extension
      compilation and all 176 XCTest cases. Readiness took one second; the test
      step took 1m52s. The updated artifact action's actual named ZIP layout was
      verified in preceding run `37731752144`; release bundle download/recovery
      remains pending. Final run `37734917348` passed all five jobs with no
      GitHub annotations, including those fixture and backend timeout repairs.
      Protected PR #3 merged as `c4f7eaf1e3e0f0102bcb053548a973072e6f466b`.
      Planned `v0.1.0` candidate
      [run `37739559953`](https://github.com/endorses/psst.zip/actions/runs/37739559953)
      now uses integrated commit `d59ca65ae172e01e98258842c2a7b50cd7d44ba8`,
      including the bucket-free publication workflow. It was dispatched on
      2026-10-08 without creating a tag or enabling publication. All five CI jobs
      passed with no annotations, including all 176 XCTest cases. Common upstream
      retention, immutable base resolution, original image builds and dependency
      collection passed on both architectures. Both native preparation jobs then
      rejected the stale Go 1.26.8/Node 22 validation guard. Final overlays,
      corresponding-source verification and recovery remain pending; this failed
      run is not a release candidate success.

Application compiler, build tool and base-image upgrades are tracked separately
in [the maintained toolchain plan](maintained-build-toolchains.md). The action
runtime modernization does not claim that every existing application dependency
has been upgraded. The maintained native pair has now passed hosted execution;
the integrated release and production gates remain incomplete.

- [x] Profile the complete routine release/notice regression suite after the
      browser source-producer and modernc archive changes; record wall time
      and slowest cases, including fixture setup and cleanup.
- [x] Recheck that profile after publication input mapping and external retention
      wiring. Review their failure contracts and fixture costs: the eighteen
      affected cases passed in approximately 1.43 seconds, using fake service
      responses and small local files without application builds or network calls.
      No material redundancy or slow fixture warranted removing tests or adding
      another test suite. Hosted timing remains a separate pending check.

Transfer staging performs browser replay once before copying and once when
independently verifying the completed destination. Intermediate consistency
checks compare retained file hashes rather than repeating OCI/Git/npm parsing.
After this optimization, the ten transfer regressions passed in 1.6 seconds.

The pre-candidate full hosted CI baseline `37734917348` took 1m36s for repository
security, 7m24s for backend, 7m36s for web, 4m11s for Android/shared and 16m16s
for native iOS. Native compilation accounted for 13m34s; the XCTest step took
1m50s, with 77.677 seconds of case execution. Routine job limits are 10, 15, 15,
15 and 25 minutes respectively. The Go package limit remains 10 minutes with
race checking preserved. All existing limits held; they catch hangs rather than
establish faster execution. Full release verification includes additional
collection and recovery work with separate budgets; these measurements do not
promise a sub-30-minute release pipeline.

The completed source-CI checkpoint inside candidate run `37739559953` took
1m33s for security, 7m27s for backend, 7m15s for web, 2m36s for Android/shared
and 23m05s for iOS. Native compilation took 18m42s, readiness five seconds,
and the XCTest step 2m53s; all 176 cases passed with 113.325 seconds of case
execution. The existing limits held. These are actual timings on the selected
runner, not a claim that the later failed native-container preparation passed.

- [x] Replace the runtime-dependent real CLI parser fixture after documentation
      PR run `37741423347` exceeded its five-second timeout. Use an unsupported
      test hostname that the real CLI rejects after identity-flag validation and
      before trust initialization or attestation retrieval. An incompatible
      signer-workflow subcase proves this sentinel does not mask argument errors.
      Retain production identity policy, the five-second fixture deadline and
      all authenticated publication gates.
- [x] Verify the focused fixture against installed `gh` 2.101.0 and the
      checksum-verified official 2.102.0 used by the failed runner image. All ten
      evidence tests passed in 0.154s and 0.152s respectively; the existing 370
      release tests passed locally in 25.045s. The timeout's historical cause
      remains unproven. No application builds or additional slow tests were added.
- [x] Align original-pair Go/Node record validation with the trusted application
      base tags rather than obsolete literals. Require the exact selected
      versions and native platform; retain immutable base maps, archive/config
      hashes and separate Caddy compiler correspondence. Fourteen focused native
      preparation tests completed in 0.062s: thirteen passed and the existing
      opt-in live Docker test was explicitly skipped. Recovery fixtures now use
      these selected producer versions; all eleven recovery checks passed in
      2.035s after the stricter guard exposed their obsolete positive records.
- [x] Preserve component-specific compiler validation in final image-scan
      aggregation. Require the graph's Go version to equal its exact runtime
      binary binding, then require the backend's selected Go version or Caddy's
      committed original-runtime source policy. Ten focused gate checks passed
      in 1.041s, including altered/rebound version refusal. This does not waive
      authenticated graph, binary/source hash, compiler-setting or native-platform
      checks. A bounded review of these changes found no blocking issue.
- [x] Run the combined lightweight release regressions after those corrections.
      All 371 cases passed on Python 3.14.7 in 25.653s. This does not repeat
      application builds or prove complete hosted container/recovery execution.
- [x] Verify these repairs and the latest stable Linux x64 runner on hosted CI.
      Exact-head run `37744385013` passed all five required checks at
      `e6995f135b8e44d0927462fc6ceeac2e555a3a47`, with no check annotations.
      The release regressions passed 371 cases in 34.855s. Native iOS took
      19m01s: compilation 14m51s, readiness two seconds and XCTest 2m36s. All
      176 cases passed with 108.306s of case execution. Existing limits held.
      Protected PR #6 merged as `3a8db34023a6bec9e8528a4ceb51b333e85c156b`;
      its tree matches the checked head. This does not prove Arm64 or the full
      container/source/recovery workflow.
- [ ] Run a new planned native candidate through complete source and recovery
      checks from integrated main, including the latest stable Linux Arm64 runner.
      Do not restart the terminal failed attempt or claim fixture results prove
      a complete image/source pair.

- [x] Inspect planned candidate `37747004338` from integrated commit
      `8485eb41cdfa7cd7102b0faf44d20285b88aa02c`. All five source-CI jobs
      passed with no annotations; Android/shared took 2m45s and native iOS
      14m56s. iOS built in 11m53s and passed all 176 cases with 76.115s of
      case execution. Both native container jobs built the original backend,
      web and web-builder images, then failed at authentic metadata recording
      with `Missing built image config digest`. Complete source replay,
      recovery, signing and publication did not execute.
- [x] Identify the image-store compatibility failure using official Buildx
      0.37.2 source and disposable Docker 29.8.2/BuildKit 0.34.0 AMD64 scratch
      builds. Containerd-backed `--load` omits `containerimage.config.digest`;
      explicit archive export preserves it but the engine's image ID remains a
      manifest digest, and lookup by config digest fails. The same current
      stable versions with the classic overlay2 store preserve the required
      build metadata, archive-config hash and engine config identity contract.
      This proves the scratch contract on AMD64, not hosted application replay
      on either architecture. Keep the strict consumer checks intact.
- [x] Configure disposable native build/recovery measurement daemons with stable
      Docker 29.8.2 and explicit classic overlay2 storage using pinned Node 24
      `docker/setup-docker-action` 5.5.0. Check the exact daemon socket, version,
      driver and architecture before use, preserving original `--load` metadata
      and helper image identity. Production's updater uses its engine's opaque
      image identity and registry manifest references; this CI setting does not
      change the VPS daemon or establish a production storage-driver prerequisite.
- [x] Verify the configured measurement daemon and original-pair metadata on both
      hosted architectures. Planned candidate `37756970689`, source
      `2eb45035582d42229c8ae9302b1e208f07853edd`, passed engine setup,
      exact socket/version/driver/architecture checks, original native builds and
      authentic build metadata plus paired saves on AMD64 and ARM64. No
      config-digest fallback or weakening of source/identity checks was needed.
- [ ] Complete native source replay, assembly and recovery. Candidate
      `37756970689` ended with both architectures refusing native preparation:
      `Runtime collection command failed: docker`. Original-pair metadata and
      application dependency collection passed; final overlays, complete source
      replay, assembly and recovery did not. Diagnose the retained failure before
      dispatching a repaired source; do not restart this terminal attempt.
- [x] Make runtime collection failures identify their operation and exit status
      without exposing command arguments, environment, or captured output. The
      earlier generic Docker failure does not establish a root cause. Add labels
      for image inspection/save, helper/runtime APK inventories and source
      fetching; preserve all collection and checksum checks. One focused
      regression checks that sensitive details stay hidden and successful output
      is preserved; all 18 existing runtime-boundary cases passed in 0.008s.
      Bounded review identified timeout and launch exceptions as another possible
      command-detail leak. Convert them to fixed operation-only failures with
      suppressed exception context; injected exception subcases in the same test
      verify that boundary without a real timeout. Native preparation passed its
      13 cases in 0.086s with the existing opt-in Docker case explicitly skipped.
- [ ] Establish the hosted runtime-collection failure from these bounded
      diagnostics before making a cause-specific repair. A local helper-only
      probe passed inventory/save and multiple source-fetch operations, which
      does not prove the complete application inventory on either hosted runner.
      A compile-free backend-runtime scaffold using the exact Alpine 3.24.2 base,
      unchanged APK installation and app user also passed inventories, image save,
      and the first retained `alpine-baselayout` source fetch with checksum
      verification. It stopped there and cleaned its image and temporary files;
      this is not complete source replay or evidence of the hosted failure cause.
- [x] Inspect the next planned candidate's exact-source CI and bounded runtime
      diagnostic. Run `37761829904`, source
      `36a9c48e1f985733281cef90c22fea98e410e802`, passed all five full source-CI
      jobs with zero annotations. Both native jobs then failed with
      `apk-source-package-fetch (exit 1)` after original builds and metadata,
      isolating the failed command stage without exposing private output.
      The run is terminal; source replay, assembly, recovery and publication did
      not complete. Both measurement daemons were explicitly rootful, so an
      automatic rootless/user-namespace ownership explanation is unsupported.
- [x] Narrow the failed source fetch without publishing arbitrary subprocess
      output. Validate and report its installed origin, version and packaging
      commit plus a fixed checksum/permission/DNS/network/TLS/HTTP/unclassified
      diagnostic category. The category is a diagnostic hint, not authenticated
      completion evidence or permission to bypass a guard. Existing timeout and
      launch privacy remains; commands, environment, URLs and raw output stay
      private. Injected category and unsafe-identity subcases reuse the existing
      regression: 18 runtime cases passed in 0.010s and native preparation had
      13 passes with one existing opt-in skip in 0.064s. An exact first-origin
      local fetch also passed under UID/GID 1001 with protected output ownership
      and unchanged SHA512 verification. No hosted cause-specific repair is yet
      established, and no checksum was regenerated or ignored.
- [x] Address the measured missing Kotlin/Native input cache in routine iOS CI.
      Preserve `~/.konan` with an immutable stable Node 24 cache action and
      exact host/toolchain inputs; enable Gradle's local build cache for shared.
      All existing tests and time budgets remain. See
      [the build-performance checkpoint](maintained-build-toolchains.md#native-ci-build-performance)
      for timing evidence and unresolved Xcode setup time.
- [x] Verify cold cache save on hosted macOS. Exact-head CI `37751784908`
      passed all five checks at `cc84fef0b9e5a2d4d0414d6b5be57dd82f0af619`,
      with zero annotations. iOS passed all 176 cases with 108.483s of case
      execution; its cold job took 23m16s, including an 18m18s build and a 57s
      successful 409,931,939-byte compiler cache save. This proves cache
      population, not faster compilation. PR #8 merged through protection as
      `cf67e1eac2082bc7f095c630be2bb100bb06b0dc`; the merged tree equals the
      checked head. The subsequent candidate verified both original measurement
      daemons; complete native source/recovery execution remains pending.
- [x] Verify warm restore on hosted macOS, including cache transfer overhead.
      Main CI `37754625017` passed with zero annotations and seeded the exact
      main-scoped cache. The candidate restored it in 29s on the same runner image
      and Xcode build. Native iOS took 17m07s versus the cold main run's 19m16s;
      first Gradle work dropped from 8m30s to 3m02s. Greater startup/Swift/test
      times offset part of that gain, so the observed complete-job improvement
      is 2m09s. All 176 cases passed in 93.706s; all five candidate source-CI jobs
      passed with zero annotations. No tests or limits changed and no benchmark
      suite or extra full CI rerun was added. Full source/recovery and publication
      remain separate pending checks.

- [x] Remove the unnecessary source-CI wait from base resolution, common upstream
      retention and native preparation. Require validated source identity first,
      retain assembly's dependency on all five CI jobs, and preserve every
      downstream recovery/review/publication gate, permission and the literal
      serialized workflow group. Bounded dependency review found no early
      consumer of CI outcomes in those three preparation jobs. No job, test or
      timeout was added or removed. Actionlint passed; the two existing
      exact-attempt/all-five-outcomes and failed/skipped/pending/missing-CI
      producer regressions passed in 0.183s. Formatting and diff checks passed.
- [x] Verify hosted overlap on an integrated candidate, including that assembly
      waits for every required exact-source server CI outcome under the current
      three-job contract. Candidate `37774541174` ran native preparation alongside
      server CI and completed assembly only after both native jobs and all three
      server checks passed. This proves scheduling and the assembly boundary,
      not a completed publication or a general workflow speedup.
- [x] Avoid unchanged application builds for the specifically reviewed release
      workflow on ordinary PR/main CI. Classify only
      `.github/workflows/release.yml` as security-only; retain all-app selection
      for CI/unknown workflows and selector/selector-test changes. Repository
      security still runs. Tag, planned dispatch and explicit full validation
      independently force all application jobs. Existing range/classification
      regressions, extended with the exact exception and unknown-workflow cases,
      passed all five tests in 1.227s; no new test methods were added. The
      selector-policy change itself must still complete all hosted app checks.
- [ ] Verify the selector-policy change on hosted CI and confirm that a later
      release-workflow-only PR skips application steps while planned/tagged
      release validation still executes every required source-CI job
      (security/backend/web for containers under the superseding scope below).

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
- [x] Run repository-security/backend/web release gates for the container
      candidate. Keep routine mobile CI and its native macOS/Xcode requirements
      separate; Linux checks do not replace mobile build/device verification.
- [ ] Demonstrate anonymous installation from the published release bundle and
      images, followed by a successful manually selected production update and a
      verified recovery path. Document any remaining operator-only checks.

This plan does not include Play Store/App Store distribution or native signing
automation. It preserves both mobile platforms' existing release validation and
shared protocol compatibility while adding server image publication/deployment.

### Server-only release validation checkpoint

- [x] Add a boolean reusable-CI container scope, defaulting off, that skips both
      mobile jobs only for the server release caller. Retain ordinary PR/main
      mobile selection, all protected main checks, server tests and time budgets.
- [x] Require exact successful security/backend/web outcomes in both the source-CI
      producer and publication consumer. Preserve exact tag/run/attempt/source
      validation, pagination/duplicate handling and rejection of failed, skipped,
      pending or missing required server checks. Known mobile jobs are excluded
      from server evidence; unknown reusable job names still fail closed.
- [x] Verify focused producer/publication regressions and both workflow schemas;
      42 existing checks passed in 6.559s, including rejection of each required
      server job when failed, skipped, pending or missing. Both workflows passed
      Actionlint. The corresponding-source command's five existing checks also
      passed in 0.028s after updating its fixture to the server-only contract.
      No new test methods or full local application rerun were added.
- [x] Verify the integrated planned candidate executes all three server/security
      checks, skips mobile compilation and overlaps native container preparation.
      Candidate `37771648745` at `f211717009c7cb4dd426cde3d8d5c3c3a621b16d`
      passed security/backend/web, skipped both mobile jobs and began both native
      image preparations while server tests were still active. Actual publication
      and production migration remain separate pending gates.

Candidate `37765615690` passed all five checks under the previous contract, then
both native container jobs failed while fetching `apk-tools` `3.0.8-r0` sources
from packaging commit `4588b452722bd4800efdc6cce4f6e980e02a997f`. The fixed
diagnostic category was HTTP. This identifies a server runtime-source input,
not an Android build failure. The original recipe/checksum passed locally;
the official Alpine distfiles mirror also served the exact original bytes.
Hosted validation of the mirror repair passed on candidate `37771648745` below.

The archive is retained from
[Alpine's official distfiles](https://distfiles.alpinelinux.org/distfiles/v3.24/apk-tools-v3.0.8.tar.gz)
against the unchanged
[exact APKBUILD](https://raw.githubusercontent.com/alpinelinux/aports/4588b452722bd4800efdc6cce4f6e980e02a997f/main/apk-tools/APKBUILD).
The diagnostic identifies an HTTP fetch failure; it does not establish its
status code or a general runner-network outage.

- [x] Restore the failing source fetch through abuild's supported official
      `DISTFILES_MIRROR`. Read the validated helper's stable Alpine release once
      without network and derive its fixed official major/minor mirror branch.
      Keep original recipes, SHA512 sums, independent source verification and
      container isolation unchanged. The exact implemented `apk-tools` path
      fetched from the mirror and passed its original SHA512 independently;
      18 runtime regressions passed in 0.009s and native preparation had 13
      passes with one existing opt-in skip in 0.064s. Temporary probes were removed.
- [x] Verify the mirror repair in both native hosted jobs. On candidate
      `37771648745`, AMD64 job `113292675879` completed native source preparation
      in 4m25s and ARM64 job `113292675915` in 4m10s, passing the previously failing
      collection and downstream overlay/image measurements with unchanged
      checksum guards. Private fetch URLs remain suppressed; this is a verified
      successful preparation, not a claim to have observed individual network
      traces. Both complete native jobs subsequently passed, including compiler
      correspondence and source/final-image scanning and measurements.
- [x] Complete source replay, candidate assembly and disposable recovery before
      publication. Tagged run `37782470022` completed signed source assembly and
      authenticated both native recovery results as recorded below. Public
      acquisition and production recovery remain separate pending checks.

The server-scope/source-fetch PR #13 completed all five ordinary protected checks
and GitGuardian with zero annotations at
`fc17542bf796a226ac1934723fbcf6e5c8a5dd9d`, and merged through protection as
`f211717009c7cb4dd426cde3d8d5c3c3a621b16d`. The merged tree equals the checked
head. The planned candidate uses that exact merged commit; publication remained
disabled at dispatch, no version tag was created, and no production state changed.
The delegated iOS investigation continues independently on a local diagnostic
branch; its follow-up changes do not gate this container candidate.

### Selected Docker daemon consistency repair

Candidate `37771648745` is terminal: both native container jobs passed, but
assembly job `113296396976` failed with `Collector and scanner actual builders
differ`. Disposable recovery and publication were skipped. The small retained
records show scanner `config_digest` equal to the shared multiarchitecture
manifest digest on both architectures, rather than distinct native configuration
digests. The collector's embedded configuration record is not included in these
small summaries, so they alone do not establish its exact observed digest.

The collector preserves the selected `DOCKER_HOST`; the source scanner subprocess
previously retained only a default `PATH`, dropping the workflow's isolated daemon
and installed CLI. This switches the scanner to the runner's original Docker
store, inconsistent with the explicit native measurement configuration. The
immutable reference/configuration/platform comparison remains required.

- [x] Preserve the installed CLI path and explicitly scoped Docker connection
      settings in source/compiler scanner subprocesses, excluding GitHub tokens
      and the remaining workflow environment. Add a bounded actual subprocess
      regression for selected-daemon propagation and secret exclusion.
- [x] Verify focused scanner, dependency-input and source-scan gate regressions
      after the final edit: 41 checks passed in 2.242 seconds. The existing
      immutable-builder mismatch checks remain active; formatting and diff checks
      passed. No local full application build or extra timing run was added.
- [x] Validate the integrated hosted assembly. PR #14 passed all five ordinary
      protected checks and GitGuardian with zero annotations at
      `8bedad47f0add26f1975067542839d57a38ccba0`, then merged through protection
      as `49bbeea73b1dc483eeb549d682ad3a862c916cd3` with an equal tree. Planned
      candidate `37774541174` used that exact commit, passed both complete native
      preparations and assembly job `113305596210`, including unchanged strict
      collector/scanner builder comparisons. Backend/web CI finished in 6m22s
      and 6m21s respectively; subsequent artifact transfer is separate release
      work. No tag was created and publication remained disabled.
- [x] Validate disposable native recovery after the default-executor repair.
      Candidate `37778271584` at
      `77c6cebc238c37830ab5421da02392f860b7aba5` passed both AMD64 job
      `113320071540` and ARM64 job `113320071495`. Their retained measurements
      bind the same manifest and deployment bundle to that exact source and
      record normal upgrade/repeat/isolated restore, preservation of a prior
      pause, and recovery after an injected post-migration startup failure.
      This is unsigned disposable evidence; signed aggregation, public
      acquisition, independent off-host backups and production restore remain
      unverified. The earlier candidate `37774541174` is terminal:
      both ARM64 job `113308361468` and AMD64 job `113308361461` reached the actual
      producer and failed before the experiment with `TypeError: command()
missing 1 required keyword-only argument: 'environment'`. The default
      command adapter was not exercised by the injected-executor fixtures.
      Aggregated authentication, distribution review and publication were skipped;
      the later successful candidate verifies the repair below on both platforms.
- [x] Repair the real default recovery command adapter by supplying its mandatory
      scoped environment while preserving the installed Docker CLI, configuration
      and native connection. Keep injected executor signatures and recovery
      assertions unchanged. Add one regression that omits the executor override
      and enforces the transport's actual required argument and secret exclusion.
      The 12 measurement checks passed in 2.631s and nine gate checks in 0.535s;
      per-file Black and diff checks passed. This verifies the wiring locally,
      not a successful hosted Docker recovery experiment.
- [ ] Publish the first reviewed server release only after the remaining gates
      pass. Production backup/restore and migration remain explicitly deferred.

PR #15 passed all five ordinary protected checks and GitGuardian with zero
annotations at `f3d69912788d85f6b179964ae0fd20d555d2b243`, then merged through
protection as `77c6cebc238c37830ab5421da02392f860b7aba5` with an equal tree.
The planned candidate at that merged commit completed successfully with zero
annotations. All three server CI jobs, both native image preparations, strict
assembly and both recovery jobs passed; mobile compilation, signed aggregation,
distribution approval and publication were intentionally skipped.

Backend CI took 6m24s. Web CI took 10m54s: integration tests 6s, browser
installation 3m26s, browser tests 5m47s and administrator lifecycle checks 40s.
These are observations from the necessary candidate, not an isolated performance
experiment. Recovery jobs took 9m03s on AMD64 and 11m36s on ARM64; their actual
measurement steps took 7m27s and 6m51s respectively, including downloaded-tree
verification. Artifact transfer is separate from those steps. No extra full test
run, test deletion or timeout increase was introduced to obtain these results.

The retained measurements explicitly report `off_host_provider_verified: false`,
`public_provenance_verified: false` and `publication_authorized: false`. They
preserve settings, encrypted payloads, quota/expiry enforcement, sessions and
factor state, reconcile restored authority and traffic allowances, and preserve
certificate storage in isolated fixture volumes. They do not establish public
ACME renewal or browser/mobile flows. Publication was disabled, `v0.1.0` remained
unused and no VPS state changed. The next step is the exact version-tag attempt,
signed gates and its protected distribution review.

The scheduling PR #12 completed all five required checks with zero annotations
at `b023bddd0669f88bcebbd050db104c9caeeae7ac` and merged through protection as
`3e599052e7170f8687004d132b47b25d3ec70221`; its tree matches the checked head.
That checkpoint verified its selector-policy change on hosted CI, not a completed
release or overlapping preparation. The later candidate above verifies overlap;
an exact release-workflow-only PR remains needed to observe application-step
skipping on that particular change scope.

### First signed v0.1.0 candidate

- [x] Create the previously unused `v0.1.0` tag at the protected and verified
      source `77c6cebc238c37830ab5421da02392f860b7aba5`, preserving `v0.0.0`.
      Tagged run `37782470022`, attempt 1, uses that exact commit. Configure
      publication enablement and protected first-package initialization only
      after the successful planned candidate; retain all signed gates and the
      required `endorses` environment approval.
- [x] Complete exact tagged security/backend/web CI, both native image jobs and
      signed source assembly. Assembly job `113333210975` authenticated the
      current native evidence, produced and attested the complete source report,
      then independently verified its signature without repeating source replay.
      All six source/CI/scan/smoke/notice reports share binding
      `sha256:4390f6af409d61603617cab58ef23a7d327e7cbbccd25f5a21b49cadbbcc8561`.
      The complete source report covers all four native images and six source
      archives; its SHA256 is
      `1b3d9c98e0b1507181e667515cb82e246bbeaab2ab9d48532b904e920f099aa0`.
- [x] Complete both tagged native recovery measurements and authenticate their
      aggregate for this exact binding and run attempt. ARM64 job `113335447979`
      and AMD64 job `113335448974` passed; aggregate job `113339640600` verified
      the measurements, attested the recovery gate and independently verified it.
      Its report shares the exact source binding above. Independent off-host
      provider, public provenance and browser/mobile-flow checks remain false.
- [x] Present the exact distribution packet before the configured human review.
      Artifact `candidate-distribution-presentation-37782470022-1` contains the
      final image/source/notice references and attempt-bound approval comment.
      GitHub initially held job `113339930195` for the configured
      `container-release` reviewer `endorses` before the operator approved it.
- [x] Obtain the configured human review, including its attempt-bound approval
      comment. Distribution job `113339930195` completed, attested the retained
      approval evidence and independently verified it. The operator separately
      approved the protected publisher. These decisions do not prove successful
      publication.
- [ ] Publish the immutable release and paired repository-linked public packages,
      then verify fresh anonymous image/source/deployment-file retrieval. Remove
      first-package initialization after successful publication.

The release tag and publication configuration do not deploy the VPS. Independent
backup/restore and production migration remain deferred. Store research drafts
and the delegated iOS diagnostic branch remain outside these release commits.

All completed pre-publication gate jobs have zero annotations. Source CI took 1m04s for
repository security, 4m18s for backend and 6m43s for web. Native image jobs took
9m12s on AMD64 and 8m59s on ARM64; signed assembly took 4m58s, with authenticated
source production taking 2m37s and independent signature verification 24s.
Recovery jobs took 9m12s on AMD64 and 7m51s on ARM64; aggregation took 33s.
The workflow reached human review in approximately 25 minutes. This includes
native image/source preparation, artifact transfer and actual recovery, rather
than a 25-minute routine application test suite. No timeout was extended or
additional timing run requested.

Publisher job `113343113073` subsequently failed in its trusted-tool preflight
with `Official hosted runner Node24 runtime is unavailable`, before setup-go,
payload downloads or the publication command. The workflow never reached a
release reservation, image push, source upload or VPS operation. A read-only
GitHub release lookup returned 404, and the existing annotated `v0.1.0` tag
remains unchanged. Do not retry mutations or move that tag to hide the failure.

- [x] Repair the hosted runtime resolver against the official runner's actual
      runtime-selection contract, preserving signer, native architecture,
      ownership/permission and bounded-execution checks. Derive the Node24
      location from the kernel-reported `Runner.Worker` ancestor and official
      relative `externals/node24/bin/node` path, with bounded ancestry traversal
      and malformed/looping/ambiguous/unsafe-worker refusal. Preserve legacy
      runtime inventory and never select a signer through caller PATH or an
      environment-provided installation root. Seventeen focused attestor checks
      passed in 0.076s, with the genuine hosted probe explicitly skipped locally;
      this verifies local behavior, not the unavailable hosted path.
- [x] Exercise the actual resolver once on ordinary hosted repository-security
      CI before preparing the next immutable patch-version candidate. Local
      fixtures alone do not establish a real hosted installation path. PR #18 at
      `f702cbed4536ef4e626640ca64a3e710d511032c` passed all five protected
      contexts and GitGuardian with zero annotations. Run `37788969183`, security
      job `113350941685`, explicitly reports the unmocked runtime probe `ok` on
      runner `2.337.0` and image `20260927.149.1`, matching the failed publisher's
      versions. Application steps were correctly skipped for these reviewed
      release-helper changes. A bounded independent review found no blocker.

The exact runner version was `2.337.0` on Ubuntu `26.04.1`, image
`ubuntu26/20260927.149`. Its published
[HostContext](https://raw.githubusercontent.com/actions/runner/v2.337.0/src/Runner.Common/HostContext.cs)
derives the installation root from the executing worker's bin directory, and its
[Node action handler](https://raw.githubusercontent.com/actions/runner/v2.337.0/src/Runner.Worker/Handlers/NodeScriptActionHandler.cs)
selects the advertised runtime relative to that root. The failed job did not
record an absolute runtime path; the repair does not invent one. One harmless,
bounded unmocked lookup now runs in the existing hosted Linux security tests,
without signing, network requests, additional application builds or benchmarks.

### Corrected v0.1.1 publication candidate

- [x] Merge the verified runtime repair through protected PR #18 as
      `84313f4d74e9f624a62327af180b1f7399e2097b`, matching the checked head's
      tree. Create the unused `v0.1.1` tag at that exact source, preserving both
      `v0.0.0` and the failed `v0.1.0` tag. Run `37790269573`, attempt 1, is
      verifying this candidate; creating the tag does not publish or deploy it.
- [x] Complete exact tagged server CI, both native image preparations and signed
      source/scan/smoke/notice assembly for the corrected source. Assembly job
      `113360559867` passed source authentication, signing and independent
      verification. All six reports passed with binding
      `sha256:161bc5080809b45b68a24108e7d72cfa355ac63bdab532358f1403b6b4b03eae`.
      The complete source report covers four images and six source archives;
      its SHA256 is
      `b440ea156dbff19971f3b50f36e2bbafa1dc1bb87f9cccd8af94f6a947396bda`.
- [x] Authenticate both native recovery measurements and their signed aggregate
      for this exact source binding and attempt. ARM64 job `113363007391` and
      AMD64 job `113363007431` passed; aggregate job `113367188509` passed
      authenticated derivation, signing and independent verification. The retained
      gate uses the source binding above, manifest
      `sha256:27533c9662bd4d87a0e788a2eeb43de6d7d19a1a81f10eb83796884e6c71040b`
      and deployment bundle
      `sha256:7a4dcb0d3ba8d5cec73e7a7aaeee75453167d7286d89229c8901bed6405f6db5`.
      Off-host provider, public provenance and browser/mobile-flow checks remain
      explicitly false.
- [x] Prepare and inspect the fresh distribution packet
      `candidate-distribution-presentation-37790269573-1`. It identifies the new
      source, image/source/notice subjects and exact attempt-bound comment.
      GitHub is holding distribution job `113367582260` for the configured
      `container-release` reviewer `endorses`.
- [x] Obtain the configured human distribution review for this candidate.
      Distribution job `113367582260` authenticated, attested and independently
      verified the operator's decision. The operator separately approved
      publisher job `113369559069`. These approvals authorize this candidate's
      attempt, not the previous version's subjects or a future changed source.
- [ ] Publish the immutable release and paired public packages, verify fresh
      anonymous retrieval, and remove first-package initialization.

Production migration and independent off-host backup/restore remain deferred.

This necessary tagged attempt's server CI took 1m38s for security, 6m31s for
backend and 7m45s for web. Native preparation took 8m32s on ARM64 and 9m47s on
AMD64. Signed assembly took 5m01s; recovery took 8m23s on ARM64 and 8m37s on AMD64.
All completed gate jobs have zero annotations at this checkpoint. Separate
mobile builds were correctly skipped. No additional benchmark run or timeout
increase was used to obtain these timings.

Publisher job `113369559069` passed the repaired hosted runtime preflight,
native transfer-tool build, retained-payload downloads and path mapping. Its
publication command then stopped after approximately 42 seconds without a
published receipt. The safe diagnostic artifact
`publication-diagnostics-37790269573-1` reports `journal_present: false` and
`records: []`. Read-only lookups returned 404 for the release and both packages.
The exact exception is absent from the log; diagnosis remains pending. Preserve
`v0.1.1` and do not retry mutations or claim public delivery from these approvals.

- [x] Check the publisher's report authentication independently using only small
      retained artifacts. The original 14-subject inventory reconstructed from
      the presentation matches the complete binding above. Production
      `GhEvidenceVerifier` authenticated all eight reports in 35.743 seconds and
      cached cross-gate validation passed in 0.011 seconds. Approximately 860KB
      of compressed reports were downloaded; no image/source archives, rebuilds,
      signing or remote writes were needed. The owned temporary directory was
      removed. This did not reproduce the failure and does not verify the
      original hosted credentials, environment or transient state.
- [x] Expose fixed preparation-stage and exception-category diagnostics before
      another publication attempt, preserving credential privacy, signed gates,
      mutation order and journal recovery. The CLI emits allowlisted constant
      stage names and fixed type-derived categories, never raw exceptions,
      paths, API bodies, credentials or subprocess stderr. Four injected
      secret-bearing pre-transaction failures preserved useful diagnostics,
      cleaned snapshots and made no remote writes or journal. The existing
      lifecycle/order and private-package checks also passed. The full focused
      publisher-command module passed all 14 tests in 1.816 seconds; per-file
      Black and diff checks passed. This validates diagnostics locally, not the
      cause or resolution of the original hosted failure.
- [x] Check the next pure smoke-configuration predicate using authenticated
      real report bytes and actual retained binding/child references. The exact
      committed `tested_configurations` predicate passed all four native
      configurations in 4.307 seconds. Its explicitly limited metadata projection
      does not establish full manifest/plan validation or the remaining hosted
      preflight checks. No image/source download, build, signing or remote write
      was required; the owned temporary directory was removed.
- [ ] Identify the failing hosted preparation boundary before claiming that
      publication is repaired. Preserve existing tags and authenticated subjects;
      changed source requires a fresh unused patch-version attempt and review.

The boundary review confirms that publisher mutations start after journal
initialization. This attempt stopped before that transaction began; the lost
exception prevents identifying the exact preparation boundary from its log.

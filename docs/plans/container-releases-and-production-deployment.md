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

The release workflow currently verifies candidates; it does not publish images,
releases, attestations or deployment bundles. It has `contents: read` permissions
and no registry, production or signing credentials. All Actions in CI and the
candidate workflow are pinned to verified upstream commits, and checkouts disable
persisted credentials. All five source CI jobs and their commands were preserved.

Strict tag/event/ancestry checks precede reusable CI. The two native build jobs
are gated on all CI jobs and share resolved index digests for Go, Alpine, Node,
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
      pending; the candidate workflow still has no signing/publishing permission.

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
      The candidate workflow records measurements without signing or publishing;
      a missing runtime pack cannot satisfy the final publication gate.

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
- [ ] Provision the `container-release` review environment and wire the strict
      distribution producer into the final trusted workflow after complete source
      production. Present concrete final artifacts before approval; attest both
      the gate and retained review evidence. Verify a real approval and rejection.
- [ ] Wire the producers and signed native records into the reviewed workflow
      and validate actual two-platform aggregation. The current candidate-only
      workflow emits unsigned smoke measurements.
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
`publish`, plus a local held lock. The current candidate-only workflow has not
been changed to publish. Immutable policy inspection may require a separate
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
Template syntax/effective configuration, host installation, production
environment protections and the actual SSH/deployment flow remain pending.

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

## Verification and completion criteria

Implementation and live rollout are separate gates. Mark tasks complete only
when their work and applicable checks have actually finished. GitHub account
setup, registry publication, production secrets, and live migration remain
pending until performed in those environments.

Keep routine checks focused on observable failures rather than coverage totals.
Use small local fixtures for release parsing, tamper rejection and preservation
rules; keep full image/source/recovery experiments in release verification. The
combined local release-tooling suite passed 345 tests in 23.9 seconds after the
source/distribution and copied-static changes (one opt-in Docker case skipped; real builder capture
was run separately). The new source-archive regression exercises recursive-link
rejection and the exact metadata-only exception; browser fixtures check source
and notice substitutions. Do not add coverage-only cases or repeat broad suites
without changes, failures or unresolved risks that justify them.

Transfer staging performs browser replay once before copying and once when
independently verifying the completed destination. Intermediate consistency
checks compare retained file hashes rather than repeating OCI/Git/npm parsing.
After this optimization, the ten transfer regressions passed in 1.6 seconds.

The last passing hosted CI run `37669775085` took approximately 1 minute for
repository security, 4.2 minutes for backend, 8 minutes for web and 14 minutes for
native iOS. Routine job limits are now 10, 15, 15 and 25 minutes respectively;
the Go test package limit is 10 minutes with race checking preserved. These catch
hangs rather than establish faster execution. Confirm the new limits on the next
hosted run. Full release verification includes additional collection and recovery
work and has separate budgets; these measurements do not promise a sub-30-minute
release pipeline.

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

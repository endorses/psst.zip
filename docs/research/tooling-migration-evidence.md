# Tooling migration map and measured evidence

Snapshot: 2026-10-24, implementation HEAD
`e7011377bee9b0d0d1c74689ed83ee666b02ddb2`. The original baseline is
`df002e447dcd04842c854b3796b2b7e84c4cf3ae`. The [migration map](tooling-migration-map.csv)
reconciles all **135** [original inventory](python-tooling-inventory.csv) rows in
their original order. The baseline inventory and its [assessment](python-tooling-assessment.md)
remain historical records and were not rewritten. This is a local implementation
reconciliation, not evidence of a hosted release, production updater installation
or measured upload savings.

## Reconciliation and current ownership

Every baseline row records current existence/size, named replacement or deliberate
retained owner, current command/manual references, Python AST imports, literal
fixture references, filename discovery, output consumers and a removal or retention
trigger. A reference is labeled as a caller/config, manual or historical record;
its mere presence is not proof of execution. No file is classified as dead from
an empty text search. Removed filenames in historical Git ranges now select the
unknown-path full CI coverage rather than keeping obsolete selector exemptions.

| State        | Baseline entries | Meaning                                                                                                                                                                        |
| ------------ | ---------------: | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Retained     |               47 | Deliberate compact validators, native/recovery probes, narrow Go-command adapters and installed/published v1 compatibility readers, with named consumers and removal triggers. |
| Migrated     |               35 | The old implementation is absent and a named Go/native owner supplies the capability.                                                                                          |
| Consolidated |               48 | The old file is absent; its behavior belongs to a shared command, package, native harness or renamed independent fixture.                                                      |
| Retired      |                5 | The approved exceptional signing bridge, exact-comment/duplicate-review protocol or custom attestor mechanism and its protocol-only tests were removed.                        |
| **Total**    |          **135** | **Every baseline row has one current owner and disposition.**                                                                                                                  |

The baseline cohort now has **47 present files /
21,712 physical lines / 844,987 bytes**, compared with
135 / 65,825 / 2,583,684 at baseline. The current tracked Python total, including
additions, is **51 files / 22,845 lines / 890,015 bytes**.
Counts use UTF-8 bytes and physical `splitlines()` counts. They include comments,
blank lines and substantive independent test scenarios, exclude untracked drafts,
user transfers, third-party caches and inline YAML, and do not reproduce GitHub
Linguist or measure runtime performance. File reduction is an observation, not a quota.

The four post-baseline Python additions are accounted for separately:

| Current addition                        | Lines |  Bytes | Actual retained owner                                                                                                               |
| --------------------------------------- | ----: | -----: | ----------------------------------------------------------------------------------------------------------------------------------- |
| `tools/container_release_policy.py`     |    63 |  2,635 | Narrow committed-policy compatibility reader for retained historical Python report consumers; Go owns current publication policy.   |
| `tools/release_process.py`              |    59 |  1,815 | Bounded private process/timeout adapter for retained native and compatibility callers; no custom action runtime.                    |
| `tools/release_result.py`               |   175 |  5,842 | Strict canonical Go-result reader at the Python native/recovery experiment boundary; no separate signed gate producer.              |
| `tools/test_release_recovery_inputs.py` |   836 | 34,736 | Renamed independent input/provenance/actual OCI/save/bundle producer-boundary tests; obsolete aggregate gate fixtures were removed. |

The Go tool owns current source retention/replay, original notice correspondence,
OCI graphs, candidate assembly/transfer, source/image scan policy, canonical release
results and publication. Existing Node/Vite tooling still compiles and instruments
the browser. Gradle owns ordinary mobile source/dependency export and the explicit
SwiftPM harness owns portable Swift tests. The map distinguishes these changes
from translating every test or native process runner into Go.

The retained `prepare_native_release.py` runs actual Docker builds/exports and
smoke probes; `measure_release_recovery.py` runs disposable upgrade/restart/failure
experiments. Their small Go-contract adapters do not maintain a second source,
scanner, OCI or publication policy. `verify_release_images.py` keeps independent
HTTP/authentication/runtime-source-offer probes. `request_production_deployment.py`
keeps the bounded fixed-SSH version-only client and secret handling without making
an operator build the development tool. Read-only historical subject/evidence
readers remain while these experiment and old published-v1 consumers need them.
Their explicit removal triggers are in the CSV; these are deliberate scope
boundaries, not unimplemented promises to port whole test harnesses.

## Callers outside a filename-only search

- `hooks/pre-commit` executes `check_repository.py staged`; CI runs independent
  complete-history/files/bidi checks. `select_ci_checks.py` serves all five
  application job selectors and fails closed on unknown paths or incomplete history.
- `ci.yml` discovers `test_release_*.py`, named native/repository/bidi tests and
  the Android test pattern. Discovery is a caller. Importing
  `test_release_updater_integration.py` does not run its guarded Docker scenario;
  the release recovery producer invokes that explicit scenario separately.
- Container release inline Python now supplies bounded native diagnostic
  redaction, a small build-metadata projection and the independent recovery
  experiment bridge. That bridge imports the native context, read-only GitHub
  evidence verifier, recovery producer and compatible artifact reader. Removed
  source/image/assembly/publication producers are no longer imported by the workflow.
- The container workflow runs Go source/image/compiler commands and one Go source
  evaluator, then one Go recovery aggregation/result command that consumes the direct typed result rather than an envelope CLI. Cross-job inputs are
  authenticated before their claims are consumed. Final assets and standard
  provenance retain their existing manifest/bundle/certificate identities.
- Native iOS inline Python selects and boots the installed simulator, bounds
  readiness waits and failure diagnostics. Optional resource sampling remains
  separate from actual `xcodebuild` timing and XCTest validation.
- `tools/fixtures/release-updater/{checkpoint,controller,flows,verify}.py` are
  copied into a disposable root runner and called through protected fixture paths.
  They are independent behavior scenarios, not production backend request code.
- The compact `tools/runtime-legal/verify.py` and its tests remain direct offline
  CI checks of the curated original legal data. Go collection/replay independently
  consumes that data; it no longer executes or packages the Python checker as its
  source verifier. The original data and full legal texts remain retained.
- Current backend notice generation/checking is Go. Its original/distributed-byte
  checks replaced the Python generator and the old hash-pinned-generator consumer
  together; the shipped normalized notice bytes remain unchanged.
- CLI and Android workflows intentionally retain small old-source fallbacks only
  for immutable pre-migration tags without the Go release tool. Those branches
  execute the old tag checkout, never a missing producer from current main.

## Local implementation and measured checks

The common-originals collection at source `6ba7e30`, planned version `v0.1.999`,
retained **193 sources** (173 npm distributions and 20 upstream originals),
**412,351,580 bytes**, in **2m35s**. An exact-source native Node builder took
**22.5 s**. Actual capture, common-original replay and preferred-source checks
passed in **8.42 s**, covering 4,337 inputs, 34 outputs, 168 rendered preferred
inputs, 276 generator inputs, 23 virtual and 11 generated associations. Docker's
temporary-mount retrieval issue was found and corrected using bounded archive
streaming from the running isolated container. These were publication-disabled
experiments, not final Caddy/backend image validation.

Actual backend preferred-source replay passed in **7.96 s**: 5,476 proxy members,
both Linux SQLite syntax translations, 622 libc recipe/overlay/header members and
three generator tool originals. A later positive corresponding-source data
rehearsal took **23.86 s**, using actual Git/common originals, 18 H1-authenticated
modules, an offline-built backend and normalized legal texts; final runtime and
browser facts were explicitly synthetic. It rejected altered AGPL bytes and an
omitted original module. Neither run claims upstream generator reproduction or
an actual final native image pair. See [source tooling verification](../testing/container-source-tooling.md).

One fresh common npm audit took **3.204 s**. The actual native AMD64 Go 1.27.3
source producer took **75.754 s**, including cold pinned-scanner installation and
both actual import graphs. Independent raw replay matched all scanner/database/
graph/finding facts: 21 module-only Go findings had every officially affected
package absent from both graphs; three npm findings remained unresolved. No npm
scan was repeated per architecture. The existing Vite finding policy and expired
Caddy exception remain closed; consolidation did not suppress either blocker.

Normal source/scanner tests stay offline: representative runs took **0.160 s /
0.148 s**, and focused scanner race checks **1.266 s**. Retained native preparation
fixtures passed in **2.149 s** (14 tests, one optional Docker export check skipped);
independent native smoke/report checks passed in **2.926 s**. The current Python
release suite passed **168 tests in 20.503 s**, and the tooling Go suite took
**0.61 s warm** before the final recovery integration. These scope/cache conditions
are explicit; they are not a cold/warm hosted benchmark. Temporary actual-data
harnesses were removed, and no network/source-download workload was added to the
ordinary unit suite merely to increase coverage.

At `7c0b45e`, the Go bundle command built a real current-checkout
`v0.1.999` deployment-ready archive of **155,691 bytes**. The existing independent
Python v1 manifest/bundle readers accepted it in **0.861 s**, including the cached
tool lookup. Image/index values for that parser cross-check were fixtures; this
was not source/OCI/publication validation. Its temporary output was removed.

Producer deadlines remain bounded: source collection uses the existing isolated
native container limits, the Go source scanner inner deadline is 15 minutes,
Git snapshot/process operations have short deadlines and strict byte limits,
and routine CLI CI keeps its five-minute limit. A deadline is a failure bound,
not a target duration. Application/mobile suites were not run to inflate tooling
coverage or to claim a complete platform release.

## Existing CI measurements

These are read-only GitHub observations. No workflow or application suite was
started to establish this baseline. Durations below are `completed_at - started_at`
for successful jobs/steps. Skipped jobs have no usable duration; API timestamps
for skipped entries must not be converted into negative or zero build results.

| Existing run / source                                                                                                       | Relevant measured jobs and steps                                                                                                                                                                                                                                                                |
| --------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [Exact baseline main CI 40023161209](https://github.com/endorses/psst.zip/actions/runs/40023161209), `df002e4`, attempt 1   | Security **100 s**, including release/Compose regressions **41 s**. CLI **158 s**: assets 15 s, vet 23 s, race tests 45 s, build/smoke 19 s, backend/TypeScript interoperability 19 s. Android/shared 161 s; native iOS 854 s, including build-for-testing 601 s and XCTest 120 s.              |
| [Prior PR CI 40021062448](https://github.com/endorses/psst.zip/actions/runs/40021062448), `1413326`, attempt 1              | Security **81 s**, including release/Compose regressions **34 s**. CLI **233 s**: assets 23 s, vet 37 s, race 66 s, build/smoke 31 s, interoperability 28 s. This verifies the earlier assessment's 1m21s security observation.                                                                 |
| [Published v0.1.6 release 37901084985](https://github.com/endorses/psst.zip/actions/runs/37901084985), `c2b0fc2`, attempt 1 | Common upstream collection **66 s**; base resolution 23 s. Native amd64 **558 s**, arm64 **577 s**; native overlay/source preparation steps 240 / 275 s, respectively. Assembly 232 s; recovery amd64 417 s and arm64 430 s; protected publication 477 s, including publication/readback 375 s. |
| [Prior Android release 39044606117](https://github.com/endorses/psst.zip/actions/runs/39044606117), `351cb91`, attempt 1    | Source validation **6 s**; unsigned build/check job **240 s**, including Gradle assemble/lint/missing tests **205 s** and notice verification 11 s; protected signing/publication 37 s.                                                                                                         |

These jobs have different source/platform/cache conditions. They are not an
apples-to-apples cold/warm benchmark. Existing local measurements recorded in
the [implementation plan](../plans/tooling-consolidation-and-go-migration.md)
include unchanged Go asset preparation **0.95 s**, packaged LAN browser scenarios
**1.6 s**, and independent backend/CLI/TypeScript interoperability **9.2 s**
including backend compilation. Portable Swift tests compiled in **32.2 s** and
ran XCTest in **70.7 s**, excluding dependency resolution. Those are reported
local results with their original scope; no new paired benchmark was run here.

### Artifact and publication bytes

The v0.1.6 run's Actions artifact API now returns no entries because its short
retention has elapsed. That means current retained artifact sizes are unavailable,
**not** that the workflow uploaded zero bytes. Its two native job logs still
contain these upload action terminal records:

| Architecture                                                                                              | Native inputs artifact (bytes) | Recovery images artifact (bytes) |       Sum (bytes) |
| --------------------------------------------------------------------------------------------------------- | -----------------------------: | -------------------------------: | ----------------: |
| amd64, [job 113723802034](https://github.com/endorses/psst.zip/actions/runs/37901084985/job/113723802034) |                    788,490,852 |                      365,476,288 |     1,153,967,140 |
| arm64, [job 113723802000](https://github.com/endorses/psst.zip/actions/runs/37901084985/job/113723802000) |                    773,360,235 |                      349,038,528 |     1,122,398,763 |
| **Both**                                                                                                  |              **1,561,851,087** |                  **714,514,816** | **2,276,365,903** |

These are the upload action's reported final artifact sizes, including its archive
representation; they are not raw uncompressed image sizes or metered network
traffic. Native inputs also contain source/evidence, and overlapping artifacts
may use different compression. Therefore **714,514,816 bytes is not a proven
net saving** from the new split. Current workflow paths exclude all image saves
and OCI exports from source/evidence and upload them once per architecture in
`candidate-native-images-*`; receiving jobs rejoin that payload before checking
the canonical inventory. A comparable completed run of that changed workflow is
still needed to measure the resulting stored/uploaded sizes.

For comparison, current API records report baseline CI's iOS result artifact as
**309,692 bytes** and prior Android release artifacts as **59,327,699 bytes**
(59,326,992-byte preview plus 707-byte source record). The immutable public
v0.1.6 GitHub release has **8 assets / 1,336,980,350 bytes**. Published assets
and Actions transport artifacts are distinct populations and must not be added
or compared as if they measured the same payload.

## Real published v1 compatibility evidence

The public [v0.1.6 release](https://github.com/endorses/psst.zip/releases/tag/v0.1.6)
is immutable, published 2026-10-09, at source
`c2b0fc2d94c71d97ac1c4ba26598604cedc27f75`. Read-only downloads established:

| Public asset                                                                                                         | Exact observed size / SHA-256                                                     |
| -------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| [release-manifest.json](https://github.com/endorses/psst.zip/releases/download/v0.1.6/release-manifest.json)         | 2,300 bytes; `ff22d5279d58e325faab46f311a59080ef724d33cbb81fe86cb0173518fb6c2b`   |
| [Deployment bundle](https://github.com/endorses/psst.zip/releases/download/v0.1.6/psst.zip-deployment-v0.1.6.tar.gz) | 146,518 bytes; `51868bd64186cb1af0fdd31f4255341469fff17b9f44d3ced71bd215ec5ae2cd` |

The manifest contains exactly the stable v1 sections `schema_version`, `version`,
`payload_profile`, `source`, `platforms`, `images`, `bundle`, `requirements`,
`notes` and `build`. It declares `deployment-ready`, ordered amd64/arm64 platforms,
paired backend/web registry index references and per-platform child digests,
Docker 27.0.0 / Compose 2.24.4 requirements, checkpoint/migration/rollback notes,
five pinned base images including optional `buildkit`, and Go `go1.27.2` / Node
`v26.10.0` build identities. Old immutable build identities are compatibility
data; they are not the tool versions selected for this migration.

The gzip bundle has **43 regular members**, all uid/gid 0 and mode 0644. Its
`release-bundle.json` has exactly `schema_version: 1`, `version: v0.1.6`,
`source_commit: c2b0fc2...` and `payload_profile: deployment-ready`. It retains
33 license members plus Compose/proxy configuration, manifest schema, environment
example, `deploy/update.py` and `tools/release_artifacts.py`. Existing readers
require member identities, content checksums, bounded regular-file archives and
the Python compatibility payloads. Switching the installed helper and changing
future bundle requirements must preserve these old immutable assets.

The manifest's public provenance **verified successfully** using `gh attestation
verify` with the exact repository, workflow certificate identity, signer/source
commit, tag reference, GitHub OIDC issuer, SLSA predicate and rejection of
self-hosted runners. The observed representation was:

```text
Sigstore bundle mediaType: application/vnd.dev.sigstore.bundle.v0.3+json
DSSE payloadType: application/vnd.in-toto+json
statement._type: https://in-toto.io/Statement/v1
subject: release-manifest.json, sha256 ff22d527...fb6c2b
predicateType: https://slsa.dev/provenance/v1
buildType: https://actions.github.io/buildtypes/workflow/v1
externalParameters.workflow:
  repository: https://github.com/endorses/psst.zip
  path: .github/workflows/release.yml
  ref: refs/tags/v0.1.6
resolvedDependencies[0].digest.gitCommit: c2b0fc2...c27f75
runDetails.builder.id:
  https://github.com/endorses/psst.zip/.github/workflows/release.yml@refs/tags/v0.1.6
runDetails.metadata.invocationId:
  https://github.com/endorses/psst.zip/actions/runs/37901084985/attempts/1
certificate issuer: https://token.actions.githubusercontent.com
certificate signer/source digests: c2b0fc2...c27f75
certificate runnerEnvironment: github-hosted
```

This records a real manifest subject and successful verification, not every
public OCI child/index attestation. The deployment bundle checksum was matched to
the manifest; its independent provenance was not separately reverified here.
No production updater, SSH command, account state or signing credential was used.

The Go contract/deployment tests already contain **synthetic** v1 manifests,
configurations, active/transaction/checkpoint records, invalid archives and
off-host receipts. Those fixtures prove parser/state/fault boundaries; their
repeated placeholder digests and simulated provider receipts are not public
release provenance. The [disposable Go updater evidence](../testing/go-vps-updater.md)
adds real Docker/TLS/SQLite behavior with local acquisition, still without a
production installation or remote provenance claim.

## Tool pins, compatibility and unavailable hosted evidence

The [maintained toolchain policy](../plans/maintained-build-toolchains.md) remains
the owner. The migration selected Go **1.27.3**, Node **26.12.1**, BuildKit
**0.34.1** and Caddy **2.11.7** after primary-source checks. Official stable Go
metadata and the exact Go source tag/hash were checked; complete older Go
originals remain available for the actual Caddy compiler. Backend compiler
identity and Caddy compiler identity are verified independently.

The normal [setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0)
and [attest v4.2.2](https://github.com/actions/attest/releases/tag/v4.2.2) pins
replace no supported immutable identity. Caddy's official legacy detached
signature format requires the reviewed, exact-hash Cosign 2.6.5 compatibility
verifier; it is not an unreviewed outdated tool or a weaker fallback. Upstream
certificate identity, transparency and Rekor verification remain required.

The baseline security and CLI annotation endpoints returned empty arrays. That
observed absence is limited to those old checks. No hosted run of the complete
migration checkout has been observed, so current runner/action annotations,
both native Linux execution results, comparable upload sizes and net transport
savings are **unavailable**. The historical timing/artifact table above must not
be presented as measured improvements of the new workflow.

The new Go `psst-deploy` candidate and opt-in installer preserve v1 manifest,
bundle and transaction contracts. Its standalone runtime does not invoke Python
or execute code downloaded inside a release bundle. That does not remove the
existing VPS's Python prerequisite: the old root-owned updater and parser remain
installed until an explicit operator cutover. V1 bundles still include their inert
compatibility payloads, and old immutable assets are unchanged. The staged
installer retains the old helper/configuration and checks both readers under the
shared lock; no production installation or SSH cutover was performed here.

## Validation that remains external

- [x] Reconcile all 135 baseline rows with actual current existence, named owners,
      import/discovery/manual consumers and removal or retention triggers; account
      for current post-baseline Python additions separately.
- [x] Replace redundant active source/publication producer chains with shared Go
      commands and retain useful raw observations and independent byte/legal tests.
- [x] Record actual bounded original-source/browser/scanner measurements and
      preserve the distinction between real retained data and synthetic final facts.
- [ ] Execute normal affected hosted CI for this migration and inspect its actual
      runner/action warnings; local compilation is not native iOS/Android validation.
- [ ] Complete a publication-disabled two-architecture container rehearsal with
      actual final images, source/scanner policy and recovery experiments, then
      compare resulting job/step and artifact-upload measurements fairly.
- [ ] Review and perform any actual VPS Go-updater cutover separately with the
      operator. Staged installer code and local Docker/SQLite/TLS checks do not
      establish a production change or off-host backup protection.

The explicit retained Python owners above are part of the simplified system.
Future retirement requires removing their real consumers and preserving their
independent behavioral role, rather than treating missing literal callers or a
GitHub language percentage as sufficient evidence.

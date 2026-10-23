# Tooling migration map and measured evidence

Snapshot: 2026-10-24, implementation HEAD
`959f9b25e7ba2f4166e38ccc483b7006ea8f9f10` plus the ongoing working-tree
migration. The original baseline is `df002e447dcd04842c854b3796b2b7e84c4cf3ae`.
The [migration map](tooling-migration-map.csv) reconciles all **135** original
[inventory](python-tooling-inventory.csv) rows in their original order. Neither
the baseline inventory nor its [assessment](python-tooling-assessment.md) was
rewritten. This snapshot records remaining work; it is not stage-8 closure.

## Reconciliation and status meanings

The map records current file existence/size, entry-point and manual callers,
Python imports/test consumers, filename discovery, output consumers, the current
owner, replacement owner and removal milestone. Evidence includes tracked
workflows, Make/Gradle files, Dockerfiles, hooks, documentation, Python AST imports
and dynamic filename references, plus the new Go tooling. A textual reference is
identified as such; it is not by itself proof that a command executes. No entry
is classified as dead merely because a literal caller was not found.

| State        | Baseline entries | Meaning                                                                                                                                                             |
| ------------ | ---------------: | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Retained     |               62 | The compact tool or active implementation's behavior/tests still has an owner and consumers. Retention does not mean every release test must remain Python forever. |
| In-progress  |               40 | Replacement primitives/commands exist, but workflow/import, source-pack or installed-host consumers still require the old surface.                                  |
| Migrated     |                8 | The old implementation is absent and a named replacement owns its capability.                                                                                       |
| Consolidated |               21 | The old file is absent and its capability/tests now belong to a shared command, package or native harness.                                                          |
| Retired      |                4 | A mechanism was deliberately removed under the agreed policy, including its protocol-only tests.                                                                    |
| **Total**    |          **135** | **Every baseline row has one state and removal/retention milestone.**                                                                                               |

The baseline cohort now has 102 present files, 56,407 physical lines and
2,217,623 bytes, compared with 135 / 65,825 / 2,583,684 at baseline. These
cohort counts exclude additions. The current tracked Python total is **104
files / 56,645 lines / 2,226,100 bytes**, including the new narrow
`tools/container_release_policy.py` (63 lines) and `tools/release_result.py`
(175 lines). Those adapters retain committed publication policy and read the
canonical Go-produced result while active Python consumers migrate. These are
source-size observations, not a language deletion quota or execution-time result.

Completed removals belong to commits `497bd7f` (publication policy/result
simplification), `8827434` (native mobile tooling/signing bridge retirement) and
`959f9b2` (CLI Go tooling). The CSV names the relevant removal milestone per row.
The remaining 27-line `github_release_attestor.py` only validates public tag
events; its former custom JavaScript/runtime-discovery signing mechanism is
already gone. Current workflow signing belongs to the normal pinned
`actions/attest` step, and tag-event checking is moving to `psst-release github`.

## Callers that a filename-only search misses

- `hooks/pre-commit` executes `check_repository.py staged`; CI separately runs
  complete-history/files checks. `select_ci_checks.py` supplies all five
  backend/web/CLI/Android/iOS job selectors. Their compact Python implementations
  and security/selection regressions remain deliberate dependencies.
- `ci.yml` discovers `test_release_*.py`, `test_android*.py` and the named native,
  runtime, repository and bidi suites. Discovery is a caller even if no source
  file spells an individual test path. It imports
  `test_release_updater_integration.py`, but does **not** execute that module's
  guarded Docker scenario; `measure_release_recovery.py` imports and invokes
  that explicit harness separately.
- Inline Python in `release.yml` imports `generate_release_gate_reports`,
  `prepare_candidate_transfer`, `prepare_release_inputs`, `release_artifacts`,
  `aggregate_release_recovery`, `measure_release_recovery`, `release_result`
  and publication helpers. These remain active production workflow consumers.
  Other inline blocks handle native record redaction, compiler metadata, small
  JSON projections and protected publication settings. They are not represented
  by the 135 standalone Python files.
- Inline Python in `ci.yml` selects/boots the installed iOS simulator, waits for
  readiness and bounds failure diagnostics. `diagnose_build.py` now samples
  resources only when requested; native `xcodebuild` timing remains available.
  Go migration of release infrastructure does not remove these native needs.
- `tools/fixtures/release-updater/{checkpoint,controller,flows,verify}.py` are
  copied into the disposable root runner. Hooks are invoked by protected paths,
  not necessarily a literal `python3 file.py` command in the caller.
- `tools/runtime-legal/verify.py` and `test_verify.py` are run directly by CI
  and copied into runtime source packages. The retained source/replay consumer
  matters even if a workflow stops naming the original repository path.
- Backend notice generation remains hash-pinned by
  `verify_application_dependency_inputs.py`; a successful Go reproduction alone
  does not authorize deleting that producer before its source consumers switch.
- `cli-release.yml` intentionally retains a Python packaging fallback only when
  checking out immutable pre-migration source tags without `tools/release/build.sh`.
  The helper is absent from the new checkout. This is historical compatibility,
  not a stale command to remove without deciding support for those tags.

The original inventory excludes inline snippets and future additions. The map
accounts for their relationships, and this section records the remaining inline
owners rather than pretending they were baseline standalone files.

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

## Changed tool pins and hosted annotations

The maintained [toolchain policy](../plans/maintained-build-toolchains.md)
remains the owner. This slice changes no pins or application dependencies. On
2026-10-24, primary sources confirmed the migration's Go **1.27.3** selection
on the [official download page](https://go.dev/dl/),
[setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0) and
[attest v4.2.2](https://github.com/actions/attest/releases/tag/v4.2.2) as their
latest stable releases. The existing `b7ad1dad...` setup-go pin is reused;
`1e69f48a...` is the standard attest pin replacing custom action execution.
Unchanged app/native dependencies were not audited or upgraded for this map.

The exact-baseline security and CLI check-run annotation endpoints both returned
empty arrays. That is observed absence of annotations on those checks, not a
claim about all jobs or the unpushed migration. There is no hosted run of the
current migration checkout, so its actual runner/action warnings remain
**unavailable** until normal affected CI executes.

## Remaining migration and measurement work

- [ ] Switch the 35 existing container source/OCI/scanner/preparation/publication
      producer/helper entries marked in-progress in the CSV, together with inline
      workflow imports, receiving-byte checks and their source-pack consumers. Shared
      Go packages alone do not complete those transitions.
- [ ] Coordinate backend notice generator migration with its hash-pinned source
      verifier/replay consumer, and preserve exact distributed notice bytes.
- [ ] Migrate the SQLite preferred-source relationship and production deployment
      request consumer. Preserve version-only SSH and provenance requirements.
- [ ] Complete the authorized installed-host Go updater cutover, keeping the old
      helper/parser and immutable v1 assets compatible until no supported consumer
      needs them. Local Docker success does not complete this item.
- [ ] Retire compatibility adapters and protocol-only tests only after their last
      real consumer switches. Keep behavior/negative cases and independent native,
      browser/crypto and explicit recovery scenarios with the owning implementation.
- [ ] Record a comparable completed migration CI/release run for job/step time,
      action annotations and native image/source artifact bytes. Cold/warm labels,
      raw image-byte duplication and net transport savings remain unmeasured.

The retained compact repository/localization/brand tools, optional native
diagnostic, source-pack checkers and active Python regression fixtures are
explicit exceptions with owners. The CSV's per-entry milestones govern future
removal; this document does not infer deletions from a missing literal caller.

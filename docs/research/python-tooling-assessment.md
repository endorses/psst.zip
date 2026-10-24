# Python tooling inventory and consolidation assessment

Date: 2026-10-23. Source baseline: `df002e447dcd04842c854b3796b2b7e84c4cf3ae`.

This is an assessment, not an implementation or permission to change publication
policy. No application, release workflow, signing configuration, or deployed
updater was changed. The accompanying [inventory](python-tooling-inventory.csv)
accounts for every tracked Python file at this baseline.

## Findings

Most Python is active release and deployment infrastructure, not abandoned
application code. The largest reduction will come from simplifying its protocol:
fewer intermediate reports, repeated inventories, copies, and approval mechanisms.
Translating the existing system wholesale into Go would preserve its complexity.

Use Go for substantial shared release/build operations and eventually the VPS
updater. Put Android build concerns into Gradle Kotlin, browser instrumentation
into Node/TypeScript, and portable Swift tests into SwiftPM. Keep small independent
Python tools where their ecosystem makes them the simpler choice.

There is no verified large pile of immediately deletable unused scripts. Many
apparently unreferenced tests are discovered by filename, and several release
modules are imported by inline Python in YAML. Recommendations below distinguish
removing a redundant mechanism from deleting an active entry point today.

## Inventory and method

| Area                    | Python files | Physical lines |  Source bytes |
| ----------------------- | -----------: | -------------: | ------------: |
| `tools/`                |          107 |         59,050 |     2,324,093 |
| `deploy/`               |            1 |          2,805 |       112,764 |
| `ios/scripts/`          |           19 |          1,735 |        63,830 |
| `client/scripts/`       |            5 |          1,689 |        62,093 |
| `assets/brand/`         |            1 |            269 |         9,867 |
| `android/localization/` |            1 |            148 |         6,111 |
| `backend/scripts/`      |            1 |            129 |         4,926 |
| **Total**               |      **135** |     **65,825** | **2,583,684** |

Counts use tracked `*.py` files, UTF-8 byte length, and physical `splitlines()`
counts, including blank lines and comments. They do not include inline Python in
YAML, generated/untracked files, or third-party caches. They are not a reproduction
of GitHub Linguist's language calculation.

Of these, 69 filenames start with `test`: 27,951 lines / 1,149,905 bytes. Fourteen
are Swift-test launchers, not Python unit tests. AST inspection finds 598 Python
`test_` function definitions: 595 in `tools/`, three in the CLI asset regression
script. These are definitions, not a measured execution count or coverage metric.

Method: inspect tracked source, Python imports, literal command references,
workflow entry points, Make/Gradle calls, test discovery, documentation, and
artifact producers/consumers. Three scoped read-only reviews covered container
orchestration, source/notices packaging, and native/client tooling; the primary
review covered the updater, repository checks, test organization, and totals.
No builds, releases, network probes, or benchmarks were run for this assessment.
Runtime savings and final deletion sizes remain unmeasured.

The CSV's purpose and recommendation fields are review classifications. They
are not a claim that static analysis proves reachability or safe deletion.

## Where Python is actually required

| Context                                          | Current dependency                                                                       | Recommended direction                                                      |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Hosted backend/web request handling              | Go backend and compiled browser assets; Python is not their application runtime          | Preserve                                                                   |
| Distributed CLI execution, including LAN serving | Compiled Go executable; Python is not required to run it                                 | Preserve                                                                   |
| CLI source build/install                         | `client/Makefile:14` runs Python asset preparation                                       | Development-only Go build tool, retaining npm/Vite for browser compilation |
| Ordinary Android source builds                   | `android/app/build.gradle.kts:45–57` invokes `android_release.py source-revision`        | Implement this narrow Git identity check in Gradle Kotlin                  |
| Native iOS CI                                    | Source/config checks and diagnostic wrapper; actual app/tests are Swift/Xcode            | Keep meaningful configuration checks; make resource diagnostics optional   |
| Linux portable Swift checks                      | Fourteen Python-generated SwiftPM workspaces                                             | One explicit SwiftPM harness and runner                                    |
| Release publication                              | Python orchestration across workflow jobs                                                | Simplified workflow plus a development-only Go release tool                |
| VPS update operations                            | Restricted SSH executes root-owned `deploy/update.py` and imports `release_artifacts.py` | Separate, compatible Go updater migration                                  |

“Native” in `prepare_native_release.py` and `measure_native_browser_inputs.py`
means native CPU-architecture server container builds. Those scripts do not build
Android or iOS apps.

## What the artifacts are for

| Class                      | Current examples                                                                                                              | Assessment                                                                                            |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Actual deliverables        | Images, APKs, CLI archives, deployable release manifest/bundle, checksums, corresponding source, notices, standard provenance | Keep capabilities and stable consumer contracts                                                       |
| Source payloads            | Two runtime-source packs, two dependency packs, common upstream sources, project Git-source archive                           | Not disposable reports; simplify packaging and deduplicate originals without dropping required inputs |
| Shipped generated content  | Embedded LAN UI and legal pages, native license resources, app icons                                                          | Keep output and reproducible source; these are product assets                                         |
| Cross-job payloads         | Per-architecture image export, inventory and source inputs                                                                    | Keep a canonical representation and verify when crossing a trust boundary                             |
| Diagnostics                | Raw scan output, timing summaries, concise recovery outcome and publication errors                                            | Retain with bounded retention; do not turn every diagnostic into a signed gate                        |
| Internal handoffs          | Publication path maps, smoke projections, derived gate envelopes, review evidence/presentation records                        | Consolidate into functions and one release result where feasible                                      |
| Local transactional state  | VPS transaction journal, local checkpoint, active/previous image references                                                   | Keep durable crash/recovery semantics; this is not equivalent to a transient CI report                |
| Existing temporary staging | SwiftPM workspaces, Gradle export JSON, notice staging directories                                                            | Already temporary in many scripts; reduce duplicate logic, not just filenames                         |

Evidence: source assets are collected in `prepare_release_inputs.py:191–319` and
bound as publication subjects in `publish_container_release.py:687–704`.
`release.yml:813` calls them the six large publication source archives.

## Container release and publication

The current path is approximately:

```text
exact source/tag
  -> CI + base resolution + upstream originals + architecture builds
  -> native input/source/browser inventories and transfer staging
  -> source/image scan measurements and aggregate gate reports
  -> per-architecture recovery experiments and recovery gate
  -> distribution presentation + exact-comment review + signed review evidence
  -> separate protected publication approval
  -> upload/readback reports + attestations + more aggregate verification
```

The workflow itself is 1,352 lines. Its producers are active. Consolidation targets:

| ID  | Target and evidence                                                                                                                                                          | Recommendation                                                                                                                            | Nature of change                                                                          |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| R1  | `github_release_attestor.py:57–246` downloads official action JavaScript and discovers runner Node/worker internals; `:648–734` signs subjects and another provenance report | Use normal pinned `actions/attest` workflow steps. Remove custom action execution/runtime discovery and provenance-about-provenance       | Simplify first; preserve final subject authenticity                                       |
| R2  | `generate_distribution_review.py:92–98,164–270`; `release.yml:1103–1195`                                                                                                     | Prefer one human-readable summary and one native protected publication approval, replacing the exact-comment protocol and second approval | Explicit policy decision; not an equivalent drop-in deletion                              |
| R3  | Eight prepublication gates and four readback/provenance gates in `publish_container_release.py:42–56`; envelope checks at `:422–563`                                         | One versioned release inventory/result with platform sections and raw report references; attest final assets/images                       | Remove obsolete envelope adapters/tests after consumers migrate                           |
| R4  | Full native input upload and overlapping recovery-image upload, `release.yml:513–542`; export list in `prepare_native_release.py:783–817`                                    | One image-only artifact per architecture, consumed by assembly/recovery/publication; separate source/evidence artifact                    | Verified duplicate payload transport; export format compatibility must be checked         |
| R5  | `prepare_candidate_transfer.py:650–684` repeatedly fingerprints before/after copying, rechecks full sets, then inventories again                                             | Streaming copy-and-hash and one inventory; verify downloaded bytes at actual receiving boundaries                                         | Consolidate local work; do not remove cross-job verification                              |
| R6  | `prepare_publication_inputs.py:29–132` maps downloaded files to declared paths                                                                                               | Internal path resolution in the release command rather than another persisted path-map protocol                                           | Remove script after consumer migration                                                    |
| R7  | Identical npm lock audit in both architecture jobs, `release.yml:373–382`; source scanner `:65–78`                                                                           | Run npm audit once per exact lockfile. Retain architecture-sensitive Go scan inputs                                                       | Deduplicate proven common work                                                            |
| R8  | `aggregate_release_image_scans.py:557–679` reconstructs advisory applicability from compiler imports                                                                         | Consider simpler scanner results plus narrow reviewed exceptions instead of bespoke automatic non-applicability derivation                | Policy/assurance tradeoff; decide before porting                                          |
| R9  | `github_release_transport.py:322–392,1540–1641`; workflow `:1346–1352`                                                                                                       | Reduce custom mutation journal/reconciliation around a simpler draft-release flow; preserve useful incident receipts                      | Current journal explicitly forbids automatic replay and its private state is runner-local |

Do not confuse source and final-image scans: they observe different inputs.
Likewise, approving a protected job and checking the identity of downloaded
artifacts protect different boundaries. Reduce redundant representations, not the
actual digest/source/authentication relationships.

Candidate simpler flow:

```text
exact tag + CI
  -> parallel architecture build/smoke/scan + shared source collection
  -> one release inventory + source package verification + recovery result
  -> one protected publication approval
  -> publish tested digests/assets and standard provenance
  -> anonymous readback + concise final receipt
```

## Source, licenses, and notices

Actual sources, license texts, retained upstream originals, build inputs, and
final-image correspondence are useful deliverables. This assessment does not
determine legal obligations or propose changing the project's licenses. The
current bespoke report architecture is not itself an obligation.

| ID  | Target and evidence                                                                                                                                   | Recommendation                                                                                                                           |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| S1  | `generate_corresponding_source_review.py` is 1,730 lines coordinating several producers                                                               | One source packaging/verifier command with typed package/origin/image associations                                                       |
| S2  | `dependency-inputs.json` plus `dependency-collection.json` (`package_application_dependencies.py:548–620`); analogous upstream/runtime/replay records | One canonical source inventory, embedded where necessary, with per-image/platform mappings; fewer wrapper reports                        |
| S3  | Every native dependency pack runs `npm_inputs()` against the complete npm lock (`package_application_dependencies.py:539`)                            | Collect common npm originals once; keep platform mappings and union of selected Go graphs                                                |
| S4  | Runtime originals overlap across backend/web and platforms                                                                                            | Shared content-addressed originals with explicit per-image mappings; do not assume all platform source inputs are identical              |
| S5  | Dependency-specific generator snippet/version checks (`backend_preferred_source_relationships.py:275–339,408–418`, browser equivalent `:315–411`)     | Smaller data-driven catalog of retained originals/recipes; remove brittle snippet relationships after the replacement contract is agreed |
| S6  | Repeated archive/JSON/hash code in application/runtime/upstream packagers and validators                                                              | Shared Go primitives with explicit format/limit/symlink policies; do not replace distinct policies with one permissive extractor         |
| S7  | `final_image_notice_inventory.py:102` reconstructs final files and already shares OCI/whiteout helpers                                                | One final-image/source/notices verifier rather than another report-producing layer                                                       |
| S8  | Backend notice generator parses `go.mod` with regex (`backend/scripts/generate-third-party-notices.py:42–44`)                                         | Shared Go notice collection using actual module/package metadata, also serving CLI packaging                                             |

The preferred-source relationship scripts explicitly do not claim byte
reproduction. Their complexity should not be mistaken for an actual reproducible
build. Keep the real retained inputs; use a genuine offline rebuild where useful.
Changing this policy requires review, not a blind deletion of source assets.

Migration hazards: `verify_application_dependency_inputs.py:47–68` pins the exact
Python notice generator hash. `release_artifacts.py:44` is part of deployment
bundle requirements. Runtime source packaging copies the `runtime-legal` folder,
including Python verifier/test files. Update these consumers atomically and
preserve already published immutable assets and URLs.

## VPS updater and deployment request

`deploy/update.py` is the largest Python file: 2,805 lines. Its `Host` adapter
starts at line 485 and `Updater` state machine at 2053. Its responsibilities are
substantive: restricted acquisition, manifest/provenance checks, storage adoption,
disk preflight, checkpoints, migration boundaries, isolated checks, activation,
recovery and scoped image retention. `deploy/ssh/psst-deploy.conf:9` makes this an
installed privileged runtime dependency, not a build helper.

**Good Go candidate, but migrate separately after the release contract stabilizes.**
Use a dedicated root-owned `psst-deploy` executable, not the user-facing `psst`
client. Keep an explicit OS/command adapter and testable durable state machine.
Preserve existing configuration, transaction records and recovery commands, or
provide an explicit versioned import/cutover. Do not deploy the replacement while
a transaction is unresolved. Update sudo/SSH command restrictions with the binary
installation, not as a separate premature change.

Keep these behaviors and their meaningful regressions:

- Failure before mutation preserves the running release.
- After a new backend can have migrated the database, an old binary must not be
  automatically restarted against that potentially changed database.
- Isolated candidate checks, durable transaction boundaries, single updater lock,
  atomic state, private credential paths and bounded command execution.
- Storage, configuration, initialized-account and database integrity checks.
- Retention protects active/checkpoint/foreign-container images and removes only
  tracked expired references; no global `docker system prune`.
- Restricted version-only SSH input, pinned host keys and protected local config.

The `runtime-v1` automatic verification path already avoids repeated manual
administrator flow checks. Keep exceptional full verification for adoption or
restore where required; do not restore the old manual checklist as the routine
update path (`require_flow_report:396`, activation near `:2270`).

`request_production_deployment.py` should become a thin adapter in the Go tooling
or a small standard SSH workflow step, preserving strict host/version validation,
pinned SSH configuration, deadlines and sanitized output. It should not grow a
second deployment state machine. Keep release publication separate from VPS
credentials and access.

## CLI and mobile tooling

| ID  | Target                                                                                                                                           | Recommendation                                                                                                                                         |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| C1  | CLI asset preparation/notices/packaging                                                                                                          | One development-only Go tool with asset, notices and package commands; keep actual Vite build in Node                                                  |
| C2  | `package-release.py:237–245` recollects notices after asset preparation; CI repeats collection at `ci.yml:284–289`; target lists also duplicated | Resolve target/module/notice inventory once and reuse it for embedding and packages, with digest checks                                                |
| C3  | `check-integration.py` (635 lines) manages processes, proxy, API fixtures and CLI scenarios                                                      | Go integration harness using contexts/HTTP test tools; retain the independent TypeScript crypto peer and actual binary interactions                    |
| M1  | Android source revision subprocess (`android/app/build.gradle.kts:45–57`)                                                                        | Gradle Kotlin implementation, preserving exact F-Droid wrapper-deletion/dirty-source semantics; do not add Go to ordinary Android builds               |
| M2  | Native notice export (`generate_native_notices.py:44–104,346–371`) embeds Groovy and invokes Gradle                                              | Gradle Kotlin task/convention logic exporting normalized dependency/license records; retain shipped Android/iOS legal resources                        |
| M3  | Android release/signature/version/archive tooling                                                                                                | Shared Go release primitives plus thin APK adapter invoking official `apksigner`, `zipalign`, Gradle and `age`; do not reimplement signing             |
| M4  | Fourteen iOS portable test launchers (1,050 lines) generate temporary packages/stubs/import rewrites                                             | One explicit SwiftPM harness and optional Linux Docker runner; preserve actual Swift tests and negative controls                                       |
| M5  | `check_sources.py:63–259` contains literal code/test-name checks and delimiter regexes                                                           | Remove assertions already superseded by real compilation/XCTest; retain bundle ID, entitlement, extension and storyboard/config checks                 |
| M6  | Localization catalog validation plus implementation-text assertions                                                                              | Keep key/placeholder/plural/catalog parity; remove brittle source-string checks where real platform tests/lint cover them                              |
| M7  | `diagnose_build.py` (176 lines) always wraps native CI with process/VM sampling                                                                  | Make incident sampling opt-in; retain normal Xcode timing summary; do not port this wrapper to Go                                                      |
| M8  | `android_signing_bridge.py` and part of `check_android_update.py`                                                                                | Decide whether future debug-to-production migrations are supported. Retire bridge-only path/tests if not; otherwise isolate optional migration tooling |

The portable Swift launchers are not run in current main CI; consolidating them
reduces maintenance rather than promising faster current macOS jobs. The separate
`test_inbox_pages.py` repeats `InboxPageWindowTests` already included by
`test_history_pages.py:22–27`; its entry point can disappear in the consolidated
harness. Lack of a documented caller for `test_release_source.py` alone does not
make its actual Swift tests obsolete.

The signing bridge plus its tests and Android update checker total 1,015 lines.
The owner migration being complete does not prove the same-signer update fixture
is unnecessary. Keep the storage-preservation release smoke capability if bridge
support is retired.

Keep `assets/brand/export.py`: a small optional authoring tool using
FontTools/Pillow/Inkscape is a reasonable Python use. Keep compact independent
localization validators initially unless native replacements simplify them.

## Tests: retain behavior, retire scaffolding with its protocol

About 44.5% of the Python source bytes are in test-named files. That is not evidence
that those tests are useless. Important cases cover unsafe archives, wrong source
or signer, partial publication, secret leakage, migration failure, foreign image
retention, and cross-client cryptography.

There is also substantial test coupling to the release protocol. For example,
`test_release_input_preparation.py:22–59` instantiates another test case's setup,
which itself nests a transport fixture (`test_release_gate_reports.py:67–85`).
The Caddy exception suite similarly reaches `fixture.native.fixture.fixture`
(`test_release_temporary_caddy_acceptance.py:24–29`). This grows large fixtures
for small policy mutations and makes tests harder to understand and change.

Recommended treatment:

- Keep behavior and adversarial boundary tests while replacing the implementation.
- Extract small data/adapter fixture builders rather than calling another suite's
  `setUp`; use table-driven tests for independent invalid fields.
- Delete exact-comment, intermediate-envelope and path-map tests when those
  contracts are intentionally removed. Do not port them just to retain counts.
- Replace source-substring assertions with actual behavior/configuration checks.
- Keep one compact real interoperability/recovery scenario per meaningful boundary;
  do not duplicate it in every packaging stage or architecture-independent job.
- Treat privileged Docker recovery experiments as release/affected-updater checks,
  distinct from ordinary offline unit discovery. The current integration driver
  is not a conventional discovered Python unit suite.
- Keep the three CLI asset regressions: dependency installation completeness,
  toolchain/license cache invalidation and nested vendor notice retention.
- Keep complete-history secret scans and staged-file protection. Branch/CI changes
  must not accidentally bypass checks; retain the CI selection regressions.

Repository security currently runs broad release unit discovery every CI run
(`ci.yml:65–91`). Consider path-scoping unaffected release-only suites after
measuring them; keep mandatory repository/secrets checks unconditional. The
recent PR's security job took 1m21s, so it is not presently evidence of a
30-minute test problem. No new timing benchmark was performed here.

## Go boundaries and bootstrap

Prefer one independent development-tool module, for example `tools/release/`,
with `cmd/psst-release`. It must not be imported by the backend request path or
bundled into the end-user CLI. Avoid a new framework or an executable for every
old script.

| Boundary                     | Responsibilities                                                                          | Move priority                                                           |
| ---------------------------- | ----------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| Small release contract       | Versioned manifest types, exact identities, digest relationships, bounded strict decoding | First; preserve fixtures and updater compatibility                      |
| Archive/inventory primitives | Streaming hashing/copy, deterministic tar/ZIP, safe extraction and explicit limits        | First; shared by CLI/server source packaging                            |
| Source/notices               | Resolved Go module graphs, originals, license paths and final-image correspondence        | Early; eliminate repeated inventories                                   |
| Product adapters             | CLI target builds, container OCI inspection, APK SDK invocation                           | After shared primitives; keep native tools                              |
| Publication orchestration    | Small GitHub API surface, immutable version checks, upload/readback                       | After workflow simplification; prefer official actions where sufficient |
| VPS updater                  | Durable deployment/recovery state machine and restricted command adapter                  | Separate later cutover after stable contract                            |

Build the tooling from the checkout before preparing application assets; it must
not depend on the CLI's embedded assets, the backend module graph, or generated
release output. A small standard-library-only contract/archive core avoids a
bootstrap cycle. Add external tooling dependencies only for demonstrated needs.
Offline source distributions must include the new tool source/build inputs.

Do not introduce Go as a prerequisite for Android/F-Droid source builds or for
running Swift checks. Gradle/SwiftPM/Node already own those build environments.
During migration retain one compatibility implementation at external boundaries,
not two full release pipelines indefinitely.

## Recommended sequence and decisions

- [ ] Agree the simplified publication policy: one protected approval; standard
      final provenance; smaller gate/receipt set. Keep existing protections until
      that explicit decision is implemented.
- [ ] Remove immediate duplicate work: repeated CLI notices, architecture-independent
      npm audit/original downloads, and overlapping image artifact uploads.
- [ ] Move Android source identity into Gradle Kotlin, coordinating the F-Droid
      recipe and its reproducibility assumptions.
- [ ] Introduce minimal Go contract/archive/notice tooling with compatibility and
      adversarial tests; switch CLI build/package callers first.
- [ ] Consolidate source packaging and final-image verification behind the same
      command; migrate consumers before deleting Python adapters/schema tests.
- [ ] Simplify workflow signing/publication and remove custom action execution,
      exact-comment machinery, and obsolete intermediate envelopes.
- [ ] Consolidate portable Swift harnesses; prune redundant source assertions and
      make incident diagnostics optional. Preserve Android/iOS parity.
- [ ] Decide future support for debug-to-production Android migration; retire or
      explicitly isolate its exceptional tooling.
- [ ] Migrate the installed VPS updater in a separate compatible rollout, preserving
      local state, checkpoint policy, recovery semantics and scoped image retention.
- [ ] Measure resulting cold/warm build time, workflow time, artifact bytes and
      maintenance footprint. Remove old entry points only after their consumers
      are gone; do not use Python percentage or test counts as acceptance criteria.

No numeric reduction estimate is justified yet. There are clear removals after
contract migration, but Go source and necessary tests will replace part of the
Python. Success means fewer mechanisms, simpler builds and releases, and preserved
meaningful safety boundaries—not merely a different language bar on GitHub.

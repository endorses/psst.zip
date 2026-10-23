# Tooling consolidation and Go migration

Created: 2026-10-23. Status: **implementation in progress**.

Implementation branch: `refactor/tooling-consolidation`.

Based on the [tooling assessment](../research/python-tooling-assessment.md) and
[complete Python inventory](../research/python-tooling-inventory.csv), measured
at `df002e447dcd04842c854b3796b2b7e84c4cf3ae`.

## Objective

Reduce the number of mechanisms required to build, verify, publish, and deploy
psst.zip. Remove repeated work and unnecessary intermediate contracts before
moving substantial shared tooling to Go. Preserve meaningful release, source,
signing, deployment, and interoperability checks.

The starting inventory contains 135 Python files, 65,825 physical lines, and
2,583,684 source bytes. These are context, not a deletion quota. Success is a
simpler system with fewer prerequisites and less repeated work, rather than a
particular GitHub language percentage.

This plan covers CLI, container, Android/iOS build tooling, and the installed VPS
updater. Application features, encryption protocols, account data, package/signing
identities, licenses, published release history, and store submission policy remain
outside the refactor. Preparing this plan does not publish a release or change the
running VPS.

## Target ownership

| Responsibility                                                              | Target owner                                                                                       | Boundary                                                                  |
| --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Release manifests, archives, inventories, source/notices, product packaging | One development-only Go module at `tools/release/`, command `psst-release`                         | Independent of backend and end-user CLI runtime dependencies              |
| VPS deployment/recovery                                                     | Separate command `psst-deploy` in the tooling module, sharing only small contract/archive packages | Root-owned installed executable with restricted SSH entry point           |
| Android source identity and native dependency export                        | Gradle Kotlin build logic                                                                          | No new Go prerequisite for ordinary Android/F-Droid builds                |
| Portable Swift checks                                                       | Explicit SwiftPM harness and one optional Docker runner                                            | Reuse actual Swift production code/tests; preserve native XCTest          |
| Browser compilation and bundle instrumentation                              | Existing npm/Vite/Node toolchain                                                                   | Go consumes normalized output; does not reimplement JavaScript tooling    |
| Attestation execution                                                       | Normal pinned GitHub action steps                                                                  | Preserve final artifact/image subjects and verifier identity requirements |
| Brand authoring and compact independent validators                          | Small existing Python tools where appropriate                                                      | No wholesale translation merely to remove Python                          |

Start with a small standard-library core: strict contracts, bounded archives,
hashing, process deadlines, and typed inventories. Add packages only as concrete
consumers require them. Do not build a general workflow engine, generic plugin
system, or one new executable per old script.

## Contracts to preserve

| Contract                     | Required behavior                                                                                                                               |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Source and artifact identity | Exact tag/commit, paired backend/web version, actual tested image/platform digests, immutable released versions                                 |
| Trust boundaries             | Read-only preparation; protected publication credentials; verification of artifacts received across jobs; provenance verified before deployment |
| Sources and legal resources  | Retained originals, recipes/build inputs, license texts, final-image correspondence, offline source rebuild inputs                              |
| Android updates              | Existing package identity, production signer, version-code monotonicity, private mapping retention, secret-safe SDK invocation                  |
| CLI                          | Existing Make/install/version behavior, six supported targets, embedded LAN assets, independent TypeScript crypto interoperability              |
| Deployment                   | Durable state, storage adoption, migration/recovery boundaries, automatic routine verification, operator configuration and restricted SSH input |
| Disk retention               | Preserve active/checkpoint/foreign-container images; prune only expired updater-owned references; retain existing disk reserves                 |
| Mobile parity                | Both Android and iOS retain equivalent notices/localization/configuration coverage and shared protocol compatibility                            |

Published artifacts and source URLs remain immutable. Preserve the current
deployable manifest/bundle format while simplifying internal CI records where
possible. The installed Python updater validates a specific file list and signer
identity: do not remove its required parser from compatible bundles or change
attestation identity without migrating that consumer.

## Decisions before changing policy

Preparation, measurements, duplicate-work removal, and compatibility-preserving
Go tooling can proceed independently of these decisions. Leave existing policy
in place until its replacement is explicitly agreed.

- [x] Confirm the recommended publication model: one protected environment
      approval immediately before credentialed publication, a human-readable
      summary, and standard final provenance. Then replace the exact-comment
      protocol and duplicate review job together with their consumers.
- [x] Retain current scan coverage initially. Decide separately whether to replace
      custom advisory non-applicability inference with scanner results and narrow
      reviewed exceptions; do not silently weaken the gate during translation.
- [x] Decide whether future debug-to-production Android signing migrations are
      supported. Retire bridge-only tooling if not; otherwise isolate it as an
      optional migration capability. Preserve ordinary same-signer update checks.
- [x] Record these decisions and the resulting artifact/consumer contract in this
      plan before implementing the corresponding policy changes.

On 2026-10-23 the user approved one protected publication approval with standard
final provenance, and retirement of the exceptional debug-to-production signing
bridge. Same-signer update checks remain supported. Existing scan policy is
retained; no advisory-applicability policy reduction is authorized by this choice.
Go's official stable release feed was checked at implementation start and lists
Go 1.27.3, matching the selected project toolchain.

## Delivery sequence

Use small commits or PRs with an independently usable result. A caller switch and
removal of its obsolete implementation belong in the same change where practical.
Keep transitional adapters narrow and give them a named removal milestone; do not
maintain two complete release pipelines indefinitely. Use responsibility-based
filenames rather than filenames named after these stages.

### 1. Establish the baseline and migration map

- [ ] Reconcile the inventory against the implementation checkout. Record each
      entry point, workflow/Make/Gradle/manual caller, output consumer, intended
      owner, and removal milestone. Include inline Python imports and discovered
      tests; absence of a literal caller is not proof of dead code.
- [ ] Capture representative published manifest/bundle/provenance structures and
      synthetic updater configuration/state fixtures without user data or secrets.
      Record which bytes and fields existing readers require.
- [ ] Measure relevant cold/warm build time, job/step time, duplicate archive
      bytes, and tool prerequisites using available CI/local evidence. Do not run
      every application suite just to establish a tooling baseline.
- [ ] Reuse the maintained toolchain policy: latest stable tools/actions/runners
      at implementation time unless compatibility requires a documented exception.
      Check official support information and CI annotations before changing pins.
- [ ] Define final payloads versus internal records. Keep deliverables, raw useful
      diagnostics, one inventory per cross-job payload, and durable VPS state;
      nominate redundant gate envelopes/path maps for removal.

Exit condition: consumers and compatibility constraints are known, and each
removal has a replacement or an explicit decision to retire the capability.

### 2. Remove repeated work without changing release policy

- [ ] Resolve CLI target/module/notice inputs once per build preparation and reuse
      them for embedded legal assets, packaging, and validation. Remove the repeated
      collection in `package-release.py` and routine CLI CI. Preserve input-based
      invalidation and verification of reused output.
- [ ] Run npm lock auditing and collection of common npm originals once per exact
      lockfile. Retain platform-sensitive Go graphs/scans and explicit mapping
      from common inputs to both architecture builds.
- [ ] Split image payloads from source/evidence payloads. Upload one image artifact
      per architecture for assembly, recovery, and publication. Select a canonical
      export; retain another format only where an actual consumer requires it.
- [ ] Consolidate repeated local fingerprint/copy passes into streaming copy/hash
      plus a canonical inventory. Keep bounds, path/symlink checks and receiving-job
      authentication; do not substitute a trusted-looking JSON record for bytes.
- [ ] Verify the affected commands and record setup/upload savings. Preserve the
      five-minute CLI job limit rather than increasing it to hide redundant work.

Exit condition: the existing release contract still works, with fewer graph
resolutions, scans of identical common inputs, copies, and uploaded payload bytes.

### 3. Introduce Go contracts and migrate CLI build/package tooling

- [x] Add `tools/release/go.mod` and `cmd/psst-release`, initially with only packages
      required by real callers. Keep it independent of generated browser assets,
      backend/client module graphs, credentials, and release outputs.
- [ ] Implement strict versioned manifest decoding/validation and archive/inventory
      primitives. Preserve rejection of duplicate JSON keys, unknown/missing fields,
      malformed identities, unsafe archive paths, unsupported entries, excessive
      member/expansion sizes, and ambiguous symlink policies.
- [ ] Build the development tool once per source/toolchain/platform combination;
      reuse it across commands in a job or Make invocation. Avoid repeated `go run`
      compilation on every substep and avoid adding a compiled-tool prerequisite
      to existing Git hooks merely for language consistency.
- [ ] Move Go dependency notice collection into a shared package using resolved
      `go list` metadata. Share target definitions with packaging and retain actual
      toolchain/platform-dependent legal inputs.
- [ ] Replace CLI Python assets/notices/package entry points with Go subcommands,
      retaining npm/Vite for actual web compilation and Node bundle instrumentation.
      Preserve existing Make targets, overrides, cached preparation, source version,
      stripped builds, Windows paths and offline source archive contents.
- [ ] Update the generator-hash pin and its consumers atomically. Where an existing
      release still needs the old generator, retain a minimal compatibility path
      until that source-verification consumer migrates; do not run both collectors
      in normal builds.
- [ ] Move CLI integration orchestration to a bounded Go harness while preserving
      the actual executable and independent TypeScript crypto peer. Keep Send,
      Receive, receipt/retry and key-recovery behavior; avoid a self-confirming
      round trip through a single crypto implementation.
- [ ] Switch Make/CI/docs callers and remove superseded CLI Python scripts and
      their implementation-specific tests. Retain the three real asset regressions
      for dependency completeness, cache invalidation and embedded vendor notices.

Exit condition: CLI source build/install/package no longer requires Python; normal
and release binaries preserve behavior; all six targets build; a clean source
archive rebuild includes matching embedded assets/notices and works offline.

### 4. Consolidate source, notice, image and scan inventories

- [ ] Introduce one typed source inventory describing package/origin/version/digest,
      license/build-input paths, image associations, and platform mappings. Collect
      common originals once; preserve platform-specific inputs explicitly.
- [ ] Consolidate application dependency, common upstream, and runtime source
      collection/packaging behind Go commands. Use content-addressed shared inputs
      where useful without inventing a separate storage service requirement.
- [ ] Consolidate OCI file inspection, final notice-byte checks, source coverage and
      bundle validation in one verifier. Keep distinct archive policies explicit
      rather than using one permissive extraction helper.
- [ ] Replace bespoke dependency generator snippet parsing with a smaller reviewed
      input catalog where the replacement supports the same source offering.
      Keep recipes/originals and a genuine offline rebuild where applicable;
      do not label source relationships as byte reproduction.
- [ ] Replace overlapping collection/replay/report wrappers with sections of the
      canonical inventory/result. Preserve raw scanner output and exact tool/input
      identity; combine local checks while retaining cross-job trust checks.
- [ ] Preserve the installed updater's final manifest/bundle/provenance contract
      and existing source download names/URLs. If a change cannot be represented
      compatibly, version it explicitly and defer emission until readers support it.
- [ ] Switch source/notice workflow and Dockerfile consumers; remove replaced
      Python producers/adapters and tests solely for retired wrapper schemas.
      Include the new Go tool source and dependencies in offline source offerings.

Exit condition: one source inventory drives collection, packages and final-image
verification, while distributed licenses/originals and consumer compatibility
remain intact. No dual full packaging pipeline remains for new releases.

### 5. Simplify publication orchestration and signing

- [x] Replace the multiple gate envelopes with one versioned release result
      containing platform results and references to raw reports. Keep the actual
      CI, smoke, scan, source and recovery checks; remove redundant serialization.
- [x] After the publication decision above, use one protected publication job and
      readable summary. Remove exact-comment parsing, duplicate approval jobs,
      signed approval-evidence wrappers and their obsolete tests/policy fields.
- [x] Invoke the normal pinned attestation action in workflow steps. Remove custom
      action JavaScript downloads, runner/Node discovery and provenance-about-
      provenance. Verify that final subject digests, tag/commit, workflow identity,
      issuer and hosted-runner constraints match existing deployment readers.
- [ ] Reduce custom transport around the supported GitHub/GHCR/action interfaces.
      Keep immutable-version checks, source-bound publication, bounded HTTP/process
      failures, readback and sanitized diagnostics. Use a draft/incomplete state
      until required assets/provenance are ready; do not advertise partial success.
- [ ] Preserve practical handling of partial registry or release publication:
      emit a concise incident receipt and require explicit reconciliation. Do not
      promise cross-service atomicity, automatic overwrite or resumability that
      the implementation does not provide.
- [ ] Remove path-map handoff scripts, duplicate readback gate envelopes and the
      superseded custom attestor/review/transport code after all callers migrate.
      Run one candidate rehearsal with publication disabled, then use the normal
      authorized release process for the first published candidate.

Exit condition: exact source/CI → architecture build/smoke/scan/source/recovery →
one result → one protected publication approval → final provenance/publication →
anonymous readback/receipt. Publication has no VPS credentials or access.

### 6. Put mobile build concerns in native tooling

This work can proceed independently of most container changes. Apply equivalent
legal-resource/localization/configuration coverage to both Android and iOS.

- [ ] Move the narrow Android source-revision check into Gradle Kotlin. Preserve
      clean/dirty source handling and F-Droid's exact wrapper-removal allowance;
      coordinate recipe expectations rather than accepting arbitrary dirty trees.
      Ordinary Android source builds require neither Python nor Go afterward.
- [ ] Move native dependency resolution/export from embedded Groovy/Python into
      Gradle Kotlin tasks. Preserve Android runtime and iOS klib/POM associations,
      output determinism, source identity, and shipped notices for both platforms.
- [ ] Reuse Go source/version/archive/GitHub primitives for Android release tooling;
      keep thin adapters invoking official Gradle/SDK/`age` programs. Preserve
      signer fingerprints, version codes, alignment, private mapping encryption,
      secret-safe failures, and exclusion of private migration lineage.
- [ ] Replace fourteen temporary SwiftPM generators with explicit targets/source
      lists and one native-or-container runner. Preserve the actual Swift XCTest
      sources, concurrency negative controls and Linux portable development checks.
      Remove duplicate Inbox test entry points without dropping unique coverage.
- [ ] Prune literal code/test-name assertions and delimiter checks superseded by
      compilation/behavior tests. Keep entitlements, identifiers, extension wiring,
      localization keys/placeholders/plurals and configuration validation.
- [ ] Make iOS process/VM incident sampling optional; keep normal build timing
      summaries. Do not claim that portable harness consolidation accelerates
      native CI, which does not currently run those Python launchers.
- [ ] Apply the signing-migration decision: remove bridge-only code and tests, or
      isolate an optional supported migration tool. Retain ordinary same-signer
      installed-data preservation checks and documented recovery information.
- [ ] Remove obsolete callers/scripts and update developer/store build documents.
      Record Android verification separately from macOS/Xcode/iOS verification;
      unavailable native environments leave those checks explicitly pending.

Exit condition: platform build logic belongs to Gradle/SwiftPM/Node, both mobile
platforms retain equivalent capabilities, and release-common logic is shared
without adding Go to standalone Android/F-Droid source builds.

### 7. Migrate the VPS updater separately

Dependencies: stable final release contract and completed compatibility fixtures.
Keep the current installed helper operational until the replacement is verified.

- [ ] Implement `psst-deploy` with a small durable state machine and concrete
      Docker/GitHub/OS adapter. Reuse only the necessary contract/archive core;
      keep signing/publication credentials and the end-user CLI separate.
- [ ] Preserve configuration, version-only SSH syntax, trusted signer policy,
      protected paths, lock/atomic state, pending transaction status and explicit
      recovery commands. Read existing transaction/checkpoint data or provide a
      bounded versioned import with validation.
- [ ] Preserve adoption/preflight, isolated routing, automatic `runtime-v1` checks,
      database integrity, operator settings, storage ownership and resource limits.
      Keep exceptional adoption/restore verification where needed; ordinary
      releases must not regain repetitive manual administrator flow checklists.
- [ ] Preserve the recorded migration boundary: no automatic old-binary restart
      against a database the new backend may have changed. Test interrupted writes,
      pre-mutation failure, post-mutation failure and explicit recovery behavior.
- [ ] Preserve existing checkpoint policy, including the authorized local-only
      mode while off-host backups are deferred. This refactor does not introduce
      a bucket requirement or silently change backup policy.
- [ ] Preserve disk reserves and bounded, retryable image/checkpoint cleanup on
      the 40 GB VPS. Protect active, retained and foreign images/containers;
      remove only expired owned references and report incomplete cleanup durably.
- [ ] Exercise one disposable Docker update/recovery/retention scenario and old
      state import. Verify root ownership, sudo/forced SSH restrictions and secret-
      safe diagnostics before preparing the installation cutover.
- [ ] Prepare a concrete installation/cutover with no unresolved transaction,
      validated SSH/sudo configuration, a retained old helper and compatible state.
      Use the authorized production rollout process; perform final host checks and
      record the installed version. Never run both mutating updaters concurrently.
- [ ] Switch the restricted SSH entry point and bundle requirements atomically;
      remove the old Python updater/parser only after no supported consumer needs
      them. Keep old immutable release assets untouched.

Exit condition: the installed update path uses the Go executable with compatible
state and recovery, automatic normal updates, and proven scoped retention.

### 8. Retire obsolete surfaces and record results

- [ ] Reconcile every inventory entry with retained, migrated, consolidated or
      retired status and its replacement. Search workflows, imports, Dockerfiles,
      Make/Gradle files, test discovery, source bundles and manual documentation.
- [ ] Remove temporary adapters and tests solely for intentionally removed
      contracts. Keep small fixture builders instead of nested test-case `setUp`
      chains; retain adversarial/behavior tests rather than matching old test counts.
- [ ] Keep staged/history secret checks and CI-selection safeguards. Path-scope
      release-specific suites only where measured cost and reliable dependency
      selection justify it; default to running affected checks on uncertainty.
- [ ] Keep appropriate small Python tools, including brand authoring and useful
      compact validators/hooks. Document remaining prerequisites accurately.
- [ ] Compare cold/warm builds, CI steps, uploaded/stored bytes and mechanism count
      to the baseline; explain remaining platform limits and deliberate exceptions.
- [ ] Format, run affected checks and commit code/docs for each completed slice.
      Check tasks off only with evidence; finish with one bounded closure audit
      against this plan rather than repeated open-ended reassessment.

## Verification and time discipline

| Change boundary        | Meaningful verification                                                                                                       | Execution discipline                                                                         |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Contracts/archives     | Existing valid contract fixtures plus malformed identity, duplicate keys, traversal/links and expansion limits                | Fast table-driven/offline tests; do not recreate full releases per invalid field             |
| CLI tooling            | Cache/inventory regressions, independent crypto peer, two LAN browser scenarios, six cross-builds, one offline source rebuild | Build once/reuse executable and dependency graph; preserve five-minute routine CLI CI budget |
| Source/image packaging | Exact final image mapping, notices/originals coverage, corrupt/mismatched archive rejection                                   | Collect common inputs once; verify actual receiving boundaries                               |
| Publication            | Wrong identity/version and partial upload/readback behavior; one publication-disabled rehearsal                               | Reuse exact-source CI; no live release per unit test or per small refactor                   |
| Mobile tooling         | Android source-build/F-Droid semantics, both platforms' legal/localization output, portable Swift tests and native XCTest     | Run affected suites; distinguish implemented changes from pending native validation          |
| VPS updater            | State/fault table, protected paths/commands, migration boundary, one disposable Docker recovery/retention flow                | Keep heavy experiment off unconditional unit loops; bounded cleanup of owned resources       |

Record cold and warm results separately. Keep raw timings concise. Do not increase
timeouts simply because a test installs redundant dependencies or repeats a build.
Do not remove source/image scans or meaningful recovery checks just to meet a
number. Existing backend/web/native job baselines provide comparison; this plan
does not claim all platform builds can finish within the CLI's five-minute limit.

## Assessment traceability

| Assessment items                                                 | Plan coverage                                         |
| ---------------------------------------------------------------- | ----------------------------------------------------- |
| R1–R3, R6, R9: signing, approvals, report/transport layers       | Stages 1, 3, 5; policy decision where required        |
| R4–R5, R7: image transport, repeated hashing, common npm work    | Stage 2; shared primitives in 3–4                     |
| R8: advisory inference                                           | Explicit decision; retain current policy until agreed |
| S1–S8: source/notices, inventories and archive primitives        | Stages 3–4                                            |
| C1–C3: CLI assets/notices/package/integration                    | Stages 2–3                                            |
| M1–M3: Gradle identity/notices and Android release               | Stage 6; shared Go primitives from 3                  |
| M4–M7: Swift harnesses, source checks, localization, diagnostics | Stage 6                                               |
| M8: exceptional Android signing migration                        | Explicit decision and stage 6                         |
| VPS updater and deployment request                               | Stage 7; shared final contract from 3–5               |
| Test coupling, repository hooks, inventory/retirement            | Stages 1, 8 and affected-stage verification           |

## Implementation evidence

The first publication slice replaces the exact-comment review and duplicate
protected job with one protected publisher and normal pinned `actions/attest`
steps. The Go `publication-result` command consolidates the six source-stage
facts, then the exact same invocation adds recovery as its seventh fact. The
temporary per-fact envelopes are removed after incorporation; useful raw replay
and scanner inputs remain bound by digest. Existing Python readers are a narrow
transition until their source/publication consumers migrate, not a second result
producer. Final deployment manifest/bundle and signer identity remain unchanged.

Local evidence: 126 focused publication/source/transport Python regressions pass
in 13 seconds; actual Python producer → compiled Go result → semantic Python
reader integration passes. Go contract/archive/cache/result/Android boundary tests
pass in under one second on a warm compiler cache. No live release, remote
publication, production update, or native Xcode verification is claimed.

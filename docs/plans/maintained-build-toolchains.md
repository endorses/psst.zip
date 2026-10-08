# Maintain current stable build toolchains

Use the latest stable releases as required by project guidance. Keep immutable
release inputs, corresponding-source checks and native platform parity intact.
Version discovery is separate from successful build validation. GitHub's preview
`xcode-27` runner is explicitly approved for its stable Xcode 27 installation.

- [x] Verify the current stable GitHub action releases and Node 24 manifests.
- [x] Update all workflows to immutable action commits, preserving named ZIP
      artifacts and Gradle's basic cache provider. Validate actionlint, shell and
      inline Python syntax locally without adding test cases.
- [x] Select stable Xcode 27.0 and iOS 27.0 explicitly on the approved preview
      runner. Preserve readiness and XCTest budgets, with bounded failure logs.
- [ ] Verify the updated actions, native build and XCTest on GitHub.
- [x] Refresh Go, Node, Buildx, BuildKit and golangci-lint pins together with
      their release source/scanning constraints. Verify meaningful affected
      regressions and application builds.
- [x] Upgrade both native version catalogs, Gradle wrappers and CI Java together.
      Migrate the Android/Kotlin plugin configuration and unit-test task as needed;
      preserve Android, iOS and share-extension protocol compatibility.
- [x] Refresh native corresponding-source inputs and generated notices for both
      mobile platforms. Verify Android/shared compilation and tests locally;
      leave macOS compilation and XCTest pending until hosted execution passes.
- [x] Refresh Alpine runtime/source-build inputs with matching package recipes,
      notices and source checks. The source helper built with Alpine 3.24.2 and
      abuild 3.17.0-r0. Its collected base sources matched all four scoped origin
      reviews; full final-image and retained-layer verification remains separate.
- [ ] Validate the actual native release base images, full corresponding sources
      and container recovery through the planned release workflow.
- [ ] Inspect hosted check annotations as well as conclusions. Verify that the
      Node action deprecation, moving Ubuntu runner and outdated Gradle notices
      are removed; investigate any new warnings without extending test budgets.
- [ ] Commit verified changes and plan checkpoints; keep store research drafts
      uncommitted. Do not publish a release or update production while its
      candidate checks remain incomplete.

The 2026-10-08 primary-source audit found stable Go 1.27.1, Node 26.11.1,
Temurin 27+35, Gradle 9.8.1, Kotlin 2.4.20, SKIE 0.10.15, AGP 9.4.0,
Buildx 0.37.2, BuildKit 0.34.0, golangci-lint 2.14.0 and Alpine 3.24.2.
Kotlin's published fully supported matrix stops at Gradle 9.7.0/AGP 9.3.1;
newer stable combinations require actual verification. An older supported
release is not automatically deprecated, but does not satisfy the requested
latest-stable policy.

The latest published official Node Docker image is currently
`26.10.0-alpine3.24`, verified against the official Docker library manifest and
both native registry architectures. Node 26.11.1 Docker tags are not published
yet. CI selects the latest standalone stable Node 26.11.1; container build/source
measurements record their actual 26.10.0 producer. Do not substitute a fabricated
image tag or report the image as containing 26.11.1.

The latest upstream Caddy binary was compiled with Go 1.26.8. Preserve its
original complete runtime source and use the matching immutable inspection
compiler for its graph. The application backend builds with Go 1.27.1 and retains
its separate exact runtime source. Replaying an upstream producer does not
change the application's selected compiler or rebuild Caddy's signed binary.

Local Temurin 27+35 validation built the Android app and passed the existing app
and shared host tests (158 and 175 cases, no failures or skips). The migrated
shared test task is `:shared:testAndroidHostTest`; its Android fixtures moved to
the plugin's `androidHostTest` source set. Both generated legal bundles verified
their resolved artifacts, and the repackaged APK's three legal assets matched
the repository. Apple app/framework/share-extension compilation and XCTest
remain pending on GitHub's Xcode 27 runner.

Primary references: [Go](https://go.dev/dl/),
[Node](https://nodejs.org/en/blog/release),
[Official Node Docker images](https://raw.githubusercontent.com/docker-library/official-images/master/library/node),
[Temurin](https://adoptium.net/news/2026/10/eclipse-temurin-27-available),
[Gradle](https://gradle.org/releases/),
[Kotlin](https://kotlinlang.org/docs/releases.html),
[SKIE](https://skie.touchlab.co/changelog/0.10.15),
[AGP](https://developer.android.com/build/releases/agp-9-4-0-release-notes),
[Buildx](https://github.com/docker/buildx/releases/tag/v0.37.2),
[BuildKit](https://github.com/moby/buildkit/releases),
[golangci-lint](https://github.com/golangci/golangci-lint/releases),
[Alpine](https://www.alpinelinux.org/downloads/),
[Apple](https://developer.apple.com/xcode/system-requirements/),
[GitHub runner](https://github.com/actions/runner-images/issues/14404).

Release/source regressions passed: 96 focused cases in 2.587 seconds. The actual
immutable-image Go and browser builds passed (7.7 and 13.2 seconds); web type
checking passed, and the six transfer integration cases passed in 7.543 seconds.
The live Go source collector verified the complete pinned Go 1.27.1 archive and
38 original notice files. Separate executable sources retain their exact
versions, archive hashes, complete trees and mandatory LICENSE/PATENTS files;
missing or swapped correspondence is rejected.

The first broad backend validation invocation omitted the read-only documentation
fixture mount and also hit a revocation timing failure under concurrent load.
The corrected bounded rerun passed both affected cases in 1.071 seconds; the
remaining packages passed. This is not a claim that the initial run passed.
Hosted race checking and the complete workflow on the updated commit remain
pending. Existing application tests are retained; Android's previously unbounded
job now has a 15-minute limit, and the native iOS/readiness/XCTest limits remain
unchanged.

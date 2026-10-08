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
- [ ] Refresh Go, Node, Buildx, BuildKit and golangci-lint pins together with
      their release source/scanning constraints. Verify meaningful affected
      regressions and application builds.
- [ ] Upgrade both native version catalogs, Gradle wrappers and CI Java together.
      Migrate the Android/Kotlin plugin configuration and unit-test task as needed;
      preserve Android, iOS and share-extension protocol compatibility.
- [ ] Refresh native corresponding-source inputs and generated notices for both
      mobile platforms. Verify Android/shared compilation and tests locally;
      leave macOS compilation and XCTest pending until hosted execution passes.
- [ ] Refresh Alpine runtime/source-build inputs with matching package recipes,
      notices and source checks. Validate the actual release base images and
      native container recovery through the planned release workflow.
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

Primary references: [Go](https://go.dev/dl/),
[Node](https://nodejs.org/en/blog/release),
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

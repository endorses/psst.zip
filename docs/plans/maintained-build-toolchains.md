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
- [x] Verify the updated actions, native build and XCTest on GitHub.
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
- [x] Inspect hosted check annotations as well as conclusions. Verify that the
      Node action deprecation, moving Ubuntu runner and outdated Gradle notices
      are removed; investigate any new warnings without extending test budgets.
- [x] Verify that Ubuntu 26.04 and its Arm64 runner are generally available;
      explicitly select the latest stable labels rather than the current
      Ubuntu 24.04 mapping behind `ubuntu-latest`.
- [x] Update all 16 Linux workflow runner labels, preserving native architectures
      and existing job limits. Verify actionlint using documented hosted labels.
- [x] Verify actual hosted Linux execution with the Ubuntu 26.04 Python 3.14
      runtime. Exact-head run `37744385013` passed repository security, backend,
      web and Android/shared with no check annotations. Security's job log records
      Ubuntu 26.04.1 image `20260927.149.1`, whose pinned software manifest lists
      default Python 3.14.4; all 371 release regressions passed in 34.855s.
      Native iOS also passed all 176 XCTest cases with no annotations. Subsequent
      candidate `37747004338` built the original images on both x64 and Arm64,
      but failed while recording image-config metadata; complete Arm64 source
      and recovery validation remains pending.
- [x] Replace Ubuntu's prerelease Skopeo package in the publishing job with
      the upstream stable 1.24.1 native build. Verify its pinned source, version,
      required containers configuration and digest-preserving OCI copy locally.
- [ ] Exercise that native tool installation and registry transport in the
      protected publishing job after the independent release gates succeed.
- [x] Commit verified changes and plan checkpoints; keep store research drafts
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
the repository. Hosted Xcode 27 subsequently passed the Apple
app/framework/share-extension build and all 176 XCTest cases.

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

Hosted run
[`37733269430`](https://github.com/endorses/psst.zip/actions/runs/37733269430)
passed the updated Android/shared, web and native iOS jobs. These checks have no
annotations; the Gradle 9.1 out-of-date notice is gone. Native compilation took
10m26s, readiness one second, and the XCTest step 1m52s. All 176 cases passed
with no failures; case execution itself took 70.525 seconds.

The security job exposed an overlooked consumer fixture that still reported Go
1.26.8 to the now-pinned Go 1.27.1 dependency collector. The fixture now uses the
collector's selected version while retaining wrong-version and wrong-platform
rejection checks. The complete existing release regression suite passed locally:
369 cases in 23.666 seconds. No tests or longer timeouts were added.

The hosted backend job separately failed
`TestColdRestoreRunsRealServerStartupAndHTTPPolicies` because its first login
request exceeded the existing three-second HTTP deadline. The same focused test
passed locally with Go 1.27.1 and race checking in 4.127 seconds:
`go test -race -count=1 -timeout 90s -run '^TestColdRestoreRunsRealServerStartupAndHTTPPolicies$' ./cmd/server`
from `backend/`. This does not establish the hosted cause or make the failing run
successful. Inspection found that the test's three-second deadline was shorter
than SQLite's existing five-second busy wait, while authentication can overlap
startup maintenance after health readiness. Requests now allow six seconds,
with no sleeps or retries and the unchanged overall 60-second test bound.
Transport failures include the method, path and child server log. Five focused
race-checked repetitions passed in 17.690 seconds. Production authentication and
database behavior are unchanged. The final hosted backend race suite passed in
run `37734917348`.

Native logs also disclosed Google's newly deprecated `sdkmanager`, despite the
current setup action release. Keep that pinned action only as the SDK downloader:
disable its legacy license invocation and package installation, then use the
supported `android --no-metrics --sdk="$ANDROID_HOME" sdk install` command in
both jobs. Preserve the project's compile SDK 35; remove the iOS job's unused
explicit build-tools 35.0.0 download. Google's replacement has no separate
license command. The normal hosted Gradle builds must verify the resulting SDK
installation. Primary references:
[SDK Manager deprecation](https://developer.android.com/tools/sdkmanager),
[Android CLI](https://developer.android.com/tools/agents/android-cli),
[SDK installation](https://developer.android.com/tools/agents/android-cli/commands/sdk_install),
[pinned setup implementation](https://github.com/android-actions/setup-android/blob/be39fa834029ff78f1a44aa3bb0819b8fc2bd8fd/src/main.ts).

The native Gradle logs contain a future Gradle 10 deprecation summary without
individual attribution. Both jobs now use supported warning mode `all` to expose
the responsible script or plugin during their existing builds; this adds no
builds or tests. Other compiler diagnostics remain visible. Empty GitHub check
annotations are not a claim that all compiler output is warning-free.

The final Android build passed with Android CLI SDK installation and no
`sdkmanager` deprecation message. Its detailed problems report attributes
`Configuration.setVisible` to current AGP 9.4.0; current SKIE 0.10.15 also uses
the API in its [pinned source](https://github.com/touchlab/SKIE/blob/7e7b8b597a4da8ba121a33775799ad51e517beb5/SKIE/skie-gradle/plugin-impl/src/main/kotlin/co/touchlab/skie/plugin/subplugin/SkieSubPluginManager.kt#L21).
Removal is scheduled for Gradle 11. The plain
`KotlinNativeBundleArtifactsTypes` enum is defined by current KGP 2.4.20 itself,
verified in its selected JAR and [pinned source](https://github.com/JetBrains/kotlin/blob/890ac1d94fdb80eb85f0eeb5be5e4352df987b2f/libraries/tools/kotlin-gradle-plugin/src/common/kotlin/org/jetbrains/kotlin/gradle/targets/native/toolchain/KotlinNativeBundleArtifactFormat.kt).
Its failure boundary is Gradle 10. The warning's advice to upgrade KGP 2.0.x to
2.1+ is stale for this selection. No newer stable AGP/KGP/SKIE release resolves
these calls; keep them visible and require upstream compatibility fixes before
adopting those future Gradle majors. The selected cryptography plugin 0.6.0 is
current and is not the identified source of these warnings.

Final exact-head workflow
[`37734917348`](https://github.com/endorses/psst.zip/actions/runs/37734917348)
passed all five jobs at `71342ed4ed9be91f1d59a7eddc89cd336b31234f`, with no
GitHub annotations. Job times were security 1m36s, Android/shared 4m11s,
backend 7m24s, web 7m36s and iOS 16m16s. Native compilation took 13m34s;
readiness took one second and the XCTest step 1m50s. All 176 cases passed
(77.677 seconds of case execution). The test, readiness and job bounds are
unchanged. Protected PR #3 merged as
`c4f7eaf1e3e0f0102bcb053548a973072e6f466b` after those checks. Full native
container source/recovery and first publication remain pending.

Ubuntu 26.04 x64 and Arm64 became generally available on 2026-09-17, with
labels `ubuntu-26.04` and `ubuntu-26.04-arm`. The initial Ubuntu 24.04 pins
were too conservative for the latest-stable requirement. `ubuntu-latest`
remains 24.04 until its rollout beginning October 19 and ending November 19;
use the explicit stable labels. The 26.04 manifests select Python 3.14 by
default. Local release regressions already passed on Python 3.14.7, while
actual hosted execution on the updated image remains pending.
[GA announcement](https://github.com/actions/runner-images/issues/14747),
[migration announcement](https://github.com/actions/runner-images/issues/14748).

Ubuntu 26.04's Skopeo package is `1.21.0~pre1-2build1`; the image's
`1.21.0-dev` label reflects prerelease sources. Upstream stable Skopeo 1.24.1
is commit `77f3d92f861271c7cb9175afcf876017bcae9202`. Its supported native
source build retains CGO and upstream containers configuration, using the
selected Go 1.27.1; it does not require a container wrapper for transport.
Local Ubuntu 26.04 AMD64 verification passed with Go 1.27.1. Skopeo reported
exactly `skopeo version 1.24.1 commit: 77f3d92f861271c7cb9175afcf876017bcae9202`.
The native CGO build linked GPGME, libassuan and libc. Upstream installation
retained its policy, registries configuration and sigstore directory. A small
OCI-to-archive copy with `--all --preserve-digests` preserved the inspected
manifest hash; it made no registry writes. Dependency installation took 75.6s,
and source fetch/build/install/checks took 32.4s, including about 27s of native
compile/install. This work runs only in publication, not routine test jobs.
Actionlint, formatting and shell syntax checks passed; owned temporary files
and the validation image were removed. Hosted publishing remains pending.
[Ubuntu package](https://packages.ubuntu.com/resolute/skopeo),
[Skopeo release](https://github.com/podman-container-tools/skopeo/releases/tag/v1.24.1),
[upstream build instructions](https://github.com/podman-container-tools/skopeo/blob/v1.24.1/install.md).

## Native CI build performance

- [x] Profile actual native CI before changing test coverage. Candidate source-CI
      run `37747004338`, commit `8485eb41cdfa7cd7102b0faf44d20285b88aa02c`,
      passed Android/shared in 2m45s and iOS in 14m56s. iOS compilation took
      11m53s, simulator readiness two seconds and the XCTest step 1m54s. All
      176 cases passed with 76.115s of case execution. These jobs had no check
      annotations.
- [x] Preserve Kotlin/Native's `~/.konan` directory across successful hosted
      builds using stable `actions/cache` 6.1.0, pinned to its Node 24 commit.
      Scope the key to host OS/architecture, actual Xcode version/build and the shared build,
      dependency catalog and Gradle wrapper inputs, with no cross-toolchain
      fallback. Keep the existing Gradle cache separate and enable Gradle's
      local build cache for the standalone shared project.
- [x] Verify the cold hosted cache save, including transfer time. Exact-head CI
      `37751784908` passed all five jobs at `cc84fef0b9e5a2d4d0414d6b5be57dd82f0af619`
      with zero annotations. Native iOS took 23m16s: build 18m18s, readiness
      three seconds and XCTest 2m41s. All 176 cases passed in 108.483s of actual
      case execution. The new cache missed as expected and saved 409,931,939
      bytes in 57s (approximately 48s compression and eight seconds upload).
      PR #8 merged as `cf67e1eac2082bc7f095c630be2bb100bb06b0dc`;
      its tree matches the checked head.
- [ ] Measure subsequent hosted restore, including transfer time, before claiming
      a build-time reduction. Preserve all meaningful XCTest cases and current
      build/readiness/test budgets. The initial cache belongs to
      `refs/pull/8/merge`; GitHub's branch scope requires a main-scoped cache
      before a candidate dispatched from main can reuse it. Use existing required
      main/candidate CI rather than adding a benchmark suite or full CI rerun.
- [ ] Resolve the roughly 4m45s before the first Kotlin build phase. The current
      buffered Xcode log does not establish its cause; do not attribute it to
      duplicate framework compilation or simulator boot without evidence.
      The existing build now requests Xcode's task timing summary; it performs
      no extra compilation. The same runner image previously had a 6m29s gap,
      with delays distributed across different tool probes. Hosted image setup
      already runs Xcode first-launch preparation, so repeating it is not an
      evidence-based fix.
      The cold-cache PR build's emitted-log gap was 7m06s, first Gradle invocation
      7m48s and second invocation 45s. Its timing summary reports accumulated
      script work of 518.283s, Swift compilation 176.297s and all tasks 837.992s;
      these sums are not the critical path. No heap/GC-pressure, OOM or daemon
      expiration warning was logged. Existing upstream Gradle and Kotlin/SKIE
      diagnostics and source warnings remain; no new cache error occurred.

The shared module's Android CI runs JVM host tests and does not build the Apple
framework or exercise its Darwin/CryptoKit implementation and Swift interfaces.
The first native Gradle invocation took 5m23s and downloaded LLVM/libffi into
`~/.konan` despite a 679MB Gradle cache hit. A second app/extension invocation
took only 18s, with Kotlin compilation already up to date. Approximately 2m16s
remained after the first invocation for Swift compilation, packaging and that
second invocation. The buffered log cannot precisely divide the first invocation
between download, Kotlin compilation and linking. The cache addresses a measured
missing input cache, not an established 5m23s saving. Kotlin explicitly recommends
preserving this directory, enabling Gradle's build cache and measuring at least
two builds. No experimental compiler flags or new test suites were added.
[Kotlin compilation guidance](https://kotlinlang.org/docs/native-improving-compilation-time.html),
[GitHub cache scope](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching),
[pinned cache action](https://github.com/actions/cache/tree/55cc8345863c7cc4c66a329aec7e433d2d1c52a9).

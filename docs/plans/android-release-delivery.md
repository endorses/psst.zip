# Android release delivery

Created: 2026-10-09. Status: preparation complete; implementation and publication
pending. First deliverable: a signed, optimized **psst.zip** APK published on
GitHub, with a verified path from the operator's debug installation and a
repeatable update process that preserves client-held keys and history.

## Scope and starting point

The web/backend publication and normal VPS update path are already operational.
This work does not deploy server containers or depend on resolving iOS CI timing.
The separate Caddy exception still expires October 23; off-host VPS backups stay
deferred under the existing operator decision.

Current Android identity is `zip.psst.android`, shared code is `zip.psst.shared`,
and the operator reports the current debug APK pairs successfully with the live
server. Keep application IDs and operator-selected server URLs stable. Do not
equate successful pairing with recovery of private keys from an older sandbox.

| Area               | Verified source baseline                                                          | Required release work                                                  |
| ------------------ | --------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| Versions           | Name `0.1.0`, code `1` hardcoded                                                  | Checked-in version metadata and increasing codes                       |
| Release variant    | R8/minification enabled; no signing configuration                                 | Build, lint, sign and verify the actual optimized APK                  |
| SDK                | Minimum 26, compile/target 35                                                     | Check stable SDK compatibility and update deliberately                 |
| Toolchains         | Kotlin 2.4.20, AGP 9.4.0, Gradle 9.8.1, CI JDK 27                                 | Recheck latest stable releases and compatibility before implementation |
| CI                 | Debug assembly, app JVM tests, shared Android host tests                          | Reuse passing source checks; add only release-specific validation      |
| Private state      | Keystore-encrypted receive keys, sessions and guest capabilities; backup disabled | Verify a safe signing transition; raw database copies are insufficient |
| Portable migration | No archive export/import on Android or iOS                                        | Implement parity if an archive is the selected transition              |
| Source identity    | `BuildConfig.SOURCE_REVISION` for clean tracked mobile source                     | Require exact nonempty release commit identity                         |

Sources inspected: [app build](../../android/app/build.gradle.kts),
[Android versions](../../android/gradle/libs.versions.toml),
[shared build](../../shared/build.gradle.kts),
[CI](../../.github/workflows/ci.yml),
[manifest](../../android/app/src/main/AndroidManifest.xml),
[receive key storage](../../android/app/src/main/java/zip/psst/android/data/InboxKeyStore.kt),
[session storage](../../android/app/src/main/java/zip/psst/android/data/SessionStorage.kt),
and [iOS session storage](../../ios/Shared/SessionStore.swift).
The refreshed local store research remains an uncommitted draft; this plan is
self-contained and must not rely on that draft being published.

## Signing and preservation decision

Use a dedicated production signing key with a tested recovery copy. Do not reuse
the debug key as the permanent public signer. The existing debug app must remain
installed until the chosen transition is verified. Future releases keep the
production signer and application ID stable.

Android supports signing rotation from Android 9, but AOSP discourages it on
Android 12 and earlier. API 26 support prevents assuming universal rotation.
A private bridge must be tested for the operator's actual OS and installed
certificate, including protected data access after upgrade. If unsuitable, an
encrypted archive export/import capability must precede any reinstall.
[AOSP signing rotation](https://source.android.com/docs/security/features/apksigning/v3),
[Android signing guidance](https://developer.android.com/studio/publish/app-signing).

- [x] Inspect signing/version configuration and confirm the absence of a portable
      key/history migration flow on both platforms.
- [ ] Record the operator's Android OS, actual installed package and public signer
      fingerprint privately. Distinguish current and older package sandboxes;
      never infer preserved data from the app's visible name or a fresh pairing.
- [ ] Evaluate a bounded private signing-lineage transition and its trust/channel
      implications. Record supported OS versions and verify in-place installation,
      preserved protected state and a later production-signed update. Do not
      publish a lineage that accidentally makes the debug key a public authority.
- [ ] Choose and record the transition. If an archive is needed, first specify
      authenticated encryption, ownership, version/size bounds, interrupted import,
      duplicate handling and atomic activation. Cover Send keys, Receive-v2 private
      keys, guest capabilities and history; exclude reusable sessions by default.
- [ ] Implement any selected export/import capability on Android and iOS using
      native file pickers and protected stores. Shared archive/protocol changes
      preserve parity; include extension access where relevant. A local-only
      signing bridge does not require an iOS store workflow.
- [ ] Verify the transition with synthetic data first, then the operator's actual
      installation without sending keys, tokens, private links or archives to chat,
      GitHub artifacts or CI logs. Leave any old installation/data intact until
      recovery has been demonstrated.
- [ ] Create the production key privately, record its public certificate fingerprint,
      verify recovery from a separately retained encrypted copy, and document
      custody/rotation. No signing passwords or private key values belong in chat.

The decision above gates replacing the operator's installation. Preparation of
unsigned release builds and metadata can proceed while it is being resolved.

## Release build and metadata

- [x] Refresh the Android research's project/toolchain snapshot and verify current
      primary signing, Play target/testing and F-Droid reproducibility guidance.
- [ ] Introduce one checked-in Android version source used by Gradle, release
      validation and future F-Droid recipes. Choose the first release code above
      every installed/private bridge version; reject reused or decreasing codes.
- [ ] Use independent `android-vX.Y.Z` release tags. Check exact commit/main ancestry,
      version agreement and source identity; protect this tag namespace against
      updates/deletion. Do not append assets to immutable container releases.
- [ ] Recheck latest stable dependencies, SDK/tools, runners/actions and compatibility
      before changing pins. Record required upgrades and behavioral implications,
      preserve API 26 unless an explicit support decision changes it, and inspect
      actual Actions warnings/notices. Avoid adding preview tools by default.
- [ ] Check the packaged native libraries and Android 16 KB compatibility using the
      final APK. Raise compile/target SDK deliberately; Play currently requires
      API 36 for new mobile app submissions, independently of GitHub sideloading.
- [ ] Add explicit release signing inputs that fail if missing; no fallback to
      debug signing. Keep keystores/passwords outside the repository and caches,
      expose only public certificate metadata, and delete temporary material on exit.
- [ ] Build `:app:assembleRelease` and `:app:lintRelease` once with the selected
      stable toolchain. Inspect R8 warnings and necessary keep rules; preserve
      resolved dependency notices, corresponding source and license obligations.
- [ ] Verify package ID, version name/code, minimum/target SDK, release/debuggable
      flags, signer, supported native ABIs, exact source revision and notices from
      the final signed APK. Compute checksum only after final signing.
- [ ] Preserve the exact APK, public metadata, source tag and build instructions
      for publication; retain the shrinker mapping and any native symbols with
      deliberate private retention. Keep intermediate signing material excluded.

References: [versioning](https://developer.android.com/studio/publish/versioning),
[Play target API](https://support.google.com/googleplay/android-developer/answer/11926878),
[16 KB compatibility](https://developer.android.com/guide/practices/page-sizes),
and [signature verification](https://developer.android.com/tools/apksigner).

## GitHub release workflow

Start with manual dispatch from protected `main`, selecting an existing Android
tag. No APK publication on ordinary commits, no Play upload and no VPS access.
Use an independent Android environment and serialized Android publication.
Reuse the repository's existing security and native-notice tools where applicable;
do not copy the container source-replay pipeline into this workflow.

- [ ] Implement read-only source/version validation before credentialed jobs.
      Require trusted successful relevant CI for that exact commit and workflow;
      reject skipped, unrelated, stale or failed evidence. If required checks are
      absent, run those checks once without signing secrets rather than rerunning
      all server and iOS jobs solely for Android publication.
- [ ] Build/check once on Linux, then sign the selected artifact with protected
      credentials and verify it again. Ensure protected jobs execute only approved
      source, have minimal permissions, preserve failure status and never expose
      passwords, keystore contents or private client data.
- [ ] Add a preview that presents tag/commit, version code, signer, checksum and
      verification results. Keep credentials and actual publication disabled until
      the artifact and first-install/update evidence are ready for review.
- [ ] Configure the Android environment and its signing material privately after
      the implementation is reviewable. Separate it from container-release and
      production deployment credentials. State any GitHub reviewer requirement
      explicitly; do not introduce repeated custom evidence-comment prompts.
- [ ] Publish a new Android GitHub release with the APK, checksum, public release
      metadata, corresponding source/notices, release notes and install/update
      instructions. Reject conflicting existing assets; do not overwrite prior
      signed releases or silently replace tags on retry.
- [ ] Verify anonymous exact-byte download and APK signature/source metadata from
      the public URL. Complete the first real update preservation check and record
      the supported transition, without claiming F-Droid or Play availability.

## Efficient verification and platform parity

This phase catches failures specific to release delivery. Existing debug/shared
tests remain useful source evidence and should not be duplicated for coverage.
Run unit fixtures without sleeps or real production credentials; document the
observable defect caught by each new case. Release lint/build and one install/update
scenario do not belong in every documentation or container-only change.

- [ ] Reuse existing app/shared tests for serialization, networking, history and
      crypto. Add only missing signing/version/artifact-boundary checks; a source
      test passing does not prove R8 preserved runtime behavior.
- [ ] Execute one compact optimized-APK smoke covering server validation/pairing,
      encrypted send/download, Receive-v2 private-key use, guest access, QR scanning
      and document/photo sharing. Use disposable resources and modest files;
      preserve existing coverage rather than adding another exhaustive transfer suite.
- [ ] Verify a fresh production install and then a newer same-signer release
      installed over it: keys/history/accounts remain usable, application ID stays
      stable, and no uninstall or renewed NetGuard rule is required for a normal update.
- [ ] Separately verify the existing debug-to-production transition. Keep first
      migration checks distinct from routine same-signer update verification.
- [ ] Check API 26 behavior and the current stable Android version, including
      packaged native library compatibility. Use an emulator/device only for
      release-specific behavior that JVM tests cannot establish.
- [ ] Keep Android publication independent of native iOS CI. For any shared or
      parity app change, run the applicable existing iOS source/portable checks
      locally and native app/extension/XCTest checks on macOS; document unrun checks
      explicitly. Do not call an Android-only APK workflow an iOS verification.
- [ ] Start release build/verification with a 15-minute job timeout and report
      build, shrinking, signing, smoke and cache-transfer timings separately.
      Use real measured timings to tune work, not a higher timeout to hide a stall.
      Human approval wait is separate from machine execution; signing/download
      metadata checks should take seconds. No extra full CI benchmark runs.

Implementation commands to adapt once signing/version inputs exist:

```sh
./android/gradlew -p android :app:assembleRelease :app:lintRelease --no-daemon
apksigner verify --verbose --print-certs path/to/psst.zip.apk
```

These commands have not been run for this plan and do not create a verified
production artifact by themselves. Device installation uses the selected safe
transition; do not run an uninstall command as a prerequisite. Native iOS
verification for applicable changes uses the existing `xcode-27` CI job's
`build-for-testing` / `test-without-building` commands from `ci.yml`.

## Later store milestones

- [ ] Demonstrate clean independent reproducible APK builds, audit dependency/assets
      eligibility and submit an official F-Droid recipe using the same source,
      public signer fingerprint and upstream APK URL. Developer-signed publication
      depends on F-Droid's independent successful comparison and acceptance.
- [ ] Create/configure the chosen Play account, prepare listing/privacy/access
      materials and meet current API/native-library requirements. Configure a
      separate upload key if using Play App Signing; choose signing custody before
      enrollment so intended cross-channel updates remain possible.
- [ ] Produce/upload the AAB to Play testing, verify a Play-installed build and
      channel-switching upgrades, then explicitly approve public promotion.
      Applicable new personal accounts currently require 12 continuous closed-test
      participants for 14 days; CI success cannot replace that testing.
- [ ] Consider a custom F-Droid repository only if its update convenience justifies
      separate index signing/hosting. Neither store path changes VPS firewall rules.

References: [F-Droid reproducibility](https://f-droid.org/en/docs/Reproducible_Builds/),
[Play account testing](https://support.google.com/googleplay/android-developer/answer/14151465),
and [Play publishing API](https://developers.google.com/android-publisher/getting_started).

## Completion criteria and current record

- [x] Inspect the actual source/CI/private-state baseline and refresh primary research.
- [x] Write the bounded plan with a separate signing-transition prerequisite,
      release-specific checks and Android/iOS parity boundaries.
- [x] Format this plan and check its local source links. Commit only this plan
      through the repository guard; keep both store research drafts uncommitted
      under the earlier instruction. No app build, key creation, tag or publication
      is implied by completing this planning step.
- [ ] Complete the selected transition, production key recovery and release
      implementation, with applicable local and hosted validation recorded.
- [ ] Publish and anonymously verify the first signed Android APK, demonstrate
      preserved data on the agreed transition and a subsequent same-signer update,
      document operator steps, and mark only actually completed items above.

The first unresolved decision is the operator's existing-installation transition.
All implementation/device/store checks remain pending. Store acceptance, iOS
distribution and deferred VPS follow-ups are separate from this milestone.

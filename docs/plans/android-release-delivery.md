# Android release delivery

Created: 2026-10-09. Status: release tooling implemented locally; signing, device verification and
publication pending. First deliverable: a signed, optimized **psst.zip** APK published on
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
- [x] Evaluate a bounded private signing-lineage transition and its trust/channel
      implications. Record supported OS versions and verify in-place installation,
      preserved protected state and a later production-signed update. Do not
      publish a lineage that accidentally makes the debug key a public authority.
- [x] Choose and record the transition. If an archive is needed, first specify
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

On October 10, the operator selected the private signing bridge and accepted the
retained debug-certificate trust for signature permissions on the migrated
installation. Encrypted export/import is not the selected prerequisite; actual
phone identity, production-key recovery and in-place preservation remain unverified.

The decision above gates replacing the operator's installation. Preparation of
unsigned release builds and metadata can proceed while it is being resolved.

## Release build and metadata

- [x] Refresh the Android research's project/toolchain snapshot and verify current
      primary signing, Play target/testing and F-Droid reproducibility guidance.
- [x] Introduce one checked-in Android version source used by Gradle and release
      validation, with rejected reused/decreasing published codes.
- [ ] Confirm the first public code exceeds the actual phone and private bridge
      codes; prepared public code `3` exceeds the observed local debug code `1`
      and tested private bridge code `2`, but the actual phone remains unchecked.
- [ ] Use independent `android-vX.Y.Z` release tags. Check exact commit/main ancestry,
      version agreement and source identity; protect this tag namespace against
      updates/deletion. Do not append assets to immutable container releases.
- [x] Recheck latest stable dependencies, SDK/tools, runners/actions and compatibility
      before changing pins. Record required upgrades and behavioral implications,
      preserve API 26 unless an explicit support decision changes it, and inspect
      actual Actions warnings/notices. Avoid adding preview tools by default.
- [ ] Check the packaged native libraries and Android 16 KB compatibility using the
      final APK. Raise compile/target SDK deliberately; Play currently requires
      API 36 for new mobile app submissions, independently of GitHub sideloading.
- [x] Add explicit release signing inputs that fail if missing; no fallback to
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

- [x] Implement read-only source/version validation before credentialed jobs.
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

- [x] Reuse existing app/shared tests for serialization, networking, history and
      crypto. Add only missing signing/version/artifact-boundary checks; a source
      test passing does not prove R8 preserved runtime behavior.
- [x] Execute one compact optimized-APK smoke covering server validation/pairing,
      encrypted send/download, Receive-v2 private-key use, guest access, QR scanning
      and document/photo sharing. Use disposable resources and modest files;
      preserve existing coverage rather than adding another exhaustive transfer suite.
- [ ] Verify a fresh production install and then a newer same-signer release
      installed over it: keys/history/accounts remain usable, application ID stays
      stable, and no uninstall or renewed NetGuard rule is required for a normal update.
- [ ] Separately verify the existing debug-to-production transition. Keep first
      migration checks distinct from routine same-signer update verification.
- [x] Check API 26 behavior and the current stable Android version, including
      packaged native library compatibility. Use an emulator/device only for
      release-specific behavior that JVM tests cannot establish.
- [ ] Keep Android publication independent of native iOS CI. For any shared or
      parity app change, run the applicable existing iOS source/portable checks
      locally and native app/extension/XCTest checks on macOS; document unrun checks
      explicitly. Do not call an Android-only APK workflow an iOS verification.
- [x] Start release build/verification with a 15-minute job timeout and report
      build, shrinking, signing, smoke and cache-transfer timings separately.
      Use real measured timings to tune work, not a higher timeout to hide a stall.
      Human approval wait is separate from machine execution; signing/download
      metadata checks should take seconds. No extra full CI benchmark runs.

Implementation commands to adapt once signing/version inputs exist:

```sh
./android/gradlew -p android :app:assembleRelease :app:lintRelease --no-daemon
apksigner verify --verbose --print-certs path/to/psst.zip.apk
```

Local optimized assembly and lint were exercised during implementation; see the
verification record below. They do not create a verified production artifact by themselves. Device installation uses the selected safe
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

## Implementation and verification record (2026-10-09–10)

The operator reports Android 16 and no USB access. The local debug APK has package
`zip.psst.android`, version code `1`, and public signer SHA256
`bcda0d16e7cdf314aceab34e240e304b53e459ca6370076d31323022353db987`.
This does not establish the phone's installed signer or older app sandbox.

Implemented locally: [release workflow](../../.github/workflows/android-release.yml),
[artifact/signing checks](../../tools/android_release.py),
[exact-source CI reuse](../../tools/android_release_ci.py),
[private signing bridge](../../tools/android_signing_bridge.py),
and [actual-storage emulator update fixture](../../docs/testing/android-release-updates.md).
The workflow defaults to an unsigned preview, reuses relevant executed checks,
builds/lints once and signs only behind the Android environment. Publication and
private device evidence remain disabled until readiness is established. There are
no new backend/web/native iOS release jobs or custom approval-comment strings.

Stable dependencies and API 37 were refreshed with preserved API 26 support.
The operator approved Kotlin 2.4.20 while SKIE 0.10.15 rejects 2.4.21. Native
notices must match the final resolution on both platforms. The API 37 LAN
permission is implemented on Android; iOS already has localized purpose strings.
The latest lint bidi rule stalled in Kotlin PSI traversal: one rule is replaced by
[a bounded source scanner](../security/lint-bidi-workaround.md), while other lint
rules stay enabled. Actual resource-type and configuration-awareness errors were
fixed rather than suppressed.

Focused tooling checks pass: 34 Android release/workflow/update/signing cases in
0.042 seconds, 8 bidi boundary cases in 0.015 seconds and 5 CI-selection cases in
1.645 seconds. Actionlint and Android/iOS localization/source checks pass. Task
instrumentation uses the supported Gradle completion service in the existing
build; no extra full test benchmark was added. These timings describe local
checks, not hosted execution.

Local optimized assembly, release lint and app/shared JVM tests passed in 75
seconds with cached dependencies. App tests: 163 cases, 0 failures, 8.871 seconds;
shared Android tests: 175 cases, 0 failures, 7.215 seconds. This is local execution
evidence, not a hosted timing or a production-signed APK.

Pending: physical-device checks, the phone's actual transition, production-key
recovery, GitHub
environment/tag rules, hosted preview and public delivery.
Local JDK is Android Studio's Java 25.0.3; the workflow selects Java 27. Native iOS
app/share-extension/XCTest validation of changed shared dependencies remains
pending on macOS using the existing `xcode-27` job. No iOS native success is claimed.

The refreshed notice resolution contains 123 Android and 117 iOS artifact
variants, including the reviewed SLF4J 2.0.19 MIT license. The selected stable AGP
still emits an upstream deprecated configuration-visibility call warning
(scheduled removal in Gradle 11); newer Kotlin attributes also expose a dependency
metadata warning. These warnings are recorded rather than hidden. Linux's disabled
iOS-target notices reflect the validation boundary above.

The first timing-enabled composite build emitted duplicate task records; a
minimal composite fixture reproduced and verified the root-only listener fix.
The 75-second wall time is valid; that build's doubled task aggregates are not.
The next 61-second fixture build produced unique task records: shrinking took
41.999 seconds across two tasks, lint 27.458 seconds across six tasks and compilation
20.381 seconds across 62 tasks. Tasks overlap; these sums are not elapsed build time.

SDK 37 changed `apksigner` certificate output. The verifier now handles its
scheme-specific certificates and Android API ranges, while public APK verification
still rejects signing rotation, including v3.2 blocks. An actual Android 16 private
bridge installation exposed AndroidX's signature-permission ownership requirement:
the lineage needs installed-data and permission continuity. Shared-UID, rollback
and auth capabilities remain disabled. Accepting historical debug permission trust
on the real phone is still an operator decision; a later production-only APK does
not prove that Android discarded that signing history.

The optimized instrumentation runner exposed a systematic shared-dependency
classpath problem after R8 removed tracing and Kotlin facades. The private fixture
now compiles one protected-state diagnostic into its target APK and uses a small
framework-only Java instrumentation runner. R8 analyzes the entire diagnostic call
graph; only its reflective entry signature stays named. Public builds omit the
diagnostic and keep rule. A fixture-only metadata marker prevents publication even
when its version matches the public release. AGP 9's separate Kotlin source roots are
configured explicitly for this diagnostic; its target and framework runner
compiled successfully in 24 seconds without the source-directory deprecation.
The unmodified optimized public configuration starts successfully on an owned
API 26 emulator with the latest stable emulator 37.2.12. This establishes startup,
not the pending crypto or sharing smoke checks. The same public configuration also
starts on the stable API 37 16 KB image (runtime page size 16384), and successfully
validates the disposable localhost server and logs in as a synthetic regular user.
Android 17's LAN rationale, platform permission denial, recovery and allowed
connection passed against the disposable gateway. Its public optimized Receive
flow created a recipient key, received a 32-byte encrypted web upload, opened the
HPKE/AES envelope and saved plaintext that matched every original byte. The runtime
smoke APK was built from `02a84baf3f0e029d78532426c15af33c68902187` with a disposable
signer. This is not production signing, camera-QR or physical-device evidence.

The corrected protected-state diagnostic passed on Android 16/API 36 from source
`1d7f435a38b5de69732d278ebd73838e7d038813`: actual debug code `1` to private bridge
code `2` to production-only code `3`. All three executions preserved the application
UID, Keystore session, Room Send history and decryption keys, Receive-v2 private
keys and HPKE/AES decryption, and guest history/capability. Signing keys were
temporary and deleted. These are fixture-modified APKs with whole-program R8
optimization, not public release APKs or the operator's phone. The tool requires
raw Android instrumentation's explicit success result as well as the one passing
diagnostic; an apparent success stream with a canceled result cannot pass.

Those three necessary APK/test-pair builds took 19.2, 57.7 and 56.6 seconds.
The final normal public configuration assembled and passed release lint in 66
seconds; it contains neither the diagnostic/runner classes nor fixture metadata.
Unsigned artifact validation confirmed package/version/source, API 26 minimum,
API 37 target, four native ABIs, 16 KB alignment and current notice bytes. Its
source is the same `1d7f435` commit. Production-signature verification remains
pending production-key custody and the first release.

The API 26 public Receive smoke exposed a missing owner-download storage permission
request: the encrypted upload arrived, but saving to public Downloads failed.
Receive now gates initial save, confirmation and retry on the legacy API 26–28
permission, preserving the selected link, private key and saved progress after
denial. Its four focused state tests passed in 0.023 seconds (20 seconds including
required compilation). iOS uses sandboxed Documents storage and needs no matching
legacy Android permission change. The normal optimized APK from `7e06e4b` assembled
and passed lint in 70 seconds. On API 26, denial preserved the link and received
file; granting permission then saved all 32 decrypted bytes correctly. The smoke
used a disposable signer, not the production key.

Background inbox refresh initially erased the denied-permission message. Refresh
now retains that specific message while continuing to clear recovered transient
errors. The same four focused tests passed (17 seconds including compilation).
The normal optimized APK from `ce31312` passed the final API 26 flow: deny storage,
refresh the inbox, retain the explanation and Retry button, grant permission on
Retry, then save all 32 decrypted bytes unchanged. Full signed-artifact validation
passed with a disposable signer. Compilation/shrinking took 66 seconds; completing
the explicitly opted-in unsigned packaging and lint used cached tasks in two
seconds. This is local evidence; production signing and physical-phone checks
remain pending.

The planned first public version code is now `3`, above the synthetic private
bridge's `2`. This also permits a normal, unmodified public-APK code `2` to code
`3` same-signer update check, independently of the private diagnostic. The actual
phone's version/signature remains a prerequisite before applying either APK.

The [compact public-APK smoke](../testing/android-release-smoke.md) passed on the
stable API 37 16 KB image with unmodified optimized APKs: source `ce31312`, code `2`,
then source `ea6d57b`, code `3`, using one disposable signer. The newer public build
and release lint took 39 seconds; the 34 tooling cases passed in 0.044 seconds.
Guest QR-image decoding saved the web fixture's exact 47 plaintext bytes. A
short-lived synthetic login QR paired the account, and the backend independently
reported its grant connected. An actual MediaStore image share and a DocumentsUI
text selection produced an encrypted Android Send; web decryption matched all
3,488 PNG bytes and 47 text bytes. The saved-file Share action opened the native
chooser with the FileProvider filename; delivery to an external receiver was not
tested.

Installing the normal higher-code APK with `adb install -r` preserved its UID and
paired session without uninstalling or clearing data. The updated app recovered
the exact Send link/key from history, and both files decrypted again. A Receive-v2
private key created before the update decrypted a 32-byte submission afterward.
After removing only the synthetic guest plaintext, retained device history
redownloaded it with all 47 bytes unchanged. The optimized camera decoder also read
the generated QR through the emulator's image-file camera mode, without using the
image picker. This is emulator evidence; real-device optics, OEM signing behavior
and NetGuard remain separate physical checks. No production signing key was used.

Read-only GitHub inspection on October 10 confirmed protected `main` and enabled
immutable releases. The existing `version-tags` ruleset covers only `refs/tags/v*`;
there is no `android-release` environment yet. Android-specific configuration
therefore remains pending. Publication preflight now also verifies an explicit
custom environment policy permitting only the `main` branch, rejecting an
unrestricted environment, wildcards, additional branches, a tag named `main` or a
missing branch type. Its actual API response shape was checked against the
existing container environment using GitHub API version `2026-03-10`. The 35
focused release/signing/update/policy cases passed in 0.035 seconds; Ruff passed.

The next prerequisites are choosing the transition, checking the actual phone and
production-key recovery. Store acceptance, iOS
distribution and deferred VPS follow-ups are separate from this milestone.

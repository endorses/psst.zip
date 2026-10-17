# Android release delivery

Created: 2026-10-09. Status: release tooling merged in PR #36 and private APKs signed;
in-place device verification and hosted publication pending. First deliverable: a signed, optimized **psst.zip** APK published on
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
- [x] Record the operator's Android OS, actual installed package and public signer
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
- [x] Create the production key privately, record its public certificate fingerprint,
      verify recovery from a separately retained encrypted copy, and document
      custody/rotation. No signing passwords or private key values belong in chat.

On October 10, the operator selected the private signing bridge and accepted the
retained debug-certificate trust for signature permissions on the migrated
installation. Encrypted export/import is not the selected prerequisite. The actual
phone identity and production-key backup recovery are now verified; in-place
preservation remains unverified.

The operator created the production keystore and reported a backup on an external
drive. Its public certificate SHA256 is
`6f10e624c6f577544bd01928b33d834ad5434e54363313617e30fe2dfa835b15`.
On October 17, the operator opened the external-drive backup with `keytool`:
the alias is a 4096-bit RSA `PrivateKeyEntry` and its certificate fingerprint
matches. The operator's subsequent recovery-helper output proves both the expected
backup fingerprint check and disposable `-certreq` completed successfully before
the helper reached mapping-identity restoration. Production-key backup recovery
is therefore confirmed. The public fingerprint is now set
as `ANDROID_RELEASE_SIGNER_SHA256` in the protected `android-release` environment.
The operator then uploaded the four signing inputs directly to GitHub. Readback
confirmed the environment contains `ANDROID_RELEASE_KEYSTORE_BASE64`,
`ANDROID_RELEASE_KEY_ALIAS`, `ANDROID_RELEASE_STORE_PASSWORD` and
`ANDROID_RELEASE_KEY_PASSWORD`; only their names and update timestamps were read.
Their values have not been tested by a signing job. The operator also added
`ANDROID_RELEASE_INSPECTION_TOKEN`; its presence was verified without reading its
value or claiming its permissions have been exercised. Publication readiness
remains unconfigured. The latest stable age release is still
1.3.2, verified against its official GitHub release metadata on October 17. Its
Linux tools were installed locally after checking the official archive SHA256;
no private age identity was generated by the agent. The operator subsequently
generated the mapping identity locally and supplied only its public recipient:
`age1ynap4qvafjyg0hf0vrfa8lvqnw7gzyc9quk0urkxx2hw0p5gxd5qde57el`.
This recipient is configured as repository variable `ANDROID_MAPPING_RECIPIENT`.
The encrypted external backup was subsequently restored and verified as recorded
below. No private key or password was supplied in chat.

Private phone-update preparation on October 17 built and linted two unmodified,
optimized unsigned APKs in clean private worktrees. The normal code `2` transition
APK uses the previously exercised historical source `ce31312c0a73896a103db49646c4ae45f646101f`;
the normal production-only code `3` APK uses merged source
`144cd60c61d6b6ed086583865b0782fcc1a74661`. Neither build enabled device-fixture
overrides or injected protected-state diagnostics. The builds took 1m23s and
1m22s, respectively; both lint XML reports contain zero Fatal/Error issues.
Their actual APKs passed package/version/source/notice/native alignment checks
from their matching source checkouts. R8 mappings were encrypted to the operator's
public recipient, retained privately with plaintext checksums, and the disposable
plaintext mappings removed. Mapping decryption/recovery is not yet verified.

A private Bash signing helper was prepared and syntax-checked in the operator's
local preparation directory. It prompts for existing local keystore passwords,
uses environment-reference signing arguments, refuses existing outputs, and runs
the matching bridge and production verification tools. The operator subsequently
executed it in their private terminal. Both signed APKs and their reports exist,
and their actual checksums match the reports. Independent verification confirmed
the bridge's code `2`, exact old/new certificate lineage and accepted capabilities,
plus the production-only code `3` signature, source, notices and native alignment.
The production APK SHA256 is
`37c85af0caa61a55478cee86964269a6c6396f711cde9f383a045c1b282a2075`;
its signer matches the recorded production certificate. The agent did not access
either private key or password. No app has been installed or published.

Both expected external backup files exist. A separate private recovery helper was
prepared and syntax-checked to open the external keystore, sign a disposable CSR,
restore the passphrase-encrypted mapping identity, and decrypt/check both retained
maps. The operator executed it: keystore checks passed, then age rejected the
mapping-backup passphrase. No full recovery-success record was produced. Header
inspection confirmed the backup is an age-v1 scrypt/passphrase ciphertext, and
the original local mapping identity remains present; the rejection's cause is
undetermined. A separate private repair helper was prepared and syntax-checked.
It keeps the existing mapping identity, creates a replacement encrypted backup,
restores it and verifies both actual map checksums before replacing the canonical
backup; the previous ciphertext is retained. The operator then executed it
successfully, reporting that the same intended passphrase was used. The earlier
rejection remains unexplained; it is not evidence of an incorrect password choice.
The private recovery report confirms the original mapping identity is unchanged,
the replacement backup restored successfully, and mappings for codes `2` and `3`
decrypted to their exact recorded plaintext checksums. Readback also confirmed the
canonical external ciphertext matches that successful recovery report's SHA256.
The previous encrypted backup was retained separately. No new identity was
generated, and no phone or signing-key changes occurred during repair.
Source worktrees and encrypted mappings remain
private for the pending recovery and phone checks. Wireless ADB initially found no
connection; after enabling it, the service was reachable but the TLS handshake
failed. The operator renewed pairing privately. A fresh read-only inspection then
confirmed the same Android 16 phone, current profile, package `zip.psst.android`,
installed version code `1`, expected debug certificate and unchanged UID. Its
installed APK was pulled only for public metadata/signature inspection and the
temporary copy removed. The new private pre-update identity record explicitly
keeps update and protected-state preservation false. No app data was read or
changed. Both external-backup recovery checks are complete. The operator then
confirmed the existing account/history and an existing transfer worked before
updating. After rechecking the current phone profile/API/UID/version and bridge
checksum, `adb install -r` successfully applied the selected private code `2`
bridge. Readback of the installed APK confirmed its exact expected checksum,
production certificate, code `2` and original UID/profile. The temporary inspection
APK was removed. The app cold-launched successfully in 151 ms. Operator functional
confirmation is pending before applying the subsequent production-only code `3`
APK; UID/signature/launch success alone does not prove client-held key preservation.

The decision above gates replacing the operator's installation. Preparation of
unsigned release builds and metadata can proceed while it is being resolved.

## Release build and metadata

- [x] Refresh the Android research's project/toolchain snapshot and verify current
      primary signing, Play target/testing and F-Droid reproducibility guidance.
- [x] Introduce one checked-in Android version source used by Gradle and release
      validation, with rejected reused/decreasing published codes.
- [x] Confirm the first public code exceeds the actual phone and private bridge
      codes; prepared public code `3` exceeds the actual phone's debug code `1`
      and tested private bridge code `2`.
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
- [x] Keep Android publication independent of native iOS CI. For any shared or
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
Read-only Wireless debugging inspection on October 10 verified that the actual
Android 16 phone has the same package, code `1` and certificate. A private identity
record retains its profile/UID and verification result outside tracked files.
Only its base APK was read and verified; the temporary copy was deleted. This
establishes the current installation, not protected-state preservation or an older
package sandbox. No phone update or private-data read has occurred.

Preparation is published in [draft PR #36](https://github.com/endorses/psst.zip/pull/36).
Its [hosted validation](https://github.com/endorses/psst.zip/actions/runs/38026753127)
passed all five CI jobs against `8c0fe3c`, including native iOS app, extension and
XCTest validation. Android/shared completed in 5m50s, repository security in
1m13s, backend in 6m30s, web in 7m7s and native iOS in 18m14s. These are hosted
job durations, not isolated test execution times or Android release build timings.
The operator approved this preparation push and the private bridge on October 10.
The [follow-up run](https://github.com/endorses/psst.zip/actions/runs/38028319836)
at `b69ecc7` also passed all five CI jobs: Android/shared 5m49s, security 1m40s,
backend 4m16s, web 6m51s and native iOS 20m30s. This native result predates the
event-reader API migration recorded below; that new shared change still needs
its own hosted native validation.
[Read-only Wireless debugging instructions](../releases/android.md#read-the-installed-identity-without-usb)
were used for the actual phone check; no phone update has run.

Implemented: [release workflow](../../.github/workflows/android-release.yml),
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

Pending: physical-device flow checks, the phone's actual transition, production-key
recovery, GitHub
signing inputs, hosted preview and public delivery.
Local JDK is Android Studio's Java 25.0.3; hosted Android CI passed with Java 27.
Native iOS app/share-extension/XCTest validation of the changed shared dependencies
passed in the existing `xcode-27` job against `8c0fe3c`; this does not establish an
iOS production signing or store release.

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
and auth capabilities remain disabled. The operator accepted historical debug
permission trust on October 10; the installed identity check is complete. Applying
the bridge still requires production-key recovery and preservation checks. A later production-only
APK does not prove that Android discarded that signing history.

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
phone's version/signature is now verified; production-key recovery and actual
preservation remain prerequisites before replacing its APK.

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
immutable releases. The existing `version-tags` ruleset covers only `refs/tags/v*`.
The separate `android-release` environment is now configured with `endorses` as
reviewer and exactly the `main` branch policy. Active ruleset `24830721`,
`android-version-tags`, protects `refs/tags/android-v*` from updates/deletion with
no bypass actors. Readback verified both configurations. The environment has no
signing secrets or enabled publication readiness; no Android tag/release exists.
Publication preflight also verifies an explicit
custom environment policy permitting only the `main` branch, rejecting an
unrestricted environment, wildcards, additional branches, a tag named `main` or a
missing branch type. Its actual API response shape was checked against the
existing container environment using GitHub API version `2026-03-10`. The 35
focused release/signing/update/policy cases passed in 0.035 seconds; Ruff passed.

Hosted warning inspection identified project-owned legacy Compose autofill APIs.
They now use stable content-type semantics and the text fields' existing value
callbacks; iOS already uses equivalent native username/password/new-password hints.
SQLite's native-access flag is scoped to the app's host unit-test JVMs. Optimized
assembly, release lint and the two affected SQL test classes passed in 71 seconds;
their five cases took 0.305 seconds. Neither the deprecated autofill diagnostics
nor SQLite native-access warning appeared. Credential-provider autofill on a
physical phone is not claimed from compilation. Hosted Android/shared validation
at `b69ecc7` passed in 5m49s under Java 27, without either warning.

The follow-up run at `acbbbef` caught stale build-input hashes in the native notice
inventories after the test JVM configuration changed. Actual offline dependency
resolution regenerated both inventories in 10 seconds: the same 123 Android and
117 iOS variants and unchanged license payloads. Only the Gradle input hash changed
in each inventory; the input freshness check now passes. The guard remains enabled.

JourneyApps [4.3.0](https://github.com/journeyapps/zxing-android-embedded/releases/tag/v4.3.0)
is still its latest published stable scanner and uses legacy Camera integer IDs
and parameter callbacks. That compatibility boundary remains documented and
visible; substituting Camera2 IDs would not modernize its backend. The earlier
optimized camera smoke remains evidence for the unchanged scanner behavior.
The upstream Gradle visibility/native-attribute warnings remain visible as well.

Further hosted warning inspection identified deprecated clipboard, Parcelable
share-intent and shared event-line APIs. Clipboard writes now use suspendable
`LocalClipboard` and show feedback after the write; share extraction uses
`IntentCompat` with API 26 support. iOS already has equivalent native clipboard
and share-extension behavior. The shared reader uses Ktor's stable line API with
the pinned 3.6.0 byte bound, lenient line endings and partial-EOF behavior preserved.
Four boundary cases protect non-ASCII byte limits, bare CR/partial EOF and error
propagation; no second transfer suite was added. All ten reader cases passed in
0.209 seconds; eight existing share-history cases passed in 0.127 seconds.
Optimized assembly and release lint completed; after correcting the test fixture's
error injection, the combined cached build/lint/test invocation passed in two
seconds. Native notice input freshness, bidi scanning and iOS source gates passed.
Hosted [run 38029537217](https://github.com/endorses/psst.zip/actions/runs/38029537217)
passed all five jobs at `12722a6`: Android/shared 5m44s, security 1m39s, backend
6m28s, web 7m49s and native iOS app/extension/XCTest 13m27s. Android logs confirm
the line-reader, clipboard and Parcelable deprecation warnings are gone. The
remaining scanner/plugin compatibility warnings stay visible. Clipboard device
interaction is not claimed from JVM tests or compilation.

GitGuardian separately flagged a dummy password in the bridge unit test. It is
used only with an inert non-key file to verify environment-reference arguments,
not a signing credential. The full-history repository secret scan passed. The
operator ignored incident `38067040` as a test credential and reran its GitGuardian
check; the check passed at 06:45 UTC on October 10. Under the operator's conditional
merge approval, [PR #36](https://github.com/endorses/psst.zip/pull/36) merged at
06:46 UTC as `144cd60c61d6b6ed086583865b0782fcc1a74661`, preserving its commits.
The normal main-branch [CI run 38032098103](https://github.com/endorses/psst.zip/actions/runs/38032098103)
passed; its terminal result was verified on October 17.
Readback after the final preparation CI run confirmed that the `android-release`
environment still has no secret or variable entries; signing and publication
readiness have not been enabled.

The private bridge is selected and the installed phone identity is verified. The
next prerequisites are the pre-update functional baseline and actual in-place transition.
Store acceptance, iOS
distribution and deferred VPS follow-ups are separate from this milestone.

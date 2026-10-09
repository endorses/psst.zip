# Android APK releases

Android publication is independent of server containers and VPS deployments.
The `Android release` workflow runs only by manual dispatch on protected `main`,
selecting an existing `android-vX.Y.Z` tag. Its default is an **unsigned preview**;
it does not publish an APK, create a signing key or update an installed app.

## Version and build

`android/release-version.properties` is the single checked-in version source.
Keep the application ID `zip.psst.android` stable and increase `versionCode` above
all public releases and any private bridge installed on the operator's device.
The first planned APK is version `0.1.0`, code `2`; this is above the observed local
debug APK's code `1`, but the phone's installed code/signature still need checking.
Container tags retain their independent `vX.Y.Z` namespace.

The current build uses stable AGP 9.4.1, Gradle 9.8.1, SDK API 37 and Build Tools
37.0.0. Kotlin stays at 2.4.20: the operator approved this compatibility exception
because latest stable SKIE 0.10.15 rejects Kotlin 2.4.21. Recheck SKIE compatibility
before upgrading; do not disable it to make Android compile at iOS's expense.
The shared module's iOS native validation still requires macOS.

The dependency refresh uses Compose BOM 2026.09.00, Activity 1.13.0, Lifecycle
2.11.0, Navigation 2.10.2, Room 2.8.5, coroutines 1.11.0, Ktor 3.6.0 and
serialization 1.11.0. Android 17/API 37 adds local-network permission handling;
public server connections do not request that permission. Android 16 and earlier
keep the existing network behavior. iOS already declares its local-network purpose.
See the [bounded lint workaround](../security/lint-bidi-workaround.md) for the
single stalled bidi rule and its replacement source check; other lint checks remain
active.

Version evidence: [official Compose BOM](https://dl.google.com/dl/android/maven2/androidx/compose/compose-bom/2026.09.00/compose-bom-2026.09.00.pom),
[AndroidX releases](https://developer.android.com/jetpack/androidx/versions),
[Ktor metadata](https://repo.maven.apache.org/maven2/io/ktor/ktor-client-core/maven-metadata.xml),
[coroutines metadata](https://repo.maven.apache.org/maven2/org/jetbrains/kotlinx/kotlinx-coroutines-core/maven-metadata.xml),
and [serialization release](https://github.com/Kotlin/kotlinx.serialization/releases/tag/v1.11.0).
The inspection permissions follow GitHub's [environment API](https://docs.github.com/en/rest/deployments/environments#get-an-environment).

From a clean tagged checkout with the SDK and workflow's JDK installed:

```sh
PSST_ANDROID_UNSIGNED_RELEASE=true ./android/gradlew -p android \
  :app:assembleRelease :app:lintRelease --no-daemon
python3 tools/android_release.py verify-apk --unsigned \
  --apk android/app/build/outputs/apk/release/app-release-unsigned.apk \
  --sdk-tools "$ANDROID_HOME/build-tools/37.0.0" \
  --revision "$(git rev-parse HEAD)" --output /tmp/android-verification.json
```

The verifier checks the actual APK's package, version, SDKs, debuggable flag,
packaged exact source revision, native ELF and ZIP alignment, and notice bytes.
An unsigned preview cannot pass signed-publication validation. Dirty mobile source
has no source revision and cannot be published. Remove your disposable report
when finished.

`PSST_ANDROID_DEVICE_TEST_BUILD_TYPE` and `PSST_ANDROID_DEVICE_TEST_VERSION_CODE`
are reserved for private disposable update fixtures. Do not set them in a release
workflow: fixture APK metadata carries `deviceFixture=true`, which public
verification rejects even if its version matches the checked-in version.
Explicit private builds compile one diagnostic into the target APK so R8 analyzes
its complete store/crypto call graph. A separate framework-only Java runner invokes
one retained entry signature, avoiding shared AndroidX/Kotlin test dependencies
that target optimization can remove. App and crypto optimization remain enabled.
Public builds omit the diagnostic sources and its keep rule entirely. These
fixture-modified APKs establish update preservation; public APK runtime smoke is
checked separately.

## Signing key custody

Ordinary builds and pull-request tests have no signing credentials. Only the
protected release signing step receives a keystore and its passwords. Client-held
file keys, sessions and production deployment credentials never enter this job.
GitHub encrypts secrets at rest; the signing process necessarily accesses the
private key while running.

Create a dedicated production key privately, outside the checkout. Keep the debug
key local for the one-time private transition; it is never a public release key.
These commands are instructions for an interactive private terminal, not work
that has already been completed:

```sh
install -d -m 700 "$HOME/.local/share/psst-signing"
keytool -genkeypair -keystore "$HOME/.local/share/psst-signing/android.p12" \
  -storetype PKCS12 -alias psst-android -keyalg RSA -keysize 4096 \
  -sigalg SHA256withRSA -validity 10950 -dname 'CN=psst.zip Android'
chmod 600 "$HOME/.local/share/psst-signing/android.p12"
keytool -list -v -keystore "$HOME/.local/share/psst-signing/android.p12" \
  -alias psst-android
```

Enter the password only in the terminal prompt. Record the **public certificate's
SHA256** fingerprint. Retain an independently stored encrypted keystore copy and
its password in a password manager. Demonstrate recovery by decrypting the backup
into a private temporary directory, checking the public fingerprint with
`keytool`, then deleting the recovered file. A copy on the same computer alone is
not independent recovery.

R8 mappings use a separate age encryption recipient. Generate its identity
privately with stable `age-keygen`, retain the identity offline, and use only the
public `age1...` recipient in GitHub. The workflow pins age 1.3.2 and verifies its
official archive checksum. Download the encrypted mapping artifact after each
release and retain it before its 30-day Actions expiry. Never upload a plaintext
mapping or the private age identity.

## Protected GitHub configuration

Complete implementation review and actual device checks before enabling
publication. Configure one `android-release` environment with a required reviewer
and deployment branches restricted to `main`; this gives one normal GitHub
approval per publication, without custom evidence-comment strings.

Environment secrets:

| Name                               | Value                                                                  |
| ---------------------------------- | ---------------------------------------------------------------------- |
| `ANDROID_RELEASE_INSPECTION_TOKEN` | Repository-scoped inspection token (Administration read, Actions read) |
| `ANDROID_RELEASE_KEYSTORE_BASE64`  | Base64 representation of the keystore file                             |
| `ANDROID_RELEASE_KEY_ALIAS`        | Production alias (`psst-android`)                                      |
| `ANDROID_RELEASE_STORE_PASSWORD`   | Keystore password                                                      |
| `ANDROID_RELEASE_KEY_PASSWORD`     | Private-key password (same for PKCS12)                                 |

The inspection token is separate from APK signing: a fine-grained token restricted
to this repository with Administration read and Actions read access, owned by an identity that can
inspect its rulesets. The policy check requires an explicitly returned empty bypass
list and enabled immutable releases; omitted API fields never count as protection.
Store it as `ANDROID_RELEASE_INSPECTION_TOKEN` directly in the environment secret, with an expiration and renewal
reminder. It is not used to push code or publish assets.

Environment variables:

| Name                                | Value                                                                      |
| ----------------------------------- | -------------------------------------------------------------------------- |
| `ANDROID_RELEASE_SIGNER_SHA256`     | Public certificate SHA256: 64 hex characters without colons                |
| `ANDROID_RELEASE_PUBLICATION_READY` | `true` only after the actual readiness checks below                        |
| `ANDROID_RELEASE_DEVICE_EVIDENCE`   | Reference to the reviewed private device record; no credentials or payload |

Repository variable `ANDROID_MAPPING_RECIPIENT` holds the **public** age recipient
so the secret-free build can encrypt its mapping. Missing mapping configuration
allows preview, but blocks publication.

Add secrets directly in GitHub or with `gh secret set --env android-release` in
your own terminal. Do not paste passwords, base64 keystores or private identities
into chat, repository files, workflow outputs, logs or Actions artifacts.

Protect `refs/tags/android-v*` using active tag rulesets that forbid updates and
deletions without bypass actors. Protect `main`. Source checks verify current main
protection and exact tag ancestry before running selected-tag tooling. The job
reuses exact-commit source CI only when its trusted definition agrees and its
required steps actually executed successfully; otherwise it runs the relevant
Android/security checks once. It does not rerun backend, web or native iOS jobs.

## Existing debug installation

The operator's phone runs Android 16, without USB access currently. Android 13+
supports a private debug-to-production signing bridge. Source review indicates a
lineage with installed-data and signature-permission continuity can preserve the
app UID, files and Keystore keys, and a
subsequent production-only APK can update it. The disposable Android 16 diagnostic
passed both updates with real Keystore, Room and HPKE/AES state. The operator's
phone has not been verified; this is **not actual-phone evidence**.

`tools/android_signing_bridge.py` prepares a local bridge, with both keystores
outside the checkout. It requires the recorded installed signer and version floor,
verifies the old APK, enables installed-data and permission capabilities, disables
shared UID, rollback and auth capabilities, and keeps outputs private. It refuses GitHub Actions
and public artifact directories. Public release verification rejects rotation
history, preventing publication of this debug-key bridge.

Permission continuity is required to retain ownership of AndroidX's package-scoped
`DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION`. It remains a signature permission;
removing it or lowering its protection would compromise older-system receiver
isolation. Android checks the signing history's `PERMISSION` capability even when
the same package updates its existing permission declaration.
[AOSP permission ownership checks](https://android.googlesource.com/platform/frameworks/base/+/1406600d75c1a30ebdf45312b7fa0f4a4355194b/services/core/java/com/android/server/pm/InstallPackageHelper.java),
[AndroidX declaration](https://android.googlesource.com/platform/prebuilts/sdk/+/5c762fb2f6235bbe5d006d3a26063a8e921354b4/current/androidx/manifests/androidx.core_core/AndroidManifest.xml).

The migrated phone can retain historical debug-key trust for signature permissions.
This trust may remain in Android's stored signing history after a subsequent
production-only APK; a successful update does not prove its revocation. The
operator must review and accept this trust before any real-device transition.
That acceptance and actual device preservation are still pending. Fresh public
installations use a production-only APK without the debug lineage.

Do not uninstall, clear app data or replace a mismatched package to get around a
signing error. If a bridge cannot preserve protected state, stop and implement an
encrypted archive migration on **both Android and iOS** before any reinstall.

Readiness record, kept privately and containing observations rather than secrets:

- [ ] Confirm the actual phone's installed package, version code and signing identity.
- [ ] Review and accept the private bridge's retained debug-key permission trust
      before applying it to the actual phone.
- [x] Verify a disposable emulator transition with actual protected stores before
      applying the production bridge to the phone.
- [ ] Demonstrate production-key recovery from the separate encrypted copy.
- [ ] Perform the real in-place bridge update; confirm retained account/session,
      history, Send decryption keys, Receive-v2 private keys and guest capabilities
      remain usable.
- [ ] Install a subsequent production-only APK with a higher version code and
      repeat the compact preservation check. A bridge at code `2` needs a later
      production-only code above `2`.
- [ ] Verify a fresh optimized production install, API 26 and current Android
      release-specific behavior, including pairing, transfer/decryption, Receive,
      guest access, QR scanning and document/photo sharing using disposable files.

## Publication and routine updates

After readiness, commit the version change and validated source, create the
independent protected Android tag, then dispatch `Android release` from `main`
with that tag. Preview first; select `publish=true` only for the reviewed release.
The build/lint job starts with a 15-minute budget and reports execution timing.
Approval waiting time is separate.

The protected job rechecks source and artifact binding before opening signing
material. It aligns, signs, verifies and packages the exact APK, public metadata,
checksums, source archive, notices and installation instructions. Existing releases
and reused/decreasing version codes are rejected. Anonymous readback verifies every
published asset and the final APK signature. Partial publication failures require
inspection; never overwrite immutable assets or move a tag to retry.

For routine updates the package and production signer remain unchanged; install
the newer APK over the existing app. Do not repeat the debug transition. Keep
release-specific optimized smoke checks compact and reuse relevant passing CI.
The first real same-signer preservation check is a prerequisite; routine CI is not
a claim that an untested migration works.

Google Play and F-Droid are later milestones. Play App Signing uses a separate
upload key, with signer custody chosen before enrollment for intended channel
compatibility. This GitHub workflow does not enroll in either store or upload an
AAB, and no Play publishing credentials are needed yet.

# Android APK releases

Android publication is independent of server containers and VPS deployments.
The `Android release` workflow runs only by manual dispatch on protected `main`,
selecting an existing `android-vX.Y.Z` tag. Its default is an **unsigned preview**;
it does not publish an APK, create a signing key or update an installed app.

The first production-signed release is available as
[Android 0.1.0](https://github.com/endorses/psst.zip/releases/tag/android-v0.1.0),
code `3`, published on October 17. Its public APK is byte-for-byte identical to the
production APK already installed and checked on the operator's phone. Anonymous
checksums, source/notices and the expected production signature were verified.
The current release is [Android 0.1.1](https://github.com/endorses/psst.zip/releases/tag/android-v0.1.1),
code `4`, published on October 17. The unchanged F-Droid recipe rebuilt its public
source and successfully compared against the actual production-signed APK; see
the [completion record below](#verified-upstream-f-droid-comparison).
The [F-Droid packet](../../fdroid/README.md) is ready for submission, which remains
deferred until the operator has a GitLab account and requests submission. Play
remains a later milestone.

## Version and build

`android/release-version.properties` is the single checked-in version source.
Keep the application ID `zip.psst.android` stable and increase `versionCode` above
all public releases and any private bridge installed on the operator's device.
The first published APK is version `0.1.0`, code `3`; this exceeds both the observed
local debug APK's code `1` and the tested private bridge's code `2`. The phone's
installed code `1` and matching debug certificate were verified read-only on
October 10. Production-key and encrypted mapping-backup recovery were confirmed on
October 17. The actual phone then completed the private bridge and production-only
updates with its UID preserved; the operator confirmed the existing account/history
and the same transfer remained usable after both updates.
Container tags retain their independent `vX.Y.Z` namespace.

The current build uses stable AGP 9.4.1, Gradle 9.8.1, SDK API 37 and Build Tools
37.0.0. Kotlin stays at 2.4.20: the operator approved this compatibility exception
because latest stable SKIE 0.10.15 rejects Kotlin 2.4.21. Recheck SKIE compatibility
before upgrading; do not disable it to make Android compile at iOS's expense.
The shared module's iOS native validation passed on the draft PR's macOS
`xcode-27` job at `8c0fe3c`; future shared changes still require native validation.

The dependency refresh uses Compose BOM 2026.09.00, Activity 1.13.0, Lifecycle
2.11.0, Navigation 2.10.2, Room 2.8.5, coroutines 1.11.0, Ktor 3.6.0 and
serialization 1.11.0. Android 17/API 37 adds local-network permission handling;
public server connections do not request that permission. Android 16 and earlier
keep the existing network behavior. iOS already declares its local-network purpose.
See the [bounded lint workaround](../security/lint-bidi-workaround.md) for the
single stalled bidi rule and its replacement source check; other lint checks remain
active. Login/password fields use stable Compose autofill content-type semantics;
iOS already provides matching native credential hints. SQLite JNI permission is
enabled only in the app's host unit-test JVMs. JourneyApps 4.3.0 is its latest
published stable scanner but still uses legacy Camera IDs/parameters; this
documented compatibility boundary remains until its backend can be migrated and
verified. Clipboard writes use `LocalClipboard`, and share extras use `IntentCompat`
to retain API 26 support. The shared event reader uses the stable Ktor line API
while preserving byte limits, line endings and EOF/error behavior on both platforms.
Upstream Gradle plugin deprecation warnings remain visible.

Version evidence: [official Compose BOM](https://dl.google.com/dl/android/maven2/androidx/compose/compose-bom/2026.09.00/compose-bom-2026.09.00.pom),
[AndroidX releases](https://developer.android.com/jetpack/androidx/versions),
[Ktor metadata](https://repo.maven.apache.org/maven2/io/ktor/ktor-client-core/maven-metadata.xml),
[coroutines metadata](https://repo.maven.apache.org/maven2/org/jetbrains/kotlinx/kotlinx-coroutines-core/maven-metadata.xml),
and [serialization release](https://github.com/Kotlin/kotlinx.serialization/releases/tag/v1.11.0).
The inspection permissions follow GitHub's [environment API](https://docs.github.com/en/rest/deployments/environments#get-an-environment).

The development-only Go command `psst-release android` owns source identity,
APK/ELF/signature checks, packaging, CI inspection and timing summaries. It invokes
the official SDK/Gradle/`age` tools at their existing boundaries. Build it once and
reuse it; this adds no Go dependency to ordinary Android/F-Droid source builds.
The workflow compiles trusted dispatcher tooling once per job before opening
credentials and uses the immutable tag's actual app/source/legal bytes. Old-tag
previews alone retain an explicit read-only native-notice compatibility call to
the generator captured in that historical checkout.

From a clean tagged checkout with the SDK and workflow's JDK installed:

```sh
psst_release=$(./tools/release/build.sh)
PSST_ANDROID_UNSIGNED_RELEASE=true ./android/gradlew -p android \
  :app:assembleRelease :app:lintRelease --no-daemon
"$psst_release" android verify-apk --unsigned \
  --apk android/app/build/outputs/apk/release/app-release-unsigned.apk \
  --sdk-tools "$ANDROID_HOME/build-tools/37.0.0" \
  --revision "$(git rev-parse HEAD)" --output /tmp/android-verification.json
```

The verifier checks the actual APK's package, version, SDKs, debuggable flag,
packaged exact source revision, native ELF and ZIP alignment, and notice bytes.
An unsigned preview cannot pass signed-publication validation. Dirty mobile source
has no source revision and cannot be published. Remove your disposable report
when finished.

F-Droid builds may use `-Ppsst.unsignedRelease=true` instead of the environment
variable. Its normal wrapper cleanup preserves source identity only for the six
exact unstaged wrapper deletions; other modified or untracked build inputs still
invalidate it. The signer now preserves existing ZIP alignment after zipalign, so
F-Droid can reproduce signatures without downgrading Build Tools 37. Use Python
3.13 for fdroidserver 2.4.5's signature-copy operation; the packet records the
observed Python 3.14 incompatibility. These are Android packaging changes and do
not alter mobile features, shared protocol or signing identity.

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

Signing uses the reviewed dispatcher helper from a separate sparse checkout;
the app payload, source checks and packaging remain tied to the immutable tag.
Failures report fixed operation/error categories without raw tool output or
credential values. If signing fails before any release is created, correct the
identified input privately and rerun only the failed publication job: its build
artifact ID is retained across attempts, so the successful build is reused.
Workflow code repairs require a new dispatch from updated `main`.

Create a dedicated production key privately, outside the checkout. A debug key
is never a public release key.
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
its password in a password manager. Demonstrate recovery by opening the separate
backup with `keytool` and checking its public fingerprint. Also run `keytool
-certreq -keystore BACKUP_PATH -alias psst-android -file /dev/null`, entering the
password interactively: creating the disposable request proves that the recovered
private key is usable, whereas listing the certificate alone does not. This reads
the backup without changing it or retaining the request. If restoring from an
additional encrypted archive, restore into a private temporary directory and
delete the restored copy afterward. A copy on the same computer alone is not
independent recovery. See the [keytool certificate-request documentation](https://docs.oracle.com/en/java/javase/25/docs/specs/man/keytool.html#commands-for-generating-a-certificate-request).

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
Select custom deployment branches and allow exactly the `main` branch, rather than
all protected branches, a wildcard or a tag named `main`. The publication preflight
inspects both the environment setting and its actual branch-policy list.

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
The existing container `refs/tags/v*` rule does not cover Android tags; preserve it
when adding the independent Android namespace.

## Existing production installation

The operator's historical debug-to-production transition completed in October
2026, followed by a production-only higher-code update. The existing UID,
account/history and the same transfer remained usable. That bounded observation
is retained as history; it does not establish physical coverage of every store.

Future debug-to-production signing bridges are retired. No tool generates private
rotation lineage, and public APK verification continues to reject any rotation
lineage or signer other than the approved production certificate. Subsequent
updates require the same package and production signer with a higher version code.
Do not uninstall or clear data to bypass a signer mismatch: private encryption
keys and history can be destroyed. A differently signed old installation needs a
separately designed encrypted-data migration before any reinstall.

The [disposable update fixture](../testing/android-release-updates.md) checks
ordinary same-signer optimized updates using one ephemeral key and real protected
storage. It never uses the operator's key, phone, accounts or production server.
The [public APK smoke](../testing/android-release-smoke.md) separately covers
fresh optimized startup, pairing, QR, transfer and sharing behavior.

For read-only inspection of a real installed APK, use Android's official
[Wireless debugging](https://developer.android.com/tools/adb#connect-to-a-device-over-wi-fi)
and [apksigner](https://developer.android.com/tools/apksigner) instructions. Select
the intended device/profile explicitly, compare its public certificate SHA256,
package and version code to the approved release, remove any temporary pulled APK,
and disable Wireless debugging afterward. Inspection alone does not prove
installed-data preservation.

## Publication and routine updates

After readiness, commit the version change and validated source, create the
independent protected Android tag, then dispatch `Android release` from `main`
with that tag. For initial setup, `publish=false` provides an unsigned dry run.
For routine releases, dispatch once with `publish=true`: the build creates the
preview before the protected signing job requests approval. Review that preview,
then approve the job; the same unsigned artifact proceeds to signing without a
second build. There is one configured environment approval and no custom review
comment. Historical signing migration is not a supported routine release operation.
The build/lint job starts with a 15-minute budget and reports execution timing.
Approval waiting time is separate.

The protected job rechecks source and artifact binding before opening signing
material. It aligns, signs, verifies and packages the exact APK, public metadata,
checksums, source archive, notices and installation instructions. Existing releases
and reused/decreasing version codes are rejected. Anonymous readback verifies every
published asset and the final APK signature. Partial publication failures require
inspection; never overwrite immutable assets or move a tag to retry.

For routine updates the package and production signer remain unchanged; install
the newer APK over the existing app. Do not introduce signing rotation or debug-key lineage. Keep
release-specific optimized smoke checks compact and reuse relevant passing CI.
The first real same-signer preservation check is a prerequisite; routine CI is not
a claim that an untested migration works.
The [compact public-APK smoke](../testing/android-release-smoke.md) describes the
disposable optimized-app checks separately from the private signing diagnostic.

Google Play remains a later milestone. Play App Signing uses a separate
upload key, with signer custody chosen before enrollment for intended channel
compatibility. This GitHub workflow does not enroll in either store or upload an
AAB, and no Play publishing credentials are needed yet.

## Verified upstream F-Droid comparison

Android `0.1.1` / code `4` is an immutable GitHub release (ID `415882536`) at source
`351cb91bbe2126577524e60dcb5377b07e3ed5ff`. Both
[PR #41](https://github.com/endorses/psst.zip/pull/41) and the
[exact merged-source CI](https://github.com/endorses/psst.zip/actions/runs/39044199372)
passed every job. The
[release workflow](https://github.com/endorses/psst.zip/actions/runs/39044606117)
reused its executed Android/security checks, built and linted the optimized APK,
then signed/published after the operator's environment approval. Anonymous
readback verified all six public assets and the final APK signature. The encrypted
R8 mapping was retained on the existing external drive and checksum-verified.

| Artifact                              | SHA256                                                             |
| ------------------------------------- | ------------------------------------------------------------------ |
| Unsigned APK, 3,803,375 bytes         | `fb02176dedaa680f35e449e2bf937cd44eba692686fbae8e8c52f5b1ce8e9993` |
| Published signed APK, 3,853,522 bytes | `67fa990b5279058a01bcd98bd1c07f3ae0ed645471f3206a32fa6ff4ea321100` |
| Production certificate                | `6f10e624c6f577544bd01928b33d834ad5434e54363313617e30fe2dfa835b15` |

Official fdroidserver **2.4.5**, Python **3.13.12**, JDK **25.0.3**, Gradle
**9.8.1** and the recipe's SDK components were used in a disposable checkout.
The recipe was byte-identical to the committed file: `Binaries` and
`AllowedAPKSigningKeys` remained enabled, with no source patch or scanner exemption.
The installed Gradle distribution was selected through local F-Droid configuration;
the earlier independent JDK 21 image build remains separate compatibility evidence.

```sh
fdroid checkupdates --allow-dirty zip.psst.android
fdroid build --test --scan-binary --refresh-scanner --stop zip.psst.android:4
```

Update detection selected the exact published tag. The complete recipe command
took **72 seconds**, including SDK prebuild, clean assembly, refreshed source
scan, source archive, binary scans, reference download and signature-copy
verification. Its unsigned output also matched the GitHub preview byte-for-byte.
The final official-tool results were:

```text
compared built binary to supplied reference binary successfully
supplied reference binary has allowed signer 6f10e624c6f577544bd01928b33d834ad5434e54363313617e30fe2dfa835b15
1 build succeeded
```

This completion record supersedes the preparation packet's pending upstream-release
status and earlier candidate hashes. It is a successful local comparison using
official tools, not F-Droid's own hosted build, review or admission. Local
verification used no private signing key; protected CI performed production signing.
No new phone installation or device-flow check is claimed for this packaging-only
update. GitLab submission and optional disposable-data screenshots remain pending.

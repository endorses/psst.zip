# Android release install and update fixture

This fixture checks what host JVM tests cannot establish: an optimized same-signer
APK update preserves the Android UID, non-exportable Keystore keys, sessions,
Room Send/Receive history, InboxKeyStore and GuestDownloadStore. It opens retained
AES ciphertext, unwraps a Receive-v2 key with actual Tink HPKE, and opens the
submission ciphertext. Re-pairing alone does not prove these properties.

Future debug-to-production bridges and private signing-lineage generation are
retired. Both fixture APKs are optimized release variants signed by one disposable
key. Public release verification still excludes rotation lineage and accepts only
the approved production signer.

The explicit fixture build compiles one diagnostic into the target APK so R8 sees
its store/crypto call graph. A framework-only Java instrumentation runner invokes
one kept signature, with no shared AndroidX/Kotlin/JUnit instrumentation dependency.
Public builds omit the diagnostic source and keep rule. Fixture metadata includes
`deviceFixture=true`; ordinary publication rejects it even at a public version.

## Requirements

Use a clean committed checkout, the existing workflow JDK/SDK, Build Tools 37.0.0,
platform-tools and a disposable Android API 26+ emulator. This local maintenance
command is forbidden in GitHub Actions and never selects a physical phone. Create
a **new AVD** named `psst-release-fixture-` plus a unique suffix, using an installed
image and a private temporary directory. Boot without a saved snapshot. The tool
requires qemu, supported emulator hardware and no existing target/instrumentation
package. It refuses to reset an everyday AVD. The caller stops/removes its own AVD
and temporary APK directory afterward.

## Build two optimized inputs

Set `fixture_dir` to a private temporary directory outside the checkout. Copy both
the target and matching instrumentation APK before the next build overwrites them.
Version codes 1 and 2 below are disposable overrides, not release metadata.

```sh
export PSST_ANDROID_UNSIGNED_RELEASE=true
export PSST_ANDROID_DEVICE_TEST_BUILD_TYPE=release
export PSST_ANDROID_DEVICE_TEST_VERSION_CODE=1
./android/gradlew -p android :app:assembleRelease :app:assembleReleaseAndroidTest --no-daemon
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$fixture_dir/seed-unsigned.apk"
cp android/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk "$fixture_dir/seed-test.apk"

export PSST_ANDROID_DEVICE_TEST_VERSION_CODE=2
./android/gradlew -p android :app:assembleRelease :app:assembleReleaseAndroidTest --no-daemon
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$fixture_dir/next-unsigned.apk"
cp android/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk "$fixture_dir/next-test.apk"
unset PSST_ANDROID_DEVICE_TEST_BUILD_TYPE PSST_ANDROID_DEVICE_TEST_VERSION_CODE
```

## Run the same-signer preservation check

Set `fixture_serial` to the new AVD's explicit `emulator-NNNN` serial. No operator
keystore, password, account, link, private mapping or production server is used.

```sh
psst_release=$(./tools/release/build.sh)
"$psst_release" android check-update \
  --seed-unsigned "$fixture_dir/seed-unsigned.apk" \
  --next-unsigned "$fixture_dir/next-unsigned.apk" \
  --seed-test-apk "$fixture_dir/seed-test.apk" \
  --next-test-apk "$fixture_dir/next-test.apk" \
  --sdk-tools "$ANDROID_HOME/build-tools/37.0.0" \
  --adb "$ANDROID_HOME/platform-tools/adb" \
  --keytool "$(command -v keytool)" \
  --serial "$fixture_serial" \
  --revision "$(git rev-parse HEAD)" \
  --output "$fixture_dir/update-preservation.json"
```

The runner creates one short-lived signing key and random environment-only
password, aligns/signs/verifies both targets, and signs both instrumentation APKs.
It seeds actual protected state in the first installed release, then installs the
second target **with `adb install -r`**, updates the test APK in place and verifies
the retained state. Neither package is uninstalled or downgraded. Raw
instrumentation must report both `OK (1 test)` and final
`INSTRUMENTATION_CODE: -1`; crashes, cancellation and incomplete output fail.

The schema-2 JSON report exists only after both fixture executions succeed. It
records same-signer preservation and explicitly does not claim verification of the
operator's real installation or network/pairing/QR/sharing flows. A failed run
leaves the disposable AVD for inspection, without inventing successful evidence;
retry on a fresh owned AVD. Ephemeral keys/APKs are removed on success or failure.

This Android-only package-signing check changes no shared encryption protocol or
iOS behavior. The separate optimized public-APK smoke remains required for
release-specific startup, pairing, QR, sharing and transfer checks.

## Release build timing

The release workflow attaches `tools/android_build_timings.gradle` to its existing
Gradle invocation. The script uses the supported
[Gradle build service task-completion API](https://docs.gradle.org/current/userguide/build_services.html#task_execution_events)
and writes public task names, measured milliseconds and success flags only. It
adds no tasks, sleeps or benchmark builds. R8, lint, source tests and compilation
are reported separately; task durations may overlap under parallel execution, so
their sum is not build wall time. Setup/cache transfer, signing, artifact checks
and public download verification retain their separate workflow timings.

`PSST_ANDROID_TASK_TIMINGS` selects a fresh JSONL output path. Initialize it once
before invoking Gradle, and pass `--init-script` with the absolute script path.
`"$psst_release" android report-timings --input task-timings.jsonl
--summary summary.md` produces the grouped task report. An empty file represents
missing timing evidence, not a zero-second successful build.

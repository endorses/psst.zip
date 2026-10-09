# Android release install and update fixture

This fixture tests one failure that JVM CI cannot establish: actual APK signing
changes and optimized app updates must preserve the Android UID, non-exportable
Keystore keys and their protected state. It uses a disposable emulator, disposable
signing keys and a synthetic account; it never connects to a real server.

It checks the installed app's actual SessionStorage, Room Send/Receive history,
InboxKeyStore, GuestDownloadStore and cryptographic implementations. After both
updates it opens retained AES ciphertext, unwraps a Receive-v2 submission key using
Tink HPKE, and opens that submission's ciphertext. A successful re-pairing alone
would not prove any of these properties.

The fixture does not replace the operator's real installation transition or the
separate optimized-app pairing, QR, sharing and transfer smoke. Its report states
those limits explicitly. Android-only package signing evaluation changes no shared
archive format or iOS app behavior.

## Requirements

Use a clean committed checkout of the exact source under evaluation. This is a
local verification tool, deliberately forbidden in GitHub Actions. Use the
stable SDK/JDK selected by the tagged Android release workflow, with `java` and
`keytool` on PATH, SDK Build Tools 37.0.0, platform-tools and an Android 13+ emulator
image. Emulator acceleration must be available for reasonable runtime.

Create a **new disposable AVD**, named `psst-release-fixture-` followed by a unique
suffix, using an already installed image. Keep its disk files in a temporary
private directory and boot it without loading a snapshot. Do not rename or reuse
an everyday AVD: the runner refuses an existing `zip.psst.android` or instrumentation
installation, and it does not reset one. The caller owns stopping/removing this
specific temporary AVD after verification. Never use `adb uninstall` or `pm clear`
on the operator's target app to work around a signing error.

All six build inputs must be copied out before the next build can overwrite them.
Create a temporary directory outside the checkout, accessible only to the current
user. Set `fixture_dir` to that directory. These overrides are for disposable
verification only; normal publication rejects a private version-code override.

## Build the inputs

The debug seed is code 1, private bridge code 2 and subsequent production-only
update code 3. These are disposable test versions, not published version metadata.
Use matching instrumentation APKs for each target; the release test build must
apply the corresponding R8 mapping. Do not use a debug test build as evidence that
a minified target retained its runtime classes.

```sh
export PSST_ANDROID_UNSIGNED_RELEASE=true
export PSST_ANDROID_DEVICE_TEST_BUILD_TYPE=debug
export PSST_ANDROID_DEVICE_TEST_VERSION_CODE=1
./android/gradlew -p android :app:assembleDebug :app:assembleDebugAndroidTest --no-daemon
cp android/app/build/outputs/apk/debug/app-debug.apk "$fixture_dir/seed.apk"
cp android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk "$fixture_dir/seed-test.apk"

export PSST_ANDROID_DEVICE_TEST_BUILD_TYPE=release
export PSST_ANDROID_DEVICE_TEST_VERSION_CODE=2
./android/gradlew -p android :app:assembleRelease :app:assembleReleaseAndroidTest --no-daemon
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$fixture_dir/bridge-unsigned.apk"
cp android/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk "$fixture_dir/bridge-test.apk"

export PSST_ANDROID_DEVICE_TEST_VERSION_CODE=3
./android/gradlew -p android :app:assembleRelease :app:assembleReleaseAndroidTest --no-daemon
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$fixture_dir/next-unsigned.apk"
cp android/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk "$fixture_dir/next-test.apk"
unset PSST_ANDROID_DEVICE_TEST_BUILD_TYPE PSST_ANDROID_DEVICE_TEST_VERSION_CODE
```

Release assembly and lint for public delivery remain separate required checks.
The fixture rebuilds only the variants necessary for the signing transition and
increasing-code update, rather than repeating the complete test suite. Build paths
above are AGP defaults; inspect the actual output if a selected toolchain changes
them.

## Run the update check

Set `fixture_serial` to the newly created emulator's explicit `emulator-NNNN`
identifier. The tool checks qemu, supported emulator hardware, AVD ownership,
API level and absence of an existing target before creating any disposable key.

```sh
python3 tools/check_android_update.py \
  --seed-apk "$fixture_dir/seed.apk" \
  --bridge-unsigned "$fixture_dir/bridge-unsigned.apk" \
  --next-unsigned "$fixture_dir/next-unsigned.apk" \
  --seed-test-apk "$fixture_dir/seed-test.apk" \
  --bridge-test-apk "$fixture_dir/bridge-test.apk" \
  --next-test-apk "$fixture_dir/next-test.apk" \
  --sdk-tools "$ANDROID_HOME/build-tools/37.0.0" \
  --adb "$ANDROID_HOME/platform-tools/adb" \
  --keytool "$(command -v keytool)" \
  --serial "$fixture_serial" \
  --revision "$(git rev-parse HEAD)" \
  --output "$fixture_dir/update-preservation.json"
```

The runner creates two short-lived signing keys inside its own temporary private
directory and passes random passwords through environment variables. It signs the
seed and instrumentation packages, prepares an Android 13+ bridge with only
installed-data trust, verifies its certificate order/capabilities, and signs the
later production-only APK without that lineage. Files and secrets are discarded
on success or failure; no private key is printed or retained in the report.

It executes `ReleaseUpdateDeviceTest#preservesClientState` three times: seed,
after bridge installation, and after the production-only higher-code installation.
The target is updated with `adb install -r`; it is never uninstalled or downgraded.
Only the disposable instrumentation package is replaced to match its target's
signing certificate and optimized code mapping.

A JSON report is written only after all three fixture runs succeed. A failed run
leaves its disposable emulator for inspection, without manufacturing successful
evidence. Stop and remove that owned AVD, then use a fresh one for a retry. Retain
the small non-secret report if useful and delete the caller's temporary APK/AVD
files afterward.

API 26 fresh optimized-app verification requires its own emulator/device. This
Android 13+ signing-rotation fixture does not claim older-system transition
support, the real phone's certificate, or end-to-end network functionality.

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
`python3 tools/android_release.py report-timings --input task-timings.jsonl
--summary summary.md` produces the grouped task report. An empty file represents
missing timing evidence, not a zero-second successful build.

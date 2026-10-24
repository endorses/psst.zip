# Building and contributing

Use the toolchain versions declared in the module/build files and
[CI workflow](../.github/workflows/ci.yml). Install the repository hooks before
committing. Run checks that exercise the code you changed; use disposable data
for transfer tests.

## Contents

- [Architecture](#architecture)
- [Manual server builds](#manual-server-builds)
- [CLI](#cli)
- [Native builds and verification](#native-builds-and-verification)
- [Translations and artwork](#translations-and-artwork)

## Architecture

```
web/         SvelteKit SPA (static build, served by Caddy)
backend/     Go HTTP server (REST API, tus uploads, SQLite, file storage)
client/      Reusable Go client and psst CLI (encrypted transfers, JSON, terminal QR)
shared/      Kotlin Multiplatform module (crypto, API client, models)
android/     Android app (Jetpack Compose)
ios/         iOS app (SwiftUI + share extension)
```

Install the [repository hooks](../hooks/README.md) before contributing. They check
staged changes for credential leaks, sensitive files, and formatting, and scan
Git history before pushes. CI repeats the security checks.

## Manual server builds

### Backend

```bash
cd backend
go build -o psst-server ./cmd/server
# Configure PUBLIC_URL and first-start ADMIN_USERNAME/ADMIN_PASSWORD first.
./psst-server
```

### Web app

```bash
cd web
npm ci
npm run build
# Serve web/build/ at the same origin as the API (see Caddyfile)
```

The public server must route `/api/*` to the Go backend and serve the web build for
other paths, falling back to `index.html` for `/d/*` and `/u/*`. The included
Docker Compose stack and Caddyfile provide this routing. Running only the Go
server does not serve download pages.

## CLI

From the repository root or `client/`, `make build` builds the CLI and
`make install` installs it in `~/.local/bin`. Use `make build-release` or
`make install-release` for a stripped executable, and `make help` for overrides.
Source builds need Go and Node/npm to embed the LAN browser interface; installed
binaries need neither Node nor Docker. See the [CLI guide](cli.md#install-and-build)
for version metadata, packaging and other platforms.

## Native builds and verification

For signed APK delivery, signing-key custody and safe in-place updates, see
[Android APK releases](releases/android.md).

The [Gradle wrapper](../android/gradle/wrapper/gradle-wrapper.properties),
[Android versions](../android/gradle/libs.versions.toml) and
[shared versions](../shared/gradle/libs.versions.toml) declare the native tools and
dependencies. CI runs Gradle on Java 27 while the app targets Java 17 bytecode.
Set `JAVA_HOME` to your JDK and `ANDROID_HOME` to the Android SDK, install the SDK
packages selected in CI, then run from the repository root:

```bash
./android/gradlew -p android :app:assembleDebug :app:testDebugUnitTest :shared:testAndroidHostTest --no-daemon
```

Set the app's server URL to the [configured public HTTP or HTTPS origin](self-hosting.md#build-and-start-with-docker-compose).
Both debug and release apps support an operator's HTTP server; HTTPS uses normal
system certificate validation. Browser encryption uses Web Crypto when available
and a compatible AES-GCM implementation on HTTP LAN pages, with the browser's
cryptographically secure random generator in both cases. After rebuilding,
reinstall `android/app/build/outputs/apk/debug/app-debug.apk` to apply changes.

Select a Java 27 JDK for local validation and use the same JDK in Android
Studio's Gradle settings. Set `ANDROID_HOME` to your own Android SDK installation.
Check [Gradle Java compatibility](https://docs.gradle.org/current/userguide/compatibility.html)
and [Kotlin plugin compatibility](https://kotlinlang.org/docs/gradle-configure-project.html).
The [Android release guide](releases/android.md#version-and-build) records the
current compatibility exceptions. Run native build and test checks when changing
the selected combination. The Android app uses AGP's built-in Kotlin support. The shared
module uses the Android KMP library plugin and explicitly enables host tests
in `src/androidHostTest`.

For iOS project generation and signing, see [ios/README.md](../ios/README.md).
Native app, share-extension and XCTest validation requires macOS with Xcode;
CI runs these checks separately from Android. Portable checks on Linux do not
replace an Xcode build or device interoperability testing.

Web tests require Node 22.18 or newer and Go on `PATH`:

```bash
cd web
npm ci
npm run check
npm test
npx playwright install chromium
npm run test:browser
npm run build
```

The tests start temporary backend instances, exercise encrypted transfers and
slot uploads, and verify single-file and ZIP downloads in Chromium. Browser tests
use local ports 8080 and 4173. CI also assembles Android and runs shared tests.

To run the same browser tests against an isolated Docker deployment, set
`PSST_TEST_BASE_URL` to its origin and run
`npx playwright test --config playwright.deployment.config.ts` from `web/`.
Optional `PSST_TEST_USERNAME` and `PSST_TEST_PASSWORD` select the test account;
`PSST_EXPECT_INSECURE_CONTEXT=1` enables the LAN HTTP assertion. This suite creates
and downloads test transfers; use a disposable deployment.

## Translations and artwork

See [translation contributions](localization/contributing.md) for English/German
resources, terminology and checks across the web, Android, iOS and share extension.
[Brand assets](../assets/brand/README.md) contains editable vector masters,
font licenses and reproducible exports.

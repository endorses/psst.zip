# Psst

Self-hosted, end-to-end encrypted file transfer. Share files between devices without trusting the server.

## How it works

1. The sender picks files and the client generates a random AES-256-GCM key.
2. Files are encrypted client-side and uploaded to the server via resumable tus uploads.
3. The server stores only encrypted blobs -- it never sees plaintext data.
4. The sender gets a link like `https://your-server/d/{id}#key` where the encryption key lives in the URL fragment (never sent to the server).
5. The recipient opens the link, and the web app (or mobile app) decrypts everything in the browser/on-device.

Drop slots work in reverse: the receiver creates a slot, shares its QR/link, and uploaders encrypt into it.
The mobile receive screen displays the full upload link with **Copy Link** and
**Share Link** actions, so the sender can also receive it through a message or email.

## Architecture

```
web/         SvelteKit SPA (static build, served by Caddy)
backend/     Go HTTP server (REST API, tus uploads, SQLite, file storage)
shared/      Kotlin Multiplatform module (crypto, API client, models)
android/     Android app (Jetpack Compose)
ios/         iOS app (SwiftUI + share extension)
```

## Quick start (Docker Compose)

```bash
# 1. Configure the hostname recipients will use in a .env file
echo 'PSST_DOMAIN=psst.example.com' > .env

# 2. Build and start the complete stack (includes the web app)
docker compose up -d --build
```

Caddy serves the website and API together at `https://psst.example.com`.
Enter that same address in the mobile app's server settings, without `/api/v1`.
Generated links then open the download page directly; recipients do not need to
change addresses or ports. Port 8080 belongs to the internal API and is not the
address to give to mobile clients or recipients.

The hostname is chosen by each operator; no hostname is built into the mobile
apps. Server setup verifies the API and both share pages before saving the URL.
An API-only address, failed connection, or HTTP LAN address is rejected with a
setup error. Deploy the updated backend and web app together before configuring
an updated mobile client. Existing saved settings are retained until changed.

For automatic public certificates, point the hostname's DNS records at the server
and make ports 80 and 443 reachable. See [Caddy's HTTPS setup](https://caddyserver.com/docs/quick-starts/https).
The default `localhost` hostname is only for testing on the server itself. LAN-only
HTTPS requires a hostname reachable by every device and a certificate trusted by
both browsers and native clients; an untrusted self-signed certificate is not a
complete deployment.

## Manual build

### Backend

```bash
cd backend
go build -o psst-server ./cmd/server
./psst-server
```

### Web app

```bash
cd web
npm ci
npm run build
# Serve web/build/ behind the same HTTPS origin as the API (see Caddyfile)
```

The public server must route `/api/*` to the Go backend and serve the web build for
other paths, falling back to `index.html` for `/d/*` and `/u/*`. The included
Docker Compose stack and Caddyfile provide this routing. Running only the Go
server does not serve download pages.

## Native builds and verification

Android uses Gradle 9.1.0, Kotlin 2.3.21, AGP 8.13.2, KSP 2.3.12,
SKIE 0.10.12, and Room 2.8.4. Gradle can run on Java 25 while the app
continues targeting Java 17 bytecode. Set `JAVA_HOME` to your JDK and
`ANDROID_HOME` to the Android SDK, then run from the repository root:

```bash
./android/gradlew -p android :app:assembleDebug :shared:testDebugUnitTest --no-daemon
```

Set the app's server URL to the shared HTTPS address described above. Debug APKs
also allow plain HTTP for isolated API testing, but using the backend's port 8080
directly produces links without a web download page. Browser decryption requires
HTTPS or localhost, so a plain HTTP LAN address is not an end-to-end sharing setup.
Release builds retain Android's default HTTPS requirement. After rebuilding,
reinstall `android/app/build/outputs/apk/debug/app-debug.apk` to apply changes.

On this Linux development machine, the JDK is `/opt/android-studio/jbr` and the
SDK is `/home/grischa/Android/Sdk`. These paths are local configuration, not
required installation locations. Android Studio's Gradle JDK setting should use
the same JDK. See [Gradle Java 25 support](https://docs.gradle.org/9.1.0/release-notes.html)
and [Kotlin plugin compatibility](https://kotlinlang.org/docs/gradle-configure-project.html).

For iOS project generation and signing, see [ios/README.md](ios/README.md).
iOS compilation and device interoperability remain unverified on this Linux host.

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
This suite creates and downloads test transfers; use a disposable deployment.

## Current limits and protocol

Clients currently encrypt whole files in memory. Web and native main apps limit
files to 25 MiB; the iOS share extension limits files to 10 MiB and processes them
one at a time. Web ZIP downloads are limited to 25 MiB total. The backend's larger
`MAX_FILE_SIZE` setting does not imply the clients support files that large.
Streaming encryption and 1 GiB transfers remain unfinished.

Encrypted blobs use `12-byte nonce || AES-GCM ciphertext || 16-byte tag`.
Encrypted manifests use `{ "files": [{ "name", "size", "mime_type", "blob_id" }] }`.
Transfer and slot creation return `id`. Uploaders create a child transfer with
`POST /api/v1/slots/{id}/transfers`, upload its files and manifest through the
transfer endpoints, then complete it. Receivers download completed child
transfers; `transfer_complete` SSE notifications include `transfer_id`.

`max_downloads` limits GET attempts for each file independently, so every file in
a multi-file transfer remains retrievable. Interrupted downloads consume an
attempt. Transfer `download_count` counts complete sets (the minimum file count),
and cleanup removes the transfer once every file exhausts its allowance. Expired
resources are rejected immediately and their stored data is removed periodically.
History deletion currently removes local records only; server-side manual deletion
is not implemented.

## Configuration

All backend settings are controlled via environment variables.

| Variable           | Default             | Description                                   |
| ------------------ | ------------------- | --------------------------------------------- |
| `LISTEN_ADDR`      | `:8080`             | Address the backend listens on                |
| `STORAGE_PATH`     | `./data/files`      | Directory for encrypted file blobs            |
| `DB_PATH`          | `./data/psst.db`    | Path to the SQLite database                   |
| `MAX_FILE_SIZE`    | `5368709120` (5 GB) | Maximum upload size in bytes                  |
| `DEFAULT_EXPIRY`   | `24h`               | Transfer expiry duration (Go duration syntax) |
| `CLEANUP_INTERVAL` | `5m`                | How often the cleanup worker runs             |

Docker Compose also accepts:

| Variable      | Default     | Description                                              |
| ------------- | ----------- | -------------------------------------------------------- |
| `PSST_DOMAIN` | `localhost` | Domain for Caddy (enables auto-HTTPS for public domains) |
| `HTTP_PORT`   | `80`        | Host port mapped to Caddy HTTP                           |
| `HTTPS_PORT`  | `443`       | Host port mapped to Caddy HTTPS                          |

## Security model

- **End-to-end encryption**: AES-256-GCM. Keys are generated client-side and shared with recipients in link fragments.
- **Key in URL fragment**: The `#key` portion of URLs is not sent to the server by browsers (per RFC 3986). The server only sees the transfer ID.
- **Zero-knowledge server**: The backend stores and serves encrypted blobs. It cannot decrypt file contents or metadata.
- **Resumable uploads**: The tus protocol supports retrying interrupted uploads within the client size limits. All data is encrypted before upload.
- **Automatic expiry**: Transfers are deleted after a configurable duration or download count.

## License

GNU Affero General Public License, version 3 only (AGPL-3.0-only). See [LICENSE](LICENSE).

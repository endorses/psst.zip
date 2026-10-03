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
# Build and start the complete stack (includes the web app)
docker compose up -d --build
```

Caddy serves the website and API together at `http://<server-ip>` on port 80.
Open that address from another device on the same LAN and enter it in the mobile
app's server settings, without `/api/v1`. No domain, certificate installation, or
custom APK is required. Allow the app through any phone firewall and allow local
network access if iOS prompts for it. Test/save the server in the main iOS app
before using the share extension, so it can request local network permission.
Generated links then open the download page directly; recipients do not need to
change addresses or ports. Port 8080 belongs to the internal API and is not the
address to give to mobile clients or recipients.

Each operator chooses an IP address or hostname; nothing is hardcoded in the
mobile apps. Server setup accepts HTTP and HTTPS and verifies the API and both
share pages before saving the URL. API-only addresses and failed connections
produce a setup error. Deploy the updated backend and web app together before
configuring an updated mobile client. Existing saved settings are retained until
changed. LAN addresses work for recipients on that LAN; remote recipients need
an address they can reach.

For HTTPS, set `PSST_DOMAIN=psst.example.com` in `.env` and restart with
`docker compose up -d --build`. Enter `https://psst.example.com` in clients.

For automatic public certificates, point the hostname's DNS records at the server
and make ports 80 and 443 reachable. See [Caddy's HTTPS setup](https://caddyserver.com/docs/quick-starts/https).
HTTPS certificate verification remains enabled in all clients.

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
# Serve web/build/ at the same origin as the API (see Caddyfile)
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

Set the app's server URL to the shared HTTP or HTTPS address described above.
Both debug and release apps support an operator's HTTP server; HTTPS uses normal
system certificate validation. Browser encryption uses Web Crypto when available
and a compatible AES-GCM implementation on HTTP LAN pages, with the browser's
cryptographically secure random generator in both cases. After rebuilding,
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
attempt. Transfer `download_count` counts complete sets of file requests (the
minimum per-file request count), not completed downloads. Once every file exhausts
its allowance, cleanup removes the encrypted file blobs; transfer metadata and the
encrypted manifest remain until the original expiry so download confirmations can
still be recorded and read.
Expired resources are rejected immediately and their stored data is removed periodically.
Deleting Android history revokes the transfer on its original server before
removing the local record. Deleting a receive entry revokes its upload slot and
all child transfers. A failed request keeps the entry so deletion can be retried.
Files already saved by a recipient are unaffected, and an already-open download
may finish; subsequent requests through the shared link are rejected.

New transfer, slot, and slot-upload creation responses include a private
`delete_token`. Store it separately from the encryption key and never include it
in a shared link. Revocation uses `DELETE /api/v1/transfers/{id}` or
`DELETE /api/v1/slots/{id}` with `Authorization: Bearer <delete_token>`. Successful
deletion returns `204`; `404` means the resource is already gone. Only a hash of
the deletion token is stored on the server, and GET responses never expose it.
If payload removal fails, revocation remains in force and cleanup retries it.

Transfers created before deletion tokens were introduced cannot establish owner
identity. Operators may set `ALLOW_LEGACY_DELETION=true` to allow deletion of
those older resources by ID; anyone with an old link can then revoke it. This
compatibility option defaults to `false` and never bypasses token checks on new
resources. Older client versions that discard creation tokens cannot later revoke
their protected transfers. iOS history removal currently remains local.

After downloading and successfully decrypting every file, a receiver sends
`POST /api/v1/transfers/{id}/downloaded` with an empty body. This idempotent endpoint
returns `204`; transfer metadata exposes the first confirmation as `downloaded_at`
(otherwise `null`). Partial downloads, manifest reads, and completed HTTP responses
alone do not set it. Confirmations report client success; they do not independently
prove the recipient saved or opened the files. No encryption key or plaintext is
included in the confirmation.

Android sent history distinguishes **Ready to download**, **Download started**
(all files requested), and **Downloaded** (receiver confirmation). Native receivers
confirm after saving their decrypted files. The browser confirms after handing the
decrypted files or ZIP to its download mechanism; it cannot verify the subsequent
filesystem save. Failed confirmations can be retried without downloading again:
the web page provides a retry button, Android retries from saved history, and iOS
keeps a pending queue and retries when the app becomes active. Older clients do not
send confirmations, so their downloads may remain **Download started**.

## Configuration

All backend settings are controlled via environment variables.

| Variable                | Default             | Description                                                      |
| ----------------------- | ------------------- | ---------------------------------------------------------------- |
| `LISTEN_ADDR`           | `:8080`             | Address the backend listens on                                   |
| `STORAGE_PATH`          | `./data/files`      | Directory for encrypted file blobs                               |
| `DB_PATH`               | `./data/psst.db`    | Path to the SQLite database                                      |
| `MAX_FILE_SIZE`         | `5368709120` (5 GB) | Maximum upload size in bytes                                     |
| `DEFAULT_EXPIRY`        | `24h`               | Transfer expiry duration (Go duration syntax)                    |
| `CLEANUP_INTERVAL`      | `5m`                | How often the cleanup worker runs                                |
| `ALLOW_LEGACY_DELETION` | `false`             | Allow deletion by ID for older resources without deletion tokens |

Docker Compose also accepts:

| Variable      | Default | Description                                                                   |
| ------------- | ------- | ----------------------------------------------------------------------------- |
| `PSST_DOMAIN` | `:80`   | Caddy site address; default serves LAN HTTP, a domain enables automatic HTTPS |
| `HTTP_PORT`   | `80`    | Host port mapped to Caddy HTTP                                                |
| `HTTPS_PORT`  | `443`   | Host port mapped to Caddy HTTPS                                               |

## Security model

HTTP LAN mode encrypts file contents but does not authenticate delivery of the
web app itself. An active network attacker could replace its JavaScript and steal
keys or plaintext. Use HTTP only on a trusted network; use trusted HTTPS for
untrusted networks or Internet-facing deployments. Encryption also assumes the
client application itself is trustworthy.

- **End-to-end encryption**: AES-256-GCM. Keys are generated client-side and shared with recipients in link fragments.
- **Key in URL fragment**: The `#key` portion of URLs is not sent to the server by browsers (per RFC 3986). The server only sees the transfer ID.
- **Zero-knowledge server**: The backend stores and serves encrypted blobs. It cannot decrypt file contents or metadata.
- **Resumable uploads**: The tus protocol supports retrying interrupted uploads within the client size limits. All data is encrypted before upload.
- **Automatic expiry**: Transfers expire after a configurable duration; exhausted download quotas remove encrypted payloads while retaining status metadata until expiry.

## License

GNU Affero General Public License, version 3 only (AGPL-3.0-only). See [LICENSE](LICENSE).

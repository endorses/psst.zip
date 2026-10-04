# psst.zip

Self-hosted, end-to-end encrypted file transfer. Share files between devices without trusting the server.

## How it works

1. The sender picks files and the client generates a random AES-256-GCM key.
2. Files are encrypted client-side and uploaded to the server via resumable tus uploads.
3. The server stores only encrypted blobs -- it never sees plaintext data.
4. The sender gets a link like `https://your-server/d/{id}#key` where the encryption key lives in the URL fragment (never sent to the server).
5. The recipient opens the link, and the web app (or mobile app) decrypts everything in the browser/on-device.

Receive links work in reverse: choose **Receive**, create a link, and share its QR
or use **Copy link** / **Share**. Anyone with that link can send files without an
account, including people elsewhere who can reach your server. Return to the
original link in History to see arrivals and choose **Save files**.

Creating transfers and receive links requires an account. Administrators create
accounts; there is no public registration. Generated download links and uploads
into a receive link remain accessible without signing in.

## Everyday use

**Send**, **Receive**, and **History** are the main account destinations. Account,
connected-device, and administrator controls live under Settings. A receive item
in History opens its existing link; retrying a save keeps that link and preserves
files already saved. Owned-link history stays scoped to the current account and server.

The mobile apps also offer **Scan QR code**, available before signing in. Scan a
download QR from either app or the web UI to immediately download, decrypt and
save its files. Alternatively, paste the complete link and choose **Receive
files**. A scanned server does not replace your configured server or receive its
login credentials. Upload-link QRs open a deliberate file-selection/send flow;
account-pairing QRs ask before connecting the account.

Scanned downloads save to **Downloads/psst.zip** on Android or the app's
**Received/Guest** folder in iOS Files. Android 8–9 requests storage permission;
later Android versions use scoped Downloads storage. **History → Downloaded**
shows saved downloads alongside the Sent and Receive links filters and remains
available after sign-out. Local downloads retain their own storage and actions. Open and Share use the saved copies, including offline or after the
link expires. Removing a local entry keeps its files and does not revoke the
sender's link. Files never open automatically.

Keep the app in the foreground while receiving. Retry reuses files already
saved, and delivery confirmation follows the final durable save; receipt retries
do not download the files again. Server expiry, revocation or download limits
can prevent retrying an unfinished file. Direct receiving supports up to 100
files, with the upload limit chosen by the server administrator (25 MiB by default). See the [native verification workflow](docs/testing/scan-to-receive.md)
for disposable interoperability fixtures and platform checks.

Link screens put the QR and **Copy link** / **Share** actions first; expand details
to inspect the complete URL. Missing encryption keys on another device are
explained on the corresponding history entry. Revoking a link prevents further
requests, but does not remove copies someone already saved.

Uploads show preparation separately from transferred-byte progress. Web uploads
remain visible when moving between the primary destinations; mobile navigation
that would stop an active upload requires confirmation. The apps do not promise
background transfers after the OS or share extension stops them. Failed cleanup
remains actionable so a partially created resource can still be revoked.

The web header offers **System**, **Light**, and **Dark** appearance on every page,
including login and public links. The choice is saved in that browser; System
follows the browser/device preference. Android and iOS offer the same choices
under **Settings → Appearance**, saved on that device independently of the
server account. System is the default; iOS also applies the choice to its share
extension.
The interface uses deep teal primary
actions, neutral surfaces, and dark-on-white QR codes in both themes. Statuses
also use text, including the distinction between files received by the server,
files saved locally, and a recipient-confirmed download.

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
cp .env.example .env
chmod 600 .env
# Edit .env: set your hostname, public HTTPS URL, and initial admin credentials.
docker compose up -d --build
```

Caddy serves the website and API together. For normal hosting, set
`PSST_DOMAIN=transfer.example.com` and `PUBLIC_URL=https://transfer.example.com`.
Point that hostname at the server and make ports 80 and 443 reachable so Caddy
can obtain a trusted certificate. No certificate is embedded in the mobile app.

Set `ADMIN_USERNAME` and a unique `ADMIN_PASSWORD` (12–72 UTF-8 bytes) for the
first startup. The administrator is created only when there are no accounts;
changing these environment variables later does not reset an existing password.
After initialization, remove the bootstrap password from `.env`; manage accounts
through the web UI. Keep `.env` private and out of version control.

For development on a trusted LAN, explicitly set `PSST_DOMAIN=:80`,
`PUBLIC_URL=http://<server-ip>`, and `AUTH_ALLOW_INSECURE_HTTP=true`.
HTTP exposes login credentials and sessions to network observers; use HTTPS
for normal hosting. Authentication is still required in development mode.

Open the public address and sign in. In Android or iOS server settings, enter that same
address, without `/api/v1`, and your username/password. Alternatively, sign in on
the website using a regular account, choose **Connect mobile app**, and scan its QR from the app’s server
settings to configure and sign in automatically. Allow the app through any phone
firewall.
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

See [Caddy's HTTPS setup](https://caddyserver.com/docs/quick-starts/https).
HTTPS certificate verification remains enabled in all clients.

## Accounts and connected devices

Administrators land on **Overview** and manage **Users**, **Traffic** and **Server
settings**. Admin accounts are administration-only: they cannot create transfers,
receive links, use the web scanner, or pair a mobile app. Create a separate regular
account for personal file transfers. Regular users land on **Send** and manage
their own links and History. Administrators can inspect operational metadata and
revoke server resources, but cannot access encryption keys or plaintext filenames.

New regular accounts use a temporary password and must replace it on first login.
An administrator reset reinstates that requirement. Existing accounts migrate
without a forced reset; administrators are exempt from mandatory first change.
Password changes require the current password and a different replacement of
12–72 UTF-8 bytes, with confirmation in the web and mobile forms. Password changes,
resets and disabling an account revoke its sessions and unused pairing grants;
sign in again after changing the password. Restricted accounts can only inspect
their sign-in state, change the password and sign out until replacement succeeds.
The last enabled administrator cannot be disabled.

The API enforces these rules for existing cookie and app sessions, including
upload continuation. Older clients may need an update to display the dedicated
password/role messages. Existing admin-owned public links remain valid until
expiry or revocation; ownership and keys are not moved. Public-link capabilities
remain independent of an ambient admin or restricted session.

The website uses an HttpOnly session cookie. Android stores its device session
encrypted with Android Keystore; iOS uses Keychain, with a shared access group for
the main app and share extension. Passwords are not saved in either app. Connected
device sessions can be revoked from the website. A pairing QR is a short-lived,
single-use login grant: display it only when connecting a device. It contains no
password or browser session token, but whoever redeems it first gets access to
that account. The website shows **Phone connected** after redemption. Canceling
an unused code or replacing it invalidates that grant; if a phone connected first,
revoke its session from **Connected devices** instead.

History from the server includes resource metadata, never encryption keys.
The browser remembers links it created locally, scoped to the signed-in account.
It can revoke resources created on another device, but cannot recover their
encryption keys or reconstruct their complete share links.

Existing anonymous download links survive the migration. Old resources cannot
automatically be assigned to an account; admins can manage them and existing
private deletion tokens remain usable. Receive links created before accounts
must be recreated after signing in before they can accept further uploads;
their already-uploaded files remain downloadable. Older apps cannot create transfers once authentication is
required. Account login and pairing are available in the web UI and both mobile
apps. See the iOS build and device validation requirements below.

Both mobile apps keep new local history scoped to the signed-in account and server.
Signing out hides account history, and signing into a different account does not
expose the previous account's links or encryption keys. Admin accounts no longer
open personal mobile History; older records are preserved locally without being
reassigned. Native downloaded files remain available independently of server
login. Sent and receive-link entries can be renamed locally, and receive-link
creation offers an optional name. Names apply only on this device/browser and are
scoped to the account, server, type and resource ID; they are never sent to the
server. Clearing a custom name restores the filename/type/date fallback.

## Manual build

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
Optional `PSST_TEST_USERNAME` and `PSST_TEST_PASSWORD` select the test account;
`PSST_EXPECT_INSECURE_CONTEXT=1` enables the LAN HTTP assertion. The previous
`PSST_` names remain accepted as fallbacks; the corresponding `PSST_` value takes
precedence when set. This suite creates and downloads test transfers; use a
disposable deployment.

## Current limits and protocol

An administrator can change **Settings → Server upload limit** in the web UI. The
setting is stored on the server and defaults to 25 MiB per file. The operator's
`MAX_FILE_SIZE` environment setting defines its ceiling (5 GiB by default, at most
1 TiB). Web, Android, iOS and the iOS share extension read the server's current
limit before uploading. New upload reservations are rejected above that limit;
previous reservations and existing downloads remain available after it is lowered.

Files are encrypted and decrypted sequentially in 4 MiB authenticated chunks,
with bounded working buffers and native temporary-file publication. The complete
file's size is subject to the administrator's limit; chunk size is internal.
Large web saves use File System Access or OPFS on supported secure origins.
Browsers without those APIs, including plain LAN HTTP pages, retain a 25 MiB
small-file save fallback and explain how to use a capable HTTPS browser or the
native app for larger files. Web ZIP downloads remain limited to 25 MiB total;
individual large-file saves do not require ZIP aggregation.

See [the chunked-v1 file format](docs/protocol/chunked-files.md) for authenticated
framing and deterministic interoperability vectors. This development format
requires updated clients; there is no old file-ciphertext fallback. Manifests stay
encrypted using the transfer key and a nonce/ciphertext/GCM-tag envelope, and now
include each file's encoding, chunk size and unique encryption context.
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
Choosing **Revoke and delete** in mobile history revokes the transfer on its original server before
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
their protected transfers.

After downloading and successfully decrypting every file, a receiver sends
`POST /api/v1/transfers/{id}/downloaded` with an empty body. This idempotent endpoint
returns `204`; transfer metadata exposes the first confirmation as `downloaded_at`
(otherwise `null`). Partial downloads, manifest reads, and completed HTTP responses
alone do not set it. Confirmations report client success; they do not independently
prove the recipient saved or opened the files. No encryption key or plaintext is
included in the confirmation.

Sent history distinguishes **Ready to download**, **Download started**
(all files requested), and **Downloaded** (receiver confirmation). Native receivers
confirm after saving their decrypted files. The browser confirms after handing the
decrypted files or ZIP to its download mechanism; it cannot verify the subsequent
filesystem save. Failed confirmations can be retried without downloading again:
the web page provides a retry button, Android retries from saved history, and iOS
keeps a pending queue and retries when the app becomes active. Older clients do not
send confirmations, so their downloads may remain **Download started**.

## Administration metrics and traffic

**Overview** separates current gauges (enabled regular users, unexpired transfers
and receive links, and encrypted storage) from recorded file events. Stored bytes
measure actual tracked blob sizes, including partial uploads, plus encrypted
manifests; they exclude the SQLite database/WAL, backups, and untracked orphan
files. Gauges can change while uploads or cleanup run. Operational resource sizes
are declared encrypted file sizes and may include pending reservations.

A file is counted as uploaded once when its parent transfer finalizes. Standalone
sends and files submitted through receive links have separate totals. Delivered
files count once on the first accepted completed-transfer receipt, which is a
client report, not independent proof of saving. Deleting resources and cleanup do
not subtract from these durable event counters.

**Traffic** measures application transfer payload bytes, in UTC. Uploaded bytes
are encrypted manifest/file bodies actually consumed by the application;
downloaded bytes are encrypted bodies accepted by the HTTP response writer.
Repeated downloads, retries, and bytes moved before an interrupted/failed request
count. Declared sizes, GET quotas and receipt counts are not traffic. Requests
rejected before reading their body contribute zero application payload bytes.

These totals exclude web assets, control/account APIs, HTTP/TLS overhead, proxy
buffering/discarded bytes and unrelated services, so provider billing may differ.
The recording-start date is shown explicitly. There is no historical backfill;
the first billing cycle may be partial. Charts support up to 367 UTC days per
request; measured lifetime totals retain the entire recorded history.

Traffic deltas flush at 1 MiB, once per second, on day rollover and when each
request finishes. Graceful shutdown waits for final request flushes before
closing the database. Abrupt termination can lose the unflushed delta per active
stream (normally below 1 MiB, plus the current I/O operation). A failed counter
write permanently marks measurement coverage **degraded** when storage permits,
also retaining an in-process warning if the database is unavailable. Uncertain
failed commits are not retried as traffic deltas because that could double-count.
An unavailable metrics database produces an explicit error rather than zero
totals. Treat this as a usage monitor, not a provider billing ledger.

Administrators may configure an optional allowance, its start day (1–31), and
whether it counts outbound downloads or both directions. A day absent in a short
month becomes that month's last day. Boundaries are UTC, with an exclusive end
date. Editing these settings never rewrites raw traffic or blocks transfers.

Admin API routes are `GET /api/v1/admin/overview`,
`GET /api/v1/admin/traffic?from=YYYY-MM-DD&to=YYYY-MM-DD` (inclusive dates), and
`PATCH /api/v1/admin/traffic/settings`. Settings require all three fields:
`allowance_bytes` (positive safe integer or null), `cycle_start_day`, and
`basis` (`outbound` or `combined`). The existing admin-only
`/api/v1/auth/resources?all=true` supplies operational revocation metadata;
ordinary History endpoints do not grant admin transfer rights.

## Configuration

Operator settings use environment variables. The administrator can also persist the per-file upload limit and traffic-monitor preferences through the web UI.

| Variable                   | Default              | Description                                                      |
| -------------------------- | -------------------- | ---------------------------------------------------------------- |
| `LISTEN_ADDR`              | `:8080`              | Address the backend listens on                                   |
| `STORAGE_PATH`             | `./data/files`       | Directory for encrypted file blobs                               |
| `DB_PATH`                  | `./data/psst.db`     | Path to the SQLite database                                      |
| `MAX_FILE_SIZE`            | `5368709120` (5 GiB) | Operator ceiling for the admin per-file plaintext-byte limit     |
| `DEFAULT_EXPIRY`           | `24h`                | Transfer expiry duration (Go duration syntax)                    |
| `CLEANUP_INTERVAL`         | `5m`                 | How often the cleanup worker runs                                |
| `ALLOW_LEGACY_DELETION`    | `false`              | Allow deletion by ID for older resources without deletion tokens |
| `ADMIN_USERNAME`           | unset                | First administrator username, required when no accounts exist    |
| `ADMIN_PASSWORD`           | unset                | First administrator password; used only during initialization    |
| `PUBLIC_URL`               | unset                | Canonical public origin, such as `https://transfer.example.com`  |
| `AUTH_ALLOW_INSECURE_HTTP` | `false`              | Explicit development-only permission to authenticate over HTTP   |
| `MAX_SLOT_TRANSFERS`       | `20`                 | Maximum child transfers created through one public receive link  |
| `MAX_SLOT_SIZE`            | `5368709120` (5 GiB) | Maximum total reserved file bytes in one receive slot            |
| `MAX_SLOT_EXPIRY`          | `168h`               | Maximum receive-link lifetime                                    |

Docker Compose also accepts:

| Variable      | Default | Description                                                                   |
| ------------- | ------- | ----------------------------------------------------------------------------- |
| `PSST_DOMAIN` | `:80`   | Caddy site address; default serves LAN HTTP, a domain enables automatic HTTPS |
| `HTTP_PORT`   | `80`    | Host port mapped to Caddy HTTP                                                |
| `HTTPS_PORT`  | `443`   | Host port mapped to Caddy HTTPS                                               |

Compose still accepts `PSST_DOMAIN` as a compatibility fallback when
`PSST_DOMAIN` is unset or empty. A nonempty `PSST_DOMAIN` takes precedence.
When running Caddy directly with the supplied Caddyfile, set `PSST_DOMAIN`.
Existing Docker volumes and database paths retain their names to preserve stored data.

## Security model

Accounts authorize creation and management; generated links intentionally grant
access to their specific transfer or receive slot. Direct file uploads require
a fully enabled regular owner session; admin sessions cannot upload. Public slot uploaders receive a private capability
for uploading only their newly created child transfer. Receive slots enforce
expiry, transfer-count, and aggregate storage limits on the server.

Browser sessions use same-origin mutation checks and HttpOnly cookies, with
Secure enabled unless development HTTP is explicitly permitted. Passwords are
hashed and session/pairing secrets are stored as hashes. See the
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
and [password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)
for the security controls informing this design.

Keep the backend on the private Docker network and expose Caddy's public origin.
`PUBLIC_URL` identifies that trusted HTTPS entry point; it does not add TLS to a
separately exposed backend port. Sessions expire after 30 days and pairing codes
after five minutes. Login and pairing redemption are rate limited; deployments
behind one reverse proxy currently share a conservative attempt bucket.

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

## Scanning and brand assets

On mobile, opening Scan QR code starts its embedded camera after permission is
granted. A rear camera is selected automatically, with another camera as fallback
when needed; mobile scanning has no camera switcher. The torch appears only when
supported. Camera failures offer retry, and leaving the screen releases capture.
Paste link and Choose QR image remain available without camera access.
Scan no longer contains a separate history screen: all records are available from
History, with All, Sent, Receive links and Downloaded filters. Settings separates
appearance and account information from the dedicated account-editing flow.

The web scanner is available only to fully enabled regular accounts. It decodes locally, asks
before opening a transfer on another server, and never redeems mobile pairing
codes. Webcam access requires HTTPS or the localhost development exception;
signed-in paste/image decoding remains available on LAN HTTP. Public transfer
links continue working without login.

The shushing symbol appears in app icons, favicons and generated QR codes. QR
rendering uses high error correction and preserves its outer quiet zone. Editable
vector masters, font license and reproducible export instructions are in
[assets/brand](assets/brand/README.md).

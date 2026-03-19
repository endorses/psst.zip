# Implementation Plan

Based on [initial brainstorm](../research/initial-brainstorm.md).

The plan is structured in phases that build on each other. Each phase produces a working, testable artifact before moving to the next.

---

## Phase 1: Backend Core

The foundation everything else depends on. A working Go server with REST API, file storage, and transfer lifecycle management.

### 1.1 Project scaffolding
- [ ] Go module init, directory structure (`cmd/`, `internal/`, `api/`, `store/`)
- [ ] Makefile / Taskfile with build, test, lint targets
- [ ] Dockerfile (multi-stage build → single binary)
- [ ] CI pipeline (GitHub Actions: lint, test, build)
- [ ] Apache 2.0 LICENSE file

### 1.2 Configuration & storage
- [ ] Config via environment variables (`LISTEN_ADDR`, `STORAGE_PATH`, `DB_PATH`, `MAX_FILE_SIZE`, `DEFAULT_EXPIRY`)
- [ ] SQLite database setup with migrations
- [ ] Local disk file store (abstracted behind an interface for future S3 support)

### 1.3 Transfer API — send flow
- [ ] `POST /api/v1/transfers` — create a new transfer, returns `transfer_id`
- [ ] tus upload endpoint (`POST/PATCH /api/v1/transfers/{id}/files`) — resumable chunked upload of encrypted blobs
- [ ] `POST /api/v1/transfers/{id}/manifest` — upload encrypted metadata manifest
- [ ] `POST /api/v1/transfers/{id}/complete` — finalize the transfer
- [ ] `GET /api/v1/transfers/{id}` — get transfer metadata (blob count, sizes, expiry)
- [ ] `GET /api/v1/transfers/{id}/files/{file_id}` — download an encrypted blob
- [ ] `GET /api/v1/transfers/{id}/manifest` — download the encrypted manifest

### 1.4 Transfer API — receive flow (drop slots)
- [ ] `POST /api/v1/slots` — create a drop slot, returns `slot_id`
- [ ] `GET /api/v1/slots/{id}` — get slot status and list of uploaded files
- [ ] `GET /api/v1/slots/{id}/events` — SSE endpoint for real-time upload notifications
- [ ] Uploaders use the same tus endpoint scoped to the slot

### 1.5 Expiry & cleanup
- [ ] Background goroutine: delete expired transfers and their files on disk
- [ ] Configurable expiry per transfer (default 24h)
- [ ] Optional download count limit — delete after N downloads

### 1.6 Testing
- [ ] Integration tests for all API endpoints
- [ ] Test for expiry/cleanup behavior
- [ ] Test for tus upload resume (simulate interrupted upload)

---

## Phase 2: Web App

A Svelte SPA that handles both the upload and download flows entirely client-side (crypto, ZIP).

### 2.1 Project scaffolding
- [ ] SvelteKit project init (static adapter — SPA mode)
- [ ] Build pipeline integrated with backend (embed static assets in Go binary, or serve separately)
- [ ] Decide: embed in Go binary vs. separate static hosting

### 2.2 Download page (`/d/{transfer_id}#key`)
- [ ] Parse `transfer_id` from path, encryption key from `#fragment`
- [ ] Fetch manifest, decrypt it → display file list (names, sizes)
- [ ] Single file download: fetch encrypted blob → decrypt with Web Crypto API → trigger browser download
- [ ] Multi-file download: decrypt all files → bundle with `fflate` → trigger ZIP download
- [ ] Progress indicators for download and decryption
- [ ] Error states: expired transfer, invalid key, network errors

### 2.3 Upload page (for drop slots) (`/u/{slot_id}#key`)
- [ ] File picker / drag-and-drop zone
- [ ] Client-side encryption with key from `#fragment`
- [ ] Resumable upload via `tus-js-client`
- [ ] Upload encrypted manifest
- [ ] Progress indicators
- [ ] Success confirmation

### 2.4 Standalone share page
- [ ] Allow uploading files directly from the web (not just via mobile app)
- [ ] Generate transfer link + display QR code
- [ ] Encryption key generated in browser, appended as `#fragment`

### 2.5 Testing
- [ ] Unit tests for crypto module (encrypt → decrypt round-trip)
- [ ] E2E tests (Playwright): upload flow, download flow, ZIP bundling
- [ ] Test with large files (streaming behavior)

---

## Phase 3: KMP Shared Module

The business logic layer shared between Android and iOS.

### 3.1 Project scaffolding
- [ ] KMP project setup (Gradle, Kotlin 2.x)
- [ ] Source sets: `commonMain`, `androidMain`, `iosMain`
- [ ] SKIE Gradle plugin configured
- [ ] CI: build and test for both targets

### 3.2 Crypto module
- [ ] `expect`/`actual` declarations for:
  - AES-256-GCM key generation
  - Streaming encrypt (chunked)
  - Streaming decrypt (chunked)
- [ ] Android `actual`: `javax.crypto.Cipher` with `AES/GCM/NoPadding`
- [ ] iOS `actual`: `CryptoKit.AES.GCM`
- [ ] Common tests with known test vectors

### 3.3 API client
- [ ] Ktor HTTP client (OkHttp engine on Android, Darwin engine on iOS)
- [ ] Transfer API operations: create transfer, upload files (tus), upload manifest, complete
- [ ] Drop slot operations: create slot, poll/SSE for updates, download files
- [ ] tus protocol client implementation in Ktor
- [ ] Server URL configuration (user-settable base URL)

### 3.4 Models & URL handling
- [ ] Data classes: `Transfer`, `DropSlot`, `FileMetadata`, `EncryptedManifest`
- [ ] URL builder: construct `/d/{id}#key` and `/u/{id}#key` URLs
- [ ] URL parser: extract transfer/slot ID and key from URLs
- [ ] Manifest JSON serialization (kotlinx.serialization)

### 3.5 ZIP module
- [ ] Multi-file → ZIP bundling (for download)
- [ ] ZIP → file extraction (for received files)
- [ ] Streaming where possible to respect iOS memory limits

### 3.6 Testing
- [ ] Common tests for crypto round-trips
- [ ] Common tests for URL construction/parsing
- [ ] Common tests for manifest serialization
- [ ] Integration tests against a running backend (optional, CI)

---

## Phase 4: Android App

Native Android app with Jetpack Compose, share sheet integration, and QR display.

### 4.1 Project scaffolding
- [ ] Android project setup, KMP shared module dependency
- [ ] Min SDK, target SDK, permissions (INTERNET, storage access)
- [ ] Material 3 theming

### 4.2 Core screens
- [ ] **Server config screen**: enter/edit backend URL, connection test
- [ ] **Home screen**: two actions — "Share files" and "Receive files"
- [ ] **Transfer detail screen**: QR code display, share link button, transfer status, expiry countdown

### 4.3 Send flow
- [ ] Share sheet receiver (`Intent` filter for all file types)
- [ ] Also: in-app file picker
- [ ] Generate encryption key → encrypt files via KMP module → upload via tus → display QR code with link
- [ ] Upload progress with cancel support

### 4.4 Receive flow
- [ ] Create drop slot via KMP module → display QR code with upload link
- [ ] SSE listener for incoming file notifications
- [ ] Download & decrypt received files
- [ ] Save to device storage / share to other apps

### 4.5 History & management
- [ ] List of recent transfers (sent and received), stored locally
- [ ] Delete / expire transfers manually

### 4.6 Testing
- [ ] UI tests (Compose testing)
- [ ] Integration tests for share sheet intent handling
- [ ] Manual test matrix: different file types, sizes, network conditions

---

## Phase 5: iOS App

Native iOS app with SwiftUI, share extension, and QR display.

### 5.1 Project scaffolding
- [ ] Xcode project setup, KMP framework dependency (via SPM or CocoaPods)
- [ ] SKIE integration verified (async/await, sealed classes)
- [ ] App Group configured (shared data between main app and share extension)

### 5.2 Core screens
- [ ] **Server config screen**: enter/edit backend URL, connection test
- [ ] **Home screen**: two actions — "Share files" and "Receive files"
- [ ] **Transfer detail screen**: QR code display (CoreImage `CIQRCodeGenerator`), share link button, status, expiry

### 5.3 Send flow
- [ ] Share extension (App Extension target)
  - Receives files from share sheet
  - Encrypts and uploads within extension memory limits (~120MB)
  - Shows progress UI in the extension
  - Hands off to main app for QR display
- [ ] In-app file picker (document picker)
- [ ] Generate key → encrypt → upload via tus → display QR

### 5.4 Receive flow
- [ ] Create drop slot → display QR with upload link
- [ ] SSE listener for incoming files
- [ ] Download & decrypt received files
- [ ] Save to Files app / share to other apps

### 5.5 History & management
- [ ] List of recent transfers, stored locally (Core Data or SwiftData)
- [ ] Delete / expire transfers manually

### 5.6 Testing
- [ ] UI tests (XCTest)
- [ ] Share extension testing
- [ ] Manual test matrix: different file types, sizes, network conditions

---

## Phase 6: Polish & Release

### 6.1 Cross-platform testing
- [ ] End-to-end: Android sends → web downloads
- [ ] End-to-end: web uploads to iOS drop slot → iOS downloads
- [ ] End-to-end: Android sends → iOS downloads (and vice versa)
- [ ] Large file testing (1GB+) across all platforms
- [ ] Interrupted upload resume testing on all clients

### 6.2 Deployment & docs
- [ ] Docker Compose example with reverse proxy (Caddy/nginx)
- [ ] README with setup instructions, screenshots, architecture overview
- [ ] CHANGELOG
- [ ] GitHub releases with pre-built binaries (goreleaser)

### 6.3 Security
- [ ] Security review of crypto implementation
- [ ] Rate limiting on API endpoints
- [ ] Input validation and hardening
- [ ] CORS configuration

### 6.4 Nice-to-haves (if time permits)
- [ ] Dark mode on all platforms
- [ ] Transfer size display in QR screen
- [ ] Notification when transfer is downloaded
- [ ] Password-protected transfers (additional layer on top of E2E)

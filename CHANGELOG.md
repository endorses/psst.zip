# Changelog

## Unreleased

- Align web and mobile manifest fields and drop-slot transfer IDs.
- Correct tus resource URLs and enforce upload length, ownership, expiry, and completed-transfer immutability.
- Emit slot completion notifications and enforce atomic per-file download quotas.
- Add real cleanup, concurrency, encrypted API, and Chromium transfer regression tests.
- Upgrade Gradle to 9.1.0 with compatible Kotlin, Android, KSP, SKIE, and Room versions for Java 25 builds.
- Add bounded client file sizes and iOS project generation; native iOS validation remains pending.

## v0.1.0 (2026-03-19)

Initial release.

### Backend

- REST API for creating transfers and drop slots
- Resumable file uploads via tus protocol
- Encrypted manifest upload and download
- SQLite-backed metadata storage with local disk file store
- Automatic expiry and cleanup of transfers
- Configurable download count limits
- SSE endpoint for real-time drop slot notifications

### Web App

- SvelteKit SPA with static adapter
- Upload page: file picker, drag-and-drop, client-side AES-256-GCM encryption, resumable upload via tus
- Download page: fetch and decrypt manifest, single-file download, multi-file ZIP bundling via fflate
- Standalone share page with QR code generation
- Progress indicators and error handling throughout

### Android App

- Jetpack Compose UI with Material 3
- Share sheet integration for sending files
- QR code display for transfer links
- Drop slot creation and SSE-based receive flow
- Transfer history with manual expiry

### iOS App

- SwiftUI with share extension
- QR code generation via CoreImage
- Drop slot creation and SSE-based receive flow
- Transfer history with manual expiry

### Shared (KMP)

- Kotlin Multiplatform module for Android and iOS
- AES-256-GCM whole-file encryption and decryption
- Ktor-based API client with tus protocol support
- URL construction and parsing for transfer and slot links
- ZIP bundling and extraction

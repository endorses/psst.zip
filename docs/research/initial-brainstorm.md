# Initial Brainstorm

**Date**: 2026-03-16

## Overview

the app is an open-source, self-hosted file transfer app with end-to-end encryption. Users share files via mobile share sheets and receive download links displayed as QR codes. The server never sees plaintext — encryption keys are shared exclusively via URL fragments (`#`), which are never sent to the server.

## Core Features

- **Share sheet integration**: select files on Android/iOS, share to the app, get an encrypted upload with a QR code link
- **Web download**: recipients open the link in any browser — no app install required
- **Receive mode**: app generates an upload link so others can send files *to* the user
- **Multi-file ZIP download**: multiple files can be downloaded as a single ZIP, created client-side after decryption
- **Configurable backend**: the app points to a user's self-hosted instance
- **End-to-end encryption**: AES-256-GCM, key in URL `#fragment`

## Architecture

```
┌─────────────────────┐     ┌──────────────────────┐
│   Android App       │     │     iOS App          │
│   Kotlin/Compose    │     │   Swift/SwiftUI      │
│                     │     │                      │
│   ┌──────────────┐  │     │   ┌──────────────┐   │
│   │  Native UI   │  │     │   │  Native UI   │   │
│   │  Share Sheet │  │     │   │  Share Ext   │   │
│   │  QR Display  │  │     │   │  QR Display  │   │
│   └──────┬───────┘  │     │   └──────┬───────┘   │
│          │          │     │          │           │
│   ┌──────┴──────────┴─────┴──────────┴──────┐    │
│   │         KMP Shared Module               │    │
│   │  - Crypto (AES-256-GCM)                 │    │
│   │  - API Client (Ktor)                    │    │
│   │  - Models & URL handling                │    │
│   │  - ZIP logic                            │    │
│   └─────────────────┬─────┬─────────────────┘    │
└─────────────────────┘     └─────────┬────────────┘
                                      │
                                    HTTPS
                                      │
                               ┌──────┴───────┐
                               │   Backend    │
                               │   (Go)       │
                               │   SQLite     │
                               └──────┬───────┘
                                      │
                               ┌──────┴───────┐
                               │  File Store  │
                               │  (disk/S3)   │
                               └──────────────┘

┌─────────────────────┐
│   Web App (SPA)     │
│   Upload/Download   │
│   Client-side E2E   │
└─────────────────────┘
```

## Tech Stack

### Backend
- **Language**: Go
- **Database**: SQLite (Postgres optional later)
- **Storage**: local disk (S3 optional later)
- **Deployment**: single binary, Docker image
- **Minimal config**: `LISTEN_ADDR`, `STORAGE_PATH`

### Mobile — Native UI, Shared Business Logic via KMP
- **Android**: Kotlin + Jetpack Compose
- **iOS**: Swift + SwiftUI
- **Shared (KMP)**: crypto, API client (Ktor), models, URL handling, ZIP logic
- **SKIE**: used to bridge KMP coroutines/sealed classes to native Swift async/await and enums

### Web App
- **Framework**: Svelte — minimal bundle size, compiles away, fast cold-load for link recipients
- Client-side encryption/decryption via Web Crypto API
- Client-side ZIP creation (e.g. `fflate`)

## Encryption Design

- AES-256-GCM, key generated client-side
- Each file encrypted individually before upload
- URL format: `https://drop.example.com/d/<transfer_id>#<base64_key>`
- The `#fragment` is never sent to the server — true zero-knowledge
- Encrypted metadata manifest (filenames, sizes) uploaded alongside blobs
- Same scheme used by Firefox Send, PrivateBin

### Receive Mode (upload links)
- App creates a "drop slot" on the server with a unique ID
- Generates URL: `https://drop.example.com/u/<slot_id>#<base64_key>`
- Uploader opens link in browser, encrypts with key from fragment, uploads
- App receives notification via SSE (polling as fallback)

## KMP Shared Module — Details

### What's shared
- Crypto operations via `expect`/`actual` wrapping platform APIs (`javax.crypto` on Android, `CryptoKit` on iOS)
- HTTP client (Ktor — OkHttp engine on Android, Darwin engine on iOS)
- Data models and transfer state
- URL construction and parsing
- ZIP bundling/unbundling

### Platform-specific (native)
- **Android**: Compose UI, `Intent` filter for share sheet, QR display, file picker, notifications
- **iOS**: SwiftUI, App Extension for share sheet, CoreImage QR generation, document picker, notifications

### iOS Interop
- KMP produces an Objective-C framework consumed by Swift
- **SKIE** (Swift Kotlin Interface Enhancer) provides:
  - `async`/`await` for Kotlin coroutines
  - Swift enums for sealed classes
  - Kotlin Flows as `AsyncSequence`
  - Proper parameter naming
- Added as a Gradle plugin — no Kotlin code changes needed

## Design Considerations

### iOS Share Extension
- Runs in a separate process with ~120MB memory limit
- Streaming encryption is essential — cannot load entire files into memory
- KMP shared module must work within these constraints

### Large File Handling & Resumable Uploads
- Streaming encryption on all platforms
- Web Crypto API streaming support via `ReadableStream` (browser support varies)
- **Resumable chunked uploads using the [tus protocol](https://tus.io/)**:
  - Open protocol with mature client libraries for all platforms (Android, iOS, JS)
  - Server tracks upload offset; client resumes from last acknowledged byte on interruption
  - Go server library: [tusd](https://github.com/tus/tusd) (embeddable as a handler)
  - KMP/mobile: Ktor-based tus client in shared module
  - Web: official [tus-js-client](https://github.com/tus/tus-js-client)
  - Each chunk is encrypted client-side before sending — server only stores opaque encrypted chunks
  - Essential for mobile on unreliable networks and for large file transfers

### QR Code Sizing
- 256-bit AES key = 44 chars in base64
- Combined with transfer URL, total length stays within comfortable QR density

### File Expiry & Limits (MVP defaults)
- Auto-expire after 24 hours
- Configurable max file size / total transfer size
- Optional download count limits

### Metadata Privacy
- Filenames and file sizes encrypted in a JSON manifest
- Server only sees opaque blobs and blob sizes

## MVP Scope

1. Go backend — single binary, SQLite, local file storage, REST API
2. Web app — upload, download, encrypt/decrypt in browser
3. Android app — Compose UI, share sheet, QR display, KMP shared module
4. iOS app — SwiftUI, share extension, QR display, KMP shared module via SKIE
5. AES-256-GCM encryption, key in URL fragment
6. 24-hour auto-expiry
7. No authentication for MVP

## Decisions

- [x] **Web app framework**: Svelte — compiles away for minimal bundle, fast cold-load
- [x] **KMP crypto**: `expect`/`actual` wrapping platform APIs (`javax.crypto` / `CryptoKit`) — battle-tested, best performance
- [x] **Upload protocol**: tus (resumable chunked uploads) — essential for mobile/unreliable networks
- [x] **Receive-mode notifications**: SSE (Server-Sent Events) with polling fallback — simple, unidirectional, well-supported
- [x] **License**: Apache 2.0 — permissive with patent grant protection

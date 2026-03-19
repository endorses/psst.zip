# Psst

Self-hosted, end-to-end encrypted file transfer. Share files between devices without trusting the server.

## How it works

1. The sender picks files and the client generates a random AES-256-GCM key.
2. Files are encrypted client-side and uploaded to the server via resumable tus uploads.
3. The server stores only encrypted blobs -- it never sees plaintext data.
4. The sender gets a link like `https://your-server/d/{id}#key` where the encryption key lives in the URL fragment (never sent to the server).
5. The recipient opens the link, and the web app (or mobile app) decrypts everything in the browser/on-device.

Drop slots work in reverse: the receiver creates a slot, shares its QR/link, and uploaders encrypt into it.

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
# 1. Build the web app
cd web && npm ci && npm run build && cd ..

# 2. (Optional) Configure your domain and port in a .env file
echo 'PSST_DOMAIN=psst.example.com' > .env

# 3. Start the stack
docker compose up -d
```

Caddy handles TLS automatically when `PSST_DOMAIN` is set to a public domain. For local use, it defaults to `localhost`.

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
# Serve the contents of web/build/ with any static file server
```

## Configuration

All backend settings are controlled via environment variables.

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Address the backend listens on |
| `STORAGE_PATH` | `./data/files` | Directory for encrypted file blobs |
| `DB_PATH` | `./data/psst.db` | Path to the SQLite database |
| `MAX_FILE_SIZE` | `5368709120` (5 GB) | Maximum upload size in bytes |
| `DEFAULT_EXPIRY` | `24h` | Transfer expiry duration (Go duration syntax) |
| `CLEANUP_INTERVAL` | `5m` | How often the cleanup worker runs |

Docker Compose also accepts:

| Variable | Default | Description |
|---|---|---|
| `PSST_DOMAIN` | `localhost` | Domain for Caddy (enables auto-HTTPS for public domains) |
| `HTTP_PORT` | `80` | Host port mapped to Caddy HTTP |
| `HTTPS_PORT` | `443` | Host port mapped to Caddy HTTPS |

## Security model

- **End-to-end encryption**: AES-256-GCM. Keys are generated client-side and never leave the client.
- **Key in URL fragment**: The `#key` portion of URLs is not sent to the server by browsers (per RFC 3986). The server only sees the transfer ID.
- **Zero-knowledge server**: The backend stores and serves encrypted blobs. It cannot decrypt file contents or metadata.
- **Resumable uploads**: The tus protocol allows large files to be uploaded reliably. All data is encrypted before upload.
- **Automatic expiry**: Transfers are deleted after a configurable duration or download count.

## License

GNU Affero General Public License, version 3 only (AGPL-3.0-only). See [LICENSE](LICENSE).

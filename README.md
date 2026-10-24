# psst.zip

[![CI](https://github.com/endorses/psst.zip/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/endorses/psst.zip/actions/workflows/ci.yml)
[![Server release](https://img.shields.io/github/v/release/endorses/psst.zip?filter=v*&label=server)](https://github.com/endorses/psst.zip/releases?q=v&expanded=true)
[![Android release](https://img.shields.io/github/v/release/endorses/psst.zip?filter=android-v*&label=Android)](https://github.com/endorses/psst.zip/releases?q=android-v&expanded=true)
[![License: AGPL-3.0-only](https://img.shields.io/badge/license-AGPL--3.0--only-blue)](LICENSE)

Encrypted file sharing for the browser, Android, iOS and terminal. Self-host a
server for internet transfers, or use the CLI for temporary sharing on a trusted
LAN. Files and filenames are encrypted on the sending device and decrypted on
the receiving device.

[Project website](https://psst.zip) · [Documentation](docs/README.md) · [Releases](https://github.com/endorses/psst.zip/releases)

## Contents

- [Features](#features)
- [Get started](#get-started)
- [How it works](#how-it-works)
- [Security](#security)
- [Documentation](#documentation)
- [Development](#development)
- [License](#license)

## Features

- **Send and Receive links:** share files with a link or QR code, or invite others
  to upload files to your private inbox. Recipients do not need an account.
- **Web and native clients:** a browser interface, Android app, and iOS app with a
  share extension. Scan QR codes or paste links to receive files.
- **Terminal and automation:** the `psst` CLI supports encrypted transfers, JSON
  output, resumable uploads and QR codes, including offline QR rendering from
  saved CLI results.
- **Temporary LAN sharing:** serve download and upload links directly from the
  CLI, using an available port and no account or separate server.
- **Transfer controls:** expiry, revocation and optional download/upload limits;
  administrators manage accounts, storage and traffic policies.
- **Device preferences:** light, dark and system appearance; English and German
  interfaces across web, Android, iOS and the share extension.

## Get started

### Self-host a server

With Docker and Compose installed, run from a checked-out copy of this repository:

```sh
cp .env.example .env
chmod 600 .env
# Edit .env: set your hostname, public HTTPS URL and initial admin credentials.
docker compose up -d --build
```

Point your hostname at the server and make ports 80 and 443 reachable for Caddy's
automatic HTTPS. Open the configured public URL, sign in as the administrator and
create a regular account for file transfers. Choose a unique bootstrap password
and remove it from `.env` after initialization.

See [self-hosting and configuration](docs/self-hosting.md) for the full setup,
trusted LAN development and mobile connections. To use published images instead
of building from source, follow [container release installation](docs/security/container-releases.md#operator-configuration-and-first-installation).

### Use the CLI

Source builds require Go, Node/npm and GNU Make. From the repository root:

```sh
make install-release
psst auth login --server https://transfer.example.com --username alice
psst send ./document.pdf --qr
```

The default installation is `~/.local/bin/psst`; add that directory to your `PATH`.
Installed binaries run without Node or Docker. For a temporary transfer on a
trusted LAN, skip account setup and run either command:

```sh
psst serve send ./document.pdf --qr
psst serve receive --output-dir ./incoming --qr
```

Keep the command running while the other device transfers files. See the
[CLI guide](docs/cli.md) for installation options, commands, scripts, credentials
and optional HTTPS for LAN sharing.

### Connect a mobile app

Get the [Android APK](https://github.com/endorses/psst.zip/releases?q=android-v&expanded=true),
or follow the [iOS build instructions](ios/README.md). Sign in to your own server
using a regular account, or pair from the website's connected-device settings.
Both apps can scan a public share QR or open a pasted link without signing in.
See the [user guide](docs/usage.md) for pairing, downloads and local history.

## How it works

1. The sending client generates a random key and encrypts the files and filenames.
2. The server stores encrypted data and operational metadata such as size,
   ownership and expiry.
3. A Send link includes the decryption key in its URL fragment (`#key`), which
   browsers do not send to the server.
4. The recipient opens the link and decrypts the files locally.

Receive invitations contain a public key. Each sender encrypts a separate file
key to it; only the invitation's creator holds the private key needed to decrypt
submissions. Pairing another device grants account access but does not copy
private encryption keys. See [transfer behavior and API](docs/protocol/transfers.md)
and the [receive design](docs/security/receive-crypto-design.md).

## Security

Encryption assumes trustworthy clients. A server that delivers altered browser
JavaScript can compromise keys and plaintext; use trusted HTTPS for normal
hosting. LAN HTTP encrypts files but does not authenticate the browser code and
is suitable only for a trusted network.

The server can see operational metadata and explicitly shared titles. A successful
download does not authenticate the sender or prove that a file is safe. See
[security and privacy](docs/usage.md#security-and-privacy),
[recipient safeguards](docs/security/recipient-safeguards.md) and
[deployment hardening](docs/security/deployment.md).

## Documentation

| For                                                | Start here                                      |
| -------------------------------------------------- | ----------------------------------------------- |
| Sending, receiving, QR codes, accounts and devices | [User guide](docs/usage.md)                     |
| Terminal commands, automation and LAN sharing      | [CLI guide](docs/cli.md)                        |
| Running your own instance and environment settings | [Self-hosting](docs/self-hosting.md)            |
| Accounts, storage, usage and traffic controls      | [Administration](docs/administration.md)        |
| Builds, tests and contributions                    | [Development](docs/development.md)              |
| Encryption, limits and API behavior                | [Transfer protocol](docs/protocol/transfers.md) |

The [documentation index](docs/README.md) also links deployment, recovery,
security, localization and platform-specific guides.

## Development

```text
web/         SvelteKit browser UI, served by Caddy
backend/     Go API, tus uploads, SQLite and encrypted file storage
client/      Go client library and psst CLI, including the LAN web server
shared/      Kotlin Multiplatform crypto, API client and models
android/     Jetpack Compose app
ios/         SwiftUI app and share extension
```

Start with the [development guide](docs/development.md) and install the
[repository hooks](hooks/README.md) before contributing. Android and iOS share
protocols; applicable mobile changes need equivalent behavior on both platforms.

## License

Copyright 2026 The psst.zip Authors. Licensed under the GNU Affero General Public
License, version 3 only (`AGPL-3.0-only`). See [LICENSE](LICENSE).

If you modify psst.zip and let users interact with that version over a network,
you must offer those users the corresponding source under the license's terms.
Commercial use and paid hosting are allowed. Third-party dependencies and bundled
assets retain their own licenses and notices.

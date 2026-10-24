# Documentation

psst.zip has a web interface, Android and iOS apps, and a Go client library and
CLI. Each hosted installation uses the operator's own server address and accounts.
The CLI can also serve temporary transfers directly on a trusted LAN.

## Using psst.zip

| Guide                                                         | Covers                                                                        |
| ------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| [User guide](usage.md)                                        | Send, Receive, History, QR scanning, accounts, pairing and device preferences |
| [CLI](cli.md)                                                 | Build/install, terminal commands, automation, private state and LAN sharing   |
| [Receiving untrusted files](security/recipient-safeguards.md) | Download preflight, filenames, storage, retries and opening files             |

## Hosting and administration

| Guide                                                                                                  | Covers                                                                  |
| ------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------- |
| [Self-hosting](self-hosting.md)                                                                        | Docker Compose setup, HTTPS, mobile connections and configuration       |
| [Administration](administration.md)                                                                    | Accounts, operator controls, dashboard metrics and traffic measurement  |
| [Container installation](security/container-releases.md#operator-configuration-and-first-installation) | Published images, manifests, Compose and persistent storage identities  |
| [Deployment hardening](security/deployment.md)                                                         | Private networking, proxies, process restrictions and dependency review |
| [Updates and recovery](security/release-update-recovery.md)                                            | Installed updater, transaction states, checkpoints and restoration      |
| [GitHub Actions deployment](security/actions-production-deployment.md)                                 | Manual deployment through restricted SSH                                |
| [Backup and restore](security/backup-restore.md)                                                       | Consistent database/payload snapshots and isolated recovery             |
| [Incident response](security/incident-response.md)                                                     | Transfer pause, account containment and local recovery                  |

## Protocols and security

| Guide                                                                    | Covers                                                                  |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------- |
| [Security and privacy](usage.md#security-and-privacy)                    | Trust assumptions, capabilities, encryption and visible metadata        |
| [Transfer behavior and API](protocol/transfers.md)                       | Limits, creation, revocation and download receipts                      |
| [Chunked files](protocol/chunked-files.md)                               | Authenticated framing and interoperability vectors                      |
| [Receive encryption](security/receive-crypto-design.md)                  | Public-key invitations, HPKE and owner-only inbox access                |
| [Administrator authentication](security/administrator-authentication.md) | Second factors, recent verification, recovery and credential protection |
| [Local history](security/local-history-storage.md)                       | Account/server isolation, client keys and saved downloads               |
| [Shared titles](security/shared-link-titles.md)                          | Explicit visible labels and private filename boundaries                 |
| [Resource limits](security/resource-limits.md)                           | Storage admission, object counts and retention                          |
| [Traffic limits](security/traffic-limits.md)                             | Allowances, bandwidth, concurrent streams and recovery                  |
| [Metadata retention](security/metadata-retention.md)                     | Bounded authentication records and traffic detail                       |

## Development and releases

| Guide                                                      | Covers                                                               |
| ---------------------------------------------------------- | -------------------------------------------------------------------- |
| [Development](development.md)                              | Repository layout, server/CLI/mobile builds and verification         |
| [iOS development](../ios/README.md)                        | Xcode project generation, share extension, signing and native checks |
| [Repository hooks](../hooks/README.md)                     | Credential scanning, sensitive files, formatting and history checks  |
| [Translations](localization/contributing.md)               | Resource locations, terminology and localization checks              |
| [Brand assets](../assets/brand/README.md)                  | Editable artwork, font licenses and reproducible exports             |
| [Transfer verification](testing/transfer-workflows.md)     | Disposable automated checks and native/manual scenarios              |
| [Android releases](releases/android.md)                    | APK preparation, signing, publication and update verification        |
| [F-Droid](../fdroid/README.md)                             | Build recipe and submission material                                 |
| [Container publication](security/container-publication.md) | Source evidence, protected approval and final provenance             |
| [Go VPS updater](testing/go-vps-updater.md)                | Migration evidence and the separately installed Go helper            |

[Implementation plans](plans/) and [research](research/) record work and evidence;
their pending items are not promises that a feature has shipped.

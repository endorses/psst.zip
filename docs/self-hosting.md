# Self-hosting psst.zip

The public website and API share one origin. Mobile apps and the CLI connect to
that operator-selected address. The supplied source-build Compose stack runs the
Go backend behind Caddy, with persistent volumes for data and TLS state.

## Contents

- [Build and start with Docker Compose](#build-and-start-with-docker-compose)
- [Configuration](#configuration)
- [Updates and recovery](#updates-and-recovery)

## Build and start with Docker Compose

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

For development on a trusted LAN, explicitly set `PSST_DOMAIN=http://`,
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
For a separate TLS gateway in front of the bundled web proxy, see the
[external-proxy deployment example](security/deployment.md#external-tls-gateway-example),
including exact proxy trust, private listeners and the disposable integration check.

For published backend/web images, persistent storage identities and release
manifest verification, use the [container release installation guide](security/container-releases.md#operator-configuration-and-first-installation).
The [update and recovery guide](security/release-update-recovery.md) covers
installed instances; [manual Actions deployment](security/actions-production-deployment.md)
is a separate operation from publishing a release. Keep the same Compose project,
volume names and database path when updating an existing installation.

## Configuration

Operator settings use environment variables. The administrator can also persist
the per-file upload limit, resource policy, traffic-monitor preferences and
separate traffic enforcement policy through the web UI.

| Variable                   | Default              | Description                                                               |
| -------------------------- | -------------------- | ------------------------------------------------------------------------- |
| `LISTEN_ADDR`              | `:8080`              | Address the backend listens on                                            |
| `STORAGE_PATH`             | `./data/files`       | Directory for encrypted file blobs                                        |
| `DB_PATH`                  | `./data/psst.db`     | Path to the SQLite database                                               |
| `MAX_FILE_SIZE`            | `5368709120` (5 GiB) | Operator ceiling for the admin per-file plaintext-byte limit              |
| `DEFAULT_EXPIRY`           | `24h`                | Transfer expiry duration (Go duration syntax)                             |
| `CLEANUP_INTERVAL`         | `5m`                 | How often the cleanup worker runs                                         |
| `ALLOW_LEGACY_DELETION`    | `false`              | Allow deletion by ID for older resources without deletion tokens          |
| `ADMIN_USERNAME`           | unset                | First administrator username, required when no accounts exist             |
| `ADMIN_PASSWORD`           | unset                | First administrator password; used only during initialization             |
| `PUBLIC_URL`               | unset                | Canonical public origin, such as `https://transfer.example.com`           |
| `AUTH_ALLOW_INSECURE_HTTP` | `false`              | Explicit development-only permission to authenticate over HTTP            |
| `MAX_SLOT_TRANSFERS`       | `20`                 | Maximum child transfers created through one public receive link           |
| `MAX_SLOT_SIZE`            | `5368709120` (5 GiB) | Maximum total reserved file bytes in one receive slot                     |
| `MAX_SLOT_EXPIRY`          | `168h`               | Maximum receive-link lifetime                                             |
| `MAX_MANIFEST_SIZE`        | `1048576` (1 MiB)    | Manifest upload ceiling; cannot exceed the clients' 1 MiB format bound    |
| `TRUSTED_PROXIES`          | unset                | Comma-separated explicit proxy CIDRs; empty trusts no forwarded addresses |
| `MAX_ACTIVE_STREAMS`       | `64`                 | Initial persisted total payload/inbox-event concurrency                   |
| `MAX_STREAMS_PER_ACCOUNT`  | `4`                  | Initial persisted concurrency per resource owner                          |
| `MAX_STREAMS_PER_IP`       | `4`                  | Initial persisted concurrency per resolved client address                 |
| `MAX_STREAMS_PER_TRANSFER` | `4`                  | Initial persisted concurrency per transfer                                |
| `MAX_STREAMS_PER_SLOT`     | `4`                  | Initial persisted concurrency per receive inbox                           |
| `MAX_ACTIVE_REQUESTS`      | `128`                | Concurrent ordinary requests, including long-lived streams                |
| `MAX_RECOVERY_REQUESTS`    | `32`                 | Separate bounded lane for auth/admin, deletion and health requests        |

Stream/request caps are process-local, not distributed limits or storage/traffic
quotas. Invalid/nonpositive values use finite defaults; values above 4096 are
clamped. Public downloads and guest submissions are attributed to their resource
owner. Requests exceeding active limits receive a retry response. Administrative
recovery has separate admission and per-address rate buckets so file traffic
cannot consume that lane.

The five stream settings seed the database once on first startup with the traffic
policy schema, including upgrades. Later environment changes do not overwrite or
cap the saved policy; use the administrator Traffic page to change stream limits.
The request/recovery caps remain environment settings and can impose lower
effective concurrency.

Only configure proxy networks you control. The backend trusts a forwarded chain
only from those peers and resolves it from the nearest trusted hop; forwarded
headers from other clients do not change limiter identity. The bundled proxy
was tested against spoofed forwarding headers; external proxy chains still need
deployment-specific verification. Persistent server/account storage, object and
retention policies now enforce admission and disk reserves; see the
[default resource profile](security/resource-limits.md). Separate
[billing-cycle traffic budgets](security/traffic-limits.md) are available;
the traffic monitor's allowance remains observational.

Docker Compose also accepts:

| Variable      | Default  | Description                                                                        |
| ------------- | -------- | ---------------------------------------------------------------------------------- |
| `PSST_DOMAIN` | required | Public hostname for automatic HTTPS; `http://` explicitly selects development HTTP |
| `HTTP_PORT`   | `80`     | Host port mapped to Caddy HTTP                                                     |
| `HTTPS_PORT`  | `443`    | Host port mapped to Caddy HTTPS                                                    |

Compose requires an explicit `PSST_DOMAIN`; it no longer silently starts an HTTP
site. When running Caddy directly with the supplied Caddyfile, set `PSST_DOMAIN`.
See [deployment hardening and recovery](security/deployment.md) before
upgrading an existing installation: the proxy now runs as UID/GID 10001 and
existing certificate/configuration volumes need matching ownership.
Existing Docker volumes and database paths retain their names to preserve stored data.

## Updates and recovery

Before changing an existing installation, review [deployment hardening](security/deployment.md)
and the [cold backup and isolated restore runbook](security/backup-restore.md).
Keep database and payload snapshots together. The [resource manager](security/resource-management.md)
and [stored-file recovery](security/storage-recovery.md) describe cleanup and integrity checks.
See [administration](administration.md) for account controls, usage and traffic policy.

Main-branch commits run CI. Container publication and production deployment are
separate actions; see [container publication](security/container-publication.md)
and [production deployment](security/actions-production-deployment.md).

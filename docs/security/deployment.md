# Deployment hardening and recovery

The supplied Compose stack exposes Caddy only. The backend has no published port
and joins an internal network; Caddy additionally joins an edge network for
certificate issuance. Files, SQLite, TLS keys and Caddy state are mounted outside
`/srv/web`. Never mount the data directory under the static root.

## Public HTTPS and development HTTP

Set `PSST_DOMAIN=transfer.example.com`, `PUBLIC_URL=https://transfer.example.com`
and leave `AUTH_ALLOW_INSECURE_HTTP=false`. Configure DNS and incoming public
ports 80/443. Caddy obtains publicly trusted certificates; no custom certificate
belongs in an APK. Compose requires an explicit domain instead of silently
starting a plain HTTP site.

For explicitly trusted local development only, set `PSST_DOMAIN=http://`,
`PUBLIC_URL=http://<LAN-IP>` and `AUTH_ALLOW_INSECURE_HTTP=true`. Both the site
choice and authentication opt-in are necessary. HTTP exposes passwords/sessions
and lets an active network attacker replace browser JavaScript. Encrypting
file contents does not repair those risks. Do not forward this development
listener onto the Internet.

Public ports map to Caddy's internal 8080/8443 listeners. Caddy's
[internal port options](https://caddyserver.com/docs/caddyfile/options#http-port)
preserve the public HTTPS address and ACME behavior. Custom reverse proxies must
route the website and `/api/*` under the same public origin, preserve Host and the
public scheme, and support streamed bodies/SSE without full-body buffering.

## Process and network restrictions

Both services run as non-root, with a read-only root filesystem, all Linux
capabilities dropped, and `no-new-privileges`. The upstream Caddy binary's
`NET_BIND_SERVICE` file capability is removed in our image because the service
uses unprivileged internal ports. Writable paths are the named data volumes and
bounded, non-executable `/tmp` filesystems. The Caddy administration port remains
container-loopback only; do not publish it.

Default container ceilings are backend 512 MiB RAM / 2 CPUs / 256 processes and
proxy 256 MiB / 1 CPU / 128 processes. Set `BACKEND_MEMORY_LIMIT`,
`BACKEND_CPU_LIMIT`, `PROXY_MEMORY_LIMIT`, or `PROXY_CPU_LIMIT` for measured
workloads. These are process ceilings, not file-size limits. Compose's `local`
log driver rotates each container at 10 MiB with three files. Existing external
log collectors need their own bounded retention. These controls use
[Compose service settings](https://docs.docker.com/reference/compose-file/services/).

The private subnet defaults to `172.30.193.0/29`, backend `.2`, Caddy `.3`.
Only Caddy's exact IPv4 address is trusted by the backend for forwarded client
addresses. If that subnet overlaps your host, VPN, or another installation,
change `PSST_PRIVATE_SUBNET`, `PSST_BACKEND_IP` and `PSST_PROXY_IP` together.
Do not attach unrelated containers to this network or publish the backend.

For a directly exposed standalone backend, leave `TRUSTED_PROXIES` empty.
For an external proxy, set it to that proxy's actual socket-source CIDR(s),
preferably individual `/32` or `/128` addresses. Do not trust `0.0.0.0/0`,
`::/0`, or a broad private range merely because the proxy uses private addresses.
The proxy must overwrite client-supplied forwarding headers. If another trusted
proxy sits before bundled Caddy, configure Caddy's own explicit trusted-proxy
policy as well; the default assumes Caddy receives the client connection.
External proxy chains still require deployment-specific integration tests.

## Existing volume ownership

Caddy now runs as UID/GID `10001:10001`. New named volumes inherit this ownership.
Before upgrading an existing stack, stop it, back up the volumes, and identify
the actual Caddy data/config volume names with `docker volume ls`. For each of
those two volumes, run a one-shot administrative container to change ownership:

```sh
docker run --rm --user 0 --entrypoint sh \
  --mount type=volume,src=YOUR_CADDY_VOLUME,dst=/state \
  caddy:2-alpine -c 'chown -R 10001:10001 /state'
```

Replace the placeholder with the actual volume name. Do not run this command
against the backend volume, whose application user has a different UID. Keep
existing volume names and database paths when upgrading. The service must fail
rather than fall back to root or silently discard inaccessible certificate data.

## Browser policy

SvelteKit emits a production CSP meta tag with hashes for its generated bootstrap
script. Theme initialization is a same-origin external script. Script evaluation,
inline event handlers, embedded objects, cross-origin fetch and framing are
blocked. Caddy supplies `frame-ancestors 'none'` as a response header, because
browsers do not enforce that directive in a meta tag. It also preserves nosniff,
frame denial, no-referrer, and camera permission only on the workspace document.

Same-origin scripts, worker/blob URLs, local QR images and blob downloads remain
available. Inline styles are allowed for Svelte transitions and progress values;
inline scripts are not. The scanner reviews an external destination and performs
an explicit top-level navigation; it does not fetch that external origin using
this server's credentials. CSP does not establish that client software is honest
or remove the need to prevent XSS.

After building and starting an isolated Caddy deployment, run:

```sh
cd web
PSST_TEST_BASE_URL=http://127.0.0.1:18784 \
  npx playwright test --config playwright.security.config.ts
PSST_TEST_BASE_URL=http://127.0.0.1:18784 \
  npx playwright test --config playwright.deployment.config.ts \
  scanner.spec.ts download-ack.spec.ts \
  --grep-invert 'branded download|administrator can'
```

The dedicated security tests exercise the actual compiled page, including
rejected inline/eval/handler execution, rejected cross-origin connections,
worker fallback decoding, theme initialization, external navigation, blob saves
and response headers. They must not be run against Vite's development server.

## Updates and vulnerability review

Keep application, runtime and base-image updates together in a reviewed release.
Use committed dependency locks (`npm ci`, Go module checksums and the Gradle
version catalog/locks where present); do not float crypto provider versions or
upgrade a wire format without interoperability tests. Pull/build new base images
in an isolated environment before replacing the running stack.

Run `npm audit` in `web`, a maintained `govulncheck ./...` in `backend`, and an
image scanner such as Docker Scout or Trivy on both final images. Review the
actual reachable findings and publish the tool/database versions and results;
no clean vulnerability report is claimed by this document. Include native
Android/iOS dependency advisories in release review. Run backend race tests,
web checks/unit/browser tests, Android/shared tests and APK build, and the native
iOS build/tests before release. The portable Swift harness supplements native
validation; it does not replace it.

The current release setup produces checksums, but does not establish signed
artifacts, provenance, or bit-for-bit reproducible binaries. A checksum from the
same compromised download site is not proof of authenticity. Operators should
build a reviewed commit or use a release whose independently verified signatures
are explicitly published. Preserve database policy settings, volume identities,
proxy configuration and environment files across updates; never solve an update
error by deleting the database.

## Backups and restores

Stop the stack before taking a consistent backup of the entire backend volume
(SQLite database, any WAL/journal, and encrypted file tree together), both Caddy
state volumes, and the protected deployment configuration. If using an online
backup, use SQLite's supported backup mechanism plus a coordinated file snapshot;
copying a live database file alone is insufficient. Encrypt backups, restrict
access, and account for their disk/traffic separately. The bootstrap admin
password should be removed from `.env` after initialization. Never publish
`docker compose config` output containing live secrets.

Restore into an isolated stack with the same application/schema version first.
Restore volume ownership, verify sign-in and persisted operator policy, compare
resource metadata with stored ciphertext, then test upload, download, revocation,
cleanup and a process restart before switching traffic. An old backup may restore
previous sessions/capabilities and stale traffic measurements: revoke sessions
and affected links as necessary and reconcile budgets before public access.
Do not run two writers against one SQLite/data volume.

Server backups cannot restore client-held download keys or receive private keys.
Those stay on their originating devices/browser profiles. Account sign-in or
pairing alone does not recover them. A restored file without its decryption key
is still ciphertext; this is not a server-side decryption or malware-scanning
service. Encryption does not authenticate anonymous senders or make content safe.

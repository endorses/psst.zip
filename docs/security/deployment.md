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
The backend receives a single resolved client address from bundled Caddy. Caddy
trusts no upstream gateway by default; the optional `/etc/caddy/proxy-trust/*.caddy`
glob is empty unless an operator mounts an explicit policy. With a gateway policy,
Caddy resolves the client using strict right-to-left parsing and normalizes its
outgoing `X-Forwarded-For`. Backend trust remains limited to bundled Caddy's socket
address. This follows [Caddy's proxy trust settings](https://caddyserver.com/docs/caddyfile/options#trusted_proxies)
and [header handling](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#headers).

## External TLS gateway example

[The external-proxy Compose overlay](../../deploy/external-proxy.compose.yml) adds
a separate Caddy TLS gateway in front of the bundled static/API proxy. It requires
Docker Compose 2.24.4 or newer, because ordinary list merging would leave the
inner proxy's public ports exposed. The overlay explicitly removes those ports
and replaces network membership. See [Compose merge behavior](https://docs.docker.com/reference/compose-file/merge/#replace-value).

The resulting path is external HTTPS gateway → bundled HTTP web proxy → backend.
Only the gateway publishes ports. Both private hops remain on internal networks;
the gateway also has an edge network for public certificate issuance. All three
services retain non-root execution, read-only roots, dropped capabilities, process/
memory ceilings and rotating logs. Gateway TLS state uses separate persistent
volumes. The gateway and web proxy share the same compiled image.

Use a bare public hostname in `PSST_DOMAIN` (no `http://` or `https://` prefix), set
`PUBLIC_URL=https://that-hostname`, and keep `AUTH_ALLOW_INSECURE_HTTP=false`.
The inner HTTP hop does **not** require enabling insecure authentication. The
backend's canonical origin comes from `PUBLIC_URL`, never forwarded scheme/Host
headers; web login and cookie mutations require that exact public Origin.

From the repository root, using the same Compose project name and environment
file as the intended deployment:

```sh
docker compose -f docker-compose.yml -f deploy/external-proxy.compose.yml config --quiet
docker compose -f docker-compose.yml -f deploy/external-proxy.compose.yml up -d --build
```

Changing the project name also changes named-volume identities. Preserve it during
an upgrade and take the documented stopped backup first. Do not start this gateway
beside an existing proxy already listening on the same public ports.

The extra internal subnet defaults to `172.30.194.0/29`: gateway `.2`, web proxy `.3`.
For an overlap, change `PSST_EXTERNAL_PROXY_SUBNET`, `PSST_EXTERNAL_PROXY_IP` and
`PSST_WEB_PROXY_IP` together. The web proxy trusts only the gateway's exact `/32`;
the backend still trusts only the web proxy's address on the original backend
network. These IPv4 example addresses are not general trusted-network ranges.

If integrating an existing external proxy, keep backend/web listeners private,
attach the gateway to the intended proxy network, and replace the example gateway
address with its actual socket source. Route every path through the web proxy,
preserve the public Host/scheme, overwrite client-supplied forwarding headers, and
support streamed uploads/responses and SSE. Never substitute a broad private CIDR
for the exact gateway policy. Operator-specific gateways, CDNs and IPv6 layouts
still need their own integration checks.

The repository's disposable TLS/proxy/restore check is:

```sh
python3 tools/test_external_proxy.py
```

It builds current sources under a unique project, generates a private **test-only**
certificate authority, verifies certificates in isolated clients, and removes its
containers, volumes, image tags and temporary files. It does not install trust on
the host or in an APK, use a certificate-verification bypass, or modify the live
development instance. Public deployment uses Caddy's automatic HTTPS rather than
this test certificate. Public ACME issuance and renewal remain separate gates.

The gate verifies real first-frame SSE delivery, canonical-origin authentication,
secure cookies, control-plane restart/restore and client-address throttling with
two distinct container peers. Varying forged forwarding headers cannot evade the
same-peer bucket or combine independent peers. The restore fixture covers backend
settings, pause, sessions and receive-slot revocation; it does not restore file
ciphertext, client keys or gateway certificate state. Docker/Compose, OpenSSL and
image build/pull access are required. The helper Python image remains in Docker's
shared dependency cache; uniquely tagged application test images are removed.

The separate managed-certificate storage check is:

```sh
python3 tools/test_external_proxy.py --certificate-state
```

This mode uses Caddy's internal issuer on the disposable gateway, stops its sole
writer, copies both `/data` and `/config` into backups, and restores new working
volumes. It verifies content hashes, ownership and permissions, preserves the
originals and backups unchanged, and checks the original leaf certificate and
client trust after recreation and another restart. Authenticated access must
continue with the original backend session. Only the test client explicitly trusts
the disposable CA; fixture-only `skip_install_trust` prevents even container
trust installation. See [Caddy's local HTTPS storage](https://caddyserver.com/docs/automatic-https#local-https)
and [trust installation option](https://caddyserver.com/docs/caddyfile/options#skip_install_trust).
No production TLS configuration is changed. Internal issuance does not use public
ACME or prove public renewal, DNS/challenge reachability, or an operator's actual
backup. Use the [cold backup runbook](backup-restore.md) for deployment-specific
state capture and protected restore.

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

The backend image and CI use Go 1.26.8. The module's `go 1.23.0` language
declaration does not promise security support for an old Go toolchain. Choose a
patched release within [Go's supported release window](https://go.dev/doc/devel/release#policy),
and record the actual compiler in the delivered binary with `go version -m`.
Keep the Docker builder, CI and release-build toolchain aligned. Base-image tags
can move; record the resolved image digests for the candidate release and retain
the matching rollback images instead of assuming the same tag reproduces them.

Run `npm audit` in `web`, a maintained `govulncheck ./...` in `backend`, and an
image scanner such as Docker Scout or Trivy on both final images. Review the
actual reachable findings and publish the tool/database versions and results;
no clean vulnerability report is claimed by this document. Include native
Android/iOS dependency advisories in release review. Run backend race tests,
web checks/unit/browser tests, Android/shared tests and APK build, and the native
iOS build/tests before release. The portable Swift harness supplements native
validation; it does not replace it.

For the currently tested source scanner/toolchain combination, from `backend`:

```sh
GOTOOLCHAIN=go1.26.8 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go version -m /actual/candidate/server
govulncheck -mode=binary /actual/candidate/server
```

Refresh the scanner version during release review. A scanner/package-loading
failure is not a clean result; use a compatible maintained parser/toolchain and
scan the actual candidate binary as well. A source scan with a newer compiler
does not establish that an older compiled artifact has patched standard-library
code. Findings classified as uncalled still need a documented applicability
decision; a zero reachable-symbol result is not a guarantee against unknown flaws.

For web updates, review the audit before changing the lockfile, then verify a
fresh install and the actual static output:

```sh
cd web
npm audit
npm audit fix --package-lock-only --ignore-scripts
npm ci
npm audit
npm run check
npm test
npm run build
```

Do not automatically force framework-major upgrades or silence audit failures.
The current SvelteKit 2 dependency graph uses a scoped `cookie: 0.7.2` override
for [the cookie field-validation advisory](https://github.com/advisories/GHSA-pxg6-pf52-xh8x).
Review/remove that override when upstream ships a compatible patched dependency;
test it again before a major framework upgrade. Crypto providers remain pinned
independently of routine tooling updates. The supplied web image contains the
compiled static site served by Caddy, not a public Vite or SvelteKit Node server.
Development-tool advisories and shipped-browser advisories require separate
applicability decisions, even when both are fixed in the same lockfile update.

`.goreleaser.yml` configures `checksums.txt`; this is configuration evidence, not
proof that a candidate release was built or verified. It does not establish signed
artifacts, provenance, or bit-for-bit reproducible binaries. A checksum from the
same compromised download site is not proof of authenticity. Operators should
build a reviewed commit or use a release whose independently verified signatures
are explicitly published. Preserve database policy settings, volume identities,
proxy configuration and environment files across updates; never solve an update
error by deleting the database.

Before replacing an instance, take the [stopped complete backup](backup-restore.md)
and test the candidate with fresh disposable volumes. Use the installation's
existing Compose project/files/environment and actual volume mappings when
switching images. Do not reset `.env`, trusted-proxy settings, resource/traffic
budgets, factor state or database volumes to make an upgrade succeed. Verify
those persisted settings, sign-in/recent proof, paused-state behavior and cleanup
after restart before deliberately resuming public traffic. Use the protected
matching backup and images for rollback; starting an old binary against a newer
schema is not a supported substitute for restore.

## Backups and restores

Use the [cold backup and isolated restore runbook](backup-restore.md) for exact
copy/pause steps, rollback implications and the disposable repository exercises.

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

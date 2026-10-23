# Temporary LAN sharing from the psst.zip CLI

Created: 2026-10-23. Status: **implementation in progress**.
Implementation branch: `feat/cli-lan-sharing`. This document does not authorize a release,
production deployment, firewall change or history rewrite.

## Goal and agreed scope

Let the existing `psst` executable serve temporary Send and Receive links directly
from the user's computer. Recipients use a browser; neither party needs a hosted
psst.zip account, Docker or a permanent server. The host must remain running and
reachable for the session's lifetime.

This mode targets trusted LANs. HTTP is the default and requires no certificate.
Optional HTTPS accepts an operator-supplied certificate and private key; receiving
devices must trust the certificate. Do not generate a self-signed certificate and
present it as automatically trusted HTTPS.

Normal `psst send`, `psst receive`, account profiles and configured servers retain
their existing behavior. Internet transfers continue to use the hosted backend.
Automatic tunnels, relays, NAT traversal, UPnP, public hosting, discovery services,
permanent accounts and cross-process session resumption are outside this initial
scope. Links from a stopped session cannot be resumed in a new session.

## Verified starting point

| Area         | Existing implementation                                                                                  | Implication                                                                                                                        |
| ------------ | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| CLI          | Independent Go module in `client/`, with hosted transfer commands, JSON output and terminal QR rendering | Add an explicit `serve` command family; reuse output and link handling.                                                            |
| Encryption   | `client/internal/crypto/`, existing browser providers and cross-client vectors                           | Preserve formats and reuse encryption; do not design a new cryptosystem.                                                           |
| Backend      | `backend/internal/api/server.go`, database/store packages and resumable upload handler                   | Existing handlers depend on accounts and database services; importing the internal packages from `client/` is not permitted by Go. |
| Browser UI   | `web/svelte.config.js` uses the static adapter with an SPA fallback                                      | Build and embed transfer screens and their assets into the executable.                                                             |
| LAN crypto   | `web/src/lib/crypto.ts` and `receive-crypto.ts` support operation without `crypto.subtle`                | Existing providers can support LAN HTTP; verify the complete browser flows.                                                        |
| Builds       | Root/client Makefiles and `client/scripts/package-release.py` build the CLI                              | Extend both developer and release builds to include matching browser assets.                                                       |
| CI selection | `tools/select_ci_checks.py` selects backend, web and CLI checks by changed paths                         | Embedded asset dependencies must trigger the appropriate checks without making native builds a LAN-release prerequisite.           |

## User experience and command contract

Proposed commands; finalize consistent flag names before implementation:

```sh
psst serve send ./document.pdf --qr
psst serve send ./photos/*.jpg --expires 1h --max-downloads 1
psst serve receive --output-dir ./incoming --qr
psst serve receive --output-dir ./incoming --max-files 20 --max-bytes 1GiB

# Select explicitly when multiple network interfaces are available.
# Port 0 asks the operating system to allocate an available port.
psst serve send ./document.pdf --listen 192.168.1.42:0 --qr

# Optional TLS; these files must contain a certificate trusted by recipients.
psst serve receive --output-dir ./incoming --listen 192.168.1.42:8443 \
  --tls-cert ./lan.crt --tls-key ./lan.key
```

- [ ] Serve one Send transfer or one Receive inbox per invocation, in the
      foreground, with a visible shutdown instruction. Never fall back to a hosted
      upload if local serving fails.
- [ ] Require no login, profile initialization or credential-store access. The
      local process owns the session; no public account/admin UI is needed.
- [ ] Reuse existing canonical `/d/{id}#...` and `/u/{id}#v2....` links, terminal QR
      rendering and saved Send/Receive JSON. Print a link only after preparation
      succeeds and the listener is ready. QR codes contain the advertised LAN
      origin, never `0.0.0.0` or the recipient's `localhost`.
- [ ] Select an unambiguous usable LAN interface automatically. If selection is
      ambiguous, prompt interactively or require `--listen` in noninteractive
      mode. Default to a specific private address rather than all interfaces;
      allow loopback explicitly for local use and tests. Handle IPv6 URLs correctly.
- [ ] Request an available port from the operating system by binding port `0` by
      default. Keep that listener open and use its actual bound port in every
      advertised origin, share link, QR code and JSON result. Do not probe a free
      port and close it before binding, which introduces a race. A busy conventional
      port must not prevent default startup. Only require a particular port when
      explicitly configured; if that port is occupied, explain the conflict and
      suggest port `0` rather than silently changing an explicit configuration.
- [ ] Initially reject public-address and wildcard binds, and do not open firewall
      ports automatically. Explain reachability failures, guest-Wi-Fi isolation,
      VPN interface ambiguity and local firewall requirements in troubleshooting.
- [ ] Display the selected address, expiry and limits, plus a concise trusted-LAN
      HTTP notice. Distinguish file encryption from authentication of the page.
      Do not claim HTTP protects against an active network attacker replacing the
      browser code. Optional TLS must not add certificate-bypass behavior.
- [ ] Adopt finite documented defaults: one-hour session lifetime, one successful
      Send download, at most 100 Receive files, a 1 GiB total encrypted-data budget
      and two concurrent payload streams. Allow explicit finite overrides and
      preflight Send inputs against the budget before publishing a link. Validate
      all limits and durations before listening.
- [ ] Define session expiry independently of the current global ten-minute CLI
      command timeout. Reuse explicit `--timeout` as an earlier whole-process
      deadline when supplied; use `--expires` for the normal serving lifetime.
      Preserve the timeout default for all existing commands.
- [ ] In `--json` mode emit one versioned startup result with the complete share
      link, resource identity, expiry and hosting metadata, then remain serving.
      Preserve compatibility with `psst qr --json-file`. Send progress and final
      status to stderr, never a second JSON result on stdout; document that process
      exit status determines final success. Noninteractive mode never prompts.

## Reuse and architecture

Expose a narrow public embedding interface, proposed as `backend/standalone/`,
which can legally use the backend's internal packages. Extract the common transfer
operations inside the backend as needed and let both hosted handlers and the
standalone interface call them. Avoid a second implementation of admission,
resumable uploads, acknowledgement counting or expiry semantics.

The resulting shared transfer core is used by both modes. Keep hosted account,
administrator and deployment policy outside that core, and supply each mode's
ownership/admission policy at a narrow boundary. Do not copy the old transfer
implementation into the LAN mode or make the hosted backend depend on the CLI.

The embedding interface owns its configuration and lifecycle explicitly. It must
not read production environment configuration, run administrator bootstrapping,
start hosted incident/account workers or expose the full hosted router. Reuse
database/storage components only where useful; retain SQLite if that is the
smallest way to preserve transfer semantics, without inventing a parallel database.
Any internal owner identity is private implementation state, not a network login.

- [x] Define configuration, owner operations, restricted HTTP handler, readiness,
      cancellation and cleanup boundaries. Restrict network routes to the active
      session and its required guest/browser APIs; public requests cannot create
      unrelated resources or invoke owner, account or administrator operations.
- [ ] Keep CLI encryption and private keys on the owner side of this interface.
      Serve encrypted manifests and payloads; never serialize private keys into
      database rows, browser assets, logs or network management endpoints.
- [ ] Keep the backend independent of the client module to prevent a dependency
      cycle. Pin the client's public embedding dependency to an immutable module
      version/revision; verify module resolution and source archives without local
      `replace` directives. Development workspace conveniences must not be needed
      by users building a release.
- [ ] Measure the effect of reused backend dependencies on binary size and build
      time. Preserve the existing CGO-free Linux/macOS/Windows target matrix; do
      not introduce a system SQLite, runtime Node installation or Docker requirement.
- [ ] Build a standalone web entry using the existing Send/Receive screens,
      providers and shared components. Include only the required routes and
      configuration, with no hosted login/settings navigation or external assets.
      Serve UI and API from the same origin with the existing relevant security
      headers and correct SPA fallback behavior.
- [ ] Embed generated web assets with Go's embedding support. Record the matching
      source revision and asset digest. Make ordinary/release/install targets
      prepare missing or stale assets efficiently; never silently embed placeholder
      files. Document Node as a source-build prerequisite, not an installed-binary
      prerequisite. Build assets once per source revision for cross-compilation.
- [ ] Update source archives, dependency inventories, notices and source-access
      information for the embedded web/backend code under existing repository
      licensing. Do not change licenses as part of this feature.

## Transfer behavior and local storage

Send uses a private encrypted snapshot/spool prepared from the explicitly selected
files. Reuse the existing chunk framing and encrypted manifest. A later change to
the original file must not change a published transfer or produce mismatched
ciphertext. Do not expose an arbitrary directory tree or a general file server.

Receive uses the current Receive-v2 invitation/public-key and submission-envelope
format. The browser encrypts uploads; only the CLI holds the private inbox key.
The CLI collects completed submissions, authenticates and decrypts them, and writes
the selected output directory. Partial or invalid uploads never appear as complete
plaintext files.

- [ ] Implement Send snapshot preparation, manifest/payload serving, transfer
      expiry and acknowledgement-based download counting using existing semantics.
      Account for concurrent requests without double-counting or exceeding limits.
- [ ] Implement Receive creation, guest resumable uploads and local collection.
      Enforce per-file, total-file, byte and concurrency budgets atomically,
      including reservation/release of capacity for partial uploads.
- [ ] Write into a private session directory with restrictive permissions or
      platform-equivalent protections. Preflight space for encrypted staging and
      decrypted outputs, enforce bounds while streaming and report disk-full
      failures without corrupting completed files. Do not load entire files into RAM.
- [ ] Reuse safe output-name handling: prevent traversal, symlink escape and
      overwriting existing files. Authenticate all encrypted chunks before
      promoting a completed plaintext file. Sanitize remote names in terminal
      output. Retain successfully received files when the session ends.
- [ ] Keep private keys in memory for the session; avoid account-vault prompts and
      persistent reusable sessions. Document that forced termination loses private
      session keys and can leave temporary ciphertext for later removal.
- [ ] On expiry, exhaustion of the Send budget or interruption, stop accepting new
      work, bound draining of active streams, close storage handles and remove only
      this session's staging data. Define exit statuses for completed transfer,
      expiry with no completion, interruption and operational failure.
- [ ] Handle crash leftovers through narrowly scoped session markers and ownership
      checks. Never remove another active session's data, source inputs or received
      files, and never offer secure-erasure guarantees for SSDs or memory.

## Compatibility and exposure boundaries

This feature changes CLI serving and browser entry points, not the encryption or
link protocol. Existing hosted workflows must continue working unchanged.

- [ ] Preserve manifest fields, Receive-v2 envelopes, chunk framing, resumable
      upload behavior and download acknowledgements. Use existing protocol vectors
      and guest client operations as compatibility checks.
- [ ] Validate Host/Origin and cross-origin mutation behavior for the selected LAN
      origin; do not trust forwarded headers or enable wildcard credentialed CORS.
      Prevent arbitrary websites from using the LAN service as an unrestricted
      upload sink. Share capabilities are intentionally exposed only in requested
      link/QR/JSON output; redact them from routine request/error logs.
- [ ] Review browser pages for HTTP-only limitations beyond encryption, such as
      clipboard and download APIs. Provide equivalent fallbacks rather than
      requiring an account or a hosted page.
- [ ] Preserve Android and iOS interoperability. Review LAN link classification,
      cleartext-network policies and guest Send/Receive flows on both platforms;
      implement any applicable mobile changes in both. Account pairing to this
      temporary server is outside scope. Do not claim native runtime validation
      without running it; record the device/simulator commands and pending checks.

## Efficient verification and delivery

Use existing suites for established cryptography and hosted behavior. Add tests
only for new failure modes and integration boundaries, with injected clocks and
small payloads. Avoid duplicated full suites, sleep-based expiry tests and native
builds as a prerequisite for every CLI artifact.

- [ ] Add focused Go checks for address/flag validation, automatic port allocation,
      listener readiness,
      session cancellation, route isolation, capacity races, safe output paths and
      cleanup. Exercise actual Send/Receive HTTP flows with small encrypted files.
      Verify simultaneous default sessions receive distinct ports and advertise
      their actual listeners even while a conventional port is occupied.
- [ ] Add a small browser set against the built embedded executable: HTTP Send
      download with matching plaintext, HTTP Receive upload with matching CLI
      output, plus expiry/limit enforcement. Exercise the LAN crypto fallback;
      loopback HTTP alone may expose Web Crypto and miss that path. Prove offline
      asset loading with external requests blocked.
- [ ] Verify supplied-certificate HTTPS, missing/mismatched TLS inputs, port-in-use
      errors for explicitly requested ports and startup failures without publishing
      unusable links. Reuse existing
      fixtures rather than external certificate services or public servers.
- [ ] Extend Makefile/source-build smoke checks and cross-build all existing CLI
      targets. Check assets in release archives, notices, source identity and QR
      JSON compatibility. Keep these checks in the existing CLI pipeline where
      practical; record timings and investigate material regressions.
- [ ] Update CI change selection so shared transfer/backend and embedded web changes
      run the necessary backend/web/CLI checks. Run native CI for applicable mobile
      changes using the established workflow, not as an extra gate on every LAN
      binary build. Keep unrun platform checks explicitly pending.
- [ ] Document commands, HTTP trust assumptions, optional TLS, interface selection,
      lifetime, JSON startup behavior, limits, disk use, shutdown and crash cleanup
      and automatic port allocation in `docs/cli.md` and introduce LAN sharing in
      the README/help.
- [ ] Perform one real two-device LAN Send and Receive verification without a
      hosted account or reachable hosted server. Record platforms and results;
      browser/native manual checks are a one-time feature validation, not a new
      mandatory checklist for every release.
- [ ] Format changes, complete relevant checks, record implementation/validation
      evidence here and commit the implementation. Publish or deploy only when
      separately authorized.

## Completion criteria

The feature is complete when one installed binary can create a reachable temporary
LAN Send or Receive link without credentials or certificate setup; a second
device can use the browser UI to transfer and recover matching plaintext; limits
and shutdown are enforced; received files survive cleanup; hosted behavior remains
compatible; and source/release builds include the correct assets and notices.
Native validation and any unavailable platform checks remain explicitly documented
until performed. Unchecked tasks remain pending.

## Implementation evidence

Backend embedding now uses `backend/standalone` and the shared
`api.transferRoutes` registration. Standalone recorder tests cover the actual
hosted handlers for Send acknowledgements, Receive-v2 submission, resource/route
isolation, Host/Origin checks, expiry, capacity races and active stream limits.
The seven standalone checks and storage checks pass under the race detector.
Hosted API race tests pass (169.6 seconds locally); the entire database package
passes normally (6.9 seconds), with targeted database capacity/download/file-list
race regressions passing (5.8 seconds). A broad concurrent race run hit its
three-minute database-package limit during fixture setup; this is recorded as an
incomplete full race run, not a passing result. No database query semantics were
changed for LAN mode.

Backend facade cross-builds pass for Windows amd64 and macOS arm64 with CGO
disabled. The existing Unix storage behavior is retained; Windows storage now
uses handle-relative no-reparse opens and bounded deletion. Windows directory
flush semantics differ from Unix; native Windows runtime checks remain pending.

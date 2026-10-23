# Temporary LAN sharing from the psst.zip CLI

Created: 2026-10-23. Status: **implemented and verified locally; external native runtime checks pending**.
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

- [x] Serve one Send transfer or one Receive inbox per invocation, in the
      foreground, with a visible shutdown instruction. Never fall back to a hosted
      upload if local serving fails.
- [x] Require no login, profile initialization or credential-store access. The
      local process owns the session; no public account/admin UI is needed.
- [x] Reuse existing canonical `/d/{id}#...` and `/u/{id}#v2....` links, terminal QR
      rendering and saved Send/Receive JSON. Print a link only after preparation
      succeeds and the listener is ready. QR codes contain the advertised LAN
      origin, never `0.0.0.0` or the recipient's `localhost`.
- [x] Select an unambiguous usable LAN interface automatically. If selection is
      ambiguous, prompt interactively or require `--listen` in noninteractive
      mode. Default to a specific private address rather than all interfaces;
      allow loopback explicitly for local use and tests. Handle IPv6 URLs correctly.
- [x] Request an available port from the operating system by binding port `0` by
      default. Keep that listener open and use its actual bound port in every
      advertised origin, share link, QR code and JSON result. Do not probe a free
      port and close it before binding, which introduces a race. A busy conventional
      port must not prevent default startup. Only require a particular port when
      explicitly configured; if that port is occupied, explain the conflict and
      suggest port `0` rather than silently changing an explicit configuration.
- [x] Initially reject public-address and wildcard binds, and do not open firewall
      ports automatically. Explain reachability failures, guest-Wi-Fi isolation,
      VPN interface ambiguity and local firewall requirements in troubleshooting.
- [x] Display the selected address, expiry and limits, plus a concise trusted-LAN
      HTTP notice. Distinguish file encryption from authentication of the page.
      Do not claim HTTP protects against an active network attacker replacing the
      browser code. Optional TLS must not add certificate-bypass behavior.
- [x] Adopt finite documented defaults: one-hour session lifetime, one successful
      Send download, at most 100 Receive files, a 1 GiB total encrypted-data budget
      and two concurrent payload streams. Allow explicit finite overrides and
      preflight Send inputs against the budget before publishing a link. Validate
      all limits and durations before listening.
- [x] Define session expiry independently of the current global ten-minute CLI
      command timeout. Reuse explicit `--timeout` as an earlier whole-process
      deadline when supplied; use `--expires` for the normal serving lifetime.
      Preserve the timeout default for all existing commands.
- [x] In `--json` mode emit one versioned startup result with the complete share
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
- [x] Keep CLI encryption and private keys on the owner side of this interface.
      Serve encrypted manifests and payloads; never serialize private keys into
      database rows, browser assets, logs or network management endpoints.
- [x] Keep the backend independent of the client module to prevent a dependency
      cycle. Pin the client's public embedding dependency to an immutable module
      version/revision; verify module resolution and source archives without local
      `replace` directives. Development workspace conveniences must not be needed
      by users building a release.
- [x] Measure the effect of reused backend dependencies on binary size and build
      time. Preserve the existing CGO-free Linux/macOS/Windows target matrix; do
      not introduce a system SQLite, runtime Node installation or Docker requirement.
- [x] Build a standalone web entry using the existing Send/Receive screens,
      providers and shared components. Include only the required routes and
      configuration, with no hosted login/settings navigation or external assets.
      Serve UI and API from the same origin with the existing relevant security
      headers and correct SPA fallback behavior.
- [x] Embed generated web assets with Go's embedding support. Record the matching
      source revision and asset digest. Make ordinary/release/install targets
      prepare missing or stale assets efficiently; never silently embed placeholder
      files. Document Node as a source-build prerequisite, not an installed-binary
      prerequisite. Build assets once per source revision for cross-compilation.
- [x] Update source archives, dependency inventories, notices and source-access
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

- [x] Implement Send snapshot preparation, manifest/payload serving, transfer
      expiry and acknowledgement-based download counting using existing semantics.
      Account for concurrent requests without double-counting or exceeding limits.
- [x] Implement Receive creation, guest resumable uploads and local collection.
      Enforce per-file, total-file, byte and concurrency budgets atomically,
      including atomic partial-upload reservations. Receive admission allowances are cumulative for the session; deleting an upload does not refund that allowance, preserving the hosted quota contract.
- [x] Write into a private session directory with restrictive permissions or
      platform-equivalent protections. Preflight space for encrypted staging and
      decrypted outputs, enforce bounds while streaming and report disk-full
      failures without corrupting completed files. Do not load entire files into RAM.
- [x] Reuse safe output-name handling: prevent traversal, symlink escape and
      overwriting existing files. Authenticate all encrypted chunks before
      promoting a completed plaintext file. Sanitize remote names in terminal
      output. Retain successfully received files when the session ends.
- [x] Keep private keys in memory for the session; avoid account-vault prompts and
      persistent reusable sessions. Document that forced termination loses private
      session keys and can leave temporary ciphertext for later removal.
- [x] On expiry, exhaustion of the Send budget or interruption, stop accepting new
      work, bound draining of active streams, close storage handles and remove only
      this session's staging data. Define exit statuses for completed transfer,
      expiry with no completion, interruption and operational failure.
- [x] Handle crash leftovers through narrowly scoped session markers and ownership
      checks. Never remove another active session's data, source inputs or received
      files, and never offer secure-erasure guarantees for SSDs or memory.

## Compatibility and exposure boundaries

This feature changes CLI serving and browser entry points, not the encryption or
link protocol. Existing hosted workflows must continue working unchanged.

- [x] Preserve manifest fields, Receive-v2 envelopes, chunk framing, resumable
      upload behavior and download acknowledgements. Use existing protocol vectors
      and guest client operations as compatibility checks.
- [x] Validate Host/Origin and cross-origin mutation behavior for the selected LAN
      origin; do not trust forwarded headers or enable wildcard credentialed CORS.
      Prevent arbitrary websites from using the LAN service as an unrestricted
      upload sink. Share capabilities are intentionally exposed only in requested
      link/QR/JSON output; redact them from routine request/error logs.
- [x] Review browser pages for HTTP-only limitations beyond encryption, such as
      clipboard and download APIs. Provide equivalent fallbacks rather than
      requiring an account or a hosted page.
- [x] Preserve Android and iOS interoperability. Review LAN link classification,
      cleartext-network policies and guest Send/Receive flows on both platforms;
      implement any applicable mobile changes in both. Account pairing to this
      temporary server is outside scope. Do not claim native runtime validation
      without running it; record the device/simulator commands and pending checks.

## Efficient verification and delivery

Use existing suites for established cryptography and hosted behavior. Add tests
only for new failure modes and integration boundaries, with injected clocks and
small payloads. Avoid duplicated full suites, sleep-based expiry tests and native
builds as a prerequisite for every CLI artifact.

- [x] Add focused Go checks for address/flag validation, automatic port allocation,
      listener readiness,
      session cancellation, route isolation, capacity races, safe output paths and
      cleanup. Exercise actual Send/Receive HTTP flows with small encrypted files.
      Verify simultaneous default sessions receive distinct ports and advertise
      their actual listeners even while a conventional port is occupied.
- [x] Add a small browser set against the built embedded executable: HTTP Send
      download with matching plaintext, HTTP Receive upload with matching CLI
      output, plus expiry/limit enforcement. Exercise the LAN crypto fallback;
      loopback HTTP alone may expose Web Crypto and miss that path. Prove offline
      asset loading with external requests blocked.
- [x] Verify supplied-certificate HTTPS, missing/mismatched TLS inputs, port-in-use
      errors for explicitly requested ports and startup failures without publishing
      unusable links. Reuse existing
      fixtures rather than external certificate services or public servers.
- [x] Extend Makefile/source-build smoke checks and cross-build all existing CLI
      targets. Check assets in release archives, notices, source identity and QR
      JSON compatibility. Keep these checks in the existing CLI pipeline where
      practical; record timings and investigate material regressions.
- [x] Update CI change selection so shared transfer/backend and embedded web changes
      run the necessary backend/web/CLI checks. Run native CI for applicable mobile
      changes using the established workflow, not as an extra gate on every LAN
      binary build. Keep unrun platform checks explicitly pending.
- [x] Document commands, HTTP trust assumptions, optional TLS, interface selection,
      lifetime, JSON startup behavior, limits, disk use, shutdown and crash cleanup
      and automatic port allocation in `docs/cli.md` and introduce LAN sharing in
      the README/help.
- [x] Perform one real two-device LAN Send and Receive verification without a
      hosted account or reachable hosted server. Record platforms and results;
      browser/native manual checks are a one-time feature validation, not a new
      mandatory checklist for every release.
- [x] Format changes, complete relevant checks, record implementation/validation
      evidence here and commit the implementation. Publish or deploy only when
      separately authorized.

## Completion criteria

The feature is complete when one installed binary can create a reachable temporary
LAN Send or Receive link without credentials or certificate setup; a second
device can use the browser UI to transfer and recover matching plaintext; limits
and shutdown are enforced; received files survive cleanup; hosted behavior remains
compatible; and source/release builds include the correct assets and notices.
Native validation and any unavailable platform checks remain explicitly documented
until performed. Unchecked tasks remain pending; external runtime checks are listed separately below.

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

The CLI now provides `serve send`, `serve receive`, and narrowly scoped crashed
session cleanup. It binds a specific private address at OS-selected port `0`,
retains the listener and advertises its actual port; explicit occupied ports fail
with a useful diagnostic. Docker-only bridges are omitted from automatic choices,
while explicit private binds and normal LAN bridges remain supported. No keyring
or account state is opened. Intentional Ctrl+C/SIGTERM stops cleanly with exit 0; explicit command deadlines remain errors, and stopped sessions cannot resume.

The standalone browser build reuses the production transfer screens and crypto
providers, with separate account-free routing and no external assets. Two actual
executable/browser cases pass in about two seconds, matching Send/Receive plaintext
with external requests blocked and secure-context crypto/clipboard APIs disabled.
They also reject hosted account/admin routes and check automatic limit shutdown
and retained output. HTTP browser saves retain the existing 25 MiB per-file
bounded-memory limit; optional trusted HTTPS or CLI/native recipients support
larger downloads. This limitation is displayed and documented.

CLI tests cover immutable Send snapshots, Receive-v2 collection with safe
submission directories, private ownership/route boundaries, simultaneous random
ports and explicit busy ports, verified supplied TLS, cancellation, active/crashed
session locks, symlink rejection, streaming owner transport and finite settings.
Review found and fixed transient Receive capacity errors incorrectly stopping the
session, and supplied certificates not checked for IP/date/server usage. The
whole client race suite passes; the LAN package takes about eleven seconds with
race instrumentation. Hosted CLI/browser-provider interoperability also passes
(12.1 seconds), including multi-frame/empty files, key recovery, receipt retry,
expiry, revocation and allowances.

On 2026-10-23 the user supplied the real phone/LAN test results: Send acknowledged
one completed download; Receive authenticated one file, retained it after Ctrl+C
and stopped. This establishes device-to-device reachability without the hosted
server. Byte-for-byte matching and crypto fallback are independently verified in
the executable browser scenarios; do not interpret an acknowledgement alone as
independent forensic proof of file contents.

Release builds pin backend commit `c1693a02d45f68415ada1c3a991a2816c95454c5`
through `v0.0.0-20261023195706-c1693a02d45f` and its Go checksum. The development
workspace is confined to `client/go.work`, with a version-specific local override;
packaging explicitly uses `GOWORK=off`. Before this branch is published, local
immutable-module checks use a task-owned file proxy made from that exact commit;
no remote publication is implied. Developer Make builds use the checked-out
backend without requiring publication of that commit.

Six CGO-free targets (Linux, macOS, Windows, each amd64/arm64) package successfully;
the stripped Linux executable is about 19 MiB. The source archive includes the
actual pinned backend source, browser preferred source/lockfile, generated assets,
notices and vendored Go packages. Its recorded source file hashes were verified,
and its CLI rebuilt successfully with an empty module cache, `GOPROXY=off`,
`GOSUMDB=off`, `GOWORK=off` and `-mod=vendor`. This verifies an offline source build,
not bit-identical output across module-cache versus vendor build settings.

Build review additionally fixed stale npm installation reuse after lock changes,
POSIX-only LAN npm scripts on Windows and stale Go toolchain notice caching. Two
small filesystem/cache regressions pass in milliseconds; the portable Node wrapper
has been exercised for the actual LAN build and check. Browser builds are cached
by source inputs, while Go-notice inputs include the resolved toolchain/legal
texts. Unchanged CLI Go edits do not cause a new browser bundle build. Release
packaging builds the assets once for all targets and reuses executed CI checks;
only two LAN browser scenarios were added to CI. Their Chromium setup downloads
only the headless shell and uses the Ubuntu runner's existing shared libraries;
it does not reinstall OS dependencies or extra font packages within the bounded
five-minute CLI job. Shared web inputs select CLI
checks; hosted-only routes/tests do not. Existing native checks are not release
publication prerequisites for these CLI artifacts.

### External validation explicitly pending

Android and iOS source review found no required feature changes: shared
`UrlHelper`, `ScanInputClassifier` and `ServerOrigin` retain HTTP/private/IPv6
links and guest APIs, Android already permits LAN/cleartext access, and iOS app and
share extension already declare the LAN usage/transport settings. Account pairing
to a temporary host remains unsupported. The browser test on the user's phone is
not a native-app validation pass.

Native Android/iOS guest transfer checks and native macOS/Windows executable
runtime checks remain external validation; cross-compilation is recorded separately.
They do not block delivering the LAN browser feature, and no platform runtime pass
has been invented. When those environments are available:

```sh
# Existing shared mobile guest/link regressions (Android SDK/JDK required).
./android/gradlew -p android :shared:testAndroidHostTest \
  --tests 'zip.psst.shared.api.GuestApiTest' \
  --tests 'zip.psst.shared.api.ServerValidationTest'

# macOS with Xcode: generate the project, then run native tests.
# Follow ios/README.md for prerequisites; replace SIMULATOR_UDID with an installed device.
(cd ios && xcodegen generate)
xcrun simctl list devices available
xcodebuild -project ios/Psst.xcodeproj -scheme Psst \
  -destination 'platform=iOS Simulator,id=SIMULATOR_UDID' test
```

For each native app, scan/open a LAN Send and Receive link, verify the selected
origin and recovered file, and confirm the configured account/server did not
change. On native macOS/Windows, build using the documented asset-preparation
command (or extract the matching packaged binary), run Send and Receive on a
specific LAN address with port `0`, verify optional trusted TLS and interrupt a
session to check output retention/staging cleanup. The external runtime checks do
not require another production rollout or new CI suite.

Final packaged-source runtime verification found a source-selection defect: a
broad vendor-directory exclusion also removed embedded Go legal texts under
`site/licenses/backend/go/src/vendor/`. The source copier now preserves every
embedded asset, including those legal texts, and a focused fixture regression
protects that boundary. This is separate from the ordinary Go vendor directory,
which is still populated from the immutable module graph during packaging.

The user's final shutdown clarification is implemented: a Receive invitation may
remain open for more uploads, and Ctrl+C/SIGTERM is a successful explicit stop.
Cleanup failures still report failure. An explicit `--timeout` is distinguished
from session expiry and reports exit 5; expiry without completed work remains
exit 4. Deterministic context tests and real-process signal/timeout checks cover
these distinct outcomes.

### Closure decision

**CLOSED_WITH_DEFERRALS** for native platform runtime checks listed above. No
required local implementation remains. The single bounded review and its affected
verification are complete; no further assurance campaign is required to use this
feature.

Implementation is committed as `c1693a0` (shared backend), `7312f59` (LAN CLI/web
and build integration) and `78080ed` (graceful shutdown and complete embedded
source assets). The final clean six-target packaging check at `78080ed` passed in
23.4 seconds on the warm local build cache. All archive checksums, 2,922 source
file hashes and 101 embedded asset/notice entries were verified. The repaired
source archive rebuilt with an empty module cache and networking disabled; both
actual browser transfer scenarios then passed against that rebuilt executable in
1.7 seconds. This explicitly covers the archive's runtime assets, not only Go
compilation.

The three tiny build-input/source-selection regressions pass, as do affected
CLI/LAN race checks and actual SIGINT/SIGTERM/explicit-timeout checks. Graceful
signals exit 0 with one startup JSON document and no error; explicit timeout exits
5 without a second JSON document. Successfully received files remain in place.
The unrelated untracked store research drafts and the user's `incoming/` data were
preserved. No tag, push, GitHub release, firewall change or production deployment
was performed. GitHub execution of the updated workflows awaits a separately
authorized push; local gates were exercised directly.

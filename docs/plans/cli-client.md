# Go client library and psst.zip CLI

Created: 2026-10-21. Status: **implemented locally; external validation pending**.
Working branch: `feat/cli`, based on `main` at `3631465`. The separate F-Droid
submission work remains on `prepare/fdroid-submission`.

## Goal and agreed scope

Add a reusable Go client library and a thin `psst` executable for interactive,
scripted and agent-driven use of an operator-selected psst.zip server. Support
encrypted Send, direct download, Receive invitations and inbox collection, resource
management, and terminal QR codes. Saved CLI JSON containing a Send or Receive link
must also be usable to display that link as a QR code without logging in or
contacting the server.

The CLI is another client of the existing API. Keep application encryption on the
client, preserve existing wire formats and account permissions, and keep server
configuration, SQLite and server-side authorization in the backend. This work
does not deploy production, change mobile licensing, rewrite history, or publish
a release merely because code is committed.

## Verified starting point

| Area                  | Existing implementation                                                                             | Consequence for this plan                                                                                              |
| --------------------- | --------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| Backend               | Go module in `backend/`, executable in `backend/cmd/server/`                                        | There is no existing Go SDK or user-facing transfer CLI.                                                               |
| Wire models           | `backend/internal/api/models.go`, with some database-coupled fields and anonymous handler responses | Reuse only database-independent contracts; do not expose server internals to consumers.                                |
| Authentication        | Device bearer sessions from password login or pairing redemption                                    | Reuse current APIs; regular accounts create transfers/inboxes, while administrators cannot transfer.                   |
| Session lifetime      | Device sessions expire after 30 days and can be revoked                                             | Noninteractive jobs need explicit expiry errors and credential renewal; perpetual machine tokens do not already exist. |
| Encryption            | TypeScript, Kotlin/provider adapters and Swift implementations                                      | Go client cryptography must be implemented and checked against existing vectors.                                       |
| Share links           | Send `/d/{uuid}#{base64url32}`; Receive `/u/{uuid}#v2.{public32}`                                   | QR codes must encode the complete existing URL, including its fragment.                                                |
| Private Receive state | Private keys are scoped to origin, account and inbox                                                | Login/pairing does not synchronize another device's private keys.                                                      |
| Build layout          | Independent backend module; no root Go module/workspace                                             | Add an independently buildable client module without disrupting backend builds.                                        |
| CI                    | Application path selection in `tools/select_ci_checks.py`                                           | Teach selection about the client so ordinary CLI changes do not trigger mobile builds.                                 |

Implementation references:
[API models](../../backend/internal/api/models.go),
[authentication](../../backend/internal/api/auth.go),
[shared link handling](../../shared/src/commonMain/kotlin/zip/psst/shared/model/UrlHelper.kt),
[input classification](../../shared/src/commonMain/kotlin/zip/psst/shared/model/ScanInput.kt),
[chunked files](../protocol/chunked-files.md),
[Receive-v2 encryption](../security/receive-crypto-design.md), and
[download acknowledgements](download-acknowledgements.md).

## Architecture and boundaries

Use one new submodule, `github.com/endorses/psst.zip/client`, rather than placing
the executable inside the server module or introducing a root module whose version
tags would collide with existing server releases.

```text
client/
  go.mod, go.sum
  *.go                  public Go client API and operations
  protocol/             typed wire values, errors, links and format constants
  cmd/psst/main.go       thin executable entry point
  internal/cli/         commands, output, terminal QR rendering
  internal/crypto/      protocol-compatible encryption adapters
  internal/state/       profiles, protected key storage and operation journals
```

The public library accepts a configured HTTP client, credential provider, context,
readers/writers and progress callbacks. It does not print, prompt, exit the process
or require a particular credential store. Keep its API small: export capabilities
that callers need, not every implementation detail.

Start client wire types from the existing API contract, removing database types
such as `database.GuestUploadCapacity`. Small client/server contract tests protect
the few overlapping representations. Do not make a broad DTO migration or a new
server release a prerequisite for the first CLI. Extract additional shared Go
types only when both consumers actually benefit; such extraction must preserve
JSON names, nullability, errors and permissions. Any eventual backend import must
use a tagged/pinned public module version and work without local `replace` rules.

Prefer standard-library command parsing and networking. Evaluate a maintained QR
encoder, terminal/credential adapters and a complete HPKE provider only where
needed. Before adopting packages, Go toolchains, Actions or runners, verify the
latest stable releases from their official sources, record compatibility limits,
pin dependencies/actions and include required notices. Do not implement an HPKE
key schedule, ECDH/KDF composition or custom cryptographic primitive.

Existing repository licensing applies to the new Go code unless separately
changed. The agreed mobile MPL direction is separate, unimplemented work; it does
not silently relicense this library or existing releases.

## Command and output contract

These commands define the intended capabilities; refine flags consistently before
freezing the first public command schema.

```sh
psst auth login --server https://files.example --username alice
psst auth pair --json-file pairing.json
psst auth status --json
psst auth logout

psst send report.pdf --expires 24h --qr
psst send report.pdf --json > send.json
psst fetch --link-stdin --output-dir ./downloads
psst upload report.pdf --link-stdin

psst receive create --expires 24h --qr
psst receive create --json > receive.json
psst receive list --json
psst receive fetch INBOX_ID --output-dir ./inbox

psst transfers list --json
psst transfers show TRANSFER_ID --qr
psst transfers revoke TRANSFER_ID
psst receive show INBOX_ID --qr
psst receive revoke INBOX_ID

psst qr --json-file send.json
psst qr --json-file receive.json
psst send report.pdf --json | psst qr --json-file -
psst --version
```

`send` creates an owned standalone transfer. `fetch` downloads a public Send link.
`upload` sends selected files into an existing Receive invitation using only the
guest capability. `receive create` creates an owned inbox; `receive fetch` collects
its submissions with the locally held private key. Do not conflate those roles.

JSON mode writes exactly one versioned result to stdout, with `schema_version`,
`ok`, and either `data` or `error`. Successful Send and Receive creation/show
results expose a typed resource identity and a complete `data.link` when the
necessary local key is available. A remote history entry with no local key must
report that state; it cannot fabricate a complete Send link or decrypt an inbox.
Define a small documented set of exit codes for usage/input, authentication,
unavailable resources/policy limits, transport failure, local persistence and
cryptographic validation. Preserve the server's stable error code separately.

Progress and human diagnostics go to stderr. JSON/noninteractive execution never
prompts, opens a browser, waits indefinitely or emits ANSI/QR graphics into a JSON
result. Define `--non-interactive`, explicit deadline/cancellation behavior and
credential input through stdin/file descriptors or a configured private provider.
Do not accept passwords as command-line arguments. Link input supports stdin/files
so capability URLs need not appear in shell history or process listings.

Document that explicitly emitted share links and saved JSON are capabilities.
Private Receive keys, account tokens and deletion capabilities are excluded from
ordinary JSON results. Standard errors and debug output redact credentials and
fragments. Filenames, titles and server error text are untrusted terminal input:
escape control characters rather than allowing terminal escape injection.

## Terminal QR codes and saved JSON

This is required in the first complete CLI milestone, not a later presentation
enhancement. Use the same renderer for `--qr`, resource `show` and `psst qr`.

- [x] Render complete canonical Send and Receive URLs locally with Unicode
      half-blocks, a proper quiet zone and explicit RGB black/white on truecolor
      terminals, avoiding configurable ANSI palette slots. Reset inherited text
      styles per row. Provide a plain block fallback using terminal colors and
      detect insufficient terminal width instead of wrapping a code.
- [x] Display the complete share URL alongside interactive QR output. Missing local
      Send keys must produce a useful error, not a QR containing an incomplete URL.
- [x] Define and document the version-1 saved JSON schema for individual successful
      Send/Receive creation and show results. Include a sample of each using
      disposable fixtures, without real user credentials or links.
- [x] Implement `psst qr --json-file PATH` and `--json-file -`. Read one bounded JSON
      document, validate its version/success/resource kind and `data.link`, and
      encode that exact validated link. Do not encode the whole result object,
      remove its fragment, infer a link from an ID or choose an arbitrary field.
- [x] Reject malformed, failed, unsupported-version, missing-link and ambiguous
      multi-resource documents with a concise diagnostic. Never silently choose
      the first element of a list. Respect encoder capacity and scanner limits;
      do not truncate or split a link into incompatible QR codes.
- [x] Keep saved-JSON QR rendering entirely offline: no profile, login, server
      availability check, credential lookup or capability redemption is required.
      Keep pairing JSON separate: `auth pair` consumes its single-use credential,
      while this QR command handles saved Send/Receive results.
- [x] Make output selection explicit: a QR rendering request produces terminal
      graphics, while `--json` produces structured output. Reject contradictory
      flag combinations before creating a resource; support deliberate rendering
      into a redirected terminal-output file without interactive prompts.

## 1. Client foundation, profiles and authentication

- [x] Create the Go submodule and thin command entry point; define the public SDK
      surface, command grammar, result schema and exit-code meanings together.
- [x] Implement normalized operator-selected origins, API/web identity checks,
      typed responses and stable errors including `X-Psst-Error-Code` and
      `Retry-After`. Preserve LAN HTTP support with explicit transport-risk
      documentation, normal HTTPS certificate verification and no TLS bypass flag.
- [x] Bind account credentials to the configured origin and owner. Use separate
      anonymous/guest clients for share links, including links from another server.
      Reject credential-forwarding redirects and HTTPS downgrade; fragments never
      enter API requests or request logs.
- [x] Implement regular-user password login, private pairing-file/stdin redemption,
      status and logout/revocation using existing device-session APIs. Explain
      administrator restrictions and never reuse browser cookies as CLI state.
- [x] Implement account-scoped profiles and protected storage with atomic writes
      and restrictive permissions/ACLs. Use an OS credential-store adapter where
      available and an explicitly configured encrypted vault for headless use;
      never silently downgrade to plaintext secrets when a keyring is unavailable.
- [x] Define vault unlocking through a private provider/file descriptor for jobs,
      schema migration, locking/concurrent commands and recovery. Provide an
      encrypted backup/restore path for locally created Receive private keys before
      presenting those inboxes as durable; signing into the same account is not a
      recovery mechanism. Logout must not unexpectedly destroy decryption keys.
- [x] Report session expiry/revocation with a machine-readable authentication error
      and document renewal before the existing 30-day expiry. Do not promise
      unattended indefinite operation. Scoped, renewable machine credentials are
      a separate backend feature requiring an explicit future design decision.

## 2. Encryption and transfer engine

- [x] Implement manifest AES-256-GCM and `chunked-v1` framing with standard
      primitives and secure randomness. Pass the existing deterministic
      [chunked-file vector](../protocol/chunked-file-vector.json), including empty
      files, exact boundaries, authenticated context and total-size arithmetic.
- [x] Select a maintained full HPKE implementation supporting the exact Receive-v2
      RFC 9180 suite. Pass the existing
      [RFC vector](../security/fixtures/hpke-rfc9180-x25519-aes256.json) and
      [cross-provider Receive fixtures](../security/fixtures/hpke-receive-v2.json)
      with the current identity-bound context and empty production AAD. Stop
      provider adoption if interoperability or public-key validation fails.
- [x] Implement bounded sequential encrypted uploads through the existing tus
      lifecycle, manifest publication and completion verification. Respect server
      limits and accepted expiry/download policies. Persist recoverable state
      before exposing a link; clean up failed allocations using their proper
      authority rather than leaving an apparently successful resource.
- [x] Implement bounded streaming downloads, manifest validation and authenticated
      frames. Reject malformed identities/sizes, substitution, reordering,
      truncation, trailing bytes and unsafe filenames before publishing outputs.
      Use restrictive temporary destinations and collision-safe atomic publication;
      do not overwrite files or follow hostile paths from a manifest.
- [x] Implement cancellation, bounded backoff and operation journals. Resume an
      upload only against persisted ciphertext/nonce state; re-encrypting a partial
      chunk cannot be combined with an old tus offset. Initially restart an
      interrupted file download and retain completed files; do not promise byte
      range resume that the current framing/protocol does not provide.
- [x] Send idempotent completion receipts only after all required files are
      durably saved. Retry a failed receipt without fetching files again. Preserve
      quota semantics: failed/retried attempts may consume download allowance and
      abandoned reservations do not imply restored capacity.

## 3. User workflows and QR delivery

- [x] Implement owned `send`, public-link `fetch`, resource list/show/revoke and
      bounded pagination, with the common output/error contract and cancellation.
      Retain only local state needed for recovery and reopening; never treat local
      history removal as remote revocation.
- [x] Implement Receive creation: generate and protect the private key before
      publishing the invitation, verify the server accepted protocol/public key
      and policy, and retain management state if verification is inconclusive.
      Revoke a failed allocation where safe; never display an unusable invitation.
- [x] Implement guest `upload` to Receive-v2 links, checking the invitation-bound
      public key against server metadata. Create a child transfer anonymously,
      then use only its issued upload capability for file creation/upload,
      finalization and cleanup.
- [x] Implement owned inbox list/show/fetch/revoke with bounded pagination, exact
      membership checks, local private-key lookup and independent checkpoints per
      submission. Report missing keys explicitly without implying login can fix it.
      Provide a bounded `--wait` option only if needed; no unbounded polling loop.
- [x] Complete every terminal-QR and saved-JSON requirement above. A saved Receive
      JSON result contains its public invitation, never its private decryption key.
- [x] Write command help and `docs/cli.md` with interactive, pipeline, scheduled-job,
      private credential input, session renewal, recovery and self-hosted examples.

## 4. Focused interoperability and efficient CI

Verify meaningful failure boundaries rather than adding tests to increase coverage.
Tests use disposable data and local servers; production accounts, phone keys and
public deployments are not fixtures.

- [x] Add fast table-driven vector/parser tests for crypto authentication, canonical
      links, hostile paths, bounded JSON, versioned outputs and exit codes. Verify
      QR correctness by independently decoding a generated matrix/image back to
      the complete expected URL, not by comparing terminal-art snapshots.
- [x] Cover the offline saved-JSON path with both Send and Receive results, stdin
      pipelines, malformed/ambiguous input and a disabled network/credential
      provider. Confirm fragments survive and secrets are absent from diagnostics.
- [x] Cover account/guest origin isolation, session expiry, redirect rejection,
      atomic persistence, cancellation, safe file publication and receipt retry
      using small deterministic fixtures and injected clocks/failures rather than
      long sleeps or large random files. Measure streaming memory using a generated
      reader instead of allocating/uploading a multi-gigabyte fixture in CI.
- [x] Add one compact integration suite against a real disposable backend with
      its production checks enabled. Exercise CLI Send to existing client crypto,
      existing client Send to CLI download, guest Receive upload and owner decrypt,
      and revoke/expiry/allowance failures. Reuse the existing TypeScript crypto
      helpers as the independent implementation; do not clone the browser suite.
- [ ] Validate equivalent Android and iOS compatibility with the unchanged links
      and shared/provider fixtures. Any required protocol/parser/UX fix must cover
      both native platforms and the iOS extension when applicable. Existing parity
      validation gaps remain explicit; this CLI does not implicitly close them.
- [x] Add a dedicated scoped CLI CI job with cached dependencies, bounded tests
      and measured step timings. Use a five-minute timeout initially; diagnose
      overruns rather than increasing the limit or removing meaningful assertions.
      Hosted cold/warm observations
      remain pending in the external validation list below.
- [x] Extend change-scope selection and its existing regression tests: client
      changes run CLI checks, backend changes also run client contract integration,
      and shared protocol-fixture changes select their real consumers. CLI-only
      changes do not require Android/iOS builds; mobile source changes still do.
- [x] Run the Go race detector once for the client test suite and vet/build once;
      avoid duplicated build/test runs in packaging. Use the existing security
      hooks/scanners and include the new module in dependency-notice checks.

Intended local checks after implementation, subject to the selected test layout:

```sh
cd client
go vet ./...
go test -race -timeout 2m ./...
go build ./cmd/psst
```

The real-backend compatibility suite runs separately with its own short timeout
and explicit temporary storage/process cleanup. On Linux, perform all available
Go/backend/web-helper verification. macOS/Windows credential-store and filesystem
runtime checks, plus any affected native app/extension build and device checks,
remain pending until executed on their required platforms. Cross-compilation or
source review does not establish a runtime pass.

## 5. Packaging and release delivery

- [x] Package Linux, macOS and Windows binaries for amd64/arm64 where supported,
      with embedded version/source revision, SHA256 checksums, licenses/notices,
      source and concise installation instructions. Avoid CGO/server/SQLite
      dependencies; validate platform adapters rather than assuming portability.
- [x] Use `client/v0.1.0` as the proposed first Go-module/GitHub release tag. Its
      prefix gives the `client/` submodule correct Go version resolution and keeps
      CLI releases separate from existing server `v*` and Android `android-v*`
      tags. Ensure triggering it does not start server publication or deployment.
- [x] Build release artifacts from the exact reviewed source, reuse passing
      applicable source checks and add only artifact-specific verification. Keep
      publication separate from ordinary commits and keep existing release tags
      immutable. Publishing a first CLI release is a later explicit action.
- [x] Smoke-test an installed release binary, offline JSON-to-QR rendering and a
      small disposable transfer. Document OS runtime validation limits; app-store
      publication, container images, package-manager repositories and background
      daemons are outside this first delivery.
- [x] Provide shared `make build`, `install`, `uninstall`, `clean` and `help`
      targets from the repository root and `client/`. Default to the user's
      `~/.local/bin`, support prefix/binary-directory/staging overrides, and embed
      the development version and current Git revision.
- [x] Add `make build-release` and `install-release`, retaining version/revision
      metadata while stripping symbol tables and DWARF using `-s -w`, consistent
      with the existing release packager.
- [x] Derive Make build versions from stable CLI tags, including commit-distance
      and tracked dirty-state suffixes. Ignore Android/server tags, retain explicit
      overrides, and fall back to `dev` without matching Git metadata.

Makefile follow-up (2026-10-23): Linux build and staged installation were verified
from both entry points, including paths with spaces, `PREFIX`, `BINDIR` and
`DESTDIR` overrides, version/revision overrides, executable mode 0755, installed
binary equality, uninstall and clean/rebuild. `make build install` builds once
within the invocation. Checks used an isolated temporary Go build cache and
staging directories. No real user installation was performed. Native Windows
keeps the documented direct Go-build path; these Make targets require GNU Make
and standard Unix shell/install tools.

Release-target verification (2026-10-23): inspected the native Linux ELF sections
to confirm the stripped binary has no symbol table or DWARF debug sections;
ordinary builds retain them. Both targets preserve `--version` metadata. Offline
QR rendering and a staged `install-release` from `client/` passed, including
executable permissions and byte-for-byte equality with the built binary. The
observed development/release sizes were 12,641,243 / 8,605,856 bytes on this machine.
These checks used a temporary Go build cache and did not publish a release.

Version-detection follow-up (2026-10-23): temporary Git fixtures verified exact
CLI tags, commits after a tag, tracked dirty state, ignored untracked files,
Android/server tags, malformed CLI tags, archive fallback and explicit overrides.
Both Make entry points resolved the expected version. A real stripped build and
staged installation preserved the derived version and full revision in
`--version`. The Makefile branch is past the `client/v0.1.0` tag, so it correctly
reports a commit-distance/hash suffix rather than claiming to be the exact tag.

## Completion criteria and record

- [x] The public Go library can perform the supported operations without terminal
      dependencies, and the executable imports no backend database/server internals.
- [x] Send/download and Receive/guest-upload/owner-download interoperate with the
      existing encryption protocol, without syncing or exposing private keys.
- [x] Terminal QR codes scan as complete existing Send/Receive URLs; saved CLI JSON
      can reproduce either QR fully offline through a file or pipeline.
- [x] Noninteractive runs have predictable outputs, exit codes, deadlines, session
      expiry handling and recoverable state, without credential/fragment leakage.
- [x] Meaningful scoped tests pass, their local timings are recorded, and unrun
      hosted/platform checks stay unchecked. Local timings do not establish a
      hosted-runner result or a supported performance promise.
- [x] Format the scoped code/docs, record commands/timings and limitations, check
      only verified tasks, clean task-owned temporary artifacts, and commit the
      implementation together with updates to this plan.

Implementation record (2026-10-21): the client module, CLI, encrypted vault,
protocol-compatible crypto, QR rendering, focused CI, integration fixture and
manual release tooling are implemented and committed on `feat/cli`. The bounded
closure audit concluded **CLOSED_WITH_DEFERRALS** after one discovery round, one
fix batch and one integrated review. Both material findings are fixed; the named
external checks below remain unrun and do not block local implementation closure.
No CLI release, tag, GitHub push or production deployment has been performed.

### Local verification evidence

- [x] Existing chunked-file and RFC 9180 vectors pass; Go opens the existing
      browser, Tink/Android and Swift Receive wrappers. Module-local public fixture
      copies are compared with root fixtures whenever the full repository exists.
- [x] `go vet ./...` and `go test -race -timeout 2m -v ./...` passed on Go 1.27.3
      Linux/amd64. This includes corruption, origin isolation, wrong contexts,
      private-state recovery, interrupted publication, receipt retry, JSON/terminal
      safety, independently decoded terminal output, and blocked-input cancellation.
      The crypto suite took 1.737 s under the race detector; other packages took
      1.008–1.261 s, including race-detector process overhead.
- [x] A generated 64 MiB input allocated 8,412,032 bytes while encrypting to a
      discard writer; no file-sized fixture was allocated or uploaded. This
      bounded-memory regression took 0.54 s under the race detector.
- [x] Six CI selector regressions pass in 1.567 s. A CLI-only push selects the CLI
      without web/Android/iOS jobs; backend and imported browser-crypto changes
      also select the actual interoperability consumers.
- [x] The compact disposable backend scenario passed in 8.5 s against the final
      built CLI, using real regular-user authentication, all four CLI/web Send and
      Receive directions, multi-frame and empty files, Receive-key recovery,
      enforced allowances/revocation/expiry, and receipt retry without another
      file GET. The fixture supplies only web identity markup; this is not browser UI
      validation. The final run used Node 26.12.0 and Go 1.27.3.
- [x] A pseudo-terminal check verified that both a private-prompt interrupt and a
      whole-command deadline restore the original terminal attributes, including
      echo, and return the documented cancellation exit code.
- [x] The single closure review found and fixed two material issues: Windows
      plaintext staging now receives a protected current-user DACL atomically at
      exclusive, root-relative creation; Receive-capacity HTTP 403 responses now
      produce the policy exit code instead of requesting authentication. The one
      integrated review found no further material issues. Affected SDK/CLI race
      tests passed in 1.066 s / 1.030 s; Windows amd64/arm64 test binaries compile.
      Native ACL execution remains pending, with its command recorded below.
- [x] Final clean-source packaging at `a7c04a32fc1f863dd06f8afd601dc1fd10832d28`
      built all six targets. Checks covered all asset checksums, ELF/Mach-O/PE
      architecture identities, embedded version/revision, installed Linux offline
      Send/Receive QR, dependency notices, license/install files and an independent
      vendored-source build with networking disabled. The installed final binary's
      disposable-backend scenario passed in 7.4 s. Earlier development packaging
      also verified deterministic archive bytes and completed in 26.31 s.
      Candidates were not published or installed into the user's PATH.
- [x] Dependency releases were checked against official upstream/module metadata:
      Go 1.27.3, CIRCL 1.6.5, age 1.3.2, go-qr/v2 2.5.0, go-keyring 0.2.8,
      x/crypto 0.58.0, x/sys 0.49.0, x/term 0.47.0 and godbus/dbus/v5 5.2.4.
      CI uses current stable Node 26.12.0; the matching official Linux archive was
      downloaded and its SHA256 verified for final interoperability validation.
      No existing browser protocol packages were upgraded for this Go client.

### Explicit external validation and delivery limits

- [ ] Record actual cold and warm hosted CLI job timings when CI first runs,
      including any timeout bottleneck. Local timings are evidence for test
      efficiency, not a GitHub runner result.
- [ ] Execute native macOS Keychain and Windows Credential Manager/ACL/filesystem
      checks. Their binaries cross-compile; that does not establish runtime passes.
      On Windows, run `cd client` then
      `go test -run '^TestPrivateDownloadWindowsOwnerOnlyACL$' .` on an ACL-capable
      filesystem, plus `go test ./internal/state`. On macOS, run the state tests
      and exercise the interactive Keychain provider with a disposable vault;
      headless/JSON checks must use the explicit passphrase mode.
      macOS JSON/headless use deliberately requires the encrypted passphrase vault
      because `security` cannot reliably suppress UI. Interactive Linux commands
      can activate/unlock Secret Service, including KWallet's facade; JSON,
      noninteractive and non-TTY commands never activate or unlock it. The user
      verified a real Linux wallet-backed login and saved-session reuse after the
      credential UX follow-up. Windows native credential syscalls have no
      cancellation handle and are checked before/after, not run asynchronously.
- [ ] Perform physical Android/iOS scans and any remaining native app/extension
      validation when those environments are available. No mobile implementation
      or protocol was changed, and previously unrun native checks remain unrun.
- [ ] Publish a first `client/v0.1.0` release only as a separately authorized action
      after exact-source executed CI and candidate review. Existing server/Android
      tags and production routing remain unchanged.

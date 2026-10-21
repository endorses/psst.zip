# psst.zip CLI

`psst` is a Go client for an operator-selected psst.zip server. It supports
encrypted Send and Receive workflows, public downloads, guest uploads, resource
management, terminal QR codes and scripted use. It does not run or administer a
server. Transfer creation requires a regular account; administrator accounts
cannot send files or create Receive links.

## Install and build

The CLI has an independent Go module in `client/`. Before a public CLI release is
published, build the checked-out source:

```sh
cd client
go build -o psst ./cmd/psst
./psst --version
./psst help
```

Use the stable Go version declared in `client/go.mod` (currently 1.27.3). Planned
release tags use `client/vX.Y.Z`, independently of server and Android releases.
Committing code does not deploy a server or publish a binary. Packaged releases
include the version/source revision, licenses, dependency notices, checksums and
corresponding build source. Do not infer a published version from this document.

## Account and private state

For an interactive desktop with a working OS credential store:

```sh
psst auth login --server https://files.example --username alice
psst auth status --json
```

The password is entered privately with terminal echo disabled. The CLI validates
the API and both web share routes before login. Configure the actual origin of
your own installation, including a port when needed. The product name does not
select a hosted service. HTTPS uses normal certificate verification; there is no
certificate-bypass option. LAN HTTP is supported, but it exposes account
credentials and traffic to that network and does not authenticate browser code.

The default encrypted vault is under the user's configuration directory at
`psst.zip/cli/` (`--state-dir DIR` overrides it). Its random encryption identity is
protected by macOS Keychain, Windows Credential Manager or Linux Secret Service.
The CLI refuses to silently substitute a plaintext file if that provider is
unavailable. The state directory must be private to the current user. Do not
delete its lock file to recover an interrupted command: the operating system
releases the process lock when the process exits.

Linux accesses only an already-unlocked Secret Service collection and never
starts an unlock dialog. On macOS the system `security` command cannot reliably
suppress Keychain UI, so JSON/headless commands require an explicitly configured
passphrase vault. An encrypted backup can move logical records from an interactive
Keychain vault into a fresh passphrase vault. Windows Credential Manager calls
are noninteractive; native OS-call latency is outside Go context cancellation.
Provider errors fail closed and never select plaintext storage.

`--profile NAME` selects an account profile. Resource keys and journals are scoped
by normalized origin and account identity. Signing into another account does not
make the previous account's private records accessible to it. Public-link
downloads and guest uploads use a separate anonymous scope and never attach an
account cookie or bearer token, even when targeting a different server.

Device sessions currently expire after 30 days and can be revoked by the account
owner. Renew with login or a fresh pairing document before unattended jobs need
them. There are no permanent or scoped machine tokens in this version. Logout
revokes the device session and removes it from the selected profile while
retaining Receive private keys, saved-resource state and download checkpoints.

Pairing uses the existing single-use web login document:

```sh
psst auth pair --json-file /private/path/pairing.json
```

Pairing JSON is a credential, distinct from Send/Receive result JSON. Do not put
it into logs, issue reports or shell arguments. Pairing and ordinary login do
**not** copy another device's encryption keys.

## Send, download and Receive

```sh
psst send report.pdf --expires 24h --max-downloads 3 --qr
psst transfers list --json
psst transfers show TRANSFER_ID --qr
psst transfers revoke TRANSFER_ID

psst fetch --link-stdin --output-dir ./downloads
psst receive create --expires 24h --max-files 10 --qr
psst receive list --json
psst receive show INBOX_ID --qr
psst receive fetch INBOX_ID --output-dir ./inbox
psst receive revoke INBOX_ID
psst upload report.pdf --link-file /private/path/receive-link.txt
```

`fetch` expects a complete Send download link. `upload` expects a Receive
invitation and sends files without an account on the recipient server. The inbox
owner uses `receive fetch`, with their local private key, to decrypt submissions.
The public Receive invitation contains only its public key. An account's remote
history is not a key backup: an item created on another device may be manageable
but unavailable for decryption or complete Send-link display on this device.

All files use the existing encrypted manifest, authenticated 4 MiB file framing
and Receive-v2 HPKE protocol. Processing is sequential with bounded memory.
Input files must be regular files with safe distinct names; directories are not
implicitly archived. Server file limits and invitation capacity are checked
before encrypting uploads. Expiry, download attempts, storage and traffic limits
remain controlled by the server.

Downloads authenticate each frame into a temporary destination and publish a
complete file atomically without overwriting an existing filename. A collision
gets a distinct name. A completion receipt is sent only when every file has
been durably saved. The CLI does not execute downloaded files. Receive collection
uses separate submission directories.

Resource lists return one bounded page by default. Follow `data.next_cursor` with
`--after CURSOR`; `--limit` accepts 1–100. The CLI does not silently crawl an
unbounded history or run a background polling daemon.

## QR codes from saved JSON

Save an individual Send or Receive result, then render it later without a server
connection, account, profile or vault unlock:

```sh
psst send report.pdf --json > send.json
psst receive create --json > receive.json

psst qr --json-file send.json
psst qr --json-file receive.json
psst send report.pdf --json | psst qr --json-file -
psst qr --json-file receive.json --plain > receive-qr.txt
```

Terminal QR codes encode the complete existing share URL, including its fragment.
Half-block rendering fixes black foreground and white background and includes a
quiet zone; `--plain` uses full blocks. The CLI detects a terminal too narrow for
the code instead of wrapping it. Widen it or deliberately render to a file.
Choose `--qr` or `--json` for a creation/show command; the combination is rejected
before resource creation.

The saved result must be one successful schema-version-1 JSON object containing
`data.kind` (`send` or `receive`), `data.id`, `data.origin`, and a matching complete
canonical `data.link`. Normal results may also include `expires_at`,
`key_available` and `operation_id`. The offline reader rejects unknown/ambiguous
structures, lists, duplicate fields, unsupported versions, missing keys, failed
results and documents larger than 64 KiB. It renders the link, not the entire JSON
document, and never chooses the first item from a list.

[Send](../client/examples/send.json) and
[Receive](../client/examples/receive.json) examples use reserved `files.example`
and public test-vector material. They exercise offline QR rendering and do not
refer to usable transfers. Normal JSON output and terminal QR codes contain
actual share capabilities; protect them as you would the links themselves.
Private Receive keys, account tokens and deletion capabilities are excluded from
ordinary output.

## Automation and encrypted headless vaults

`--json` writes one result to stdout:

```json
{
  "schema_version": 1,
  "ok": false,
  "error": {
    "code": "authentication_required",
    "message": "The device session expired. Sign in again; device sessions currently last 30 days."
  }
}
```

Successful results contain `data` instead of `error`. Errors can additionally
include the server's stable `server_code`, `retry_after_seconds`, or a retained
upload's `operation_id`. Progress and human diagnostics use stderr. JSON and
`--non-interactive` never prompt, launch a browser or mix QR/ANSI graphics into a
JSON result. `--timeout` bounds the entire command (default ten minutes); individual
control requests have shorter deadlines. Interrupting a command preserves useful
recovery state where available.

| Exit | Meaning                                                            |
| ---- | ------------------------------------------------------------------ |
| 0    | Completed successfully                                             |
| 2    | Invalid arguments, input or output selection                       |
| 3    | Authentication, account restriction or session renewal required    |
| 4    | Resource/policy unavailable or unsupported protocol response       |
| 5    | Transport failure, interruption or pending completion receipt      |
| 6    | Local storage, vault, missing key or invalid saved-file checkpoint |
| 7    | Cryptographic validation failed                                    |

On a headless host, explicitly select `--vault-mode passphrase` and provide its
passphrase through a private file descriptor numbered 3 or greater:

```sh
psst auth login --server https://files.example --username alice \
  --vault-mode passphrase --vault-passphrase-fd 3 \
  --password-stdin --non-interactive --json \
  3< /private/provider/vault-passphrase < /private/provider/account-password

psst send report.pdf --json --non-interactive \
  --vault-mode passphrase --vault-passphrase-fd 3 \
  3< /private/provider/vault-passphrase
```

The paths illustrate credentials supplied privately by your password manager or
job runner, with appropriate permissions. The CLI never writes those input files.
Unix shells can also use private provider process substitution. Credentials are
not command-line values; keep them out of shell tracing, chat and CI logs. The
encrypted vault still needs a protected unlocking credential; encryption does not
remove that custody requirement.

## Interruption, backup and recovery

Upload journals retain encrypted ciphertext so retries reuse the same frame
nonces and bytes at the server's tus offset. Resume using the reported operation
ID, the same profile and the same vault:

```sh
psst send --resume OPERATION_ID --json
psst upload --resume OPERATION_ID --server https://files.example --json
```

Guest upload resume requires its original origin. Successful uploads remove their
ciphertext spool files. Retained incomplete operations consume disk until resumed
or deliberately cleaned up; do not delete the vault merely to reclaim ciphertext
space. A fresh upload can require enough local disk for its encrypted file bytes.

Downloads retain verified completed files and retry incomplete files from the
start. They do not promise byte-range resume. If a completion receipt is pending,
repeat the same fetch to retry the receipt without downloading again. If a saved
file was moved or altered, choose a fresh output directory for an explicit
redownload, which remains subject to the server's remaining download allowance.

Back up the encrypted vault after creating Receive links, before relying on them
for files you need to recover. Protect the backup independently of this computer:

```sh
psst vault backup --output /external/psst-vault.age \
  --backup-passphrase-fd 4 4< /private/provider/backup-passphrase

psst vault restore --input /external/psst-vault.age \
  --backup-passphrase-fd 4 4< /private/provider/backup-passphrase
```

Passphrase vaults additionally need their normal unlock descriptor, distinct from
the backup descriptor. Interactive use can prompt privately instead. Backups have
their own passphrase and contain the encrypted logical records, including Receive
private keys, profiles and journals. They do not contain downloaded plaintext or
the external ciphertext spool files referenced by interrupted uploads. A restored
account session may need renewal. Restore merges only nonconflicting records and
refuses conflicting state without deleting existing keys; use a fresh private
`--state-dir` when restoring to another installation. Backup refuses an existing
destination file. Keep and test the backup passphrase separately: a new login
cannot recover a lost Receive private key.

## Go library and validation

Import `github.com/endorses/psst.zip/client` and `client/protocol`. `client.New`
accepts a normalized origin, an optional HTTP client and a credential-provider
function. SDK methods accept contexts and use readers/writers/progress callbacks;
they never print, prompt or exit. High-level operations accept the small `Store`
interface for caller-controlled encrypted, account-scoped persistence. The CLI's
internal vault is one implementation, not an SDK requirement. No backend/SQLite
package is imported by the module.

For caller-managed destinations, `ReadManifest` returns validated file metadata
and `DownloadFileTo` streams an authenticated file to an `io.Writer`. Keep that
destination unpublished until the method succeeds: earlier frames may have been
written before a later frame fails authentication. These methods do not send a
download receipt; the caller acknowledges completion only after all files are
durably stored. `Fetch` handles protected temporary files, atomic publication and
receipt recovery for filesystem downloads.

Local validation uses existing protocol vectors, focused Go/race tests and one
compact disposable-backend interoperability check with the production TypeScript
crypto helpers. The backend runs its real authentication and quota enforcement;
the harness supplies only web-route identity markup, not a browser UI test. It
uses no production credentials or files. See the evidence in the
[implementation plan](plans/cli-client.md) for actual commands, timings and limits.

The manual **CLI release** workflow checks an existing `client/vX.Y.Z` tag against
successful, actually executed CLI and repository-security checks at that exact
main commit. If a documentation-only commit skipped the CLI job, run **CI** from
main with `cli_only=true`, then retry the release candidate. This runs security
and CLI validation without rebuilding the mobile apps. The release workflow
produces candidate artifacts by default; publication requires explicitly selecting
its separate `publish` input. No CLI release has been published by implementing
this tooling.

Publication assumes the repository's existing immutable-release setting remains
enabled. The workflow refuses existing releases, checks the source tag again,
then verifies the published release's immutable flag. It does not inspect that
administrative setting before publication; keep it enabled before selecting
`publish`.

Cross-compilation does not establish macOS Keychain or Windows credential/ACL
runtime behavior. Native desktop provider/filesystem checks remain pending until
run on those systems. The CLI preserves mobile link/encryption formats; it does
not claim to complete older pending physical-device or native iOS validation.

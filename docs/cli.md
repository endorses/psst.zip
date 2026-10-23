# psst.zip CLI

`psst` is a Go client for an operator-selected psst.zip server. It supports
encrypted Send and Receive workflows, public downloads, guest uploads, resource
management, terminal QR codes and scripted use. It can also serve temporary Send
and Receive links directly on a trusted LAN without an account or hosted server.
Hosted transfer creation requires a regular account; administrator accounts cannot
send files or create Receive links.

## Install and build

The CLI has an independent Go module in `client/`. Before a public CLI release is
published, build the checked-out source. From the repository root or `client/`:

```sh
make build            # plain make also builds the CLI
make install          # builds and installs ~/.local/bin/psst
make build-release    # strips symbol tables and DWARF debug information (-s -w)
make install-release  # builds and installs the stripped executable
psst --version
psst help
```

Add `~/.local/bin` to your shell's `PATH` if it is not already present. The default
installation does not require root permissions. To select a different destination or stage a
package, use the same overrides when uninstalling:

```sh
make install PREFIX=/your/install/prefix
make install BINDIR=/your/bin
make install DESTDIR=/your/staging/root PREFIX=/usr/local
make uninstall PREFIX=/your/install/prefix
```

`make help` lists targets and overrides. `make clean` removes only the built
`client/psst` executable; account state and local encryption keys are stored
elsewhere. Builds derive their version from stable `client/vX.Y.Z` tags. An exact
tag builds as `X.Y.Z`; later commits include the distance and hash, for example
`0.1.0-3-gabcdef0`. Tracked working-tree changes add `-dirty`. Server and Android
tags are ignored. Without a matching CLI tag or Git metadata, the version is
`dev`. The full Git revision is embedded separately (`unknown` outside Git).
`VERSION` and `REVISION` can still be overridden explicitly.
Both release targets retain this version/revision metadata and use the same
`client/psst` output. Use `install-release` to install a stripped build;
`install` builds the ordinary executable. These targets do not publish a release.
Source builds also need Node/npm for the embedded LAN browser interface. The Make
targets prepare matching assets automatically and reuse unchanged browser builds;
installed binaries need neither Node nor Docker. The Make targets use GNU Make
and standard Unix shell/install tools. To prepare assets and build directly,
including on native Windows:

```sh
python3 client/scripts/prepare-lan-assets.py
cd client
go build -o psst ./cmd/psst
./psst --version
./psst help
```

On native Windows, use `go build -o psst.exe ./cmd/psst` and run `.\psst.exe`.

Use the stable Go version declared in `client/go.mod` (currently 1.27.3). Planned
release tags use `client/vX.Y.Z`, independently of server and Android releases.
Committing code does not deploy a server or publish a binary. Packaged releases
include the version/source revision, licenses, dependency notices, checksums and
corresponding build source. Do not infer a published version from this document.

## Temporary sharing on a trusted LAN

```sh
psst serve send ./document.pdf --qr
psst serve receive --output-dir ./incoming --qr
```

The command runs in the foreground and prints a browser link. A recipient on the
same network can open that link or scan its QR code. No account, login, keyring or
connection to the configured hosted server is used. Ordinary `psst send` and
`psst receive` still use the configured server.

The OS assigns an available port, so a busy conventional port does not prevent
startup. The link and QR contain the actual port. If several private interfaces
are available, choose one when prompted, or explicitly select the address:

```sh
psst serve send ./document.pdf --listen 192.168.1.42:0 --qr
psst serve receive --output-dir ./incoming --listen '[fd12:3456::42]:0'
```

Port `0` requests an available port. An explicitly requested occupied port reports
the conflict rather than silently changing your configuration. Only specific
private or loopback IPs are accepted; wildcard and public binds are rejected.
Loopback is useful for local checks but its links cannot be opened on another
device. IPv6 link-local addresses with interface zones are not portable share links
and are excluded from automatic selection. Docker/container-only bridges are
omitted from the chooser; their private addresses remain usable with `--listen`.

HTTP is the default: no TLS certificate is needed. Files remain encrypted, but an
active network attacker could replace the HTTP page and its encryption code. Use
this mode on networks you trust. HTTP browser downloads use the existing bounded
memory fallback, limited to 25 MiB per file; larger downloads require a CLI/native
receiver or trusted HTTPS. Browser uploads use the existing chunked encryption.

Optional HTTPS uses your own certificate and private key:

```sh
psst serve receive --output-dir ./incoming --listen 192.168.1.42:8443 \
  --tls-cert ./lan.crt --tls-key ./lan.key
```

The certificate must be valid for the advertised IP address and trusted by the
recipient's device. A self-signed certificate does not automatically establish
trust. No certificate-verification bypass or automatic certificate service is used.

Each invocation owns one temporary Send transfer or Receive inbox. Defaults are
one hour, 1 GiB of encrypted payload, two concurrent streams, one acknowledged Send
download, and at most 100 Receive files. Override finite limits explicitly:

```sh
psst serve send ./photo.jpg --expires 30m --max-downloads 3 --max-bytes 100MiB
psst serve receive --output-dir ./incoming --expires 2h --max-files 20 --max-bytes 1GiB
```

Lifetimes range from one second to seven days; byte budgets range from 1 MiB to
1 TiB. File limits are 1–100, stream limits 1–32, and Send download limits
1–1,000,000. Receive quotas count cumulative admitted uploads; deleting an upload
does not restore its lifetime allowance. `--expires` controls normal serving
lifetime independently of the ten-minute timeout for other commands. An explicit
`--timeout` adds an earlier whole-command deadline.

Send prepares a private encrypted snapshot before advertising its link; changing
the original files afterward does not change that transfer. Receive stores
authenticated plaintext under `OUTPUT_DIR/SUBMISSION_ID/`, prevents overwrites and
path escape, and preserves successfully received files after shutdown. Both modes
preflight disk space and stream files within bounded memory. Receive needs space
for encrypted staging and plaintext output.

Keep the host awake and connected until completion. Send stops after its download
allowance is acknowledged; Receive stops after collecting its file allowance.
Ctrl+C/SIGTERM and expiry close the listener and remove private staging. A stopped
session cannot be resumed; its in-memory keys are gone. Expiry after completed
work exits successfully; expiry without a completion exits 4, interruption or an
explicit command timeout exits 5. Other failures use the ordinary CLI error codes.

`--json` emits one startup result, then keeps serving. It includes the usual share
fields plus `hosting` with the bound listener and limits. Progress/final status
goes to stderr; the final process exit code reports success or failure. A saved
startup result works with `psst qr --json-file FILE`:

```sh
psst serve send ./document.pdf --json > lan-share.json
# While that process remains running, from another terminal:
psst qr --json-file lan-share.json
```

Forced termination can leave a private `psst-lan-*` directory in the OS temporary
directory. To remove one confirmed stopped session, use
`psst serve cleanup --session-dir EXACT_DIRECTORY`. This checks the owner, marker
and held session lock, refuses active sessions and never scans/deletes unrelated
directories. Cleanup does not promise secure erasure on an SSD.

If the recipient cannot connect, check that both devices can communicate on the
same LAN, that the selected address belongs to the intended network, and that the
host firewall permits the printed TCP port. Guest Wi-Fi isolation and VPN routing
can prevent access. The CLI never changes firewall rules, forwards router ports
or opens an internet tunnel. The browser's Source and licenses page identifies
the embedded source revision and notices; custom uncommitted or unpublished builds
do not claim a publicly available source release.

## Account and private state

For an interactive desktop with a working OS credential store:

```sh
psst auth login --server https://files.example --username alice
psst auth status
psst send report.pdf --qr
```

The password is entered privately with terminal echo disabled. The CLI validates
the API and both web share routes before login. Configure the actual origin of
your own installation, including a port when needed. The product name does not
select a hosted service. HTTPS uses normal certificate verification; there is no
certificate-bypass option. LAN HTTP is supported, but it exposes account
credentials and traffic to that network and does not authenticate browser code.

After the first login, commands reuse the saved account token, server and local
keys. When the 30-day session expires, `psst auth login` reuses this profile's
server and username and asks for the account password again.

The default encrypted vault is under the user's configuration directory at
`psst.zip/cli/` (`--state-dir DIR` overrides it). Its random encryption identity is
protected by macOS Keychain, Windows Credential Manager or Linux Secret Service.
Storage selection defaults to `auto`: an existing vault remembers its mode and
provider; a new vault uses the desktop wallet. The CLI refuses to silently
substitute plaintext storage if that provider is unavailable. An explicit
`--vault-mode` that conflicts with existing storage produces an error rather than
replacing its keys. The state directory must be private to the current user. Do not
delete its lock file to recover an interrupted command: the operating system
releases the process lock when the process exits.

Interactive Linux commands can activate the desktop wallet and display its
normal unlock dialog when needed. KWallet is supported through its Secret Service
interface. Once the wallet is unlocked, subsequent commands reuse it without a
separate CLI vault password. The wallet's own lock policy still applies.

JSON, explicitly noninteractive and non-TTY commands never activate a desktop
wallet or open a dialog; Linux can reuse an already running, unlocked Secret
Service collection. Unlock it first using an interactive command if necessary.
Legacy native KWallet APIs without Secret Service are unsupported because they
cannot guarantee prompt-free, create-only credential storage.

On macOS the system `security` command cannot reliably suppress Keychain UI, so
JSON/headless commands require an explicitly configured
passphrase vault. An encrypted backup can move logical records from an interactive
Keychain vault into a fresh passphrase vault. Windows Credential Manager calls
are noninteractive; native OS-call latency is outside Go context cancellation.
Provider errors fail closed and never select plaintext storage.

An encrypted passphrase vault also works interactively when no desktop keyring
is available:

```sh
psst auth login --server https://files.example --username alice --vault-mode passphrase
psst auth status
```

On first use, choose and confirm a new local vault passphrase; the account
password prompt then takes your existing server password. Subsequent commands
remember passphrase mode without repeating `--vault-mode`; they still need the
local vault passphrase. JSON and noninteractive commands require a private
passphrase descriptor, as shown in the automation examples below.

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
Half-block rendering requests RGB black (`#000000`) on white (`#ffffff`) and
includes a quiet zone. This requires truecolor terminal support and avoids the
theme's configurable ANSI palette colors. Each row resets inherited text styles
before rendering and resets colors afterward. `--plain` uses full blocks with the
terminal's current foreground/background colors, so its contrast depends on the
theme. The CLI detects a terminal too narrow for the code instead of wrapping it.
Widen it or deliberately render to a file.
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

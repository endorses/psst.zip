# Receiving untrusted files and links

Recipient policy applies independently of the scanned server's upload settings.
A server controlled by someone else may advertise unlimited uploads or return
malformed metadata. A successfully scanned QR code establishes neither the
server's trustworthiness nor a file's safety.

## Default profile

| Boundary                                      |             Value |
| --------------------------------------------- | ----------------: |
| Files in one manifest                         |             1–100 |
| Plaintext aggregate in one inspected transfer |             1 TiB |
| Encrypted manifest response                   |             1 MiB |
| File plaintext frame                          |             4 MiB |
| Encrypted overhead per frame                  |          60 bytes |
| Native automatic receiving threshold          | 100 MiB aggregate |
| Native/browser-managed storage reserve        |           256 MiB |
| Browser buffered individual save              |            25 MiB |
| Browser ZIP plaintext total                   |            25 MiB |
| Source filename length                        |  1,024 characters |
| Saved filename component                      |   200 UTF-8 bytes |

The 1 TiB ceiling is a protocol/client safety bound, not a promise of available
device storage. Native owner-inbox saves apply the aggregate preflight to the
selected submissions; paging does not automatically authorize future arrivals.
Configured server budgets, per-file size limits and per-link allowances can be
lower. Every file, including an empty file, has at least one authenticated frame.

Native scanned downloads at or below 100 MiB can proceed after inspection.
Larger transfers require explicit consent showing the destination server, count
and size. Consent is bound to the inspected file list and transfer context;
changed content requires fresh consent. Saving only the available subset of an
attempt-limited transfer also requires consent. Browser downloads always start
from an explicit Save action; scanning alone does not fetch file payloads.
There is no separately configurable metered-data policy in this release.

## Validation before publication

Clients inspect the complete decrypted manifest before requesting file content.
They validate file count, resource IDs, unique encryption contexts, supported
framing, nonnegative sizes and the aggregate bound. They cross-check the server's
complete-transfer metadata, file identities and encrypted sizes against that
manifest. Optional unknown download counters remain unknown; they are not
treated as zero. Known policies and counters must have valid integer ranges.

Authenticated frames bind the file context, total size and chunk position.
Streaming readers enforce actual frame/plaintext lengths and exact EOF, rather
than trusting Content-Length or the manifest alone. A truncated, modified,
reordered, surplus or mismatched stream must not publish a completed local file.
Do not interpret successful decryption as malware detection.

Browser transfer metadata is capped at 128 KiB and encrypted manifests at 1 MiB.
Control reads have a 10-second deadline and accept cancellation. Error responses
are read once with a 4 KiB cap; only recognized policy codes are translated into
messages, never raw server HTML/text. A cancelled save skips the final metadata
refresh so a stalled response cannot delay cleanup. Payload transfer deadlines
remain distinct from these small-control-response limits.

## Space and temporary copies

Android and iOS check available space for the remaining selected plaintext plus
256 MiB. Their pipelines stream into pending/private destination files and
publish in place after verification; there is no full ciphertext spool, ZIP or
mandatory second on-disk plaintext copy. Writes recheck space and exact length.
Other applications can still consume free space after admission, so IO failures
must leave incomplete output unpublished and resumable state actionable.

Browser saves up to 25 MiB use a bounded Blob. Larger saves require a secure
context and either a save-file picker or browser-managed private storage
(OPFS). Without those facilities, the page explains that a supported HTTPS
browser or the mobile app is needed.

For OPFS, a storage estimate must report valid known quota and usage. Admission
leaves 256 MiB plus room for the staging file and a full-file browser download
handoff copy. During writes and before handoff, checks retain the full handoff
allowance even when only a few bytes remain to write. The check conservatively
charges its own staged bytes when an estimate omits uncommitted writes. Low or
unknown quota blocks the save before file payload retrieval. Quota failures
after admission abort the pending file and show an actionable storage message.

Origin quota is advisory and is **not** physical free space in the Downloads
folder. Browsers generally cannot report that destination's free space for
picker or Blob saves. The page explains this limitation and asks recipients to
allow room for files and temporary copies with at least 256 MiB left free. A
picker may ask the user whether to replace an existing file. Other applications
and concurrent browser tabs can race any preflight; these checks cannot reserve
disk space globally or guarantee the operating system completes a save.

OPFS files are removed after the download consumer has had time to acquire the
file-backed Blob; abandoned files in the dedicated temporary directory are
eligible for cleanup after 24 hours. Cancel is checked again after committing an
OPFS writer and reading its Blob, so a cancelled close does not initiate a
download handoff. Cleanup does not touch other origin storage.

## Filenames, retries and opening files

Clients reject paths, traversal and NUL bytes. Display/save names remove control
and bidirectional-formatting characters, portable filesystem hazards and
trailing dots; UTF-8 component limits retain meaningful extensions. Unicode
letters remain usable. Browser filenames also avoid Windows device names;
duplicate download/ZIP names and native destination collisions do not silently
merge file content. Raw authenticated manifest identity is retained separately
where resume comparisons need it; rendering a safe label does not rewrite the
manifest or grant fresh download consent.

Files stay inert until the user chooses Open or Share. The web hands plaintext
off as a download rather than embedding active HTML/SVG in the service origin.
Neither app installs an APK, executes content or expands an archive
automatically. Native URI grants cover the selected file only. Receiving an
executable or archive still requires the recipient's judgment before opening.

Receiving jobs are sequential/bounded. Cancellation preserves verified saved
files, removes incomplete outputs, and does not trigger automatic payload
retry. Native checkpoints verify local publication before reusing it. A fresh
network download can consume another attempt, even after a failed or cancelled
previous one. Delivery receipts are retried independently of file downloads;
saving only a subset cannot acknowledge the entire transfer. Browser handoff
means decryption and handoff succeeded, not that the operating system completed
writing the recipient's disk.

Scanned external links use isolated anonymous clients, visible server identity
and explicit actions. Saved account credentials are not forwarded to a different
server. Redirect restrictions and normal TLS validation still apply. Pairing is
a separate confirmed flow; private keys/fragments must not be sent to server-side
scanners, diagnostics or abuse reports.

## Verification boundary

The plan records unit, mocked-browser and real-backend evidence separately.
Relevant regressions exercise malformed/oversized metadata, aggregate limits,
deceptive names, unknown/low quota, changing quota, failed writes, cancellation,
surplus/truncated framing and legitimate streamed files above 100 MiB. Source
and portable helper checks do not establish native iOS application behavior.
Physical/older Android, native iOS app/share-extension builds, OS save/open flows,
low-storage recovery and background/process-termination behavior remain release
gates until run in their required environments.

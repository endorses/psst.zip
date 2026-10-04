# Stored-file recovery

The server checks payload files against their database metadata before accepting
an upload continuation, publishing a transfer, or starting a download. A
background worker also checks database-referenced files, including files that
receive no requests. Restarting the server invalidates previous scan coverage.

## Interrupted uploads

The upload offset is the number of bytes committed in the database. Disk writes
sync the payload, its directory entry, and its parent directory before an offset
can be acknowledged. A write, sync, close, or database failure can still leave
uncommitted bytes on disk. Recovery never advances the committed offset merely
because those bytes exist.

For an unpublished transfer, the check truncates bytes beyond the committed
offset. If the payload is shorter or missing, it rewinds the offset to the bytes
that remain and clears upload completion as necessary. A missing empty payload
is distinct from an existing empty file. Repair preserves the full allocation
reservation and never refills cumulative receive-link allowances.

An upload client must use the server's offset and resend the exact original
ciphertext. The current web and native clients stop when recovery requires rewinding the
offset and use a fresh transfer when the user retries. Web retries can recover a
lost acknowledgment within retained ciphertext, but do not support rewinding to
an earlier frame. They must not regenerate an arbitrary
ciphertext suffix: encryption uses fresh nonces, so that could combine bytes
from different encrypted streams. This recovery change does not add automatic resume after an offset rewind.

## Published files

Published payloads are immutable. Missing files, incorrect lengths, or
inconsistent completion metadata block download with `503 payload_unavailable`.
Inspection or repair failures return `503 storage_check_failed`. These checks
happen before reserving a download attempt. Error responses and persisted issue
records contain fixed categories, not filesystem paths or encryption material.

The server does not shorten, recreate, or refund a published payload. Restore
the correct encrypted file from a matching backup, or revoke the resource and
let normal cleanup remove it. A later successful check clears the recorded
issue. Downloads advertise their declared content length and never stream bytes
beyond it; a payload shortened during a download produces an incomplete response.

These are length and metadata checks, not cryptographic integrity verification.
Only the recipient can authenticate and decrypt file contents. Replacing bytes
with different bytes of the same length cannot be detected by this scan.

## Administrator visibility and worker bounds

The Resources page includes **Stored file checks**, available through the
administrator-only, non-cacheable `/api/v1/admin/storage-checks` endpoint. It
separates unavailable published payloads, inspection/repair failures, and busy
files. Global scan failures remain visible even if the database could not persist
an individual issue. A clean pass cannot clear a failure that arrived after the
pass started. Refresh failures preserve the previous snapshot with an explicit stale
notice. A completed pass can still contain unresolved issues.

The worker selects at most 64 database file rows per sweep using an ordered
cursor. It uses nonblocking transfer locks and retries busy files on subsequent
passes. Issue records are bounded to one per file and disappear when file
metadata is removed. Database operations observe cancellation; synchronous
filesystem calls cannot be forcibly interrupted if the operating system or
underlying device stalls. Shutdown stops scheduling work and allows five seconds
for the worker to finish, reporting a timeout if it cannot.

Payload inspection, reads, writes, and truncation use descriptor-relative opens
without following symlinks. Payloads must be regular files under the configured
storage root. Storage configurations with symlinked directory components must
use the actual directory path instead.

Bounded resource cleanup also opens directories relative to held parent
descriptors and unlinks entries without following symlinks. It checks directory
identity during traversal, bounds enumeration/deletion work, and syncs parent
directories before acknowledging removal. A sync or close failure keeps the
cleanup task and its reservations, even if the file already appears absent;
retry confirms durable removal before deleting database metadata. Application
resource and reader locks remain necessary. These checks do not make arbitrary
privileged filesystem renames or mount changes atomic with cleanup.

## Database summary reconstruction

Storage admission uses the `resource_usage` database view, calculated from
canonical rows. It does not depend on the administrative resource summaries.
The separate counter worker checks derived resource totals and the cleanup and
file-check summary counts. It restores missing resource summaries and removes
summaries for resources that no longer exist. Until a missing summary is repaired,
the resource remains visible in the administrator inventory with its totals
explicitly unavailable; lookup, revocation and cleanup remain accessible.

Reconstruction reads canonical files, manifests and inbox membership rather than
trusting child summaries. It stages bounded work in the database and checks source
revisions under the writer lock before replacing a summary. A resource that
changes during reconstruction must be retried, allowing other resources to make
progress. Inbox membership changes after inbox discovery begins require another
pass: ordinary membership accounting can temporarily copy an unrepaired child
summary into an already checked inbox. This also covers a child removed before
its own repair finishes. Continued membership changes can keep verification
pending, without blocking upload admission or other reconstruction jobs. Restart
invalidates completed coverage but preserves valid partial jobs.

Each step consumes at most 64 source-row/probe units, and at most 64 jobs can be
queued. If blocked jobs fill that queue, discovery can defer them to another
complete pass so later healthy resources are still reached. Deferred work keeps
coverage incomplete. Queue counts therefore describe the current queue, not
every resource still needing verification. Existing identity keys cannot be
renamed during a scan. Timestamp-only retries do not invalidate a summary's
source revision when its counted state is unchanged.

Normal writes maintain the derived counters transactionally after a completed
pass; the counter worker then avoids idle database writes. A database failure
during an idle check invalidates coverage and starts a fresh verification pass
when the database becomes available again. Missing cleanup or
file-check summary rows are recreated with conservative default scan cursors,
while repair of existing rows preserves their operational metadata. Overflow or
invalid source values prevent publication instead of producing wrapped totals.

The Resources page's **Counter checks** panel reports queued, retrying and failed
work separately from physical payload checks. Its administrator-only
`/api/v1/admin/counter-checks` response is non-cacheable. A stale or failed status
request keeps the last snapshot with an explicit warning. Counter verification
does not imply a complete disk inventory or that an independently restored backup
is consistent.

Reconstruction never reduces cumulative receive-file/byte allowances, received
submission counts, download attempts or traffic history. Those values cannot be
recovered from the remaining files after historical data has been deleted. Use
matching database and payload backups; counter reconstruction cannot recreate
consumption that is absent from a restored database.

## Scope still to complete

- [ ] Discover and safely account for or remove orphan filesystem entries that
      have no database file row.
- [ ] Validate database and physical-capacity consistency after a mismatched
      restore. Derived database summaries are reconstructed as described above;
      lost lifetime allowances require matching historical database state and
      cannot be inferred from surviving payloads.
- [ ] Integrate reconciliation coverage with effective-capacity admission and
      public guest capacity responses.
- [ ] Perform the complete backup/restore and hostile-client release exercises
      in the security plan.

A completed database-file pass is not evidence of complete disk health, physical
disk usage, orphan-free storage, or a consistent backup. Restore the database and
encrypted payloads together with the server stopped. Keep the instance private
until the remaining security-plan release gates are verified.

# Storage, object and retention policy

The administrator's resource settings apply to all creation APIs, including
anonymous submissions charged to the receive-link owner. Per-link allowances
remain additional cumulative limits; they are not a substitute for account and
server capacity protection.

| Setting                                         | Server default | Per-account default |
| ----------------------------------------------- | -------------: | ------------------: |
| Reserved encrypted bytes, including manifests   |         10 GiB |               2 GiB |
| File records                                    |         10,000 |               1,000 |
| Transfer records, including receive submissions |          2,000 |                 200 |
| Receive links                                   |            500 |                  50 |

Maximum retention defaults to seven days. Unfinished uploads have a separate
24-hour lifetime. The disk safety reserve is the larger of 256 MiB and 5% of the
relevant filesystem's total capacity. Data and database volumes are checked,
including when they are separate mounts. A small disk is constrained by actual
available space even when its configured server quota is 10 GiB. A large,
almost-full disk can reach its 5% reserve while still showing many GiB free.
Administrators can review and adjust the reserve rather than silently disabling
it; accepted settings remain finite (at least 1 MiB and 1%).

Storage quotas accept 1 MiB–1 PiB, object ceilings 1–1,000,000, and retention
60 seconds–365 days. Pending-upload lifetime cannot exceed maximum retention.
The disk byte reserve accepts 1 MiB–1 TiB and the percentage 1–50%. File-size and
receive-link policies may impose lower limits. Quotas account for encrypted wire
bytes, so a plaintext file exactly equal to the remaining byte budget will not
fit once its framing/manifest are included.

## Reservation and reclamation

Creating a file reserves its full declared encrypted size before content is
accepted. Manifest writes charge their stored lengths, including replacement
deltas. Atomic database operations prevent parallel links/accounts from spending
the same server allowance. Abandoned and unfinished allocations still occupy
budget until cleanup succeeds; creating many empty files also consumes object
allowances.

“Reserved storage, including occupied” is the quota charge, not an additional
amount to add to occupied bytes. “Tracked occupied storage” is an estimate from
persisted upload offsets and manifest lengths, not `du` or filesystem allocation.
A crash between a disk write and offset persistence can make that estimate low;
the complete file reservation remains charged. Filesystem block overhead,
SQLite/WAL/indexes, stale/untracked data and other processes are separate from
the ciphertext estimate. “Available within storage quota” is therefore not a
promise of available disk capacity.

Current capacity can be reclaimed after verified payload deletion and completion
of active reader leases. A failed deletion stays charged and can be retried.
A crash after deletion but before the database update conservatively retains the
charge until retry. Receive-link cumulative file/byte allowances do not refill
when stored children are deleted. Counters are derived from retained resource
records during migration/reconciliation; already deleted historical allocations
cannot be reconstructed.

Lowering a quota does not delete user files. It blocks allocations that would
exceed the new policy while leaving administration, revocation and cleanup
usable. Restoring a database does not reset policy intentionally; validate
restored policy and reservations before reopening public traffic.

## Live disk pressure and support boundary

Admission checks include outstanding reservations and recheck the volume reserve
during upload IO. External processes may consume disk after admission; write
failures still need handling. A policy change or newly reached safety reserve
interrupts a PATCH with a stable capacity error and preserves a resumable offset
for accepted bytes. Clients must not automatically retry terminal quota/disk
rejections. Free space, reclaim expired data, or change the policy before an
explicit retry.

Run one backend process per SQLite/data store. Transactional allocation checks
are atomic across database connections, but reader leases and active-stream
cancellation are process-local. Multiple independent backend writers sharing
one volume are unsupported.

The public configuration publishes policy ceilings. Authenticated usage views
show quota headroom and explicitly retain a stale/error state when refresh fails.
`GET /api/v1/auth/usage` and `GET/PATCH /api/v1/admin/resource-policy` also return
a timestamped `capacity` snapshot. Account snapshots use the lesser of account
and server headroom; they expose the account's usage, not other users' records.
Database counters are read together in one transaction. The filesystem check
subtracts the configured reserve, 1 MiB of metadata/WAL room, and outstanding
declared file bytes not yet written. A separate database volume must also have
metadata room. `available_wire_bytes` refers to encrypted file allocations;
manifests need their own space, and transfer/inbox object ceilings still apply.

Snapshot state is `ready`, `blocked`, or `unknown`. Known quota/disk exhaustion
reports zero; a failed or unconfigured filesystem check reports `unknown` with
null bytes rather than inventing zero. Policy and usage remain readable for
recovery when disk probing fails. Available object counts and the check time
are included. The web UI distinguishes these states and explains that a snapshot
is not a reservation. Pauses, per-link limits and final allocation checks still
apply, and concurrent work can change availability immediately. Public/guest
effective-capacity integration and explicit reconciliation-state reporting are
not yet complete.

## Bounded history

Resource and administrator-user queries accept `limit` (1–100) and an opaque
`after` cursor, returning `next_cursor` or null. The web UI loads one 50-entry
page at a time. Native complete-snapshot synchronization uses at most 100 pages,
a 1 MiB response bound per page, and a 30-second aggregate deadline. A failed,
repeated-cursor or oversized scan leaves existing local history intact rather
than treating unseen records as deleted. Very large native history still needs
a separate incremental browsing UX; the hard bound is deliberate.

Account/session metadata retention, audit/traffic-table retention, enforceable
traffic budgets are separate plan requirements. Manual [incident controls](incident-response.md)
can pause payload traffic or shut down an account; storage policy itself does
not enforce a hosting provider's traffic bill.

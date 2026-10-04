# Traffic and authentication metadata retention

File quotas do not bound operational records. The server separately limits daily
traffic detail, accounts, signed-in sessions and pairing grants. These limits
apply through the APIs as well as the web UI.

## Traffic history

Daily traffic detail covers 400 UTC dates, including today. A chart request can
still select at most 367 dates. The API reports `history_retained_from` and
`history_retention_days`; the web date pickers use that coverage boundary. Requests
outside coverage return `400 traffic_history_unavailable`. Missing retained days
mean no recorded traffic; dates removed by retention are not presented as zero.

Measured lifetime upload/download bytes and file counters live in a separate
durable row. The migration seeds it from existing chart records once, and later
measurement updates modify the lifetime row and daily detail in the same
transaction. Removing daily detail never subtracts lifetime traffic. The overview
and lifetime totals therefore remain useful after years of operation.

The enforcement ledger also retains 400 dates of observed and conservative daily
charges. Older amounts are aggregated into bounded archive counters. This covers
the current billing cycle for every supported start day, including February and
day 31. Changing budget settings does not reset those charges. Monitoring totals
and enforcement charges remain separate measurements as described in
[traffic limits](traffic-limits.md).

Outstanding byte leases are never discarded by retention. Late settlement or
crash recovery credits the retained ledger or archive according to the lease's
original date; it cannot recreate a pruned day or refund transferred bytes. The
persisted coverage boundary only moves forward. If the system clock moves back
beyond available accounting coverage, affected reports/budget operations fail
rather than assume missing traffic was zero. Correct the system clock and
restore healthy accounting; do not clear counters to reopen transfers.

Cleanup runs at startup and on the ordinary cleanup interval. Each traffic pass
removes at most 512 chart rows and 512 enforcement detail rows in one transaction.
Large pre-upgrade history drains over multiple passes; reads already restrict
their detail to the retained window. A failed pass rolls back, and file cleanup
still runs. Freed SQLite pages can be reused; retention does not promise the
database file immediately shrinks on disk.

## Accounts, sessions and pairing

| Record                                                   | Limit |
| -------------------------------------------------------- | ----: |
| Accounts, including administrators and disabled accounts | 1,000 |
| Active sessions per account                              |    32 |
| Pending pairing grants per account                       |     8 |
| Retained pairing records per account                     |    64 |

These metadata ceilings are fixed for this release.

Account creation beyond capacity is rejected with `409 account_capacity`.
Disabling an account preserves its record and does not restore account capacity.
Existing accounts are not deleted to enforce this ceiling; an upgraded server
already above the limit must stop creating additional accounts.

Successful sign-in replaces the earliest-expiring active session when necessary
(normally the oldest, since issued sessions have a thirty-day lifetime). This
happens only after credential/factor verification and within the transaction that
creates the new session. Invalid login attempts cannot evict sessions, and a
failed transaction does not consume a recovery code. Pairing a device preserves
the web session that authorized that pairing. A rotated device receives the
ordinary session-expired response and must sign in again; Android and iOS keep
their existing handling for that response.

Pairing grants still expire after five minutes. Terminal records normally remain
for the existing fifteen-minute observation window after expiry so a page can
explain what happened. At capacity, creating another pairing returns
`429 pairing_capacity`; wait for expired records to be reclaimed before creating
another code. Cancellation or replacement cannot create an unbounded terminal
history. This does not restrict public file recipients or require them to sign in.

Legacy session overages are reconciled incrementally. Retirement removes a
session's authority before physical deletion; expired parent sessions also make
their pairing grants unusable. Cleanup deletes bounded batches of dependent
pairings before deleting session records, avoiding an unbounded foreign-key
cascade. Valid authentication remains available during this reconciliation.
The session response contains at most 32 entries, including the requesting
session, and reports `sessions_limited`, `total_active_sessions` and
`total_active_sessions_exact`. During a legacy overage the count is a bounded
lower bound, and the web UI says “at least” instead of claiming an exact total.
The web UI explains a partial legacy view rather than silently calling it complete.

Administrator pending-factor and recovery-code records retain their separate
limits: one pending enrollment and ten hashed recovery codes per administrator.
Metadata cleanup does not reset factors, passwords, operator policy or accounts.

## Verification and remaining scope

Run the backend race suite from `backend/`:

```sh
go test -race ./...
```

The [implementation plan](../plans/security-abuse-prevention-and-link-limits.md)
records the executed migration, accounting, concurrent issuance, bounded-cleanup
and UI cases. Separate [security activity](security-activity.md) controls retain
bounded administrator audit metadata, and optional [abuse contact](abuse-contact.md)
supports operator reporting without an in-app mail relay. Backups can contain older
records and must have their own retention/access policy. Restoring an old backup
can also restore revoked credentials and old accounting; use the documented
incident/recovery controls before reopening public traffic.

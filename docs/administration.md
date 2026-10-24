# Administration

Administrator accounts manage users, server resources and policy; use a separate
regular account for personal file transfers. The [account and connected-device guide](usage.md#accounts-and-connected-devices)
explains roles, temporary passwords, sessions and pairing.

## Contents

- [Operator guides](#operator-guides)
- [Metrics and traffic](#metrics-and-traffic)

## Operator guides

| Task                                                    | Guide                                                                    |
| ------------------------------------------------------- | ------------------------------------------------------------------------ |
| Enroll an authenticator or recover administrator access | [Administrator authentication](security/administrator-authentication.md) |
| Inspect resources, revoke links and track cleanup       | [Resource management](security/resource-management.md)                   |
| Inspect storage checks and repair interrupted uploads   | [Stored-file recovery](security/storage-recovery.md)                     |
| Set storage, upload and retention limits                | [Resource limits](security/resource-limits.md)                           |
| Enforce traffic allowances, bandwidth and concurrency   | [Traffic limits](security/traffic-limits.md)                             |
| Review recorded security events                         | [Security activity](security/security-activity.md)                       |
| Pause transfers or contain an account                   | [Incident response](security/incident-response.md)                       |
| Publish a contact for reports                           | [Abuse contact](security/abuse-contact.md)                               |
| Back up or restore a complete instance                  | [Backup and restore](security/backup-restore.md)                         |

## Metrics and traffic

**Overview** separates current gauges (enabled regular users, unexpired transfers
and receive links, and encrypted storage) from recorded file events. Stored bytes
measure actual tracked blob sizes, including partial uploads, plus encrypted
manifests; they exclude the SQLite database/WAL, backups, and untracked orphan
files. Gauges can change while uploads or cleanup run. Operational resource sizes
are declared encrypted file sizes and may include pending reservations.

A file is counted as uploaded once when its parent transfer finalizes. Standalone
sends and files submitted through receive links have separate totals. Delivered
files count once on the first accepted completed-transfer receipt, which is a
client report, not independent proof of saving. Deleting resources and cleanup do
not subtract from these durable event counters.

**Traffic** measures application transfer payload bytes, in UTC. Uploaded bytes
are encrypted manifest/file bodies actually consumed by the application;
downloaded bytes are encrypted bodies accepted by the HTTP response writer.
Repeated downloads, retries, and bytes moved before an interrupted/failed request
count. Declared sizes, GET quotas and receipt counts are not traffic. Requests
rejected before reading their body contribute zero application payload bytes.

These totals exclude web assets, control/account APIs, HTTP/TLS overhead, proxy
buffering/discarded bytes and unrelated services, so provider billing may differ.
The recording-start date is shown explicitly. There is no historical backfill;
the first billing cycle may be partial. Charts support up to 367 UTC days per
request; measured lifetime totals retain the entire recorded history.

Traffic deltas flush at 1 MiB, once per second, on day rollover and when each
request finishes. Graceful shutdown waits for final request flushes before
closing the database. Abrupt termination can lose the unflushed delta per active
stream (normally below 1 MiB, plus the current I/O operation). A failed counter
write permanently marks measurement coverage **degraded** when storage permits,
also retaining an in-process warning if the database is unavailable. Uncertain
failed commits are not retried as traffic deltas because that could double-count.
An unavailable metrics database produces an explicit error rather than zero
totals. Treat this as a usage monitor, not a provider billing ledger.

Administrators may configure an optional allowance, its start day (1–31), and
whether it counts outbound downloads or both directions. A day absent in a short
month becomes that month's last day. Boundaries are UTC, with an exclusive end
date. Editing these settings never rewrites raw traffic or blocks transfers.

Admin API routes are `GET /api/v1/admin/overview`,
`GET /api/v1/admin/traffic?from=YYYY-MM-DD&to=YYYY-MM-DD` (inclusive dates), and
`PATCH /api/v1/admin/traffic/settings`. Settings require all three fields:
`allowance_bytes` (positive safe integer or null), `cycle_start_day`, and
`basis` (`outbound` or `combined`). The existing admin-only
`/api/v1/auth/resources?all=true` supplies operational revocation metadata;
ordinary History endpoints do not grant admin transfer rights.

The separate **Enforce transfer traffic budget** control adds durable global and
account allowances, optional account overrides, upload/download bandwidth and
concurrent-stream limits. Budget enforcement defaults off; bandwidth and finite
concurrency still apply. Leases reserve at most 64 KiB before payload IO, and
uncertain outstanding leases become conservative charges after a crash. See
[traffic limits and recovery](security/traffic-limits.md) for defaults,
measurement boundaries, API routes, cycle rules and deployment limitations.
The monitoring allowance described above remains observational.

Daily traffic detail is retained for 400 UTC dates. Measured lifetime totals stay
available after old detail is removed. The date picker and API expose the retained
range; an unavailable period is not reported as zero. See
[metadata retention](security/metadata-retention.md) for pruning, outstanding
leases and upgrade behavior.

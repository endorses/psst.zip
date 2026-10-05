# History synchronization backend verification

The version 1 contract fixture is [history-sync-v1.json](fixtures/history-sync-v1.json).
Snapshots retain created-time pagination and add `generation`, `sync_cursor` and
per-resource numeric `revision`. Every row also includes server-issued
`history_after` (All) and `history_after_kind` (its kind filter) continuation
anchors, preserving exact seek position after cache eviction or deletion of the
anchor row. These use stored created-time ordering, including legacy timestamp
formats. Deltas use
`GET /api/v1/auth/history/changes?cursor=...&limit=50` and return `version`,
`generation`, `changes`, `next_cursor`, `has_more`. Upserts carry the same compact
resource object as snapshots; removals carry no resource. Revisions are
nonnegative safe integers. The feed's newest authoritative resource revision may
be later than the examined continuation, but never later than the database read
snapshot. Clients compare revisions independently by resource identity.

Default/max feed counts are 50/100; responses are limited to 1 MiB. Cursors are
opaque canonical base64url strings of at most 512 characters. HTTP 409 and
`X-Psst-Error-Code: history_sync_reset_required` require bounded rebootstrap.
Malformed and cross-account cursors return 400. Public config advertises
`history_sync_version: 1`. Both snapshot and feed read transactions recheck account/session permission,
and use `Cache-Control: no-store`.

SQL mutation triggers write identity-only events atomically, including base
resources, shared titles, derived totals, manifests, parent memberships, per-file
download attempts and deletion. A bounded private-transfer identity marker (one per retained
private child, cascading on deletion) suppresses child identities as well as
payload metadata. Creation marks the child before its triggers run without
changing its owner or quota accounting. Formerly public attachments retain their
necessary removal marker; private children remain hidden after unlinking. File offsets update physical occupied-byte
counters without emitting transient history changes. Read transactions capture
rows/revisions/watermark consistently. Journal count triggers trim at 10,000 per
account and 100,000 globally, in at most 256-event batches. Existing maintenance
prunes up to 256 events older than seven days. Persistent floors reject cursors
before retained coverage; opening the backend database rotates the generation.
`history_metadata_bytes` exposes conservative journal/index estimates separately
from file payload reservations; physical database capacity checks include actual
SQLite/WAL space.

Run from `backend/` with a writable Go cache. The complete API suite starts
loopback HTTP servers, so it needs sandbox permission to bind local ports.

```sh
go test ./internal/database ./internal/api ./internal/cleanup -count=1
GOMAXPROCS=2 go test -race -p 1 ./... -count=1
go vet ./...
```

- [x] Record final race, vet and formatting execution results below.
- [x] Exercise two real web clients against a disposable backend: bootstrap, rename an
      old entry, complete a receive upload, exhaust a send file, revoke/delete and
      restart. The real browser flow passed; native integration checks remain
      separately pending in the Android/iOS verification notes.

The backend tests cover empty stable responses, default/max request bounds,
strict cursor validation, parent summaries/private child hiding, download and
manifest facts, derived repair writes, older-record updates, inactive versus
removed records, rolled-back writes, no payload-byte events, per-account/global
retention, age floors, startup generations, canceled reads, missing-journal
rollback, account shutdown, real expiry cleanup, private identity suppression and concurrent
snapshot/feed handoff. Tests use disposable temporary databases, not operator data.

## Executed verification, 2026-10-05

The focused snapshot, feed, pagination and slot-reservation tests passed:

```sh
GOMAXPROCS=2 GOCACHE=/tmp/psst-history-sync-check-cache go test ./internal/database ./internal/api ./internal/cleanup -run 'HistorySync|HistoryPage|SlotReservation' -count=1
```

Database completed in 3.713s, API in 0.256s and cleanup in 0.051s. This focused
run preceded the final two private-identity tests; both are included in the race
results below. `go vet ./...`, Go formatting and `git diff --check` also passed.

All backend packages passed race validation across two serialized invocations
using the same Go source. The first invocation was intentionally interrupted
while running database tests when work paused; its completed package results
remain valid:

```sh
GOMAXPROCS=2 GOCACHE=/tmp/psst-history-sync-check-cache go test -race -p 1 ./... -count=1
```

| Package                     | Completed race result |
| --------------------------- | --------------------- |
| `cmd/server`                | Passed, 4.181s        |
| `internal/adminsecuritycli` | Passed, 4.817s        |
| `internal/api`              | Passed, 388.152s      |
| `internal/cleanup`          | Passed, 26.590s       |
| `internal/config`           | Passed, 1.006s        |

The remaining packages then completed with exit code 0:

```sh
GOMAXPROCS=2 GOCACHE=/tmp/psst-history-sync-check-cache go test -race -p 1 ./internal/database ./internal/incidentcli ./internal/reconcile ./internal/store ./internal/tus -count=1
```

| Package                | Completed race result |
| ---------------------- | --------------------- |
| `internal/database`    | Passed, 525.288s      |
| `internal/incidentcli` | Passed, 3.508s        |
| `internal/reconcile`   | Passed, 74.768s       |
| `internal/store`       | Passed, 1.089s        |
| `internal/tus`         | No test files         |

These results include the actual-model contract fixture, maximum-size response,
private-child identity suppression, retention, live-session authorization and
expiry cleanup tests. They do not claim native builds, device flows or the
native two-client manual scenarios. The real web two-client test, including
server restart, is recorded in [history-sync-web.md](history-sync-web.md). The coordinating agent owns this shared
temporary Go cache, removed after all backend verification completed.

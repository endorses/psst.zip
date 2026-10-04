# Transfer traffic budgets and bandwidth

Administrators configure **Enforce transfer traffic budget** in the web UI's
Traffic page. This is independent of the chart's monitoring allowance. Changing
the chart allowance does not block transfers; enabling enforcement does.

| Setting                                            | Default                             |
| -------------------------------------------------- | ----------------------------------- |
| Enforcement                                        | Off                                 |
| Server budget per cycle                            | 100 GiB                             |
| Default account budget per cycle                   | 10 GiB                              |
| Counting basis                                     | Outbound downloads                  |
| Cycle start                                        | Day 1, UTC                          |
| Upload bandwidth                                   | 100 MiB/s, shared across the server |
| Download bandwidth                                 | 100 MiB/s, shared across the server |
| Concurrent payload/inbox-event streams             | 64 total                            |
| Streams per account, IP, transfer and receive link | 4 each                              |

Budgets are suggested initial values until enforcement is enabled. Bandwidth and
concurrency controls remain active when budget enforcement is off. Each direction
has a 64 KiB token-bucket burst allowance. Bandwidth values accept 1 byte/s through
10 GiB/s; stream limits accept 1–4096. Request admission and rate limits apply
separately and may impose a lower effective concurrency.

The administrator may override an individual account's allowance or restore
inheritance from the default. An account cannot bypass the server budget. The
account default and a newly saved override cannot exceed the server allowance;
lowering the server allowance still constrains existing larger overrides.
Byte budgets use positive integers no greater than 9,007,199,254,740,991.

The five `MAX_*STREAMS*` environment settings seed concurrency once, on the first
startup with this traffic-policy schema, including an upgraded database. Invalid
or nonpositive seed values use finite defaults and values above 4096 are clamped.
Thereafter the persisted administrator policy is authoritative; changing these
environment variables or restarting does not overwrite it. Saving a traffic
policy also marks initialization complete.

## What spends an allowance

Encrypted file bodies and encrypted manifests count, including repeated reads,
replacement writes, retries and bytes transferred before a failure. Uploads count
bytes consumed from the request body; downloads count bytes accepted by the HTTP
response writer. A response write is not proof that a recipient saved the file.
Receipts, declared file sizes and download-attempt counters are separate concepts.
Requests rejected before payload IO do not spend observed payload bytes.

Public traffic is attributed to the resource owner. Anonymous submissions belong
to the receive-link owner. Deleting or revoking files never refunds traffic that
was already transferred. File download-attempt limits also apply; budget admission
is checked before charging a new file-download attempt. Manifest reads spend
traffic but do not spend file-download attempts.

**Outbound** counts downloads. **Combined** counts uploads plus downloads. Both
directions are recorded even with enforcement disabled. Billing periods use UTC,
an inclusive start and exclusive end, and a start day from 1–31. A missing day in
a short month becomes its last day. Changing the basis or cycle recomputes the
applicable charges from retained daily records; it does not clear usage or rewrite
the monitoring chart.

At migration, known global monitoring history seeds the global budget ledger.
Older account attribution cannot be reconstructed and is not invented. Account
views display the new ledger's recording start. The monitoring chart's existing
flush behavior and coverage warnings remain separate from this durable admission
ledger; their totals can differ around failures and migration.

## Reservations, interruption and recovery

Before payload IO the server atomically leases at most 64 KiB against both the
global and account budgets. Parallel requests cannot reserve the same remaining
allowance. After each IO, actual bytes become observed usage and unused allowance
is released. Reserved and conservative bytes appear separately from observed
traffic in usage views. Their sum, using the selected basis, determines the
charge against the budget.

A lease belongs to the UTC day on which it was granted. An IO spanning a billing
boundary can therefore carry at most 64 KiB per active payload stream charged to
the old period. The next lease uses the new period. With an unchanged policy,
admitted observed traffic plus reservations cannot exceed the budget. After a
limit is lowered, already granted leases can still finish: the maximum additional
payload admitted under the old policy is 64 KiB per active payload stream. A
policy-generation check prevents any stale stream from obtaining a fresh lease,
even before its cancellation callback runs.

Policy changes cancel affected active streams. A server policy change affects
all streams; an account override change affects that owner's streams. They must
be retried explicitly under the new policy. Saved local files remain available.

An uncertain accounting failure stops payload work. Do not retry an uncertain
settlement or pretend usage is zero. The process retains an unavailable state;
restore the database and restart the single backend process. Startup converts
every remaining durable lease into a conservative charge, without calling it
observed traffic. Thus a crash can overcharge by up to 64 KiB per outstanding
lease, but cannot refill that allowance. Conservative charges remain in their
original period. Do not edit ledger tables to recover capacity; inspect policy,
restore healthy storage, and deliberately adjust the allowance if appropriate.

Run one backend process per SQLite/data store. Database allocation is atomic
across connections, but stream cancellation, concurrency and bandwidth pacing
are process-local. Running independent backend writers against the same store
is unsupported.

## Status and operator recovery

| Status/code                          | Meaning and recovery                                                                     |
| ------------------------------------ | ---------------------------------------------------------------------------------------- |
| `429 traffic_budget_exhausted`       | Wait for the next cycle or ask the administrator to change the budget; retry explicitly. |
| `409 traffic_policy_changed`         | Controls changed during the operation; retry explicitly under the new policy.            |
| `503 traffic_accounting_unavailable` | Restore accounting/storage health and restart as described above.                        |
| `503 public_transfers_paused`        | The administrator paused transfers; resumption requires an operator action.              |
| `410 resource_revoked`               | The link was revoked and cannot be resumed.                                              |

Exhaustion responses include `retry_at`, `Retry-After` and `X-Psst-Retry-At`.
Machine-readable error codes are also in `X-Psst-Error-Code`. Once a response body
has begun, an interruption cannot reliably deliver a new HTTP error. Clients may
make a small, metadata-only traffic-status request to explain a recognized
transport failure. They must not automatically fetch the payload again to diagnose
it. A failed status check is not evidence of a zero allowance or a revoked link.

Administration, sign-in, health, policy changes, status, revocation and cleanup
remain available through bounded recovery/control paths. Budget exhaustion does
not disable recovery. Ordinary account sign-in disabling does not revoke existing
public downloads; use [account shutdown or link revocation](incident-response.md)
when those links must stop working.

API paths below are relative to `/api/v1`:

| Route                                                           | Access and purpose                                                                                                                                     |
| --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `GET/PATCH /admin/traffic-policy`                               | Administrator server policy and global ledger snapshot.                                                                                                |
| `GET/PATCH /admin/users/{id}/traffic-policy`                    | Administrator account policy and usage. PATCH accepts `{"account_budget_bytes": number}`; use `{"account_budget_bytes": null}` to restore inheritance. |
| `GET /auth/traffic-usage`                                       | Signed-in account's policy and usage snapshot.                                                                                                         |
| `GET /transfers/{id}/traffic-status?direction=upload\|download` | Minimal capability/owner-authorized state; default direction is download.                                                                              |
| `GET /slots/{id}/traffic-status?direction=upload\|download`     | Minimal receive-link state; default direction is upload.                                                                                               |

Usage snapshots separate observed, reserved and conservative bytes, include the
cycle and recording start, and report `ready`, `exhausted` or `unavailable`.
An account can have allowance left while the global budget is exhausted. These
snapshots are advisory, not reservations. Public configuration exposes policy
ceilings, not account counters. Public status exposes only a state and optional
retry time; it does not enumerate private inbox submissions or usage.

## Measurement boundary and validation

This bounds application transfer payload, not a hosting invoice. HTTP/TLS and
proxy overhead, rejected network traffic, static assets, control requests,
backups, other services and network-level attacks can still incur charges.
Use provider firewall/DDoS protection, billing alerts and provider spending caps
where available. Application budgets cannot guarantee a maximum provider bill.

The implementation has automated race, lease, rollover, fault, account-scope,
policy-change, pacing and interrupted-IO coverage. A disposable real-process
SIGKILL test verified conservative restart charges and usable recovery routes.
Browser and Android checks cover explicit recovery while retaining saved files.
iOS implementation includes the app and share extension, with portable Swift
mapper tests; native builds, Kotlin bridging and URLSession retry behavior still
need the [iOS validation steps](../../ios/README.md). Ledger retention/rollups and
the broader security plan's remaining controls are not complete. This document
does not declare the overall security release ready.

# Pausing transfers and shutting down an account

Administrators have two incident controls in the web UI. Neither can recall copies
already saved by recipients. Encryption does not establish that uploaded content
is safe or relieve operators of responding to abuse reports.

## Pause the instance

**Pause public transfers** appears in Overview and Server settings. It persists
across restarts and blocks new send links, receive links, receive submissions,
file allocations, file/manifest upload and download, and upload completion.
Authenticated owners' payload requests are also paused: signing in is not a bypass.
Existing links and server files remain stored, subject to their normal expiry.

The action cancels active payload streams. New payload requests receive HTTP 503
with `code: "public_transfers_paused"` and `X-Psst-Error-Code` carrying the same
code. A stream whose response already began may end as an interrupted connection;
its status cannot be replaced after headers have been sent. Accepted upload
progress remains resumable. Pausing does not reset download attempts or refund
bytes already measured. Clients preserve completed local files and must not
automatically replay a payload request after an explicit pause rejection.

Sign-in, administration, metadata inspection, upload-offset/status recovery,
revocation, health and cleanup remain available in their bounded control lanes.
Public configuration exposes the pause boolean, without exposing global usage.
Resumption requires **Resume public transfers**. It never restores revoked links,
reenables disabled accounts, recreates deleted files, or extends expiry.

For a standalone installation, use the same binary and database path as the
running server, under an OS account permitted to access that database:

```sh
DB_PATH=/actual/path/to/server.db ./server pause
DB_PATH=/actual/path/to/server.db ./server incident-status
DB_PATH=/actual/path/to/server.db ./server resume
```

For the supplied Compose backend, the process already has the correct `DB_PATH`:

```sh
docker compose exec backend /app/server pause
docker compose exec backend /app/server incident-status
docker compose exec backend /app/server resume
```

Use the same Compose project/file/environment options as the running installation.
The CLI requires an existing database and refuses to create a new one. It does
not start a listener, require a web session, or read private encryption keys.
The running server checks persisted state every 250 ms and cancels payload work
when paused or when the state cannot be read. Database contention and scheduling
can extend this interval; this is not a hard latency guarantee. Run one backend
process per SQLite/data store, as described in the [resource policy](resource-limits.md).
OS/database access is the authorization boundary for this local recovery path.

## Shut down one account

The Users page distinguishes **Disable sign-in** from the incident action that
shuts down the account and revokes its links. Disabling sign-in revokes sessions
and unused pairing grants, but does not itself revoke public links.

Account shutdown atomically disables login, deletes sessions and pairing grants,
and marks all owned transfers and receive links—including anonymous receive
submissions—as revoked before attempting filesystem cleanup. It then cancels
that owner's active streams. Other accounts remain available. The last enabled
administrator cannot be shut down.

The response reports the number of sessions, pairings, transfers and receive
links revoked, plus `cleanup_pending: true`. This acknowledges durable denial,
not completed deletion. Existing sessions subsequently receive an authentication
failure. Revoked resources return HTTP 410 with `code: "resource_revoked"` while
their records exist; after deletion, a missing-resource response is expected.
Reenabling an account does not un-revoke its old links.

Revoked rows form a persistent cleanup queue. Each one-second sweep visits up to
16 transfers and 16 receive links, advancing its cursor past failed or busy
resources. Inbox cleanup waits for its separately processed children. Files with
active readers are not deleted or refunded prematurely. Failed deletion remains
charged and is retried; restarting the server restarts the sweep without making
revoked resources usable. The administrator Resources view shows bounded cleanup
backlog, oldest-pending age and retry failures; see the
[resource policy and recovery guide](resource-limits.md).

## API and release status

Administrator endpoints are `GET/PATCH /api/v1/admin/incident-state` and
`POST /api/v1/admin/users/{userID}/shutdown`. PATCH takes the required boolean
`public_transfers_paused`; shutdown accepts an empty JSON object. Existing admin
authorization, same-origin mutation checks, recent administrator authentication
and bounded request handling apply. If proof expires, confirm your identity and
explicitly resubmit the action; it is not automatically replayed. Local commands
remain available under the OS/database authorization boundary. See
[administrator authentication](administrator-authentication.md) for factor setup
and recovery.

These controls are part of the ongoing security plan. Administrator second
factors, recent-authentication checks and separate
[traffic/bandwidth budgets](traffic-limits.md), bounded
[security activity](security-activity.md) and cleanup visibility are implemented.
A manual pause is
not an automatic spending cap. It also does not prevent charges for rejected
network requests, proxy/static traffic or network-level attacks.

Android and iOS present dedicated pause/revocation messages, retain saved files,
and require user action to retry payload work. Android disables OkHttp's implicit
connection retry. No equivalent claim is made about all underlying URLSession
transport retries on iOS; native bridge/build/device verification remains open.

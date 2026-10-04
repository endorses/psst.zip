# Security activity

Administrators can open **Security activity** in the web workspace to review
administrative changes, explicit link revocations and summarized authentication
failures. The server also exposes the same administrator-only, non-cacheable
metadata through `GET /api/v1/admin/security-events`.

## Recorded information

Each record contains a server-assigned integer ID and UTC timestamp, an
allowlisted action, authority category, outcome, count, and applicable opaque
actor/resource IDs. The authority distinguishes an authenticated administrator,
account owner, deletion capability, explicit local operator command and system
activity. An unrelated signed-in account is not credited with using someone
else's deletion capability. Background expiry and cleanup retries do not create
additional successful revocation events.

Actions cover account creation, enabling/disabling, password replacement,
shutdown, session/pairing revocation and pairing issuance/redemption;
administrator sign-in, recent authentication, authenticator changes and recovery;
file-size/resource/traffic policy changes; public-transfer pause/resume; and
explicit transfer/receive-link revocation. Internal initialization without an
authenticated actor is not presented as an administrator action. Initial
administrator creation is identified as system activity.

There are deliberately no free-form messages or arbitrary request fields.
Passwords, authentication headers/cookies, session tokens, pairing codes,
authenticator/recovery secrets, link fragments, encryption keys, URLs/query
values, filenames, decrypted metadata, request bodies, IP addresses and user
agents are not collected. Opaque resource IDs can support an operator's
investigation without giving access to plaintext. Records survive deletion of
the referenced account, session or resource metadata.

Authentication monitoring uses four fixed in-memory counters: rejected login,
rejected pairing, rejected administrator proof and throttling. At most four
summary rows are written per minute per server process, with an atomic batch
and retry after failed persistence. A normal request for the second factor is
not a failed proof. The summary timestamp is persistence time; its count covers
the interval since the previous successful flush, potentially longer than one
minute after a storage failure. It does not identify individuals or reconstruct
individual requests. Counts saturate at one billion and report degraded
coverage rather than allocating additional attacker-controlled state.

## Retention and access

Visible history covers the last 90 days, with a hard maximum of 10,000 rows:

| Category                                  | Maximum retained rows |
| ----------------------------------------- | --------------------: |
| Administration and local operator actions |                 8,000 |
| Account/link lifecycle                    |                 1,000 |
| Authentication summaries                  |                 1,000 |

New records evict the oldest rows in their own category. Authentication floods
and ordinary account/link activity cannot evict administrative records. Busy
categories can therefore cover much less than 90 days; this is bounded activity
history, not a guaranteed 90-day archive. Inserts and periodic maintenance remove
expired rows in batches of at most 256. Expired records are excluded from reads
even while the deletion backlog drains. The row cap applies immediately.

The API defaults to 50 records per page, allows at most 100, and returns a
`next_before` cursor for descending ID pagination. The web view replaces the
current page instead of retaining an unlimited client-side history. Only enabled
administrators can read the API. Mobile clients remain transfer clients and do
not gain administrative audit access.

SQLite files and backups contain this administrative metadata and require the
same protected access as authentication data. Retention removes live rows; it
does not securely erase old database pages, WAL files, snapshots or backups.
Operators must set backup retention separately.

## Failure and recovery behavior

Ordinary administrative changes and new access grants record their event in the
same transaction. If that recording fails, the change rolls back. This includes
resuming transfers through the web UI, changing policy, account creation and
password changes, pairing grants, and authenticator changes.

Actions needed to restrict access or recover control use a database savepoint
to isolate audit failures: public-transfer pause, account disable/shutdown,
link/session/pairing revocation, administrator sign-in/reverification and explicit
local pause/resume or factor-reset commands. They can succeed without an audit
record when the audit statement fails and its savepoint can be rolled back.
Failure of the underlying action transaction still fails the action; this does
not bypass authentication, authorization, recent-proof or last-admin checks.

The web history warns when recording is degraded. That flag is sticky for the
current server process; it is not proof of completeness before a restart.
Local commands report their own audit failure (`audit_degraded` in incident
command JSON, or a fixed warning for factor reset). A separate CLI process's
in-memory warning is not shared with the running server. Check the command's
output, repair database/disk problems and recheck controls explicitly.

Graceful server shutdown attempts a bounded final authentication-summary flush.
A crash, forced stop or exhausted storage can lose the pending interval.
Database/host administrators can alter records, and a backup restore can rewind
history. This feature is an operational aid, not an immutable forensic log or a
complete accounting of every request. No automatic public-transfer suspension
is triggered solely by missing audit history.

## Application and container logs

The supplied Compose services rotate container stdout/stderr at 10 MiB with
three files per container. The audit collector and cleanup report fixed failure
messages rather than printing raw event/request data. See
[deployment hardening](deployment.md) for the supplied stack's logging and
external proxy boundaries. Standalone services, external proxies and log
collectors need their own bounded rotation/retention and must not log secrets,
request bodies or full share links. Forwarding logs to another service expands
the set of people/systems that can read them; configure that deliberately.

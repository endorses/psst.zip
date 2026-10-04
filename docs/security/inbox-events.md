# Inbox event streams

Inbox events are an authenticated owner feature. A public receive invitation
permits submissions; it does not authorize event subscriptions or expose child
transfer IDs. Administrators and unrelated regular accounts cannot subscribe.
Clients retrieve the current inbox state through its owner-only metadata API;
events are refresh notifications, not a durable activity log.

## Resource bounds

Subscribers to the same inbox share one lifecycle poller. The poller exists only
while that inbox has subscribers and checks current inbox/session authorization
on a one-second cadence, with a two-second timeout for each check. Adding another
viewer does not create another polling loop.
Distinct session identities are checked together, rather than running one
database query for every viewer. This does not remove authorization checks when
a request opens or immediately before an event is delivered.

The event hub has an additional hard ceiling of 256 subscriptions in total and
16 per inbox. The administrator's [stream limits](traffic-limits.md) also apply:
the defaults permit 64 payload/event streams globally and four per account,
client address and inbox. The lower applicable limit wins. Admission fails
without an unbounded waiting queue; a rejected client receives a retry hint.

Each subscriber has room for 16 queued events. A subscriber that cannot keep up
is disconnected rather than silently losing notifications indefinitely. Its
pending network IO is cancelled so a stalled reader cannot retain its admission
slot. Each connection lasts at most ten minutes, and never beyond inbox expiry.
Blocked network writes also remain subject to the application stream deadline.

The worker shuts down when its last subscriber leaves. Database checks run
outside the hub lock and use cancellation, so an inbox waiting for the database
does not serialize all event subscriptions. Authorization lookup failures close
affected streams; they do not extend access on the basis of a cached result.

## Authorization and reconnects

Revoking a session, disabling an account, requiring password replacement,
changing ownership, revoking an inbox or expiring it ends the corresponding
access. A fresh check before delivering a queued event prevents a previously
queued notification from bypassing a known loss of authorization. As with any
response, data already written to a connection cannot be recalled.

The response is marked `Cache-Control: no-store`. Event payloads keep the existing
format and contain opaque IDs, never filenames, encryption keys or session
tokens. Revocation can close a stream without delivering a deletion notification.
Events are not retained for replay. On connection loss, retrieve the current
authorized inbox metadata before relying on an earlier event stream. Ordinary
request and stream admission still apply to reconnects.

Android keeps its existing metadata polling fallback after event disconnection;
iOS and the web inbox also retrieve metadata independently. This server change
does not add a new native event protocol or require clients to interpret a new
event type.

Event streams remain available during **Pause public transfers**, which blocks
payload operations. Account shutdown still cancels that owner's streams, and
changing stream policy can interrupt active connections. Administrative access
uses its separate bounded request lane.

## Verification

Run the event hub and HTTP tests, including the race detector, from `backend/`:

```sh
go test -race ./internal/api ./internal/database
```

Use isolated test data. The automated cases and their concrete results are
recorded in the [security implementation plan](../plans/security-abuse-prevention-and-link-limits.md).
This application-level verification does not establish external proxy buffering,
camera/device behavior or a multi-process event bus. The supported deployment
still uses one backend process per SQLite database/store.

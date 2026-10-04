# Resource management and cleanup

Administrators open **Resources** from Overview or Server settings to investigate
reported resource IDs. The resource manager lists send transfers, receive links
and receive submissions. It shows the owning account, lifecycle state,
creation/expiry times, file and child-transfer counts, reserved bytes, estimated
occupied bytes and encrypted-manifest bytes. The detail view includes retained
security events for the resource, its parent inbox and relevant owner actions.
It never retrieves filenames, decrypted manifests, encryption keys or deletion
capabilities.

Use the resource type and opaque ID when investigating a report. Do not paste
complete share links into logs, tickets or reports: their fragments may contain
decryption material. An ID identifies a server resource; it does not provide its
file contents. Metadata for resources already removed is unavailable, but their
direct security events can remain until audit retention removes them.

## Bounded inspection

`GET /api/v1/admin/resources` accepts `type`, `owner_id`, `status`, `after` and
`limit`. The default page size is 50 and the maximum is 100. Each resource-table
branch uses an ordered index and reads at most one extra row beyond the limit;
the server merges those bounded candidates. Cursors are bound to their filters.
Changing a filter starts a new page sequence. Status filtering uses the stored
lifecycle state; expiry is displayed separately from its timestamp.

`GET /api/v1/admin/resources/{type}/{id}` performs exact lookup. Types are
`transfer` and `slot`; a transfer with `parent_slot_id` is a receive submission.
The corresponding `/events` endpoint provides bounded, cursor-paginated related
security activity. Admin responses are not cacheable. Regular users and public
link holders do not gain access to this inventory.

Current file, child-transfer and byte totals are maintained transactionally as
files, manifests and inbox membership change. These totals avoid rescanning all
of a large inbox's children for every list row. The migration initializes them
from existing records. Historical submissions with no transfer owner inherit an
existing inbox owner, matching the existing inbox access and shutdown rules;
standalone resources without an owner remain unowned.

Reserved bytes include the undeleted payload reservation and encrypted manifest.
Occupied bytes are an estimate based on committed upload offsets and manifest
lengths, not a filesystem inventory or provider bill. Receive-link lifetime
allocation limits are separate: removing a submission does not refund the
number of files or bytes already allocated through that link.

## Revocation and retry

`POST /api/v1/admin/resources/{type}/{id}/revoke` accepts an empty JSON object.
It requires recent administrator authentication, persists denial and cleanup
intent, then cancels active streams. Revoking a receive link also denies its
existing submissions. File removal runs asynchronously; a `202` response means
cleanup is pending, not that files have been deleted. A `200` response with
`state: removed` confirms the resource metadata has already gone. Downloaded
copies on recipients' devices remain outside the server's control.

The resource view keeps pending or failed cleanup visible instead of removing
the row immediately after revocation. `POST .../cleanup` requests another cleanup
attempt with the same authentication requirement. It only accepts a resource
already eligible for cleanup, such as a revoked/expired resource or exhausted
payload; it cannot delete an active usable resource without revoking it first.
An active-resource request returns `409 cleanup_not_eligible`.
If payload-only cleanup finishes before the action response, `200` with
`state: complete` confirms cleanup while retaining the resource metadata; it
does not claim that the resource was removed or newly revoked.

Cleanup status uses fixed categories rather than exposing filesystem paths or
raw database/storage errors. Inspect the pending time, last attempt, next retry,
attempt count and failure category. Busy readers or an inbox waiting for its
children are distinct from a failed storage/database operation. A failed status
read means the outcome is unknown; refresh and inspect it before assuming the
operation completed.

## Cleanup visibility and recovery limits

The administrator-only `GET /api/v1/admin/cleanup` reports tracked pending,
failed and busy work, the oldest pending time, and discovery progress. Discovery
coverage matters: these counters describe discovered/queued work, not a claim
that every expired resource or physical disk entry has just been inspected.

Pending work and retry metadata survive restart. Expiry discovery advances
through at most 64 transfers and 64 receive links per sweep; deletion processes
at most 16 due items of each type. The incident worker runs every second. Failed
attempts back off up to five minutes; busy or partially processed items retry
after one second. Due work is ordered by its deadline, so continuous new arrivals
do not take priority over overdue retries. Explicit administrator retry can
prioritize the selected resource.

Inbox children are separate deletion work items. Initial revocation atomically
denies existing children in the database; subsequent cleanup attempts do not
repeat that bulk update or recursively delete every child's payload. The bundled
disk store limits each deletion attempt to 256 entry operations, directory reads
to 64 entries and nesting to 32 levels. It unlinks symlinks instead of traversing
them. Context cancellation is checked between operations; it cannot interrupt
an operating-system filesystem call already in progress. Alternate storage
implementations must implement bounded deletion to provide the same guarantee.

Resource reservations remain charged
until payload deletion is confirmed. Exhausted-download payload cleanup waits
for the final permitted response to release its reader lease and leaves readable
metadata until normal expiry. Explicit revocation cancels that response.
The discovery timestamp records the last completed pass, not worker health or
proof that storage is currently reconciled. Failed and busy counts are subsets
of the pending total, not additional resources.

These controls do not establish complete disk/database reconciliation after a
crash, power loss, external filesystem changes or a restored backup. In
particular, committed upload offsets are not proof that every physical byte is
present, and this resource manager does not enumerate orphan disk entries.
Those reconciliation and release-validation requirements remain open in the
[security implementation plan](../plans/security-abuse-prevention-and-link-limits.md).
See [incident response](incident-response.md), [resource limits](resource-limits.md)
and [security activity](security-activity.md) for related policy and recovery
boundaries.

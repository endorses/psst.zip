# Account history pages

Account history uses `GET /api/v1/auth/resources?limit=50&after={cursor}`. An optional
`kind=transfer` or `kind=slot` restricts the underlying query to sends or receive
links; omit it for both. The
regular account sees only its own standalone sends and receive links. An
administrator must explicitly use `all=true` for operational metadata; a regular
account cannot use that scope. Encryption keys, filenames and saved-file paths
remain on clients.

## Response and traversal

The response requires `paginated: true`, `transfers` and `slots` arrays, and
`next_cursor` (a string or explicit null). Limits default to 50 and permit 1–100.
The opaque base64url cursor is versioned, bound to the requesting account and
administrative scope and kind filter, and at most 512 bytes. Start without a cursor
after changing accounts, kind, or upgrading from the previous cursor format.

Each resource has a required summary:

```json
{
  "state": "ready",
  "completed_files": 2,
  "file_count": 3,
  "total_size": 180
}
```

Historical summaries being rebuilt use `state: "updating"` and null for all three
numbers. Required top-level `file_count`, `total_size`, and receive-link
`completed_files` match their summary values, including null. Clients must not
interpret unknown as zero. Counts describe retained canonical metadata; bytes are
stored ciphertext sizes, not local plaintext sizes or lifetime allowances. Inbox
summary semantics match [owner inbox browsing](inbox-pagination.md).

Pages use descending creation time, resource ID and resource kind. The database
seeks at most `limit + 1` raw candidates from each of the two indexed resource
tables, merges them and inspects at most `limit` candidates. Receive child
transfers consume candidate positions before being excluded from the account
history result. Consequently, a short or empty page can still have a next cursor.
Clients follow that cursor only on explicit navigation, without draining empty
pages automatically.

Membership checks and maintained counter reads use indexed lookups; page requests
do not aggregate the files of every inbox or scan ahead until a page is full.
Authorization, metadata and summaries share one cancellable read snapshot with a
two-second database deadline. Failed database reads return a fixed unavailable
error without partial history data.

## Client behavior

The browser, Android and iOS load one server page at a time and refresh only that
page. Previous/Next/First page controls keep a rolling window of 100 previous
cursors without capping forward traversal. Failed navigation preserves the last
successful page. Obsolete results are ignored after page, account, server or
credential changes. Filters apply to the underlying query and reset its cursor,
so matching older records are not hidden by a page of another type. Single-page
collections hide navigation controls; controls follow the entries.

Mobile history separates the server page from **On this device**, which retains
access to locally remembered links and downloaded files. Page merges update only
returned identities and preserve local names, keys, deletion capabilities and
save checkpoints. A record absent from a page is not evidence of revocation.
Explicit revocation and exact resource reads can establish unavailability. Opening
a sent-file detail on iOS refreshes that transfer directly instead of enumerating
account history.

On-device All merges indexed account and download streams in deterministic
descending time/identity order. Each source reads at most one bounded page at a
time; its cursor remains internal and the UI offers one Load more action.
Android shows a 50-entry merged page; iOS keeps a rolling 100-entry visible window
with 50-entry source buffers. Downloaded queries only downloads; Sent and Receive
links query only matching account records. This presentation does not combine
ownership, encryption keys or local removal with server revocation.

Resources include optional [shared titles](shared-link-titles.md). Fully exhausted
limited sends report `status: "exhausted"` and
`inactive_reason: "download_limit"`. Owner History retains the closed reason;
clients hide active sharing actions and offer creating a replacement. Successful
delivery receipts remain separate from spending an attempt.

Update server and clients together: clients now require the pagination/summary
contract and reject malformed or older responses rather than silently displaying
partial history as a complete snapshot. The endpoint does not delete stored
resources or refund lifetime allowances.

## Remaining storage and release checks

Remote pagination does not finish local storage bounds. Browser link/name metadata
now uses exact IndexedDB entries, Android account and guest history use indexed
local pages, and iOS account history uses a shared SQLite store with resumable
legacy import. These changes preserve original migration sources and local keys.
Mobile inbox checkpoints and iOS guest history/retry queues now use indexed records
as well. See [local history storage](local-history-storage.md) for the bounds,
migration exceptions and remaining checks. Native iOS builds and device checks
remain pending on macOS/Xcode; portable Swift checks are not an iOS build.

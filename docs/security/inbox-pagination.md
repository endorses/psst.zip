# Bounded owner inbox browsing

The web UI, Android and iOS use authenticated inbox pages. A public receive link
continues to grant submission access only; it cannot enumerate pages or retrieve
another sender's submission. Opening a specific browser submission uses the
[exact membership lookup](receive-crypto-design.md) instead of scanning pages.

## API contract

`GET /api/v1/slots/{slotID}/inbox?limit=50&after={cursor}` requires the regular
account owning the inbox. `limit` defaults to 50 and permits 1–100. Omit `after`
for the first page; subsequent requests use the opaque `next_cursor` returned by
the server. Cursors are versioned, inbox-bound and at most 512 bytes.

The response includes the slot identity, lifecycle and receive policy, plus:

```json
{
  "paginated": true,
  "transfers": [
    {
      "transfer_id": "submission-uuid",
      "status": "complete",
      "file_count": 2
    }
  ],
  "next_cursor": null,
  "summary": {
    "state": "ready",
    "completed_files": 2,
    "file_count": 3,
    "total_size": 180
  }
}
```

Identity/lifecycle fields are omitted from this illustrative fragment. All four
pagination/summary fields are required, including an explicit null cursor at the
end. The top-level legacy `completed_files` field is omitted from this endpoint;
clients use `summary`. While a historical summary is being reconstructed, its
state is `updating` and all three numbers are null. Unknown is never zero or an
unlimited allowance.

Pages seek raw membership IDs in ascending order. These IDs do not encode arrival
time, so controls say **Previous**, **Next** and **First page**, not newer/older.
The query examines at most `limit + 1` membership rows, then filters revoked,
expired and expired unfinished submissions. An empty visible page can therefore
still have a continuation. Each visible child's file count uses an indexed probe
of at most 101 records; an unsupported oversized child fails explicitly rather
than returning a truncated file list. Slot policy, child metadata and the summary
come from one read snapshot with a two-second database deadline.

Summary numbers describe retained canonical metadata across the entire inbox:

| Field             | Meaning                                                        |
| ----------------- | -------------------------------------------------------------- |
| `completed_files` | Files marked uploaded in submissions whose status is complete. |
| `file_count`      | All retained file allocations, including unfinished ones.      |
| `total_size`      | Sum of declared file sizes, excluding encrypted manifests.     |

Expired rows can remain in those totals until cleanup removes them, even though
they are absent from the visible page. Revoked submissions contribute no completed
files. These counters are neither plaintext sizes, cumulative lifetime receive
allowances, nor proof that payloads remain readable on disk. Counts update with
file completion, transfer status, membership changes and deletion. Historical
values are filled by the existing bounded counter-rebuild worker; ordinary page
requests never scan the complete inbox to calculate them.

## Client behavior

- [x] Load one page at a time. Poll only that page and replace it only after a
      successful, validated response. Failed navigation preserves the last
      loaded page. Reject late responses and errors from a different inbox,
      account, credential generation or page request.
- [x] Keep at most 100 previous cursors in memory, while allowing further forward
      navigation. First page remains available after older backtracking entries
      fall out of the window. New submissions with IDs before the current cursor
      can be found by returning to the first page.
- [x] Show loaded-page file counts separately from whole-inbox totals. Unknown
      totals show as updating. Do not silently fall back to an unpaged server
      response or automatically drain all pages.
- [x] On mobile, save only the displayed submission set. Freeze its page and
      submission identities while requesting storage/data consent and saving;
      later arrivals do not expand that approval. Preserve per-file checkpoints
      and delivery receipts. A partial page cannot establish that every file in
      the inbox has been saved.

## Upgrade and verification

Update the server and clients together. The legacy `GET /slots/{slotID}` still
returns a complete response for at most 100 raw memberships. Larger inboxes
return HTTP 409 `inbox_pagination_required`; historical unknown summaries return
HTTP 503 `inbox_summary_updating`. It never silently returns just the first page.
The new endpoint permits browsing while totals are updating. Neither condition
deletes stored submissions or changes cumulative upload/download allowances.

The migration adds derived fields without scanning historical file rows. It
resets at most 64 in-progress derived-counter jobs so the bounded worker can
reconstruct the new fields. Completion/status mutations invalidate staged scans;
restarts retain valid completed summaries and resume normal reconstruction.

Source and regression evidence is recorded in the
[implementation plan](../plans/security-abuse-prevention-and-link-limits.md).
Native iOS build and device validation require macOS/Xcode and remain a separate
release gate. Account-history paging and local history storage bounds are separate
requirements; inbox paging alone does not complete them.

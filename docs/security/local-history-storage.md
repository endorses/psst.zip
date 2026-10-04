# Local history storage

Remote history pages do not by themselves bound local database reads or saved
metadata. Local stores must address exact records or explicit pages, preserve
account/server boundaries, and retain keys and save checkpoints across migration.
This document records the current implementation and its remaining storage work.

## Browser

Links and local labels use IndexedDB records keyed by account, resource kind and
resource ID. The browser origin supplies server isolation. History loads entries
for the visible server page; opening an inbox loads that exact entry. Renames,
new links and removals change individual records. Deletion tombstones prevent an
interrupted legacy import from restoring a deliberately removed link.

Existing receive private keys remain in their per-inbox records. Import retains
the original localStorage link/label maps and parses their entries incrementally,
without decoding the whole map into an object. Batches yield and can be retried;
new edits and tombstones take precedence over imported entries. Missing storage
access, malformed entries or failed persistence produce visible recovery feedback.
A failed write must not be presented as a saved link.

The legacy localStorage API necessarily allocates its complete string when reading
an old map. This is a migration exception, not a bound on that initial allocation.
Normal operations after import do not read or rewrite the map. Original sources
remain available for recovery rather than being pruned to meet a page limit.

## Android

Local account links use indexed, account/origin-scoped Room keyset pages. Downloaded
files have independent page controls in the same History view, backed by a SQLite
index of guest records. Import processes a fixed number of old directory entries
at a time; existing per-record JSON and key material remain intact. Committed
records survive interruption, newer writes win, and deletion tombstones prevent
resurrection. Restarted directory scanning skips already indexed payloads.
Legacy guest metadata reads are capped at 512 KiB per record. Oversized or corrupt
records retain their original files and produce a visible recovery warning;
unsupported metadata is not silently truncated. The Room schema's index/backfill
is a one-time table migration, separate from bounded normal history reads.

Background guest receipt/cleanup retries select a bounded batch. Neither local
browsing nor normal background retries need to load and sort the full directory.
Older unowned account records retain their isolation and are reported as preserved
legacy history; signing into a different account does not adopt their keys.

## iOS account history

The main app and share extension share a SQLite account-history database in a
protected App Group directory. Keys retain their existing Keychain identities.
Rows contain one serialized transfer/receive record, with indexed scope, kind,
creation time and identity. Exact reads and writes do not decode the complete
history. Local link pages seek at most 50 entries; the cursor trail is capped at
100, with First page and further forward navigation available.

The initial legacy JSON array is streamed into the database in resumable batches.
Each batch commits rows and its byte offset together. Main-app/share-extension
writer transactions protect concurrent local names, paths and metadata. Import
never overwrites newer rows or revives a tombstoned deletion. The original JSON
and earlier preferences remain intact. Completed migration markers are authoritative
across backup/restore; an incomplete import rejects a missing or changed source.

Account history writes wait until import finishes. The main app shows import
progress/retry feedback, and sending through the share extension waits before
creating an upload. An account change or cancellation during that wait prevents
the upload from starting under a different session. Very old UserDefaults data
requires one allocation to copy it to a streaming source file; the preferences
API cannot supply it incrementally.

The SQLite layer rejects individual record bodies above 16 MiB and caps the total
body bytes loaded by a page at 16 MiB. These are explicit failures that preserve
source data, not truncation. Unsupported oversized legacy records require the
remaining checkpoint-storage work below; this limit is not a claim that every
historical receive inbox has already been normalized.

## Remaining work and validation

- [ ] Replace growing per-inbox saved-file/checkpoint maps with indexed child/file
      records on both mobile platforms, preserving existing paths and receipt
      state. A bounded number of history rows is not sufficient when one row can
      grow with every received submission.
- [ ] Migrate iOS guest download history, receipts and other device-wide queues
      to bounded indexed operations. Its existing guest JSON snapshot still
      loads the complete collection; account-history migration does not fix it.
- [ ] Verify native iOS SQLite linking, protected-file behavior, main-app/share-
      extension concurrency and migration on macOS/Xcode and devices. Portable
      SQLite tests and Swift parsing do not constitute that native validation.
- [ ] Run real Android Room/index migration and device history flows, including
      restart, low storage, cancellation and account changes.

History databases and retained legacy sources can contain filenames, paths and
legacy link fragments. Include their protection and recovery in the client-data
handling documented by the [security plan](../plans/security-abuse-prevention-and-link-limits.md).

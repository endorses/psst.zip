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
source data, not truncation. Unsupported oversized legacy records require recovery;
this limit is not a claim that arbitrarily large historical records can be imported.

## Mobile inbox checkpoints

Both platforms keep inbox save state in account/origin/slot-scoped child and file
rows. Normal history metadata does not load growing path maps or completion arrays.
Only visible children or an exact active child are queried; each child is limited
to the protocol's 100 files. Scalar totals update transactionally without summing
every historical child. Refreshing metadata cannot replace already committed saves.

Android archives the original JSON columns in a separate scoped table before
clearing the active parent fields, in the same transaction. Import checkpoints
UTF-8 offsets and processes at most 64 entries/256 KiB per parent call using 16 KiB
slices. It preserves newer state and retries after interruption without counting a
file twice. Exceptional sources above 64 MiB or entries above 64 KiB remain archived
with visible recovery and saving paused. SQLite may materialize a whole source
internally while archiving, measuring or slicing it: the small returned slices do
not prove a bound on that one-time engine allocation.

iOS retains an immutable source row before stripping maps from the active parent.
Discovery seeks metadata by primary key and examines at most one parent body per
step. Import decodes that capped 16 MiB source and commits at most 32 entries per
batch. The original JSON and source row remain retained; account history stays
gated until normalization finishes. Existing/newer rows and deletion tombstones
take precedence. Normal writes reject reintroduced maps and preserve local totals.
New per-file checkpoints include expected length, so truncated output is missing;
legacy checkpoints without lengths retain an explicit existence-only check.

The iOS receive view loads checkpoints before committing a page change and publishes
them with that page only after metadata persistence succeeds. Each save reloads
its exact child. Receipt persistence precedes the child completion marker, so a
process exit between them cannot silently lose delivery confirmation.

## iOS guest history and retry queues

Guest downloads use individual SQLite records and independent 50-row local pages.
The streamed migration accepts both the earlier root array and the later
`records`/`receipts` snapshot. Rows and source progress commit together, including
derived receipt/reconciliation jobs. Original files and Keychain identities stay
intact; the main app waits for import before modifying guest history.

Publication recovery checks exact transfers or a batch of four indexed jobs.
File hashing runs off the main actor with cancellation, then re-reads current
metadata before committing. A delayed check cannot restore a deleted history item
or overwrite a newer publication. Missing output remains eligible for explicit
resume; transient read errors preserve and rotate recovery work. Receipts survive
removing a history item, retry at most four jobs per pass, and rotate failed or
malformed entries so other hosts can progress.

Owner download receipts and unfinished guest-upload cleanup also have indexed
queues. Retry selection includes only anonymous receipts and the current
account/origin, and skips the active guest upload. Cleanup capabilities live in
per-job Keychain entries; SQLite stores only references. Keychain reads distinguish
missing data from inaccessible data before migration can be marked complete.
Capability persistence precedes job indexing, while retirement precedes secret
removal. Old arrays remain as recovery sources; imported retirement markers
prevent resurrection. Corrupt jobs retain their original bodies and show recovery
feedback while healthy jobs continue.

The old Keychain/UserDefaults APIs expose one complete `Data` allocation during
migration. Its parser commits at most 25 bounded entries and a fingerprint/offset
per transaction; no capability staging file is written to disk. Normal operations
check the completed marker without re-reading legacy blobs.

The offline guest-store harness runs exact production storage code with real
SQLite, while substituting Apple protection, Keychain, network and cryptographic
boundaries. It proves storage/recovery behavior, not those native integrations or
the cryptographic implementation. Separate portable foundation and retry-queue
tests cover their actual database and parser implementations.

## Remaining work and validation

The native Android storage regression requires a newly created disposable
ranchu/goldfish emulator. It writes migration fixtures and must not use a real
device or a user's existing emulator data. Build from `android/`:

```sh
JAVA_HOME=/opt/android-studio/jbr ./gradlew :app:testDebugUnitTest :app:assembleDebug :app:assembleDebugAndroidTest --offline --no-daemon
```

Install both APKs from the repository root, replacing `emulator-5582` with the
disposable emulator's explicit serial:

```sh
adb -s emulator-5582 install -r android/app/build/outputs/apk/debug/app-debug.apk
adb -s emulator-5582 install -r android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
```

Run each method separately in this order: `prepareRoomMigration`,
`resumeRoomMigration`, `prepareGuestMigration`, `resumeGuestMigration`. Before
each invocation, force-stop the app; replace `METHOD` below with that method:

```sh
adb -s emulator-5582 shell am force-stop zip.psst.android
adb -s emulator-5582 shell am instrument -w -r -e disposableStorage true -e class zip.psst.android.data.StorageMigrationDeviceTest#METHOD zip.psst.android.test/androidx.test.runner.AndroidJUnitRunner
```

Each invocation must report `OK (1 test)` and status code zero. The resume phases
assert a different process ID from preparation. The checked run used API 36.1
with KVM and airplane mode; all four phases passed. Stop the emulator afterward
and remove only its task-owned AVD/data directory.

- [ ] Complete recovery handling for exceptional oversized legacy sources. Retained
      originals and visible rejection do not establish successful migration of
      arbitrary historical blobs; Android's one-time SQLite materialization and
      iOS's capped source decoding remain explicit migration limitations.
- [ ] Verify native iOS SQLite linking, protected-file behavior, main-app/share-
      extension concurrency and migration on macOS/Xcode and devices. Portable
      SQLite tests and Swift parsing do not constitute that native validation.
- [x] Run Android Room 7→8→9 and guest-index migration on a disposable API 36.1
      emulator with separate prepare/resume processes. Verify persisted migration
      offsets, scoped Keystore access, counter/path deduplication, origin/account
      isolation, archive-clear rollback, AtomicFile backup recovery, bounded guest
      pages, deletion tombstones, receipts and encrypted cleanup capabilities.
- [ ] Complete physical-device, locked/protected-storage, low-storage and full UI
      migration/cancellation checks. The emulator storage tests do not prove those
      remaining device behaviors or older Android versions.

History databases and retained legacy sources can contain filenames, paths and
legacy link fragments. Include their protection and recovery in the client-data
handling documented by the [security plan](../plans/security-abuse-prevention-and-link-limits.md).

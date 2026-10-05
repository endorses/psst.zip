# iOS history synchronization verification

The main app and share extension share SQLite schema 4. Server summaries, removal
revisions, page coverage and sync cursors are disposable tables. Private records,
Keychain secrets, receipt queues and saved files remain in their existing stores.
Server-only rows are projected directly from the bounded facts cache. Indexed
private enrichment reads only the visible identities, and server updates modify
only existing retained device records. Paging/syncing never copies a growing
server history into the permanent device store. Metadata-only detail refreshes
return current display state without creating a retained mirror or tombstone.
SQLite WAL, full synchronous commits and writer transactions coordinate both
processes; the extension writes private history but never starts a feed poller.

## Executed locally

- [x] `python3 ios/scripts/test_history_pages.py`: 30 tests passed; strict snapshot/feed decoding,
      cursor validation, empty/removal replies, capability and byte bounds,
      existing pagination/filter/presentation tests.
- [x] `python3 ios/scripts/test_history_database.py`: 27 tests passed; actual SQLite migration,
      atomic batch/cursor rollback and replay, old revisions, resets, scoped
      reads, eviction, retained device records, concurrent app/extension writers,
      and schema-3 upgrade preserving metadata while correcting descending ties.
- [x] `python3 ios/scripts/test_history_sync.py`: 25 tests passed; production metadata cache
      reconciliation and `HistoryPageViewModel` compiled in Swift with offline
      network/Keychain/private-record boundaries. Cached rows precede a suspended
      HTTP reply; cancellation rejects it; changed and removed rows reconcile
      without deleting retained device history. Seventeen model/cache tests cover actual
      cancellation, coalescing, quiet feeds, legacy snapshots, reset with 100 new
      arrivals, exact server-issued pagination anchors, account isolation,
      corruption recovery, offline expiry and later snapshots preserving the
      earlier feed cursor, 2,100 server summaries without permanent mirrors,
      metadata-only details, fact/body revision consistency and a server-imposed
      account cooldown that manual/mutation refresh or a reconstructed view
      cannot bypass, more than 50 equal-timestamp mixed-kind arrivals followed by
      successive pagination, scheduling from the latest manual/mutation/filter
      completion, a failed mutation spanning a routine deadline without stopping
      polling, and finite cooldown overflow/reconstruction. Eight parser tests include the frozen backend fixture,
      required nullable summary round trips, deadlines and `Retry-After`.
- [x] `python3 ios/scripts/check_sources.py`: source/project gates passed only,
      including routine refresh bypassing the view's mutation busy state while
      preserving authentication/device guards and the manual disabled state.
- [x] `python3 ios/scripts/check_localization.py`: 398 keys and 19 plural messages
      passed in English/German, including both permission bundles; no new
      presentation strings.

These results were recorded on 2026-10-05. The database suite also covers the
2,000-row account budget, 10,000-row global budget and 500-scope global state
budget without deleting private history. All temporary harnesses used
`PYTHONDONTWRITEBYTECODE=1` for the final runs.
Fact/tombstone eviction invalidates affected sync checkpoints as well as page
coverage, so losing removal evidence requires a bounded authoritative bootstrap.
Server facts use the backend's descending creation time, ID and kind order;
schema 4 upgrades the cache index without discarding private or cached records.
The visible History scheduler follows one deadline after each completed request,
so a manual refresh cancels the old routine wait and starts a new ten-second wait.
Shared server cooldowns retain at most 500 active account deadlines plus one
conservative overflow deadline. When those slots are full, untracked scopes may
wait until the overflow deadline rather than losing an active restriction.

The portable runners use the existing `swift:6.0-noble` Docker image with
`--network=none`. Temporary harnesses are removed automatically. They do not
prove UIKit/SwiftUI integration, actual URLSession behavior, Keychain/App Group
entitlements, extension signing or an iOS app build.

## Pending on macOS with Xcode

From the repository root, generate the Xcode project and select an installed
simulator from `xcrun simctl list devices available`:

```sh
cd ios
xcodegen generate
xcodebuild -project Psst.xcodeproj -scheme Psst \
  -destination 'platform=iOS Simulator,name=iPhone 16' \
  -derivedDataPath /tmp/psst-history-ios-build \
  CODE_SIGNING_ALLOWED=NO build test
xcodebuild -project Psst.xcodeproj -target PsstShareExtension \
  -sdk iphonesimulator -configuration Debug \
  SYMROOT=/tmp/psst-history-ios-extension/products \
  OBJROOT=/tmp/psst-history-ios-extension/objects \
  CODE_SIGNING_ALLOWED=NO build
```

Use a simulator name installed on that Mac. The project build script produces the
shared Kotlin framework; the Mac needs the project's Java/Gradle environment.
Remove the two task-owned derived-data directories after recording the results.

- [ ] Build the main app and embedded share extension and run `PsstTests`.
- [ ] With a disposable backend, observe initial newest-page bootstrap followed
      by a feed request. Quiet History then makes about six small feed requests
      per minute, with no repeated resource-page fetch. Old servers without
      `history_sync_version: 1` retain bounded snapshots at ten-second cadence.
- [ ] Verify no history requests after leaving History, selecting device-only
      downloads, backgrounding, signing out or switching server/account. Returning
      to foreground History must immediately sync with exactly one in-flight job.
- [ ] Verify failures back off at 10/20/40/60 seconds, honor `Retry-After`, preserve
      rows and show existing stale status without a shifting progress bar.
      During an explicit server cooldown, manual/mutation refresh and leaving or
      reconstructing History must not issue a new request before its deadline.
- [ ] During a routine wait, perform manual, mutation and filter refreshes. Verify
      the next routine request waits ten seconds after the latest completion,
      and leaving History cancels the sleeping task. Delay a rename/revoke past
      the routine deadline and fail it; polling must continue afterward.
- [ ] Create, rename, receive, acknowledge, exhaust, revoke and delete from a
      second client. Verify updates to old entries, removal/inactive distinctions,
      preserved local keys/files/checkpoints and unchanged older-page position.
- [ ] Arrivals alter the cached newest window. On Next, verify the server-issued
      last-row anchor is used without skipping shifted rows; snapshots from older
      servers without anchors are revalidated before navigation.
- [ ] Restart the backend or force retention reset; verify bounded newest-page
      rebootstrap, stale older coverage revalidation and preserved local material.
- [ ] Kill/reopen between batches and during a SQLite commit; cursor and metadata
      must commit together or replay together. Exercise simultaneous extension
      uploads and foreground app sync, including protected/low-storage failure.
- [ ] Change English/German and light/dark appearance without a cursor reset or
      history fetch. Verify cached expiry disables expired actions offline.
- [ ] Verify live permission checks reject disabled sessions, administrator roles
      and required password changes despite cached rows.

No CI access or new CI setup is needed. These remaining Apple-platform checks
must stay pending until actually run.

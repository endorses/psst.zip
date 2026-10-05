# History cache and incremental synchronization

## Status and scope

Implemented locally, 2026-10-05. Backend, shared Kotlin, Android, iOS and web
implementation is complete. Available automated checks passed; Apple-platform
builds and native device validation remain explicitly pending below.

Make History open immediately from a local cache and update quietly through a
bounded server change feed. Include backend, shared Kotlin, Android, iOS and web.
Include the iOS share extension where it writes shared history; it must not start
a background history poller.

Preserve psst.zip branding, operator-configured URLs, account isolation, keys,
saved downloads, receipts, link limits, retention, revocation and admin-only
accounts. Cached history is display data, never authorization for a server action.

This builds on [transfer workflows and shared titles](transfer-workflow-ux-and-shared-link-titles.md),
[security and link limits](security-abuse-prevention-and-link-limits.md) and
[English/German localization](english-german-localization.md). It does not reopen
completed reviews or change the encryption protocol. Missing macOS/Xcode limits
validation, not iOS implementation scope.

## Foundation before this change

Android and iOS fetch account history in pages of up to 50 records. Older pages
load on demand. Both persist account metadata and separate saved-download
records, but server History still needs a response to identify its initial page.
They poll the current page approximately five seconds after a successful request
while History is active, with failure backoff.

Web fetches server pages before enriching them with locally saved links/labels.
IndexedDB currently stores that local enrichment, not server resource metadata
or page membership. The workspace poller refreshes visible History approximately
every three seconds, with failure backoff.

The backend returns compact summaries with `Cache-Control: no-store`. Its
created-time `after` cursor means older history, not changes since a prior sync.
Reusing it for synchronization would miss updates to older records. Neither
native client nor the endpoint implements ETags or incremental synchronization.

## Agreed behavior

- [x] Show the appropriate cached page once its server/account scope is
      established. Keep rows visible while checking updates; routine sync must
      not show a progress bar that moves content.
- [x] Use one sync request carrying a server-issued change cursor. Return only
      new/updated summaries and removal markers, or a small empty successful
      response when nothing changed. Do not first request a version or HEAD.
- [x] Sync on entering History, foregrounding it and manual Refresh. While the
      screen/document is active, schedule the next request ten seconds after a
      successful request finishes. Allow only one request in flight per scope.
- [x] Coalesce immediate refreshes after this client's create, rename, revoke,
      upload completion or download receipt. Refreshing metadata must never
      repeat an upload/download or spend another allowance.
- [x] Stop automatic requests when leaving History, selecting purely local
      device/download history, backgrounding, signing out or changing server/account.
- [x] Back off transient failures at 10, 20, 40 and at most 60 seconds, with small
      jitter; honor longer server `Retry-After` instructions. Preserve cached rows
      and existing concise retry/stale states without adding explanatory clutter.
- [x] Keep older-history pagination and All/Sent/Receive filters. Never fetch an
      entire growing history to initialize, filter, merge or refresh a list.
- [x] Exclude ETag-only polling, WebSockets, push infrastructure and continuously
      running background jobs from this release. ETags can be considered later;
      they cannot substitute for the change feed.

## 1. Protocol and consistent bootstrap

Freeze the following versioned contract and shared fixtures before wiring UIs.

- [x] Retain `GET /api/v1/auth/resources` for bounded history snapshots and older
      pages. Add a sync watermark/generation captured in the same database read
      snapshot as the rows, and revisions on individual resource summaries.
      Include server-issued `history_after` and `history_after_kind` anchors on
      each row. Cached overflow can then seek its last displayed row exactly
      after eviction/reset without walking all prior history.
- [x] Add authenticated `GET /api/v1/auth/history/changes?cursor=...&limit=50` for
      regular-user history. Do not grant administrator transfer access or expose
      another account's history/private inbox children.
- [x] Return an envelope with protocol version, `changes`, `next_cursor`,
      `has_more` and sync generation. Each change carries resource kind/ID,
      revision, and an `upsert` compact authoritative summary or `remove` marker.
      Reuse snapshot summary types and their strict validation rules.
- [x] Return HTTP 200 with `changes: []` and `has_more: false` when up to date.
      Keep `Cache-Control: no-store`; clients explicitly persist application data.
      Do not treat 304 as a partial-update response or add a preliminary check.
- [x] Use monotonic database revisions rather than client timestamps, creation
      dates or UUID ordering. Opaque cursors must validate version, account,
      generation and retention floor. Reject malformed/cross-account cursors safely.
- [x] Bootstrap using one newest 50-record snapshot and its watermark, followed
      by changes after that watermark. Fetch older snapshots on demand. Track
      coverage so uncached or unloaded older records are not treated as deleted.
- [x] Prevent the bootstrap race: a concurrent mutation must appear in the
      snapshot or subsequent changes. Read feed identities, authoritative summaries
      and watermark in one consistent database snapshot; returned state cannot
      advance beyond that read snapshot. The examined continuation can trail a
      resource's newest revision; identity/revision merging safely handles replay.
- [x] Advance cursors only through examined/delivered changes. Continue
      `has_more` responses in bounded batches without jumping to the latest
      revision. Empty batches may still have a valid advancing continuation.
- [x] Bound catch-up work per refresh cycle and yield between batches. Persist
      progress and schedule remaining work without blocking the UI; reject a
      continuation that repeats its cursor without making progress.
- [x] Merge by resource identity/revision. Repeated batches are idempotent; late
      snapshots must not overwrite newer deltas or resurrect removed identities.
- [x] Track one account cursor independent of All/Sent/Receive presentation.
      Query the cache using indexed filters. Keep server snapshot filters for
      fetching older matching records not yet cached.
- [x] Preserve chronological ordering, pagination and scroll position. New rows
      update the newest window without sending a user browsing older pages back
      to the top. Do not reset navigation after every background sync.
- [x] Set default 50/max 100 changes per request; enforce byte limits, indexed
      scan bounds and deadlines. Reconcile existing platform response caps with
      worst-case valid summaries before freezing the contract. Invalid/oversized
      responses must not advance a client cursor.

## 2. Durable backend change tracking

- [x] Migrate the database to indexed account-history revisions and a minimal
      change log. Store identities/actions/revisions, not filenames, file contents,
      link fragments, passwords, tokens or encryption keys.
- [x] Commit relevant metadata mutations and change markers atomically. Cover
      creation, shared titles, upload completion, summary totals, received-file
      counts, download attempts/acknowledgments, exhaustion, revocation,
      expiration, removals and administrator/account incident actions.
- [x] Propagate private child changes to their parent receive-link summary.
      Return parent state/counts only; manifests and children still use existing
      owner-authorized inbox APIs.
- [x] Audit mutation paths including cleanup and counter/recovery repair. Avoid
      per-byte/per-chunk events when history-visible fields did not change.
      Coalesce repeated identities within a bounded batch without losing removals
      or advancing beyond unexamined changes.
- [x] Distinguish inactivity from removal. Revoked/exhausted/expired records may
      remain as inactive history upserts under existing retention; emit removals
      when an identity no longer belongs in server history.
- [x] Derive expired availability from cached timestamps, including while offline.
      Feed server expiry/cleanup transitions too: a quiet account must not retain
      expired links as live entries.
- [x] Keep removal markers after physical deletion. Never infer deletion from
      absence in one snapshot page or from a failed resource request.
- [x] Bound log retention by age and count. Initial defaults: seven days,
      10,000 entries per account and 100,000 globally. Persist a retention floor
      whenever trimming invalidates cursors; prune in bounded indexed batches.
- [x] Include log metadata in storage estimates. Catch-up/pruning must respect
      resource/rate limits and not starve transfers or administrative recovery.
      Never accept a metadata mutation while silently dropping its required event.
- [x] Return typed `history_sync_reset_required` for expired/incompatible cursors.
      For this initial release, rotate the sync generation on server process
      start so restarts/database restores trigger safe bounded rebootstrap rather
      than accepting ambiguous old cursors.
- [x] On reset, reconcile the authoritative newest window from a fresh snapshot
      and resume deltas. Mark older cached coverage stale and revalidate on demand.
      Retain local secrets/files; do not require a whole-history download.
- [x] Apply authorization even to empty responses. Disabled accounts, revoked
      sessions, required password changes and role changes must never be bypassed
      because a client holds a cache/cursor.

## 3. Cache and reconciliation rules

- [x] Separate server facts, page coverage/sync state and private local material.
      Scope storage by canonical server identity and account ID, plus resource
      kind/ID where applicable. Username alone is not an account identity.
- [x] Persist each validated batch and its cursor in one local transaction. A
      crash, cancellation, malformed batch or write failure preserves the previous
      cursor; replay is safe. Never acknowledge unapplied changes.
- [x] Remove stale server facts/coverage without deleting encryption keys, saved
      files, receipt checkpoints or retained device-history records. Preserve
      remote revocation versus local-only removal and existing migration tombstones.
- [x] Bound metadata/page queries, cursor trails, rendered windows and cache size.
      Start with 2,000 cached server rows per account and bounded bytes per row.
      Evict old server-only facts independently of keys/files, marking coverage
      incomplete so older pages can be fetched again.
- [x] Set a global device/browser metadata budget as well as per-account bounds.
      Bound cached removal revisions by coverage/generation so tombstones cannot
      grow indefinitely. Evict disposable facts without deleting private material
      or allowing a late response to resurrect an entry.
- [x] Keep request epochs and cancellation guards. Late responses from an old
      account/server, screen, generation or snapshot cannot update another scope.
      Clear active rows/cursors on sign-out/account changes according to existing
      private-history retention rules; do not expose them to the next account.
- [x] Server actions recheck live permissions and limits. Cached expiry may
      disable an action; cached availability cannot authorize it.
- [x] Preserve existing offline authentication boundaries. Native device history
      remains readable locally. Web must not fabricate a login if `/auth/me` is
      unavailable; a confirmed active workspace may retain read-only cached rows
      during transient failure. Authentication rejection clears the session normally.
- [x] Store language-neutral facts/errors and translate new concise states in
      English/German. Changing language/theme must not reset cursors, fetch history
      or restart transfers.

## 4. Android, iOS and web integration

- [x] Shared Kotlin: add strict snapshot/change models and a bounded feed client
      in `shared/.../api/`, including typed reset/auth/rate-limit failures and
      explicit response limits/cancellation semantics.
- [x] Android: migrate Room metadata and add scoped sync/coverage state. Update
      `HistoryViewModel` to query cached windows first and apply deltas atomically,
      replacing its full-page poller. Preserve `AccountHistory`, device/download
      merging and `HistoryScreen` lifecycle cancellation.
- [x] iOS: migrate App Group SQLite and implement equivalent sync/coverage state
      in `HistorySnapshot`, `HistoryRefresh`, `HistoryPageViewModel` and `HistoryView`.
      Coordinate app/share-extension writes; only foreground History owns polling.
- [x] Web: add separate resource/sync/coverage IndexedDB stores through
      `local-history.ts` or a focused cache module. Preserve saved links/labels
      and receive-key storage. Extract cancellable history orchestration from
      `+page.svelte`, building on `resource-history.ts` validation.
- [x] Web: replace full-page History work in the workspace poller with the
      ten-second sync lifecycle. Preserve independent account/security, receive
      inbox and pairing checks. Avoid duplicate timers; coordinate relevant tabs
      with scoped IndexedDB/BroadcastChannel notifications where available.
- [x] Wire local create/rename/revoke/receipt success into immediate scoped cache
      updates and coalesced sync triggers. Prefer authoritative response metadata;
      reconcile optimistic state safely.
- [x] Preserve current public-link/scanner permissions, administrator dashboards,
      native guest download stores and payload-loading APIs. History synchronization
      must not change transfer protocol behavior or existing pagination UX.

## 5. Compatibility and delivery

- [x] Deliver backend migration/API and contract fixtures first, retaining the
      snapshot API for existing clients.
- [x] Advertise versioned sync capability in server configuration. New clients
      use deltas when supported; older self-hosted servers get bounded snapshot
      refresh at the new active-view cadence. Do not classify arbitrary failures
      or authentication rejections as unsupported-server responses.
- [x] Complete cache/reconciliation tests before wiring polling. Implement
      Android, iOS and web together; do not exclude iOS due to missing Xcode.
- [x] Migrate existing stores without destructive clearing. Use disposable data
      and backups for migration/restore tests, not real operator data.
- [x] Format files, record executed versus pending verification, check off only
      completed tasks and commit implementation with the updated plan.

## 6. Verification and acceptance

- [x] Backend tests: empty responses; every mutation path; older-record changes;
      parent totals; removals; ownership; cursor validation; bounded reads;
      pruning/reset; deadlines and authorization after session/account changes.
- [x] Concurrency tests: bootstrap handoff, mutation during feed reads, multi-batch
      catch-up, duplicate delivery, deletion during pagination, restart/restore
      generations and repair/cleanup races.
- [x] Cache tests: atomic batch/cursor commits, crash/replay, stale snapshots,
      eviction/coverage and preservation of keys, migration tombstones, saved
      files and pending receipts through revocation/removal/reset.
- [x] UI tests: cached rows appear before a sync response; unchanged results do
      not rewrite/reorder rows unnecessarily; older changed rows update; new
      arrivals preserve pagination/scroll position; filters remain bounded.
- [x] Lifecycle tests: ten-second active-view cadence, one request in flight,
      coalesced mutation refreshes, retry/backoff and cancellation after navigation,
      backgrounding, account/server switching and device-only history selection.
      Include web tab visibility and language/theme changes.
- [x] Failure tests: denied/corrupt browser storage, full local storage, malformed
      or oversized responses, unsupported capability, stale cursors, empty
      continuation pages, large histories and offline/reconnect. Retain keys/rows;
      never retry file payloads as sync recovery.
- [x] Run affected Go database/API tests with race checks, shared Kotlin and
      Android unit tests/build, web unit/browser tests, localization/type checks
      and production build. Use disposable backends; clean task-owned caches.
- [x] Run portable iOS store/protocol checks available locally. Keep app/share
      extension builds, XCTest and simulator/device checks pending until available.
      Document exact commands and scenarios in `docs/testing/` during implementation.
- [x] Measure quiet History: initial bounded cache query/bootstrap when needed,
      then roughly six small sync responses per minute and no repeated 50-row
      downloads. Hidden/background screens issue no history polls. Mutation
      batches, pruning and recovery remain bounded under load.
- [x] End-to-end two-client check: create, rename, receive, acknowledge downloads,
      exhaust, revoke and clean up on one client; verify the other updates without
      losing keys or saved files. Repeat offline/reconnect and server restart.

Completion requires backend tracking/protocol, migrations/cache behavior and all
three client integrations. Report unavailable native validation separately from
implementation completion. No new CI infrastructure or CI access is required.

## Implementation evidence and remaining platform validation

The bounded closure pass finished with permitted validation deferrals. It fixed
iOS timestamp-tie ordering, refresh completion scheduling, bounded retry cooldowns,
and the failed-mutation scheduling edge case. No material implementation finding
remains open. Cache migrations preserve private records, keys, saved files and
receipt checkpoints; server-only facts remain disposable.

Available checks completed:

| Area             | Executed evidence                                                                                                                                                                                                               |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backend          | All package race checks across two serialized runs, focused database/API/cleanup tests, and `go vet` passed.                                                                                                                    |
| Shared / Android | 171 shared tests, 153 app unit tests, both debug APK builds, eleven emulator scenarios and the final replacement-ViewModel regression passed.                                                                                   |
| iOS portable     | 30 page/parser tests, 27 actual SQLite tests, 25 production sync/model tests, source gates and bilingual localization checks passed.                                                                                            |
| Web              | 110 unit tests, localization checks, 17 cache/controller browser cases, two final retry cases, real two-client flow including server restart, existing transfer regression, zero-error type checks and production build passed. |

Exact commands, environment limits and further manual scenarios are recorded in
[backend verification](../testing/history-sync-backend.md),
[Android verification](../testing/history-sync-android.md),
[iOS verification](../testing/history-sync-ios.md),
[web verification](../testing/history-sync-web.md) and
[web cache verification](../testing/history-sync-web-cache.md).
Task-owned build caches, disposable backend state, emulator data and temporary
harnesses were cleaned up. Operator data was not used for migration tests.

- [ ] On macOS/Xcode, build the iOS app and share extension and run XCTest.
- [ ] Run the native Apple lifecycle, Keychain/App Group, extension concurrency
      and simulator/device scenarios documented in the iOS verification notes.
- [ ] Complete remaining Android Compose/physical-device and real-backend manual
      scenarios documented separately. The actual Room/ViewModel emulator tests
      and real two-client web flow passed; these additional native flows are not
      claimed as tested.

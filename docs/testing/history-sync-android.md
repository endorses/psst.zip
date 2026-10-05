# Android and shared history synchronization verification

Room schema 12 adds disposable server facts, removal revisions, page coverage and
one sync cursor per normalized server/account scope. Migration 11→12 preserves
private history, encryption keys, owner capabilities, save checkpoints and receipt
records. Server-only summaries are displayed without copying the whole account
history into the private-record table.

The cache retains at most 2,000 facts per account and 10,000 globally, including
removal revisions, with an 8 KiB encoded fact cap. Coverage is bounded to 1,000
windows globally and older-server page bodies to 20 windows globally. Eviction
invalidates coverage/cursors; rebootstrap uses the newest 50-record snapshot and
server-issued row continuation anchors to revalidate a displayed older page.

## Executed locally

Results recorded on 2026-10-05:

- [x] Shared Kotlin Android/JVM unit suite: 171 tests, zero failures/errors/skips.
      Includes the actual frozen backend fixture, strict snapshot/feed decoding,
      safe revisions, removal/unknown summaries, bounded response handling,
      persisted metadata validation, typed resets and numeric/HTTP-date
      `Retry-After` handling on the feed, bounded snapshot and config endpoints.
- [x] Android JVM unit suite: 153 tests, zero failures/errors/skips. Includes
      ten-second cadence, 10/20/40/60-second failure backoff, longer server delays,
      scope-safe mutation notifications, a hard scoped retry deadline preserved
      across refresh/poller restarts, metadata/private-record separation and
      revocation without adopting another account's colliding key.
- [x] Debug app and Android instrumentation APKs compiled successfully with the
      current implementation and all eleven instrumentation scenarios.
- [x] Final instrumentation suite on a fresh disposable API 36.1 x86_64 emulator:
      `OK (11 tests)` in 43.116 seconds. All seven actual Room and four production
      ViewModel scenarios below passed, including the hard `Retry-After` gate.
- [x] Focused deadline regression rerun against the final rebuilt APKs after
      moving deadline ownership to the process scope: `OK (1 test)` in 6.964
      seconds. A replacement `HistoryViewModel` still waits for the completed
      429 response's deadline, including its initial config request. The final
      shared snapshot/config typed-429 seam is verified by the JVM tests above.

Unit/build command, from the repository root:

```sh
JAVA_HOME=/opt/android-studio/jbr ./android/gradlew -p android \
  :shared:testDebugUnitTest :app:testDebugUnitTest \
  :app:assembleDebug :app:assembleDebugAndroidTest \
  --offline --no-daemon --max-workers=1
```

The existing Gradle cache and SDK are reused; this environment requires sandbox
permission for Gradle cache locks and emulator/ADB local sockets. The Java path
is environment-specific. A normal development machine can use its configured
Java installation and omit `--offline` when dependencies are not cached.

## Disposable emulator scenarios

Use a fresh disposable AVD, not an AVD containing real account/device history.
The lifecycle scenarios explicitly require an emulator and the
`disposableStorage=true` instrumentation argument. They exercise the production
Room database and `HistoryViewModel` against a local HTTP fixture server, rather
than the live backend or a physical phone.

```sh
adb -s emulator-5560 install -r android/app/build/outputs/apk/debug/app-debug.apk
adb -s emulator-5560 install -r \
  android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
adb -s emulator-5560 shell am instrument -w \
  -e disposableStorage true \
  -e class zip.psst.android.data.HistorySyncDeviceTest,zip.psst.android.data.HistorySyncLifecycleDeviceTest \
  zip.psst.android.test/androidx.test.runner.AndroidJUnitRunner
```

Seven Room scenarios cover atomic batch/cursor commit and rollback, replay and
late-snapshot removal guards, scope isolation, preservation of private material,
newest-window overflow without skipped rows, stable older boundaries, account
and global budgets, a no-op batch causing zero SQLite writes, and migration of
existing keys/receipt checkpoints from schema 11.

Four production ViewModel scenarios cover cached rows appearing before a
suspended response, one in-flight request, coalesced refresh/mutation wake-ups,
ten seconds between successful routine requests, no polling after stopping or
selecting device history, corrupt metadata/cursor rebootstrap with retained
private keys, and evicted overflow recovery using the original server anchor
without jumping to the newest page. The fourth returns a completed 429 response
and verifies manual refresh, mutation notifications and stop/foreground resume
cannot send another feed or config request before its six-second server deadline.
The final deadline belongs to the process/server-account scope rather than a
ViewModel instance, so reopening History cannot shorten it.

The final suite observed cached rows before a blocked reply, one coalesced manual
refresh followed by a ten-second routine request, a scoped mutation wake-up, and
no further requests for eleven seconds after stopping. The quiet
batch test sampled SQLite's `total_changes()` on the same transaction connection
and observed no persisted writes.

Task-owned AVD data and emulator logs are removed after the final run; the user's
original AVD is only a configuration template. The expanded eleven-test run above
supersedes the earlier four-scenario run.

## Remaining integration checks

- [ ] Against a disposable real backend, exercise Android and another actual client: create,
      rename an older entry, receive an upload, acknowledge a saved download,
      exhaust a limit, revoke/delete and restart. Verify old-record updates,
      inactive/removal distinctions and retained keys/files/checkpoints.
- [ ] Verify the Compose screen lifecycle on a phone or emulator through actual
      navigation, background/foreground, account/server changes and sign-out;
      verify no shifting progress bar or older-page scroll jump.
- [ ] Exercise a server without `history_sync_version: 1`; only bounded snapshots
      should refresh, at ten-second foreground cadence.
- [ ] Exercise offline expiry, denied sessions/roles/password-change restrictions,
      low/full storage and process termination during a commit. Verify failed
      persistence never advances the cursor or removes private material.
- [ ] Change English/German and appearance while History is visible. Metadata and
      cursors must remain language-neutral and cached rows must retain their
      current page.

Apple-platform builds and device validation are tracked separately in
[history-sync-ios.md](history-sync-ios.md). No iOS build or physical-device flow
is implied by these Android/JVM results.

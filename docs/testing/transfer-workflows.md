# Transfer workflow verification

Use disposable accounts and data for tests that create resources or spend download
allowances. Do not point the fixtures at a production instance.

## Available automated checks

From `web/`, run the unit/integration tests, type checks and production build:

```sh
npm test
npm run check
npm run build
```

The integration tests bind a disposable loopback server. Browser checks use a
separate disposable backend; select a free port and set both variables:

```sh
PSST_TEST_BACKEND_PORT=18081 \
PSST_TEST_BACKEND_URL=http://127.0.0.1:18081 \
PSST_TEST_STATE_FILE=/tmp/psst-workflow-browser-state.json \
npx playwright test tests/browser/workflow-ux.spec.ts \
  tests/browser/transfer.spec.ts tests/browser/history-pages.spec.ts \
  tests/browser/inbox-pages.spec.ts tests/browser/local-history.spec.ts \
  tests/browser/ux.spec.ts tests/browser/download-ack.spec.ts \
  tests/browser/slot-membership.spec.ts tests/browser/administration.spec.ts \
  tests/browser/scanner.spec.ts
```

`workflow-ux.spec.ts` includes phone/desktop light/dark screenshots, enlarged text,
keyboard operation of limit controls, shared titles, inbox save context and
all-files exhaustion. The existing inbox suite seeds 101 submissions and opens a
later page. Authentication, manifests, membership and receipts use the real
backend in those integration cases; isolated layout/error fixtures do not prove
cryptographic or native camera behavior.

The scanner suite checks immediate same-server receive-link file selection,
secondary Scan again, discarded-draft confirmation, session/role gating and
foreign-server confirmation without contacting that server first. Both scanned
and direct invitations use the same public upload task and key/capacity checks.

From `backend/`, run `go test -race ./...` and `golangci-lint run ./...` with a Go
version supported by the installed linter. Do not downgrade the project or skip
lint to work around a linter binary built against an older compiler.

From the repository root, with the README's JDK/Android SDK setup:

```sh
./android/gradlew -p android :app:testDebugUnitTest :shared:testDebugUnitTest \
  :app:assembleDebug :app:assembleDebugAndroidTest --no-daemon
python3 ios/scripts/check_sources.py
python3 ios/scripts/test_history_pages.py
python3 ios/scripts/test_guest_store.py
python3 ios/scripts/test_receive_checkpoints.py
```

Android's guarded storage instrumentation requires a newly created disposable
emulator; never seed or wipe a user's phone to run it. Follow
[local storage verification](../security/local-history-storage.md) for its
prepare/restart/resume checks. Include the Room 10→11 migration to verify that
adding shared titles preserves original local labels, keys, capabilities and
saved-file references.

## Native and manual checks

- [ ] Check History with interleaved saved downloads and account records, records
      beyond the first page, equal timestamps, offline reopening and account
      switching. All has one Load more control; type filters select their source.
- [ ] Check empty and populated inboxes at ordinary/enlarged text sizes. Naming
      is visible before creation; arrivals put saving first; QR sharing remains
      available without the creation form occupying the active inbox.
- [ ] Scan and paste a receive invitation, select files and send. Add files and
      Send remain within reach; Back/Scan again release the camera and preserve
      the expected draft/transfer state. Verify bytes with the real native
      [interoperability fixtures](scan-to-receive.md).
- [ ] Inspect Settings and its separate Usage destination in both appearances.
      Actual blocked/missing-key/HTTP conditions remain actionable.
- [ ] Rename and clear a title from another authorized device. Confirm public
      link pages update without changing the URL or exposing private filenames.
      Verify downloaded snapshots remain useful offline.
- [ ] Exhaust each file in a multi-file limited send. Earlier files closing does
      not block the others; final admitted readers can finish. An interrupted
      last attempt closes the link without claiming successful delivery.
- [ ] Build/test the iOS app and share extension on macOS/Xcode, then check camera,
      save, accessibility and navigation on an iOS simulator/device. Portable
      Swift harnesses and source parsing are not native builds or device checks.

The [implementation plan](../plans/transfer-workflow-ux-and-shared-link-titles.md)
records executed checks and any unavailable validation separately. Delete
fixture state, task-owned caches and temporary servers after testing.

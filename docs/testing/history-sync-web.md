# Web history synchronization verification

The web workspace uses a separate disposable IndexedDB cache for authenticated
server history. Private links, receive keys and saved files keep their existing
stores. A confirmed account opens cached pages before capability discovery or a
history HTTP response. Empty feed responses do not rewrite cache rows or re-render
private enrichment. History owns one cancellable foreground timer; account and
security checks keep their independent lifecycle.

## Bounds and recovery

Snapshots contain at most 50 summaries; a refresh examines at most four feed
batches. Responses are bounded to 1 MiB and each resource to 16 KiB of UTF-8 JSON.
The cache holds at most 2,000 facts per account and 10,000 globally, including
removal revisions. Coverage is limited to 100 windows per account, 500 globally,
200 descriptors per window, and 500 sync scopes. UUIDs, timestamps and bounded
server anchors bound descriptor size, so the count limits also bound metadata
bytes. Extra nested summary fields are discarded rather than persisted.

New arrivals preserve the original page tail using a local continuation. Each
row carries server-issued All/kind anchors so a continuation remains seekable
when its source coverage is evicted or reset. Eviction invalidates checkpoints
and stale coverage; subsequent bootstrap is one bounded page. Corrupt metadata
is removed only from this disposable database and account scope. Denied storage
or quota failures display an online bounded snapshot without claiming to have
committed a cursor. Neither recovery path requests file payloads.

Same-origin BroadcastChannel messages contain a scope identifier for changed
cache rows or a content-free mutation hint. Active History coalesces hints into
an authorized metadata request; hidden/inactive History starts no requests.
Successful controls and receipts send hints, while tus payload chunks do not.
Long Retry-After delays use cancellable timer intervals within browser limits.
A hard retry deadline also prevents manual refreshes, mutation notifications and
foreground transitions from bypassing a server cooldown.

## Executed checks

- [x] `npm run test` outside the sandbox: 110 tests passed. The transfer suite
      requires its own loopback HTTP server. The sandbox-only run could not bind
      that server; the authorized run passed without product changes for that.
- [x] `npm run check:localization`: 1,094 bilingual keys and 51 documented source
      exceptions passed.
- [x] `npm run check`: zero Svelte/TypeScript errors and warnings at the latest
      executed check, repeated after the final cache and retry tests.
- [x] Initial focused browser run: eight History pagination/cache/controller tests
      passed with a disposable backend.
- [x] Recovery and real two-client run: nine tests passed. The observer saw
      create, rename, download exhaustion, received files and revocation with one
      bootstrap snapshot followed by deltas, and zero payload requests from
      History. Offline/reconnect retained rows and resumed the feed.
- [x] Final corrupt-envelope/coverage, timer-boundary/coalescing and cache-bound
      browser run: 17 tests passed. An earlier combined run passed the other 26
      History/real two-client/transfer cases; its four invalid cursor fixtures
      were corrected before the final cache run. The LAN-deployment-only case is
      intentionally skipped on a loopback secure context.
- [x] Final real two-client run with an opt-in disposable server restart: one
      end-to-end test passed. The observer recovered a changed generation with
      one bounded snapshot, retained current rows, then resumed deltas. It also
      passed create/rename/receive/acknowledgment/exhaustion/revoke/offline cases.
- [x] Corrected the existing transfer regression assertion to the singular
      “1 attempt remaining,” as explicitly authorized. That regression passed.
- [x] Final retry regression run: two tests passed, including mutation, manual
      refresh and foreground triggers during an explicit server cooldown.
- [x] `npm run check` and `npm run build`: final type check returned zero errors
      and warnings, and the static production build completed successfully.

Browser commands use disposable servers and a free backend port:

```sh
PSST_TEST_BACKEND_PORT=18089 \
PSST_TEST_BACKEND_URL=http://127.0.0.1:18089 \
npx playwright test tests/browser/history-sync.spec.ts \
  tests/browser/history-pages.spec.ts tests/browser/transfer.spec.ts

PSST_TEST_STATE_FILE=/tmp/psst-history-sync-restart-state.json \
PSST_TEST_BACKEND_PORT=18089 \
PSST_TEST_BACKEND_URL=http://127.0.0.1:18089 \
npx playwright test tests/browser/history-sync-live.spec.ts
```

Run these from `web/`. Do not run the production build while a Playwright dev
server is using `.svelte-kit`; finish browser tests first. The fixtures remove
owned backend databases, binary and files when the server shuts down.

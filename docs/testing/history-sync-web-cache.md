# Web history cache verification

The isolated `web/tests/browser/history-cache-bounds.spec.ts` suite runs the
production cache and controller in Chromium using real IndexedDB. Its fixtures
reuse the same compact resource helpers as the existing history sync suite.

- [x] Verify the 2,000-fact account budget includes removal revisions, invalidates
      covered windows/checkpoints when evicting facts and retains private links.
- [x] Verify the 10,000-fact global budget across six account scopes, account
      isolation and independent preservation of private links.
- [x] Instrument actual IndexedDB writes and verify an unchanged empty batch
      performs no puts or deletes and preserves the sync cursor.
- [x] Verify virtual-page navigation retains the server-issued row anchor after
      coverage eviction, fetching the exact older page without
      walking intervening snapshots.
- [x] Verify the same virtual-page recovery after a complete metadata reset.

On 2026-10-05 the isolated suite passed four cases and failed reset recovery:
after bootstrap replaces the base window, the retained virtual cursor produces
an empty non-stale page instead of fetching its server-issued anchor. This
implementation issue remains pending; the test keeps its expected 50 older rows.

Run from `web/`, after other tests using ports 4173 and 8080 finish:

```sh
PSST_TEST_BACKEND_PORT=18089 PSST_TEST_BACKEND_URL=http://127.0.0.1:18089 \
  npx playwright test tests/browser/history-cache-bounds.spec.ts --workers=1
```

This suite checks browser persistence and request behavior; it does not claim
native app or backend authorization validation. The existing history sync suite
covers storage rollback, corruption recovery, hidden-screen lifecycle, quiet
polling, generation resets and private-link preservation.

Executed on 2026-10-05: all five cache cases passed with the corrected canonical
base64url fixtures and the virtual-page reset fix. The combined cache/controller
run passed 17 tests against disposable servers.

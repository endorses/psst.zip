import { test } from "node:test";
import assert from "node:assert/strict";
import {
  validateHistoryChanges,
  loadHistoryChanges,
  historyRetryDelay,
  historySyncSupported,
} from "../src/lib/history-sync.ts";
import { historyCursor, historyID, historyTransfer } from "./history-page-fixture.ts";
import { validateResourcePage } from "../src/lib/resource-history.ts";
import { AccountError } from "../src/lib/account.ts";
const generation = historyID(999);
const cursor = historyCursor(1),
  next = historyCursor(2);
const envelope = (patch = {}) => ({
  version: 1,
  generation,
  changes: [],
  next_cursor: cursor,
  has_more: false,
  ...patch,
});
const upsert = () => ({
  kind: "transfer",
  id: historyID(1),
  revision: 2,
  action: "upsert",
  resource: historyTransfer({ revision: 2 }),
});
test("feed validates version, generation, identities, revisions and progress before advancing", () => {
  assert.equal(validateHistoryChanges(envelope(), cursor, generation).changes.length, 0);
  assert.equal(
    validateHistoryChanges(envelope({ changes: [upsert()], next_cursor: next }), cursor, generation)
      .changes.length,
    1,
  );
  for (const patch of [
    { version: 2 },
    { generation: historyID(998) },
    { has_more: 1 },
    { has_more: true },
    { next_cursor: "bad+cursor" },
    { changes: [upsert()] },
    { changes: [{ ...upsert(), revision: 0 }], next_cursor: next },
    { changes: [{ ...upsert(), id: historyID(2) }], next_cursor: next },
    { changes: [{ ...upsert(), resource: historyTransfer({ revision: 3 }) }], next_cursor: next },
    { changes: [upsert(), upsert()], next_cursor: next },
    {
      changes: [
        {
          kind: "slot",
          id: historyID(3),
          revision: 3,
          action: "remove",
          resource: historyTransfer(),
        },
      ],
      next_cursor: next,
    },
  ])
    assert.throws(() => validateHistoryChanges(envelope(patch), cursor, generation));
  assert.equal(
    validateHistoryChanges(
      envelope({
        changes: [{ kind: "slot", id: historyID(3), revision: 3, action: "remove" }],
        next_cursor: next,
      }),
      cursor,
      generation,
    ).changes[0].action,
    "remove",
  );
});
test("snapshot sync metadata is complete and revision-bearing, legacy snapshots remain valid", () => {
  const page = {
    paginated: true,
    transfers: [historyTransfer({ revision: 0 })],
    slots: [],
    next_cursor: null,
    sync_cursor: cursor,
    generation,
  };
  assert.equal(validateResourcePage(page), page);
  for (const patch of [
    { generation: undefined },
    { sync_cursor: undefined },
    { transfers: [historyTransfer()] },
    { transfers: [historyTransfer({ revision: -1 })] },
  ])
    assert.throws(() => validateResourcePage({ ...page, ...patch }));
});
test("capability is explicit; reset/auth failures stay typed and do not silently downgrade", async (t) => {
  let response = Response.json({ history_sync_version: 1 });
  t.mock.method(globalThis, "fetch", async () => response);
  assert.equal(await historySyncSupported(new AbortController().signal), true);
  response = Response.json({ max_file_size: 25000000 });
  assert.equal(await historySyncSupported(new AbortController().signal), false);
  response = Response.json({ history_sync_version: 2 });
  await assert.rejects(historySyncSupported(new AbortController().signal));
  response = Response.json(
    { code: "history_sync_reset_required" },
    { status: 409, headers: { "Retry-After": "120" } },
  );
  await assert.rejects(
    loadHistoryChanges(cursor, generation, new AbortController().signal),
    (error) =>
      error instanceof AccountError &&
      error.code === "history_sync_reset_required" &&
      error.retryAfterMs === 120000,
  );
  response = Response.json({ code: "invalid_session" }, { status: 401 });
  await assert.rejects(
    historySyncSupported(new AbortController().signal),
    (error) => error instanceof AccountError && error.status === 401,
  );
});
test("retry backoff is bounded and respects longer server instructions", () => {
  assert.deepEqual(
    [1, 2, 3, 4, 5, 20].map((n) => historyRetryDelay(n, 0, () => 0)),
    [10000, 20000, 40000, 60000, 60000, 60000],
  );
  assert.equal(
    historyRetryDelay(1, 120000, () => 0),
    120000,
  );
});

test("frozen cross-platform fixture matches the strict web snapshot and delta decoders", async () => {
  const { readFile } = await import("node:fs/promises");
  const fixture = JSON.parse(
    await readFile(
      new URL("../../docs/testing/fixtures/history-sync-v1.json", import.meta.url),
      "utf8",
    ),
  );
  const page = validateResourcePage(fixture.snapshot);
  for (const name of ["empty", "upsert", "remove", "unknown_summary"])
    validateHistoryChanges(fixture[name], page.sync_cursor!, page.generation!);
});

test("oversized UTF-8 summaries and nested extras are rejected before persistence", () => {
  const resource = historyTransfer({ revision: 2, extra: "ä".repeat(9000) });
  assert.throws(() =>
    validateHistoryChanges(
      envelope({ changes: [{ ...upsert(), resource }], next_cursor: next }),
      cursor,
      generation,
    ),
  );
});

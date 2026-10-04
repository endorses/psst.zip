import { test } from "node:test";
import assert from "node:assert/strict";
import { validateInboxPage, validInboxCursor } from "../src/lib/inbox-page.ts";
import { getSlotInbox } from "../src/lib/api.ts";
import { inboxPage, inboxID, inboxChild, inboxCursor } from "./inbox-page-fixture.ts";
test("strict inbox pagination rejects unpaged, oversized or contradictory pages", () => {
  const p = inboxPage();
  assert.equal(validateInboxPage(p, inboxID), p);
  for (const change of [
    { paginated: undefined },
    { next_cursor: undefined },
    { next_cursor: "a".repeat(513) },
    { next_cursor: "private/path" },
    { id: inboxChild(9) },
    { receive_protocol: 3 },
    { recipient_public_key: "bad" },
    { reserved_files: -1 },
    { remaining_files: 1 },
    { created_at: "bad" },
    { summary: undefined },
    { summary: { state: "ready", completed_files: 2, file_count: 1, total_size: 1 } },
    { summary: { state: "updating", completed_files: 0, file_count: null, total_size: null } },
    {
      transfers: Array.from({ length: 51 }, (_, i) => ({
        transfer_id: inboxChild(i),
        status: "complete",
        file_count: 1,
      })),
    },
    { transfers: [p.transfers[0], p.transfers[0]] },
  ])
    assert.throws(() => validateInboxPage({ ...p, ...change }, inboxID));
  assert.throws(() =>
    validateInboxPage({ ...p, next_cursor: inboxCursor(1) }, inboxID, inboxCursor(1)),
  );
  assert.equal(validInboxCursor("YWJj="), false);
});
test("empty filtered pages retain next cursor and unknown summary stays null", () => {
  const p = inboxPage({
    transfers: [],
    next_cursor: inboxCursor(1),
    summary: { state: "updating", completed_files: null, file_count: null, total_size: null },
  });
  assert.deepEqual(validateInboxPage(p, inboxID), p);
});
test("historical active inbox status is accepted but terminal statuses and undersized ready totals are rejected", () => {
  const p = inboxPage({
    status: "has_uploads",
    transfers: [
      { transfer_id: inboxChild(1), status: "complete", file_count: 3 },
      { transfer_id: inboxChild(2), status: "pending", file_count: 2 },
    ],
    summary: { state: "ready", completed_files: 3, file_count: 5, total_size: 300 },
  });
  assert.equal(validateInboxPage(p, inboxID), p);
  for (const status of ["revoked", "expired", "unknown"])
    assert.throws(() => validateInboxPage({ ...p, status }, inboxID));
  assert.throws(() =>
    validateInboxPage({ ...p, summary: { ...p.summary, completed_files: 2 } }, inboxID),
  );
  assert.throws(() =>
    validateInboxPage({ ...p, summary: { ...p.summary, file_count: 4 } }, inboxID),
  );
  assert.doesNotThrow(() =>
    validateInboxPage(
      {
        ...p,
        summary: { state: "updating", completed_files: null, file_count: null, total_size: null },
      },
      inboxID,
    ),
  );
});
test("inbox API requests exactly one bounded authenticated page and refuses legacy fallback", async (t) => {
  let calls = 0,
    response = Response.json(inboxPage());
  t.mock.method(globalThis, "fetch", async (url: string, init: RequestInit) => {
    calls++;
    assert.equal(url, `/api/v1/slots/${inboxID}/inbox?limit=50&after=${inboxCursor(1)}`);
    assert.equal(init.credentials, "same-origin");
    assert.equal(init.cache, "no-store");
    assert.ok(init.signal);
    return response;
  });
  await getSlotInbox(inboxID, inboxCursor(1));
  response = new Response(" ".repeat(32769));
  await assert.rejects(getSlotInbox(inboxID, inboxCursor(1)));
  response = new Response("sensitive error", { status: 404 });
  await assert.rejects(getSlotInbox(inboxID, inboxCursor(1)), /^Error: API 404$/);
  assert.equal(calls, 3);
  await assert.rejects(getSlotInbox(inboxID, "x".repeat(513)));
  assert.equal(calls, 3);
});

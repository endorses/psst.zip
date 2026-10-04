import { test } from "node:test";
import assert from "node:assert/strict";
import { getSlotTransferMembership } from "../src/lib/api.ts";
const slot = "11111111-1111-4111-8111-111111111111";
const transfer = "22222222-2222-4222-8222-222222222222";
const membership = {
  slot_id: slot,
  transfer_id: transfer,
  receive_protocol: 2,
  recipient_public_key: "A".repeat(43),
};
test("exact membership uses owner credentials, no-store and a bounded cancelable request", async (t) => {
  let captured: AbortSignal | undefined;
  t.mock.method(globalThis, "fetch", async (url: string, init: RequestInit) => {
    assert.equal(url, `/api/v1/slots/${slot}/transfers/${transfer}/membership`);
    assert.equal(init.credentials, "same-origin");
    assert.equal(init.cache, "no-store");
    assert.equal(init.redirect, "error");
    captured = init.signal!;
    return Response.json(membership);
  });
  const caller = new AbortController();
  assert.deepEqual(await getSlotTransferMembership(slot, transfer, caller.signal), membership);
  assert.ok(captured);
  assert.notEqual(captured, caller.signal);
  caller.abort();
  assert.equal(captured.aborted, true);
});
test("membership rejects wrong IDs, unsupported protocol, malformed key and non-ID paths", async (t) => {
  let value: unknown = membership;
  let calls = 0;
  t.mock.method(globalThis, "fetch", async () => {
    calls++;
    return Response.json(value);
  });
  for (value of [
    null,
    [],
    {},
    { ...membership, slot_id: transfer },
    { ...membership, transfer_id: slot },
    { ...membership, receive_protocol: 1 },
    { ...membership, recipient_public_key: 1 },
    { ...membership, recipient_public_key: "A".repeat(42) + "B" },
  ])
    await assert.rejects(getSlotTransferMembership(slot, transfer));
  const before = calls;
  await assert.rejects(getSlotTransferMembership(`${slot}?secret`, transfer));
  await assert.rejects(getSlotTransferMembership(slot, `${transfer}#secret`));
  assert.equal(calls, before);
});
test("membership bounds success bodies and never reads arbitrary server error text", async (t) => {
  let response = new Response(" ".repeat(4097));
  t.mock.method(globalThis, "fetch", async () => response);
  await assert.rejects(getSlotTransferMembership(slot, transfer), /supported file size/);
  for (const status of [401, 403, 404, 410, 503]) {
    response = new Response("private server error", { status });
    await assert.rejects(
      getSlotTransferMembership(slot, transfer),
      new RegExp(`^Error: API ${status}$`),
    );
  }
});

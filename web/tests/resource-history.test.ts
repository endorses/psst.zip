import { test } from "node:test";
import assert from "node:assert/strict";
import { loadResourcePage, validateResourcePage } from "../src/lib/resource-history.ts";
import {
  accountRequest,
  AccountError,
  resourceFileCount,
  receivedFileCount,
} from "../src/lib/account.ts";
import {
  historyPage,
  historyTransfer,
  historySlot,
  historyCursor,
} from "./history-page-fixture.ts";
test("history requires an explicit bounded page and valid identities, policy and global summaries", () => {
  const p = historyPage({ transfers: [historyTransfer()], slots: [historySlot()] });
  assert.equal(validateResourcePage(p), p);
  for (const change of [
    { paginated: undefined },
    { next_cursor: undefined },
    { next_cursor: "abc+secret" },
    { next_cursor: "a".repeat(513) },
    { slots: undefined },
    { transfers: Array.from({ length: 51 }, () => historyTransfer()) },
    { slots: [historySlot({ id: "fake-id" })] },
    { slots: [historySlot({ receive_protocol: 3 })] },
    { slots: [historySlot({ remaining_files: 2 })] },
    { transfers: [historyTransfer({ summary: undefined })] },
    {
      transfers: [
        historyTransfer({
          file_count: 2,
          summary: { state: "ready", file_count: 1, completed_files: 1, total_size: 60 },
        }),
      ],
    },
    {
      slots: [
        historySlot({
          completed_files: 2,
          summary: { state: "ready", file_count: 2, completed_files: 1, total_size: 60 },
        }),
      ],
    },
    {
      transfers: [
        historyTransfer({
          summary: { state: "ready", completed_files: 2, file_count: 1, total_size: 60 },
        }),
      ],
    },
    { transfers: [historyTransfer(), historyTransfer()] },
  ])
    assert.throws(() => validateResourcePage({ ...p, ...change }));
  assert.throws(() =>
    validateResourcePage({ ...p, next_cursor: historyCursor(1) }, historyCursor(1)),
  );
});
test("unknown totals remain explicit and filtered empty pages can advance", () => {
  const slot = historySlot({ file_count: null, completed_files: null, total_size: null });
  const transfer = historyTransfer({ file_count: null, total_size: null });
  const p = validateResourcePage(historyPage({ slots: [slot], transfers: [transfer] }));
  assert.equal(receivedFileCount(p.slots[0]), null);
  assert.equal(resourceFileCount(p.transfers[0]), null);
  for (const field of ["file_count", "total_size", "completed_files"])
    assert.throws(() =>
      validateResourcePage(historyPage({ slots: [{ ...slot, [field]: undefined }] })),
    );
  assert.equal(
    validateResourcePage(historyPage({ next_cursor: historyCursor(1) })).next_cursor,
    historyCursor(1),
  );
});
test("page reader bounds chunked success and error bodies and never follows the cursor", async (t) => {
  let read = 0,
    canceled = false;
  let response = Response.json(historyPage({ next_cursor: historyCursor(1) }));
  const urls: string[] = [];
  t.mock.method(globalThis, "fetch", async (url: string) => {
    urls.push(url);
    return response;
  });
  await loadResourcePage();
  assert.equal(urls.length, 1);
  response = new Response(
    new ReadableStream({
      pull(controller) {
        read++;
        controller.enqueue(new Uint8Array(1024));
      },
      cancel() {
        canceled = true;
      },
    }),
  );
  await assert.rejects(loadResourcePage(), /supported size/);
  assert.ok(read <= 66);
  assert.equal(canceled, true);
  response = new Response("private".repeat(1000), { status: 503 });
  await assert.rejects(
    loadResourcePage(),
    (error) =>
      error instanceof AccountError && error.status === 503 && !error.message.includes("private"),
  );
});
test("ordinary account responses retain a finite larger allowance and preserve safe status handling", async (t) => {
  let response = Response.json({ items: "x".repeat(100000) });
  t.mock.method(globalThis, "fetch", async () => response);
  assert.equal((await accountRequest<{ items: string }>("/admin/resources")).items.length, 100000);
  response = Response.json(
    { code: "recent_authentication_required", error: "Authenticate again" },
    { status: 403 },
  );
  await assert.rejects(
    accountRequest("/admin/settings"),
    (error) => error instanceof AccountError && error.code === "recent_authentication_required",
  );
  response = new Response(null, { status: 204 });
  assert.equal(await accountRequest("/auth/logout", "POST"), undefined);
});

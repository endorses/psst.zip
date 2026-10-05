import { test } from "node:test";
import assert from "node:assert/strict";
import {
  normalizeLinkTitle,
  validSharedTitle,
  downloadLinkExhausted,
} from "../src/lib/link-title.ts";
import { createSlot, createTransfer, renameLinkTitle } from "../src/lib/api.ts";
import { loadResourcePage, validateResourcePage } from "../src/lib/resource-history.ts";
import { historyID, historyPage, historyTransfer, historySlot } from "./history-page-fixture.ts";

test("shared titles normalize Unicode whitespace, bound scalars/bytes and reject controls before trimming", () => {
  assert.equal(normalizeLinkTitle("\u2003 Wedding photos \u00a0"), "Wedding photos");
  assert.equal(normalizeLinkTitle(" \u2003"), null);
  assert.equal(normalizeLinkTitle(null), null);
  assert.equal(normalizeLinkTitle("🤫".repeat(200)), "🤫".repeat(200));
  for (const title of ["🤫".repeat(201), "\nprivate", "name\u0000", "\u0085name", "\ud800"])
    assert.throws(() => normalizeLinkTitle(title));
  assert.equal(validSharedTitle(undefined), true);
  for (const value of [" padded ", "", 1, { title: "secret" }, "x".repeat(201)])
    assert.equal(validSharedTitle(value), false);
});
test("creation and owner rename send only explicitly entered shared metadata", async (t) => {
  const calls: { url: string; method: string; body: Record<string, unknown> }[] = [];
  t.mock.method(globalThis, "fetch", async (url: string, init: RequestInit) => {
    const body = JSON.parse(init.body as string);
    calls.push({ url, method: init.method!, body });
    return Response.json(url.endsWith("/title") ? { title: body.title } : { id: historyID(1) });
  });
  await createTransfer(1, "  Holiday  ");
  await createSlot("A".repeat(43), 2, "Photos");
  await renameLinkTitle("transfers", historyID(1), "  Revised  ");
  await renameLinkTitle("slots", historyID(2), null);
  assert.deepEqual(
    calls.map((c) => c.body.title),
    ["Holiday", "Photos", "Revised", null],
  );
  assert.equal(calls[2].url, `/api/v1/transfers/${historyID(1)}/title`);
  assert.equal(calls[2].method, "PATCH");
  await assert.rejects(renameLinkTitle("slots", "not-an-id", "title"));
  t.mock.method(globalThis, "fetch", async () => Response.json({ title: "different" }));
  await assert.rejects(renameLinkTitle("slots", historyID(2), "intended"), /did not save/);
});
test("History filters query the source and reject a server silently ignoring the filter", async (t) => {
  let response = historyPage({
    transfers: [
      historyTransfer({ title: "Shared", status: "exhausted", inactive_reason: "download_limit" }),
    ],
  });
  const urls: string[] = [];
  t.mock.method(globalThis, "fetch", async (url: string) => {
    urls.push(url);
    return Response.json(response);
  });
  const page = await loadResourcePage("", false, undefined, "transfer");
  assert.equal(page.transfers[0].title, "Shared");
  assert.equal(new URL(urls[0], "http://localhost").searchParams.get("kind"), "transfer");
  assert.equal(downloadLinkExhausted(page.transfers[0]), true);
  response = historyPage({ slots: [historySlot()] });
  await assert.rejects(
    loadResourcePage("", false, undefined, "transfer"),
    /unsupported resource page/,
  );
  assert.throws(() =>
    validateResourcePage(historyPage({ transfers: [historyTransfer({ title: "x".repeat(201) })] })),
  );
  assert.equal(downloadLinkExhausted({ status: "complete" }), false);
});

import { historyPage, historyTransfer, historyCursor } from "./history-page-fixture.ts";
import { test } from "node:test";
import assert from "node:assert/strict";
import { resourcePolicy, resourceUsage } from "./resource-policy-fixture.ts";
import {
  validateResourcePolicy,
  validateResourceSnapshot,
  remainingCapacity,
  ResourceLimitError,
} from "../src/lib/resource-policy.ts";
import { loadResourcePage } from "../src/lib/resource-history.ts";
import { uploadEncryptedFile } from "../src/lib/stream-upload.ts";

test("quota reductions preserve occupied/reserved usage and show zero headroom; corrupt snapshots fail", () => {
  const policy = { ...resourcePolicy, account_storage_bytes: 1024 ** 2, account_files: 1 };
  const snapshot = validateResourceSnapshot({ policy, usage: resourceUsage });
  assert.equal(snapshot.usage.reserved_bytes, 1024 ** 3);
  assert.equal(
    remainingCapacity(snapshot.policy.account_storage_bytes, snapshot.usage.reserved_bytes),
    0,
  );
  assert.equal(remainingCapacity(snapshot.policy.account_files, snapshot.usage.files), 0);
  assert.throws(
    () =>
      validateResourceSnapshot({
        policy,
        usage: { ...resourceUsage, occupied_bytes: 2 * 1024 ** 3 },
      }),
    /reconciliation/,
  );
  assert.throws(() => validateResourcePolicy({ ...policy, account_files: 0 }));
  assert.throws(() =>
    validateResourcePolicy({ ...policy, server_storage_bytes: Number.MAX_SAFE_INTEGER }),
  );
  assert.throws(() =>
    validateResourcePolicy({ ...policy, pending_upload_seconds: policy.max_retention_seconds + 1 }),
  );
});

test("history fetches only the requested bounded page and safely encodes opaque cursors", async () => {
  const original = globalThis.fetch;
  const urls: string[] = [];
  try {
    globalThis.fetch = async (input) => {
      urls.push(String(input));
      return Response.json(
        historyPage({ transfers: [historyTransfer()], next_cursor: historyCursor(1) }),
      );
    };
    const page = await loadResourcePage();
    assert.equal(page.next_cursor, historyCursor(1));
    assert.equal(urls.length, 1, "must not eagerly follow next_cursor");
    globalThis.fetch = async (input) => {
      urls.push(String(input));
      return Response.json(historyPage());
    };
    await loadResourcePage(page.next_cursor!, true);
    const request = new URL(urls[1], "https://example.test");
    assert.equal(request.searchParams.get("limit"), "50");
    assert.equal(request.searchParams.get("after"), historyCursor(1));
    assert.equal(request.searchParams.get("all"), "true");
    globalThis.fetch = async () =>
      Response.json({ transfers: Array.from({ length: 51 }, () => ({ id: "x" })), slots: [] });
    await assert.rejects(loadResourcePage(), /unsupported resource page/);
  } finally {
    globalThis.fetch = original;
  }
});

test("a disk-reserve rejection stops the guest PATCH without HEAD or automatic retry", async () => {
  const originalFetch = globalThis.fetch;
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { location: { origin: "https://example.test" } },
  });
  const methods: string[] = [];
  try {
    globalThis.fetch = async (_input, init) => {
      assert.equal(init?.credentials, "omit");
      methods.push(init?.method ?? "GET");
      if (init?.method === "POST")
        return new Response(null, {
          status: 201,
          headers: {
            Location: "/api/v1/transfers/transfer/files/12345678-1234-1234-1234-123456789012",
          },
        });
      return Response.json({ code: "disk_capacity" }, { status: 507 });
    };
    await assert.rejects(
      uploadEncryptedFile({
        key: new Uint8Array(32).fill(7),
        file: new Blob(["one"]),
        encryptionId: "12".repeat(16),
        endpoint: "/api/v1/transfers/transfer/files",
        token: "public-test-only",
        signal: new AbortController().signal,
        onProgress: () => {},
      }),
      (error) =>
        error instanceof ResourceLimitError && /free-disk safety reserve/.test(error.message),
    );
    assert.deepEqual(methods, ["POST", "PATCH"]);
  } finally {
    globalThis.fetch = originalFetch;
    if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});

test("effective disk snapshots keep unknown distinct from zero and reject inconsistent headroom", () => {
  const capacity = {
    checked_at: "2026-10-04T12:00:00Z",
    state: "ready",
    scope: "account",
    available_wire_bytes: 500,
    available_files: 2,
    available_transfers: 3,
    available_slots: 4,
  };
  const input = { policy: resourcePolicy, usage: resourceUsage, capacity };
  assert.equal(validateResourceSnapshot(input).capacity?.available_wire_bytes, 500);
  assert.equal(
    validateResourceSnapshot({
      ...input,
      capacity: { ...capacity, state: "unknown", available_wire_bytes: null },
    }).capacity?.available_wire_bytes,
    null,
  );
  assert.equal(
    validateResourceSnapshot({
      ...input,
      capacity: { ...capacity, state: "blocked", available_wire_bytes: 0 },
    }).capacity?.state,
    "blocked",
  );
  for (const mutation of [
    { state: "unknown" },
    { state: "blocked" },
    { available_wire_bytes: -1 },
    { available_files: NaN },
    { checked_at: "bad date" },
    { scope: "other" },
  ])
    assert.throws(() =>
      validateResourceSnapshot({ ...input, capacity: { ...capacity, ...mutation } }),
    );
});

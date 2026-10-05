import assert from "node:assert/strict";
import { test } from "node:test";
import { checkBrowserStorage, RECEIVE_RESERVE_BYTES } from "../src/lib/recipient-policy.ts";
import { validateDownload } from "../src/lib/download-validation.ts";
import { getTransferInfo, responseError } from "../src/lib/api.ts";
import { wireSize } from "../src/lib/chunked-files.ts";

test("browser quota reserves two copies and rejects unknown/invalid headroom", () => {
  const reserve = RECEIVE_RESERVE_BYTES;
  assert.doesNotThrow(() => checkBrowserStorage({ quota: reserve + 20, usage: 0 }, 10));
  for (const estimate of [
    { quota: reserve + 19, usage: 0 },
    {},
    { quota: 10 },
    { quota: -1, usage: 0 },
    { quota: Number.MAX_SAFE_INTEGER + 1, usage: 0 },
    { quota: reserve + 20, usage: 1 },
    { quota: 1, usage: 2 },
  ])
    assert.throws(() => checkBrowserStorage(estimate, 10));
});

test("server policy must identify exactly the authenticated files, including sizes and attempt counters", () => {
  const id = "12345678-1234-1234-1234-123456789012";
  const entry = {
    name: "file",
    size: 1,
    mime_type: "text/plain",
    blob_id: id,
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: "ab".repeat(16),
  };
  const manifest = { files: [entry] };
  const info = {
    id,
    status: "complete",
    file_count: 1,
    total_size: wireSize(1),
    expires_at: "",
    downloaded_at: null,
    max_downloads: 2,
    files: [{ id, size: wireSize(1), download_count: 1, remaining_downloads: 1 }],
  };
  assert.doesNotThrow(() => validateDownload(info, id, manifest));
  for (const max_downloads of [0, 2])
    assert.doesNotThrow(() =>
      validateDownload(
        { ...info, max_downloads, files: [{ id, size: wireSize(1) }] },
        id,
        manifest,
      ),
    );
  for (const change of [
    { id: "different" },
    { status: "pending" },
    { max_downloads: 2.5 },
    { files: undefined },
    { files: [{ ...info.files[0], size: 1 }] },
    { files: [{ ...info.files[0], download_count: -1 }] },
    { files: [{ ...info.files[0], remaining_downloads: 3 }] },
    { files: [info.files[0], info.files[0]] },
  ])
    assert.throws(() => validateDownload({ ...info, ...change }, id, manifest));
  assert.doesNotThrow(() =>
    validateDownload({ ...info, max_downloads: 0, files: undefined }, id, manifest),
  );
});

test("partial staging still reserves a whole-file handoff copy", () => {
  const mib = 1024 * 1024;
  assert.throws(() =>
    checkBrowserStorage({ quota: RECEIVE_RESERVE_BYTES + 20 * mib, usage: 0 }, 8 * mib, 200 * mib),
  );
  assert.throws(() =>
    checkBrowserStorage({ quota: RECEIVE_RESERVE_BYTES + 199 * mib, usage: 0 }, 0, 200 * mib),
  );
  assert.doesNotThrow(() =>
    checkBrowserStorage({ quota: RECEIVE_RESERVE_BYTES + 208 * mib, usage: 0 }, 8 * mib, 200 * mib),
  );
});

test("oversized chunked metadata/error bodies stop reading and cancel the stream", async () => {
  const previous = globalThis.fetch;
  let cancelled = 0;
  const response = (limit: number, status = 200) =>
    new Response(
      new ReadableStream({
        pull(controller) {
          controller.enqueue(new Uint8Array(limit + 1));
        },
        cancel() {
          cancelled++;
        },
      }),
      { status },
    );
  try {
    globalThis.fetch = async () => response(128 * 1024);
    await assert.rejects(getTransferInfo("12345678-1234-1234-1234-123456789012"), /exceeds/);
    assert.equal(cancelled, 1);
    assert.equal(
      ((await responseError(response(4096, 500))) as Error & { status: number }).status,
      500,
    );
    assert.equal(cancelled, 2);
  } finally {
    globalThis.fetch = previous;
  }
});

test("cancel interrupts a stalled metadata body even when fetch ignores the signal", async () => {
  const previous = globalThis.fetch;
  const controller = new AbortController();
  let cancelled = false;
  try {
    globalThis.fetch = async () =>
      new Response(
        new ReadableStream({
          cancel() {
            cancelled = true;
          },
        }),
      );
    const pending = getTransferInfo("12345678-1234-1234-1234-123456789012", controller.signal);
    await new Promise((resolve) => setTimeout(resolve, 0));
    controller.abort();
    await assert.rejects(pending, { name: "AbortError" });
    assert.equal(cancelled, true);
  } finally {
    globalThis.fetch = previous;
  }
});

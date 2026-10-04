import { test } from "node:test";
import assert from "node:assert/strict";
import {
  loadServerLimits,
  loadUploadLimit,
  assertFileSize,
  DEFAULT_FILE_BYTES,
} from "../src/lib/limits.ts";
test("loads server limits without credentials and rejects unavailable or invalid settings", async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async (_input, init) => {
      assert.equal(init?.credentials, "omit");
      return Response.json({
        max_file_size: 512 * 1024 * 1024,
        max_file_size_ceiling: 5 * 1024 ** 3,
      });
    };
    assert.equal(await loadUploadLimit(), 512 * 1024 * 1024);
    globalThis.fetch = async () => new Response(null, { status: 404 });
    await assert.rejects(loadUploadLimit());
    globalThis.fetch = async () => new Response(null, { status: 503 });
    await assert.rejects(loadUploadLimit());
    globalThis.fetch = async () => Response.json({ max_file_size: -1 });
    await assert.rejects(loadServerLimits());
    globalThis.fetch = async () =>
      Response.json({ max_file_size: 1024, max_file_size_ceiling: 100 });
    await assert.rejects(loadServerLimits());
  } finally {
    globalThis.fetch = original;
  }
});
test("upload validation honors a lower configured cap", () => {
  assertFileSize(1024, 1024);
  assert.throws(() => assertFileSize(1025, 1024));
});

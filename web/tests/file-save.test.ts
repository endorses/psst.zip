import assert from "node:assert/strict";
import { test } from "node:test";
import { createSaveSink } from "../src/lib/file-save.ts";
import { MAX_BUFFERED_BYTES } from "../src/lib/limits.ts";
import { RECEIVE_RESERVE_BYTES } from "../src/lib/recipient-policy.ts";

const entry = {
  name: "file.bin",
  size: MAX_BUFFERED_BYTES + 1,
  mime_type: "application/octet-stream",
  blob_id: "12345678-1234-1234-1234-123456789012",
  encoding: "chunked-v1" as const,
  chunk_size: 4194304 as const,
  encryption_id: "ab".repeat(16),
};

async function storage(
  run: (state: {
    estimate: () => Promise<StorageEstimate>;
    opened: number;
    removed: number;
    closed: number;
    aborted: number;
    failWrite: boolean;
    closeGate?: () => Promise<void>;
    fileReads: number;
  }) => Promise<void>,
) {
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const previousNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const state = {
    estimate: async () => ({ quota: RECEIVE_RESERVE_BYTES + entry.size * 2, usage: 0 }),
    opened: 0,
    removed: 0,
    closed: 0,
    aborted: 0,
    failWrite: false,
    closeGate: undefined as (() => Promise<void>) | undefined,
    fileReads: 0,
  };
  const writer = {
    async write() {
      if (state.failWrite) throw new DOMException("Disk full", "QuotaExceededError");
    },
    async close() {
      await state.closeGate?.();
      state.closed++;
    },
    async abort() {
      state.aborted++;
    },
  };
  const directory = {
    async getDirectoryHandle() {
      return directory;
    },
    async getFileHandle() {
      state.opened++;
      return {
        async getFile() {
          state.fileReads++;
          return new Blob();
        },
        async createWritable() {
          return writer;
        },
      };
    },
    async removeEntry() {
      state.removed++;
    },
  };
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { isSecureContext: true },
  });
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { storage: { estimate: () => state.estimate(), getDirectory: async () => directory } },
  });
  try {
    await run(state);
  } finally {
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else Reflect.deleteProperty(globalThis, "window");
    if (previousNavigator) Object.defineProperty(globalThis, "navigator", previousNavigator);
    else Reflect.deleteProperty(globalThis, "navigator");
  }
}

test("low or unknown browser quota refuses a large save before creating a temporary file", async () => {
  await storage(async (state) => {
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES + entry.size * 2 - 1, usage: 0 });
    await assert.rejects(createSaveSink(entry), /Not enough browser storage/);
    state.estimate = async () => ({}) as StorageEstimate;
    await assert.rejects(createSaveSink(entry), /Could not check browser storage/);
    assert.equal(state.opened, 0);
  });
});

test("partial or surplus plaintext never commits an OPFS file and abort removes it", async () => {
  await storage(async (state) => {
    const sink = await createSaveSink(entry);
    await sink.write(new Uint8Array(1));
    await assert.rejects(sink.close(), /Incomplete file/);
    await assert.rejects(sink.write(new Uint8Array(entry.size)), /Unexpected file size/);
    assert.equal(state.closed, 0);
    await sink.abort();
    assert.equal(state.removed, 1);
    assert.equal(state.aborted, 1);
  });
});

test("quota changing after preflight is checked again before publication", async () => {
  await storage(async (state) => {
    const sink = await createSaveSink(entry);
    await sink.write(new Uint8Array(entry.size));
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES - 1, usage: 0 });
    await assert.rejects(sink.close(), /Not enough browser storage/);
    assert.equal(state.closed, 0);
    await sink.abort();
    assert.equal(state.removed, 1);
  });
});

test("cancel during a stalled storage estimate returns promptly without a temporary file", async () => {
  await storage(async (state) => {
    state.estimate = () => new Promise(() => {});
    const controller = new AbortController();
    const pending = createSaveSink(entry, controller.signal);
    controller.abort();
    await assert.rejects(pending, { name: "AbortError" });
    assert.equal(state.opened, 0);
  });
});

test("a storage write failure after admission stays unpublished and removes the temporary file", async () => {
  await storage(async (state) => {
    const sink = await createSaveSink(entry);
    state.failWrite = true;
    await assert.rejects(sink.write(new Uint8Array(1)), /Not enough storage/);
    await sink.abort();
    assert.equal(state.closed, 0);
    assert.equal(state.removed, 1);
  });
});

test("later writes retain full handoff headroom even when only 8 MiB remains", async () => {
  await storage(async (state) => {
    const mib = 1024 * 1024;
    const large = { ...entry, size: 200 * mib };
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES + 400 * mib, usage: 0 });
    const sink = await createSaveSink(large);
    const chunk = new Uint8Array(4 * mib);
    for (let index = 0; index < 48; index++) await sink.write(chunk);
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES + 212 * mib, usage: 192 * mib });
    await assert.rejects(sink.write(new Uint8Array(1)), /Not enough browser storage/);
    await sink.abort();
    assert.equal(state.closed, 0);
    assert.equal(state.removed, 1);
  });
});

test("Cancel during OPFS close never hands off the committed temporary file", async () => {
  await storage(async (state) => {
    const controller = new AbortController();
    const sink = await createSaveSink(entry, controller.signal);
    await sink.write(new Uint8Array(entry.size));
    let finishClose!: () => void, started!: () => void;
    const entered = new Promise<void>((resolve) => {
      started = resolve;
    });
    state.closeGate = () => {
      started();
      return new Promise<void>((resolve) => {
        finishClose = resolve;
      });
    };
    const closing = sink.close();
    await entered;
    controller.abort();
    finishClose();
    await assert.rejects(closing, { name: "AbortError" });
    await sink.abort();
    assert.equal(state.fileReads, 0);
    assert.equal(state.removed, 1);
  });
});

test("quota estimates that omit uncommitted staging cannot erase its space charge", async () => {
  await storage(async (state) => {
    const mib = 1024 * 1024;
    const large = { ...entry, size: 200 * mib };
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES + 400 * mib, usage: 0 });
    const sink = await createSaveSink(large);
    const chunk = new Uint8Array(4 * mib);
    for (let index = 0; index < 48; index++) await sink.write(chunk);
    // The browser still reports zero usage for 192 MiB of writable staging.
    // A 399 MiB allowance cannot fit staging + remaining bytes + full handoff.
    state.estimate = async () => ({ quota: RECEIVE_RESERVE_BYTES + 399 * mib, usage: 0 });
    await assert.rejects(sink.write(new Uint8Array(1)), /Not enough browser storage/);
    await sink.abort();
    assert.equal(state.closed, 0);
    assert.equal(state.removed, 1);
  });
});

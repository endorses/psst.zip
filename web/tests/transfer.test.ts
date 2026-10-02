import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createServer } from "node:net";
import { Upload } from "tus-js-client";
import * as api from "../src/lib/api.ts";
import * as crypto from "../src/lib/crypto.ts";
import { assertFileSize, MAX_BUFFERED_BYTES } from "../src/lib/limits.ts";

let server: ChildProcess;
let directory: string;
let origin: string;
const realFetch = globalThis.fetch;

before(async () => {
  directory = mkdtempSync(join(tmpdir(), "psst-integration-"));
  const listener = createServer();
  await new Promise<void>((done, reject) => {
    listener.once("error", reject);
    listener.listen(0, "127.0.0.1", done);
  });
  const address = listener.address();
  assert(address && typeof address !== "string");
  const port = address.port;
  await new Promise<void>((done) => listener.close(() => done()));
  origin = `http://127.0.0.1:${port}`;
  execFileSync("go", ["build", "-o", join(directory, "server"), "./cmd/server"], {
    cwd: resolve("../backend"),
    timeout: 60000,
  });
  server = spawn(join(directory, "server"), [], {
    env: {
      ...process.env,
      LISTEN_ADDR: `127.0.0.1:${port}`,
      DB_PATH: join(directory, "psst.db"),
      STORAGE_PATH: join(directory, "files"),
    },
    stdio: "ignore",
  });
  let startupError: Error | undefined;
  server.on("error", (error) => {
    startupError = error;
  });
  globalThis.fetch = (input, options) =>
    realFetch(typeof input === "string" ? new URL(input, origin) : input, options);
  for (let i = 0; i < 100; i++) {
    if (startupError) throw startupError;
    if (server.exitCode !== null) throw new Error(`Backend exited: ${server.exitCode}`);
    try {
      await realFetch(`${origin}/api/v1/transfers/missing`, { signal: AbortSignal.timeout(1000) });
      return;
    } catch {
      await new Promise((done) => setTimeout(done, 50));
    }
  }
  throw new Error("Backend did not start");
});
after(async () => {
  globalThis.fetch = realFetch;
  if (server?.pid && server.exitCode === null && server.signalCode === null) {
    const exited = new Promise((done) => server.once("exit", done));
    server.kill("SIGTERM");
    await exited;
  }
  if (directory) rmSync(directory, { recursive: true, force: true });
});

function upload(id: string, encrypted: ArrayBuffer): Promise<string> {
  return new Promise((done, reject) => {
    const task = new Upload(Buffer.from(encrypted), {
      endpoint: `${origin}${api.tusEndpoint(id)}`,
      chunkSize: 7,
      retryDelays: [],
      onError: reject,
      onSuccess: () => {
        assert(task.url);
        done(new URL(task.url).pathname.split("/").pop()!);
      },
    });
    task.start();
  });
}

async function roundTrip(id: string) {
  assert.match(id, /^[0-9a-f-]{36}$/);
  const key = await crypto.generateKey();
  const restoredKey = await crypto.importKey(await crypto.exportKey(key));
  const files = [];
  for (const text of ['hello "world"\n', ""]) {
    const plaintext = new TextEncoder().encode(text);
    const blob_id = await upload(id, await crypto.encrypt(key, plaintext.buffer));
    files.push({
      name: `file-${files.length}.txt`,
      size: plaintext.length,
      mime_type: "text/plain",
      blob_id,
    });
  }
  await api.uploadManifest(id, await crypto.encryptManifest(key, { files }));
  await api.completeTransfer(id);
  const manifest = await crypto.decryptManifest(restoredKey, await api.downloadManifest(id));
  assert.deepEqual(manifest.files, files);
  for (const [index, file] of manifest.files.entries()) {
    const decrypted = await crypto.decrypt(restoredKey, await api.downloadFile(id, file.blob_id));
    assert.equal(new TextDecoder().decode(decrypted), index === 0 ? 'hello "world"\n' : "");
  }
}

test("web API and real tus client upload encrypted files and download them", async () => {
  const { id } = await api.createTransfer();
  await roundTrip(id);
});
test("drop slot uploads create a completed child transfer", async () => {
  const slot = await api.createSlot();
  const { id } = await api.createSlotTransfer(slot.id);
  await roundTrip(id);
  const result = await api.getSlotInfo(slot.id);
  assert.equal(result.transfers[0].transfer_id, id);
  assert.equal(result.transfers[0].status, "complete");
});
test("AES-GCM rejects a wrong key and modified ciphertext", async () => {
  const key = await crypto.generateKey();
  const encrypted = await crypto.encrypt(key, new TextEncoder().encode("private").buffer);
  await assert.rejects(crypto.decrypt(await crypto.generateKey(), encrypted));
  new Uint8Array(encrypted)[15] ^= 1;
  await assert.rejects(crypto.decrypt(key, encrypted));
});
test("buffered clients reject oversized files", () => {
  assertFileSize(MAX_BUFFERED_BYTES);
  assert.throws(() => assertFileSize(MAX_BUFFERED_BYTES + 1));
});

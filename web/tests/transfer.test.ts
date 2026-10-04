import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createServer } from "node:net";
import { Upload } from "tus-js-client";
import * as api from "../src/lib/api.ts";
import { encryptFileFrame, decryptFileStream, newEncryptionId } from "../src/lib/chunked-files.ts";
import * as crypto from "../src/lib/crypto.ts";
import { assertFileSize, MAX_BUFFERED_BYTES } from "../src/lib/limits.ts";
import {
  generateReceiveKeyPair,
  sealSubmissionKey,
  openSubmissionKey,
  encodeReceiveEnvelope,
  decodeReceiveEnvelope,
} from "../src/lib/receive-crypto.ts";

let server: ChildProcess;
let directory: string;
let origin: string;
let authToken = "";
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
      ADMIN_USERNAME: "admin",
      ADMIN_PASSWORD: "Test-admin-password-2026",
      AUTH_ALLOW_INSECURE_HTTP: "true",
      LISTEN_ADDR: `127.0.0.1:${port}`,
      DB_PATH: join(directory, "psst.db"),
      STORAGE_PATH: join(directory, "files"),
      // Tiny tus chunks intentionally create a burst of requests in this fixture.
      RATE_LIMIT_GLOBAL: "1000",
      RATE_LIMIT_BURST: "1000",
      RATE_LIMIT_CREATION: "1000",
      RATE_LIMIT_CREATION_BURST: "1000",
    },
    stdio: "ignore",
  });
  let startupError: Error | undefined;
  server.on("error", (error) => {
    startupError = error;
  });
  globalThis.fetch = (input, options) =>
    realFetch(typeof input === "string" ? new URL(input, origin) : input, {
      ...options,
      headers: {
        ...Object.fromEntries(new Headers(options?.headers)),
        ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
      },
    });
  for (let i = 0; i < 100; i++) {
    if (startupError) throw startupError;
    if (server.exitCode !== null) throw new Error(`Backend exited: ${server.exitCode}`);
    try {
      await realFetch(`${origin}/api/v1/transfers/missing`, { signal: AbortSignal.timeout(1000) });
      const login = await realFetch(`${origin}/api/v1/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Origin: origin },
        body: JSON.stringify({
          username: "admin",
          password: "Test-admin-password-2026",
          session_type: "web",
        }),
      });
      assert.equal(login.status, 200);
      const adminToken = login.headers.get("set-cookie")?.match(/psst_session=([^;]+)/)?.[1];
      assert(adminToken);
      const temporary = "Temporary-member-password-2026",
        replacement = "Final-member-password-2026";
      const created = await realFetch(`${origin}/api/v1/admin/users`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${adminToken}` },
        body: JSON.stringify({ username: "member", password: temporary, role: "user" }),
      });
      assert.equal(created.status, 201);
      const memberLogin = async (password: string) =>
        realFetch(`${origin}/api/v1/auth/login`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ username: "member", password, session_type: "device" }),
        });
      const first = await memberLogin(temporary);
      assert.equal(first.status, 200);
      const restricted = (await first.json()).token;
      const changed = await realFetch(`${origin}/api/v1/auth/password`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${restricted}` },
        body: JSON.stringify({ current_password: temporary, password: replacement }),
      });
      assert.equal(changed.status, 204);
      const final = await memberLogin(replacement);
      assert.equal(final.status, 200);
      authToken = (await final.json()).token;
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
      headers: { Authorization: `Bearer ${authToken}` },
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

async function roundTrip(
  id: string,
  receiver?: { slot: string; publicKey: Uint8Array; privateKey: Uint8Array },
) {
  assert.match(id, /^[0-9a-f-]{36}$/);
  const key = await crypto.generateKey();
  const restoredKey = await crypto.importKey(await crypto.exportKey(key));
  const files = [];
  for (const text of ['hello "world"\n', ""]) {
    const plaintext = new TextEncoder().encode(text);
    const encryption_id = newEncryptionId();
    const blob_id = await upload(
      id,
      await encryptFileFrame(key, encryption_id, 0, plaintext.length, plaintext.buffer),
    );
    files.push({
      name: `file-${files.length}.txt`,
      size: plaintext.length,
      mime_type: "text/plain",
      blob_id,
      encryption_id,
      encoding: "chunked-v1" as const,
      chunk_size: 4194304 as const,
    });
  }
  const encryptedManifest = await crypto.encryptManifest(key, { files });
  await api.uploadManifest(
    id,
    receiver
      ? encodeReceiveEnvelope(
          await sealSubmissionKey(receiver.publicKey, receiver.slot, id, key),
          new Uint8Array(encryptedManifest),
        ).buffer
      : encryptedManifest,
  );
  await api.completeTransfer(id);
  const downloaded = await api.downloadManifest(id);
  const envelope = receiver ? decodeReceiveEnvelope(new Uint8Array(downloaded)) : null;
  const downloadKey =
    receiver && envelope
      ? await openSubmissionKey(
          receiver.privateKey,
          receiver.publicKey,
          receiver.slot,
          id,
          envelope.wrappedKey,
        )
      : restoredKey;
  const manifest = await crypto.decryptManifest(
    downloadKey,
    envelope ? envelope.encryptedManifest.buffer : downloaded,
  );
  assert.deepEqual(manifest.files, files);
  for (const [index, file] of manifest.files.entries()) {
    const chunks: Uint8Array[] = [];
    for await (const chunk of decryptFileStream(
      restoredKey,
      file,
      new Blob([await api.downloadFile(id, file.blob_id)]).stream(),
    ))
      chunks.push(chunk);
    const decrypted = Buffer.concat(chunks);
    assert.equal(new TextDecoder().decode(decrypted), index === 0 ? 'hello "world"\n' : "");
  }
}

test("web API and real tus client upload encrypted files and download them", async () => {
  const { id } = await api.createTransfer();
  await roundTrip(id);
});
test("only explicit download acknowledgment records downloaded_at and repeated acknowledgment is idempotent", async () => {
  const { id } = await api.createTransfer();
  await roundTrip(id);
  assert.equal((await api.getTransferInfo(id)).downloaded_at, null);
  await api.acknowledgeDownload(id);
  const downloadedAt = (await api.getTransferInfo(id)).downloaded_at;
  assert.ok(downloadedAt);
  await api.acknowledgeDownload(id);
  assert.equal((await api.getTransferInfo(id)).downloaded_at, downloadedAt);
});
test("private v2 inbox encrypts for its owner, hides children and enforces cumulative file count", async () => {
  const pair = await generateReceiveKeyPair();
  const slot = await api.createSlot(await crypto.exportKey(pair.publicKey), 2);
  const publicAvailability = await realFetch(`${origin}/api/v1/slots/${slot.id}/availability`);
  assert.equal(publicAvailability.status, 200);
  assert.equal((await publicAvailability.json()).transfers, undefined);
  const { id } = await api.createSlotTransfer(slot.id);
  await roundTrip(id, { slot: slot.id, ...pair });
  const result = await api.getSlotInfo(slot.id);
  assert.equal(result.transfers[0].transfer_id, id);
  assert.equal(result.transfers[0].status, "complete");
  assert.equal(result.reserved_files, 2);
  assert.equal(result.remaining_files, 0);
  await assert.rejects(api.createSlotTransfer(slot.id), /receive_file_limit/);
  for (const path of [`slots/${slot.id}`, `transfers/${id}`, `transfers/${id}/manifest`])
    assert.ok([401, 404].includes((await realFetch(`${origin}/api/v1/${path}`)).status));
});
test("download count is independently enforced per file", async () => {
  const { id } = await api.createTransfer(2);
  await roundTrip(id);
  const info = await api.getTransferInfo(id);
  assert.ok(info.files?.length === 2);
  assert.ok(info.files.every((file) => file.remaining_downloads === 1));
  await api.downloadFile(id, info.files[0].id);
  await assert.rejects(api.downloadFile(id, info.files[0].id), /download_limit/);
  assert.equal((await api.getTransferInfo(id)).files?.[1].remaining_downloads, 1);
});
test("AES-GCM rejects a wrong key and modified ciphertext", async () => {
  const key = await crypto.generateKey();
  const encrypted = await crypto.encrypt(key, new TextEncoder().encode("private").buffer);
  await assert.rejects(crypto.decrypt(await crypto.generateKey(), encrypted));
  new Uint8Array(encrypted)[15] ^= 1;
  await assert.rejects(crypto.decrypt(key, encrypted));
});
test("buffered clients reject oversized files", () => {
  assertFileSize(MAX_BUFFERED_BYTES, MAX_BUFFERED_BYTES);
  assert.throws(() => assertFileSize(MAX_BUFFERED_BYTES + 1, MAX_BUFFERED_BYTES));
});

import { readFile } from "node:fs/promises";
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  encryptFile,
  decryptFileStream,
  FILE_CHUNK_SIZE,
  wireSize,
} from "../src/lib/chunked-files.ts";
import { generateKey, type FileManifestEntry } from "../src/lib/crypto.ts";
const id = "ab".repeat(16);
function entry(size: number): FileManifestEntry {
  return {
    name: "test",
    size,
    mime_type: "application/octet-stream",
    blob_id: "01234567-89ab-cdef-0123-456789abcdef",
    encoding: "chunked-v1",
    chunk_size: 4194304,
    encryption_id: id,
  };
}
async function collect(stream: AsyncIterable<ArrayBuffer | Uint8Array>): Promise<Buffer> {
  const parts = [];
  for await (const part of stream)
    parts.push(Buffer.from(part instanceof ArrayBuffer ? new Uint8Array(part) : part));
  return Buffer.concat(parts);
}
test("chunked files roundtrip empty, partial, full and multiple frames with bounded source reads", async () => {
  for (const size of [0, 12, FILE_CHUNK_SIZE, FILE_CHUNK_SIZE + 17]) {
    const key = await generateKey(),
      bytes = Buffer.alloc(size, 0x5a),
      file = new Blob([bytes]);
    const frames = [];
    for await (const frame of encryptFile(key, file, id)) {
      assert(frame.byteLength <= FILE_CHUNK_SIZE + 60);
      frames.push(frame);
    }
    assert.equal(
      frames.reduce((n, f) => n + f.byteLength, 0),
      wireSize(size),
    );
    const result = await collect(decryptFileStream(key, entry(size), new Blob(frames).stream()));
    assert.deepEqual(result, bytes);
  }
});
test("context, index, total, corruption, truncation and surplus all fail before completion", async () => {
  const key = await generateKey(),
    size = FILE_CHUNK_SIZE + 3;
  const frames = [];
  for await (const frame of encryptFile(key, new Blob([new Uint8Array(size)]), id))
    frames.push(frame);
  const damaged = frames[1].slice(0);
  new Uint8Array(damaged)[damaged.byteLength - 1] ^= 1;
  for (const [descriptor, payload] of [
    [{ ...entry(size), encryption_id: "cd".repeat(16) }, frames],
    [{ ...entry(size), size: size + 1 }, frames],
    [entry(size), [frames[1], frames[0]]],
    [entry(size), [frames[0], damaged]],
    [entry(size), [frames[0]]],
    [entry(size), [...frames, new Uint8Array([1])]],
  ] as [FileManifestEntry, BlobPart[]][])
    await assert.rejects(collect(decryptFileStream(key, descriptor, new Blob(payload).stream())));
});

test("decrypts independent protocol vector also consumed by shared Kotlin", async () => {
  const vector = JSON.parse(
    await readFile(
      new URL("../../docs/protocol/chunked-file-vector.json", import.meta.url),
      "utf8",
    ),
  );
  const key = new Uint8Array(Buffer.from(vector.key, "hex"));
  const file = { ...entry(vector.total_size), encryption_id: vector.encryption_id };
  const result = await collect(
    decryptFileStream(key, file, new Blob([Buffer.from(vector.frame, "hex")]).stream()),
  );
  assert.equal(result.toString("hex"), vector.plaintext);
});

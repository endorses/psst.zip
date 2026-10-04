import assert from "node:assert/strict";
import { test } from "node:test";
import { unzipSync, zipSync } from "fflate";
import { safeFilename, uniqueFilenames, zipEntryName } from "../src/lib/filenames.ts";
import { decryptManifest, encryptManifest, generateKey } from "../src/lib/crypto.ts";

test("safe filenames reject directories and remove hidden direction/control characters", () => {
  for (const name of ["", " ", ".", "..", "... ", "../a.txt", "a/b", "a\\b", "a\0b"])
    assert.throws(() => safeFilename(name), /unsafe name/);
  assert.equal(safeFilename("report\u202egpj.exe"), "report_gpj.exe");
  assert.equal(safeFilename("a\u061c\u200e\u200f\u2066\u2069\x7f\x85.txt"), "a_______.txt");
  assert.equal(safeFilename("<script>:a?.html"), "_script__a_.html");
  assert.equal(safeFilename(" COM1.txt. "), "_COM1.txt");
  assert.equal(safeFilename("Cafe\u0301.txt"), "Café.txt");
  const long = safeFilename("😀".repeat(250) + ".pdf");
  assert.ok(new TextEncoder().encode(long).length <= 200);
  assert.ok(long.endsWith(".pdf"));
  assert.ok(!long.includes("\ufffd"));
});

test("normalization, casing, truncation and sanitization collisions retain every file", () => {
  const names = uniqueFilenames([
    "report\u202e.exe",
    "report_.exe",
    "REPORT_.exe",
    "report_ (2).exe",
    "Café.txt",
    "Cafe\u0301.txt",
    "a".repeat(250) + ".pdf",
    "a".repeat(251) + ".pdf",
  ]);
  assert.equal(new Set(names.map((name) => name.toLowerCase())).size, names.length);
  assert.equal(names[1], "report_ (2).exe");
  assert.equal(names[2], "REPORT_ (3).exe");
  assert.ok(names.every((name) => new TextEncoder().encode(name).length <= 200));
  const entries = Object.create(null);
  for (const [index, name] of [...names, "__proto__"].entries())
    entries[zipEntryName(name, index)] = new Uint8Array([index]);
  const unpacked = unzipSync(zipSync(entries));
  assert.equal(Object.keys(unpacked).length, names.length + 1);
  for (const [index, name] of [...names, "__proto__"].entries())
    assert.deepEqual(unpacked[zipEntryName(name, index)], new Uint8Array([index]));
});

test("decrypted manifests expose the same safe distinct names to display and save consumers", async () => {
  const key = await generateKey();
  const files = ["photo\u202egpj.exe", "photo_gpj.exe", "<img onerror=alert(1)>.svg"].map(
    (name, i) => ({
      name,
      size: 0,
      mime_type: "text/html",
      encoding: "chunked-v1" as const,
      chunk_size: 4194304 as const,
      encryption_id: i.toString(16).padStart(32, "0"),
      blob_id: `12345678-1234-1234-1234-${i.toString().padStart(12, "0")}`,
    }),
  );
  const manifest = await decryptManifest(key, await encryptManifest(key, { files }));
  assert.deepEqual(
    manifest.files.map((file) => file.name),
    ["photo_gpj.exe", "photo_gpj (2).exe", "_img onerror=alert(1)_.svg"],
  );
  for (const file of manifest.files) assert.equal(safeFilename(file.name), file.name);
  await assert.rejects(
    decryptManifest(key, await encryptManifest(key, { files: [{ ...files[0], name: ".." }] })),
    /unsafe name/,
  );
});

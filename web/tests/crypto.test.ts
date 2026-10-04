import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { test } from "node:test";
import {
  decrypt,
  encrypt,
  decryptManifest,
  encryptManifest,
  exportKey,
  generateKey,
  importKey,
  type EncryptionKey,
} from "../src/lib/crypto.ts";

// Model a real HTTP browser: crypto exists and provides a CSPRNG, but subtle is
// absent. The reference implementation remains Node's independent Web Crypto.
async function withoutSubtle(run: () => Promise<void>) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  Object.defineProperty(globalThis, "crypto", {
    configurable: true,
    value: { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) },
  });
  try {
    assert.equal(globalThis.crypto.subtle, undefined);
    await run();
  } finally {
    if (descriptor) Object.defineProperty(globalThis, "crypto", descriptor);
    else Reflect.deleteProperty(globalThis, "crypto");
  }
}

function packed(iv: Uint8Array, ciphertext: ArrayBuffer): ArrayBuffer {
  const bytes = new Uint8Array(iv.length + ciphertext.byteLength);
  bytes.set(iv);
  bytes.set(new Uint8Array(ciphertext), iv.length);
  return bytes.buffer;
}

test("LAN HTTP fallback exchanges AES-256-GCM vectors with independent Web Crypto", async () => {
  await withoutSubtle(async () => {
    const raw = Uint8Array.from({ length: 32 }, (_, index) => index);
    const key = await importKey(Buffer.from(raw).toString("base64url"));
    const native = await webcrypto.subtle.importKey("raw", raw, "AES-GCM", false, [
      "encrypt",
      "decrypt",
    ]);
    const vectors = [
      new Uint8Array(0),
      new TextEncoder().encode("Plaintext with Unicode: café 日本語 🔐"),
      Uint8Array.from({ length: 257 }, (_, index) => index % 256),
    ];
    for (const [index, plaintext] of vectors.entries()) {
      const iv = Uint8Array.from({ length: 12 }, (_, offset) => offset + index);
      const nativeCiphertext = await webcrypto.subtle.encrypt(
        { name: "AES-GCM", iv, tagLength: 128 },
        native,
        plaintext,
      );
      assert.deepEqual(new Uint8Array(await decrypt(key, packed(iv, nativeCiphertext))), plaintext);

      const fallbackCiphertext = await encrypt(key, plaintext.buffer);
      assert.equal(fallbackCiphertext.byteLength, 12 + plaintext.length + 16);
      const fallbackBytes = new Uint8Array(fallbackCiphertext);
      const fallbackIV = fallbackBytes.slice(0, 12);
      const nativePlaintext = await webcrypto.subtle.decrypt(
        { name: "AES-GCM", iv: fallbackIV, tagLength: 128 },
        native,
        fallbackBytes.slice(12),
      );
      assert.deepEqual(new Uint8Array(nativePlaintext), plaintext);
      // Identical inputs produce identical ciphertext and tag across providers.
      const reference = await webcrypto.subtle.encrypt(
        { name: "AES-GCM", iv: fallbackIV, tagLength: 128 },
        native,
        plaintext,
      );
      assert.deepEqual(fallbackBytes.slice(12), new Uint8Array(reference));
    }
  });
});

test("LAN HTTP fallback generates 32-byte URL-fragment keys and unique nonces", async () => {
  await withoutSubtle(async () => {
    const key = await generateKey();
    assert.equal(key.byteLength, 32);
    assert.notDeepEqual(await generateKey(), key);
    const fragment = await exportKey(key);
    assert.match(fragment, /^[A-Za-z0-9_-]{43}$/);
    assert.deepEqual(await importKey(fragment), key);
    const first = new Uint8Array(await encrypt(key, new ArrayBuffer(0)));
    const second = new Uint8Array(await encrypt(key, new ArrayBuffer(0)));
    assert.equal(first.byteLength, 28);
    assert.notDeepEqual(first.slice(0, 12), second.slice(0, 12));
    const manifest = {
      files: [
        {
          name: "empty.txt",
          size: 0,
          mime_type: "text/plain",
          encoding: "chunked-v1" as const,
          chunk_size: 4194304 as const,
          encryption_id: "ab".repeat(16),
          blob_id: "12345678-1234-1234-1234-123456789012",
        },
      ],
    };
    assert.deepEqual(await decryptManifest(key, await encryptManifest(key, manifest)), manifest);
  });
});

for (const provider of ["Web Crypto", "LAN HTTP fallback"]) {
  test(`${provider} rejects tampering, wrong keys, truncated blobs and invalid key sizes`, async () => {
    const verify = async () => {
      const key = await generateKey();
      const plaintext = new TextEncoder().encode("authenticated plaintext").buffer;
      const encrypted = await encrypt(key, plaintext);
      await assert.rejects(decrypt(await generateKey(), encrypted));
      // Independently modify the nonce, ciphertext and authentication tag.
      for (const offset of [0, 12, encrypted.byteLength - 1]) {
        const modified = new Uint8Array(encrypted.slice(0));
        modified[offset] ^= 1;
        await assert.rejects(decrypt(key, modified.buffer));
      }
      for (const size of [0, 11, 12, 27]) {
        await assert.rejects(decrypt(key, new ArrayBuffer(size)), /too short/);
      }
      await assert.rejects(decrypt(key, encrypted.slice(0, -1)));
      for (const size of [0, 16, 24, 31, 33]) {
        const invalidKey: EncryptionKey = new Uint8Array(size);
        await assert.rejects(importKey(Buffer.from(invalidKey).toString("base64url")), /32 bytes/);
        await assert.rejects(exportKey(invalidKey), /32 bytes/);
        await assert.rejects(encrypt(invalidKey, plaintext), /32 bytes/);
        await assert.rejects(decrypt(invalidKey, encrypted), /32 bytes/);
      }
      await assert.rejects(importKey("invalid!fragment"));
    };
    if (provider === "LAN HTTP fallback") await withoutSubtle(verify);
    else {
      assert.ok(globalThis.crypto.subtle);
      await verify();
    }
  });
}

test("encryption refuses to generate keys or nonces without a CSPRNG", async () => {
  const key = await generateKey();
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  Object.defineProperty(globalThis, "crypto", { configurable: true, value: undefined });
  try {
    await assert.rejects(generateKey(), /cryptographically secure/);
    await assert.rejects(encrypt(key, new ArrayBuffer(0)), /cryptographically secure/);
  } finally {
    if (descriptor) Object.defineProperty(globalThis, "crypto", descriptor);
    else Reflect.deleteProperty(globalThis, "crypto");
  }
});

test("manifests reject duplicate blob IDs with conflicting file metadata", async () => {
  const key = await generateKey();
  const file = {
    name: "file.txt",
    size: 1,
    mime_type: "text/plain",
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: "ab".repeat(16),
    blob_id: "12345678-1234-1234-1234-123456789012",
  };
  const encrypted = await encryptManifest(key, { files: [file, { ...file, size: 2 }] });
  await assert.rejects(decryptManifest(key, encrypted), /Invalid file manifest/);
});

test("manifest rejects reused encryption context across different blobs", async () => {
  const key = await generateKey();
  const file = {
    name: "first",
    size: 0,
    mime_type: "text/plain",
    blob_id: "12345678-1234-1234-1234-123456789012",
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: "ab".repeat(16),
  };
  const encrypted = await encryptManifest(key, {
    files: [file, { ...file, name: "second", blob_id: "12345678-1234-1234-1234-123456789013" }],
  });
  await assert.rejects(decryptManifest(key, encrypted), /Invalid file manifest/);
});

test("authenticated manifests respect the aggregate 1 TiB recipient ceiling", async () => {
  const key = await generateKey();
  const entry = {
    name: "large.bin",
    size: 1024 ** 4,
    mime_type: "application/octet-stream",
    blob_id: "12345678-1234-1234-1234-123456789012",
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: "ab".repeat(16),
  };
  assert.equal(
    (await decryptManifest(key, await encryptManifest(key, { files: [entry] }))).files[0].size,
    entry.size,
  );
  await assert.rejects(
    decryptManifest(
      key,
      await encryptManifest(key, {
        files: [
          entry,
          {
            ...entry,
            name: "extra.bin",
            size: 1,
            blob_id: "12345678-1234-1234-1234-123456789013",
            encryption_id: "cd".repeat(16),
          },
        ],
      }),
    ),
    /Invalid file manifest/,
  );
});

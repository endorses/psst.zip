import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { webcrypto } from "node:crypto";
import { test } from "node:test";
import * as HPKE from "hpke";
import * as noble from "@panva/hpke-noble";
import {
  generateReceiveKeyPair,
  sealSubmissionKey,
  openSubmissionKey,
  receiveContextInfo,
  encodeReceiveEnvelope,
  decodeReceiveEnvelope,
  RECEIVE_ENVELOPE_MAX_BYTES,
} from "../src/lib/receive-crypto.ts";
import { encryptManifest, decryptManifest } from "../src/lib/crypto.ts";

const fixture = JSON.parse(
  readFileSync(
    new URL("../../docs/security/fixtures/hpke-receive-v2.json", import.meta.url),
    "utf8",
  ),
);
const rfc = JSON.parse(
  readFileSync(
    new URL("../../docs/security/fixtures/hpke-rfc9180-x25519-aes256.json", import.meta.url),
    "utf8",
  ),
);
const bytes = (hex: string) => new Uint8Array(Buffer.from(hex, "hex"));
const publicKey = bytes(fixture.public_key);
const privateKey = bytes(fixture.private_key);
const key = bytes(fixture.submission_key);
const wrapped = bytes(fixture.wrapped_key);
const { slot_id: slot, transfer_id: child } = fixture;

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

function cipherSuite(provider: typeof HPKE | typeof noble) {
  return new HPKE.CipherSuite(
    provider.KEM_DHKEM_X25519_HKDF_SHA256,
    provider.KDF_HKDF_SHA256,
    provider.AEAD_AES_256_GCM,
  );
}

test("RFC9180 exact-suite Base vector decrypts with noble and independent WebCrypto primitives", async () => {
  assert.deepEqual([rfc.mode, rfc.kem_id, rfc.kdf_id, rfc.aead_id], [0, 32, 1, 2]);
  for (const provider of [HPKE, noble]) {
    const suite = cipherSuite(provider);
    // Supplying both keys avoids exporting non-extractable private keys on runtimes
    // such as Node 22, which do not provide subtle.getPublicKey().
    const recipient = {
      privateKey: await suite.DeserializePrivateKey(bytes(rfc.skRm)),
      publicKey: await suite.DeserializePublicKey(bytes(rfc.pkRm)),
    };
    assert.deepEqual(
      await suite.Open(recipient, bytes(rfc.enc), bytes(rfc.ct), {
        info: bytes(rfc.info),
        aad: bytes(rfc.aad),
      }),
      bytes(rfc.pt),
    );
  }
  await withoutSubtle(async () => {
    const suite = cipherSuite(noble);
    assert.deepEqual(
      await suite.Open(
        await suite.DeserializePrivateKey(bytes(rfc.skRm)),
        bytes(rfc.enc),
        bytes(rfc.ct),
        { info: bytes(rfc.info), aad: bytes(rfc.aad) },
      ),
      bytes(rfc.pt),
    );
  });
});

test("production empty-AAD fixture has exact context and decrypts on WebCrypto", async () => {
  const info = receiveContextInfo(slot, child, publicKey);
  assert.equal(Buffer.from(info).toString("hex"), fixture.context_info);
  assert.deepEqual(await openSubmissionKey(privateKey, publicKey, slot, child, wrapped), key);
  const native = cipherSuite(HPKE);
  const recipient = {
    privateKey: await native.DeserializePrivateKey(privateKey),
    publicKey: await native.DeserializePublicKey(publicKey),
  };
  assert.deepEqual(
    await native.Open(recipient, wrapped.slice(0, 32), wrapped.slice(32), { info }),
    key,
  );
  assert.equal(typeof fixture.tink_wrapped_key, "string", "Independent Tink fixture is required");
  assert.deepEqual(
    await openSubmissionKey(privateKey, publicKey, slot, child, bytes(fixture.tink_wrapped_key)),
    key,
  );
  assert.equal(typeof fixture.swift_wrapped_key, "string", "Independent Swift fixture is required");
  assert.deepEqual(
    await openSubmissionKey(privateKey, publicKey, slot, child, bytes(fixture.swift_wrapped_key)),
    key,
  );
});

test("random HPKE wrapping and authenticated manifest envelopes work without subtle", async () => {
  await withoutSubtle(async () => {
    const pair = await generateReceiveKeyPair();
    assert.equal(pair.privateKey.length, 32);
    assert.equal(pair.publicKey.length, 32);
    assert.notDeepEqual(pair.publicKey, (await generateReceiveKeyPair()).publicKey);
    const first = await sealSubmissionKey(pair.publicKey, slot, child, key);
    const second = await sealSubmissionKey(pair.publicKey, slot, child, key);
    assert.equal(first.length, 80);
    assert.notDeepEqual(first, second);
    const manifest = {
      files: [
        {
          name: "file.txt",
          size: 0,
          mime_type: "text/plain",
          blob_id: "12345678-1234-1234-1234-123456789014",
          encoding: "chunked-v1" as const,
          chunk_size: 4194304 as const,
          encryption_id: "12".repeat(16),
        },
      ],
    };
    const encrypted = new Uint8Array(await encryptManifest(key, manifest));
    const envelope = encodeReceiveEnvelope(first, encrypted);
    assert.equal(new TextDecoder().decode(envelope.slice(0, 8)), "PSSTRCV2");
    const parsed = decodeReceiveEnvelope(envelope);
    const recovered = await openSubmissionKey(
      pair.privateKey,
      pair.publicKey,
      slot,
      child,
      parsed.wrappedKey,
    );
    assert.deepEqual(await decryptManifest(recovered, parsed.encryptedManifest.buffer), manifest);
    const appended = new Uint8Array(envelope.length + 1);
    appended.set(envelope);
    await assert.rejects(
      decryptManifest(recovered, decodeReceiveEnvelope(appended).encryptedManifest.buffer),
    );
  });
});

test("wrong recipient, slot, child, encapsulation, ciphertext and invalid keys fail closed", async () => {
  const other = await generateReceiveKeyPair();
  const changedId = "12345678-1234-1234-1234-123456789099";
  await assert.rejects(openSubmissionKey(other.privateKey, publicKey, slot, child, wrapped));
  await assert.rejects(openSubmissionKey(privateKey, other.publicKey, slot, child, wrapped));
  await assert.rejects(openSubmissionKey(privateKey, publicKey, changedId, child, wrapped));
  await assert.rejects(openSubmissionKey(privateKey, publicKey, slot, changedId, wrapped));
  for (const offset of [0, 31, 32, 79]) {
    const modified = wrapped.slice();
    modified[offset] ^= 1;
    await assert.rejects(openSubmissionKey(privateKey, publicKey, slot, child, modified));
  }
  for (const size of [0, 31, 33]) {
    await assert.rejects(sealSubmissionKey(publicKey, slot, child, new Uint8Array(size)));
    await assert.rejects(openSubmissionKey(new Uint8Array(size), publicKey, slot, child, wrapped));
    assert.throws(() => receiveContextInfo(slot, child, new Uint8Array(size)));
  }
  await assert.rejects(sealSubmissionKey(new Uint8Array(32), slot, child, key));
  await assert.rejects(openSubmissionKey(privateKey, publicKey, slot, child, wrapped.slice(1)));
  assert.throws(() => receiveContextInfo("../slot", child, publicKey));
});

test("envelope parser strictly bounds framing, version and allocation, and returns copies", () => {
  const minimum = encodeReceiveEnvelope(wrapped, new Uint8Array(28));
  assert.equal(minimum.length, 116);
  assert.equal(decodeReceiveEnvelope(minimum).wrappedKey.length, 80);
  const maximum = encodeReceiveEnvelope(wrapped, new Uint8Array(RECEIVE_ENVELOPE_MAX_BYTES - 88));
  assert.equal(maximum.length, RECEIVE_ENVELOPE_MAX_BYTES);
  decodeReceiveEnvelope(maximum);
  for (const size of [0, 87, 115, RECEIVE_ENVELOPE_MAX_BYTES + 1])
    assert.throws(() => decodeReceiveEnvelope(new Uint8Array(size)));
  const wrongVersion = minimum.slice();
  wrongVersion[7] = 51;
  assert.throws(() => decodeReceiveEnvelope(wrongVersion));
  assert.throws(() => encodeReceiveEnvelope(wrapped, new Uint8Array(27)));
  assert.throws(() =>
    encodeReceiveEnvelope(wrapped, new Uint8Array(RECEIVE_ENVELOPE_MAX_BYTES - 87)),
  );
  const parsed = decodeReceiveEnvelope(minimum);
  minimum.fill(0);
  assert.deepEqual(parsed.wrappedKey, wrapped);
});

test("generating or sealing refuses to run without cryptographic randomness", async () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  Object.defineProperty(globalThis, "crypto", { configurable: true, value: undefined });
  try {
    await assert.rejects(generateReceiveKeyPair(), /cryptographically secure/);
    await assert.rejects(
      sealSubmissionKey(publicKey, slot, child, key),
      /cryptographically secure/,
    );
  } finally {
    if (descriptor) Object.defineProperty(globalThis, "crypto", descriptor);
  }
});

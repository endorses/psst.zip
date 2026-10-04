/**
 * AES-256-GCM using Web Crypto when available, or noble-ciphers on LAN HTTP.
 *
 * Keys are transported as base64url-encoded strings in URL fragments,
 * so they never leave the client or reach the server.
 */

import { gcm } from "@noble/ciphers/aes.js";

const ALGORITHM = "AES-GCM";
const KEY_LENGTH = 256;
const IV_LENGTH = 12; // 96-bit nonce recommended for AES-GCM
const TAG_LENGTH = 128; // 128-bit auth tag

// ---------------------------------------------------------------------------
// Base64url helpers (no padding, URL-safe alphabet)
// ---------------------------------------------------------------------------

export function base64urlEncode(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = "";
  for (const b of bytes) {
    binary += String.fromCharCode(b);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function base64urlDecode(str: string): ArrayBuffer {
  const padded = str.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

// ---------------------------------------------------------------------------
// Key generation & serialisation
// ---------------------------------------------------------------------------

// Raw key bytes work in both secure contexts and LAN HTTP, where browsers omit
// crypto.subtle but still expose the cryptographically secure getRandomValues.
export type EncryptionKey = Uint8Array<ArrayBuffer>;

function validateKey(key: EncryptionKey): void {
  if (key.byteLength !== KEY_LENGTH / 8) {
    throw new Error("Encryption keys must contain exactly 32 bytes");
  }
}

function randomBytes(length: number): Uint8Array<ArrayBuffer> {
  if (!globalThis.crypto?.getRandomValues) {
    throw new Error("This browser cannot generate cryptographically secure random bytes");
  }
  return globalThis.crypto.getRandomValues(new Uint8Array(length));
}

export async function generateKey(): Promise<EncryptionKey> {
  return randomBytes(KEY_LENGTH / 8);
}

export async function exportKey(key: EncryptionKey): Promise<string> {
  validateKey(key);
  return base64urlEncode(key.slice().buffer);
}

export async function importKey(encoded: string): Promise<EncryptionKey> {
  const key = new Uint8Array(base64urlDecode(encoded));
  validateKey(key);
  return key;
}

// ---------------------------------------------------------------------------
// Encrypt / Decrypt
// ---------------------------------------------------------------------------

/**
 * Encrypt plaintext bytes.
 * Returns a single ArrayBuffer: `iv (12 bytes) || ciphertext+tag`.
 */
export async function encrypt(key: EncryptionKey, plaintext: ArrayBuffer): Promise<ArrayBuffer> {
  validateKey(key);
  const iv = randomBytes(IV_LENGTH);
  const subtle = globalThis.crypto?.subtle;
  const ciphertext = subtle
    ? new Uint8Array(
        await subtle.encrypt(
          { name: ALGORITHM, iv, tagLength: TAG_LENGTH },
          await subtle.importKey("raw", key, ALGORITHM, false, ["encrypt"]),
          plaintext,
        ),
      )
    : gcm(key, iv).encrypt(new Uint8Array(plaintext));

  // Both implementations append a 16-byte authentication tag. Keep the existing
  // 12-byte nonce prefix so web and native clients can exchange the same blobs.
  const result = new Uint8Array(iv.length + ciphertext.byteLength);
  result.set(iv, 0);
  result.set(ciphertext, iv.length);
  return result.buffer;
}

/**
 * Decrypt a buffer produced by `encrypt()`.
 * Expects the first 12 bytes to be the IV and the final 16 bytes to be the tag.
 */
export async function decrypt(key: EncryptionKey, data: ArrayBuffer): Promise<ArrayBuffer> {
  validateKey(key);
  if (data.byteLength < IV_LENGTH + TAG_LENGTH / 8) {
    throw new Error("Encrypted data is too short");
  }
  const bytes = new Uint8Array(data);
  const iv = bytes.slice(0, IV_LENGTH);
  const ciphertext = bytes.slice(IV_LENGTH);
  const subtle = globalThis.crypto?.subtle;
  if (subtle) {
    return subtle.decrypt(
      { name: ALGORITHM, iv, tagLength: TAG_LENGTH },
      await subtle.importKey("raw", key, ALGORITHM, false, ["decrypt"]),
      ciphertext,
    );
  }
  return new Uint8Array(gcm(key, iv).decrypt(ciphertext)).buffer;
}

// ---------------------------------------------------------------------------
// Manifest helpers
// ---------------------------------------------------------------------------

export interface FileManifestEntry {
  name: string;
  size: number;
  mime_type: string;
  blob_id: string;
  encoding: "chunked-v1";
  chunk_size: 4194304;
  encryption_id: string;
}

export interface Manifest {
  files: FileManifestEntry[];
}

export async function encryptManifest(
  key: EncryptionKey,
  manifest: Manifest,
): Promise<ArrayBuffer> {
  const json = JSON.stringify(manifest);
  const encoded = new TextEncoder().encode(json);
  return encrypt(key, encoded.buffer);
}

export async function decryptManifest(key: EncryptionKey, data: ArrayBuffer): Promise<Manifest> {
  const plaintext = await decrypt(key, data);
  const json = new TextDecoder().decode(plaintext);
  const manifest = JSON.parse(json) as Manifest;
  if (
    !manifest ||
    !Array.isArray(manifest.files) ||
    manifest.files.length < 1 ||
    manifest.files.length > 100 ||
    new Set(
      manifest.files.map((file) =>
        typeof file?.blob_id === "string" ? file.blob_id.toLowerCase() : undefined,
      ),
    ).size !== manifest.files.length ||
    new Set(manifest.files.map((file) => file?.encryption_id)).size !== manifest.files.length ||
    manifest.files.some(
      (file) =>
        !file ||
        typeof file.name !== "string" ||
        !file.name.trim() ||
        file.name.length > 1024 ||
        /[\\/\0]/.test(file.name) ||
        typeof file.mime_type !== "string" ||
        typeof file.blob_id !== "string" ||
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(file.blob_id) ||
        !Number.isSafeInteger(file.size) ||
        file.size < 0 ||
        file.size > 1024 ** 4 ||
        file.encoding !== "chunked-v1" ||
        file.chunk_size !== 4194304 ||
        typeof file.encryption_id !== "string" ||
        !/^[0-9a-f]{32}$/.test(file.encryption_id),
    )
  )
    throw new Error("Invalid file manifest");
  return manifest;
}

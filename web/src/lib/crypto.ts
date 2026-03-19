/**
 * AES-256-GCM encryption/decryption via the Web Crypto API.
 *
 * Keys are transported as base64url-encoded strings in URL fragments,
 * so they never leave the client or reach the server.
 */

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

export async function generateKey(): Promise<CryptoKey> {
  return crypto.subtle.generateKey({ name: ALGORITHM, length: KEY_LENGTH }, true, [
    "encrypt",
    "decrypt",
  ]);
}

export async function exportKey(key: CryptoKey): Promise<string> {
  const raw = await crypto.subtle.exportKey("raw", key);
  return base64urlEncode(raw);
}

export async function importKey(encoded: string): Promise<CryptoKey> {
  const raw = base64urlDecode(encoded);
  return crypto.subtle.importKey("raw", raw, { name: ALGORITHM, length: KEY_LENGTH }, true, [
    "encrypt",
    "decrypt",
  ]);
}

// ---------------------------------------------------------------------------
// Encrypt / Decrypt
// ---------------------------------------------------------------------------

/**
 * Encrypt plaintext bytes.
 * Returns a single ArrayBuffer: `iv (12 bytes) || ciphertext+tag`.
 */
export async function encrypt(key: CryptoKey, plaintext: ArrayBuffer): Promise<ArrayBuffer> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_LENGTH));
  const ciphertext = await crypto.subtle.encrypt(
    { name: ALGORITHM, iv, tagLength: TAG_LENGTH },
    key,
    plaintext,
  );

  // Prepend IV so the receiver can extract it
  const result = new Uint8Array(iv.length + ciphertext.byteLength);
  result.set(iv, 0);
  result.set(new Uint8Array(ciphertext), iv.length);
  return result.buffer;
}

/**
 * Decrypt a buffer produced by `encrypt()`.
 * Expects the first 12 bytes to be the IV.
 */
export async function decrypt(key: CryptoKey, data: ArrayBuffer): Promise<ArrayBuffer> {
  const bytes = new Uint8Array(data);
  const iv = bytes.slice(0, IV_LENGTH);
  const ciphertext = bytes.slice(IV_LENGTH);
  return crypto.subtle.decrypt({ name: ALGORITHM, iv, tagLength: TAG_LENGTH }, key, ciphertext);
}

// ---------------------------------------------------------------------------
// Manifest helpers
// ---------------------------------------------------------------------------

export interface FileManifestEntry {
  name: string;
  size: number;
  type: string;
  fileId: string;
}

export interface Manifest {
  files: FileManifestEntry[];
}

export async function encryptManifest(key: CryptoKey, manifest: Manifest): Promise<ArrayBuffer> {
  const json = JSON.stringify(manifest);
  const encoded = new TextEncoder().encode(json);
  return encrypt(key, encoded.buffer);
}

export async function decryptManifest(key: CryptoKey, data: ArrayBuffer): Promise<Manifest> {
  const plaintext = await decrypt(key, data);
  const json = new TextDecoder().decode(plaintext);
  return JSON.parse(json) as Manifest;
}

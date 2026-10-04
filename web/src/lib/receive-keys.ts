import { base64urlDecode, base64urlEncode } from "./crypto.ts";

type StoredKey = { publicKey: string; privateKey: string };
const keyName = (owner: string, slot: string) => `psst.receive-key.v2.${owner}.${slot}`;

/** localStorage already scopes the record to this server origin; account and inbox are explicit. */
export function storeReceiveKey(
  owner: string,
  slot: string,
  pair: { publicKey: Uint8Array; privateKey: Uint8Array },
): void {
  if (pair.publicKey.length !== 32 || pair.privateKey.length !== 32)
    throw new Error("Invalid receive key");
  const record: StoredKey = {
    publicKey: base64urlEncode(new Uint8Array(pair.publicKey).buffer),
    privateKey: base64urlEncode(new Uint8Array(pair.privateKey).buffer),
  };
  const value = JSON.stringify(record);
  localStorage.setItem(keyName(owner, slot), value);
  if (localStorage.getItem(keyName(owner, slot)) !== value)
    throw new Error("Could not save the receive key on this device.");
}

export function loadReceiveKey(
  owner: string,
  slot: string,
): { publicKey: Uint8Array<ArrayBuffer>; privateKey: Uint8Array<ArrayBuffer> } | null {
  try {
    const stored = localStorage.getItem(keyName(owner, slot));
    if (stored === null || stored.length > 512) return null;
    const record = JSON.parse(stored) as StoredKey | null;
    if (!record) return null;
    const publicKey = decodeReceivePublicKey(record.publicKey);
    const privateKey = decodeReceivePublicKey(record.privateKey);
    return { publicKey, privateKey };
  } catch {
    return null;
  }
}

export function removeReceiveKey(owner: string, slot: string): void {
  localStorage.removeItem(keyName(owner, slot));
}

export function decodeReceivePublicKey(value: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]{43}$/.test(value)) throw new Error("Invalid receive public key");
  const key = new Uint8Array(base64urlDecode(value));
  if (key.length !== 32 || base64urlEncode(key.buffer) !== value)
    throw new Error("Invalid receive public key");
  return key;
}

export function parseReceiveFragment(fragment: string): {
  encoded: string;
  publicKey: Uint8Array<ArrayBuffer>;
} {
  if (!fragment.startsWith("v2."))
    throw new Error("This receive link uses an unsupported version. Ask its owner for a new link.");
  const encoded = fragment.slice(3);
  return { encoded, publicKey: decodeReceivePublicKey(encoded) };
}

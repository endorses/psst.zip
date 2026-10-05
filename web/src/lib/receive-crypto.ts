import { message as m, LocalizedError, type DisplayText } from "./i18n/index.ts";
import { CipherSuite } from "hpke";
import { KEM_DHKEM_X25519_HKDF_SHA256, KDF_HKDF_SHA256, AEAD_AES_256_GCM } from "@panva/hpke-noble";

// All three providers must work without crypto.subtle on a trusted LAN HTTP origin.
const suite = new CipherSuite(KEM_DHKEM_X25519_HKDF_SHA256, KDF_HKDF_SHA256, AEAD_AES_256_GCM);
const magic = new TextEncoder().encode("PSSTRCV2");
const domain = new TextEncoder().encode("psst.zip/receive-key/v2\0");
export const RECEIVE_ENVELOPE_MAX_BYTES = 1_048_576;
export const RECEIVE_ENVELOPE_MIN_BYTES = 116;
export const RECEIVE_WRAPPED_KEY_BYTES = 80;

function fixed(bytes: Uint8Array, size: number, name: DisplayText): Uint8Array<ArrayBuffer> {
  if (!(bytes instanceof Uint8Array) || bytes.length !== size)
    throw new LocalizedError(m("invalidValueLength", { arg0: name }));
  return new Uint8Array(bytes);
}

function randomness(): void {
  if (!globalThis.crypto?.getRandomValues)
    throw new LocalizedError(m("thisBrowserCannotGenerateCryptographicallySecureRandomBytes"));
}

function uuidBytes(id: string): Uint8Array {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id))
    throw new LocalizedError(m("invalidReceiveResourceIdentifier"));
  return Uint8Array.from(id.replace(/-/g, "").match(/../g)!, (value) => parseInt(value, 16));
}

/** Inputs are expected identities from the link and request, never trusted from an envelope. */
export function receiveContextInfo(
  slotId: string,
  submissionId: string,
  publicKey: Uint8Array,
): Uint8Array<ArrayBuffer> {
  const recipient = fixed(publicKey, 32, m("recipientPublicKey"));
  const context = new Uint8Array(domain.length + 6 + 16 + 16 + 32);
  context.set(domain);
  context.set([0, 32, 0, 1, 0, 2], domain.length);
  context.set(uuidBytes(slotId), domain.length + 6);
  context.set(uuidBytes(submissionId), domain.length + 22);
  context.set(recipient, domain.length + 38);
  return context;
}

export async function generateReceiveKeyPair(): Promise<{
  privateKey: Uint8Array<ArrayBuffer>;
  publicKey: Uint8Array<ArrayBuffer>;
}> {
  randomness();
  const pair = await suite.GenerateKeyPair(true);
  return {
    privateKey: fixed(
      await suite.SerializePrivateKey(pair.privateKey),
      32,
      m("recipientPrivateKey"),
    ),
    publicKey: fixed(await suite.SerializePublicKey(pair.publicKey), 32, m("recipientPublicKey")),
  };
}

/** Fresh one-shot HPKE Base context; exactly enc[32] || ciphertext-and-tag[48], no Tink prefix. */
export async function sealSubmissionKey(
  publicKey: Uint8Array,
  slotId: string,
  submissionId: string,
  key: Uint8Array,
): Promise<Uint8Array<ArrayBuffer>> {
  randomness();
  const recipient = fixed(publicKey, 32, m("recipientPublicKey"));
  const plaintext = fixed(key, 32, m("submissionKey"));
  const info = receiveContextInfo(slotId, submissionId, recipient);
  try {
    const { encapsulatedSecret, ciphertext } = await suite.Seal(
      await suite.DeserializePublicKey(recipient),
      plaintext,
      { info },
    );
    const wrapped = new Uint8Array(RECEIVE_WRAPPED_KEY_BYTES);
    wrapped.set(fixed(encapsulatedSecret, 32, "encapsulation"));
    wrapped.set(fixed(ciphertext, 48, m("sealedSubmissionKey")), 32);
    return wrapped;
  } finally {
    plaintext.fill(0);
  }
}

export async function openSubmissionKey(
  privateKey: Uint8Array,
  publicKey: Uint8Array,
  slotId: string,
  submissionId: string,
  wrappedKey: Uint8Array,
): Promise<Uint8Array<ArrayBuffer>> {
  const secret = fixed(privateKey, 32, m("recipientPrivateKey"));
  const wrapped = fixed(wrappedKey, RECEIVE_WRAPPED_KEY_BYTES, m("wrappedSubmissionKey"));
  const info = receiveContextInfo(slotId, submissionId, publicKey);
  try {
    return fixed(
      await suite.Open(
        await suite.DeserializePrivateKey(secret),
        wrapped.slice(0, 32),
        wrapped.slice(32),
        { info },
      ),
      32,
      m("submissionKey"),
    );
  } finally {
    secret.fill(0);
  }
}

export function encodeReceiveEnvelope(
  wrappedKey: Uint8Array,
  encryptedManifest: Uint8Array,
): Uint8Array<ArrayBuffer> {
  const wrapped = fixed(wrappedKey, RECEIVE_WRAPPED_KEY_BYTES, m("wrappedSubmissionKey"));
  if (
    !(encryptedManifest instanceof Uint8Array) ||
    encryptedManifest.length < 28 ||
    encryptedManifest.length > RECEIVE_ENVELOPE_MAX_BYTES - 88
  )
    throw new LocalizedError(m("invalidEncryptedReceiveManifestLength"));
  const envelope = new Uint8Array(88 + encryptedManifest.length);
  envelope.set(magic);
  envelope.set(wrapped, 8);
  envelope.set(encryptedManifest, 88);
  return envelope;
}

/** Parsing only: HPKE open and AES manifest authentication must both succeed before use. */
export function decodeReceiveEnvelope(envelope: Uint8Array): {
  wrappedKey: Uint8Array<ArrayBuffer>;
  encryptedManifest: Uint8Array<ArrayBuffer>;
} {
  if (
    !(envelope instanceof Uint8Array) ||
    envelope.length < RECEIVE_ENVELOPE_MIN_BYTES ||
    envelope.length > RECEIVE_ENVELOPE_MAX_BYTES ||
    magic.some((byte, index) => envelope[index] !== byte)
  )
    throw new LocalizedError(m("invalidOrUnsupportedReceiveEnvelope"));
  return {
    wrappedKey: new Uint8Array(envelope.subarray(8, 88)),
    encryptedManifest: new Uint8Array(envelope.subarray(88)),
  };
}

import { message as m, LocalizedError } from "./i18n/index.ts";
import { encrypt, decrypt, type EncryptionKey, type FileManifestEntry } from "./crypto.ts";
export const FILE_CHUNK_SIZE = 4194304;
export const FRAME_OVERHEAD = 60;
export function wireSize(size: number): number {
  if (!Number.isSafeInteger(size) || size < 0 || size > 1024 ** 4)
    throw new LocalizedError(m("invalidFileSize"));
  const value = size + Math.max(1, Math.ceil(size / FILE_CHUNK_SIZE)) * FRAME_OVERHEAD;
  if (!Number.isSafeInteger(value)) throw new LocalizedError(m("invalidFileSize"));
  return value;
}
export function newEncryptionId(): string {
  return [...crypto.getRandomValues(new Uint8Array(16))]
    .map((n) => n.toString(16).padStart(2, "0"))
    .join("");
}
function context(id: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/.test(id)) throw new LocalizedError(m("invalidFileEncryptionContext"));
  return Uint8Array.from(id.match(/../g)!, (v) => parseInt(v, 16));
}
export async function encryptFileFrame(
  key: EncryptionKey,
  id: string,
  index: number,
  total: number,
  data: ArrayBuffer,
): Promise<ArrayBuffer> {
  const header = new Uint8Array(32 + data.byteLength);
  header.set(context(id));
  const view = new DataView(header.buffer);
  view.setBigUint64(16, BigInt(index));
  view.setBigUint64(24, BigInt(total));
  header.set(new Uint8Array(data), 32);
  return encrypt(key, header.buffer);
}
export async function* encryptFile(
  key: EncryptionKey,
  file: Blob,
  id: string,
  signal?: AbortSignal,
): AsyncGenerator<ArrayBuffer> {
  const count = Math.max(1, Math.ceil(file.size / FILE_CHUNK_SIZE));
  for (let index = 0; index < count; index++) {
    signal?.throwIfAborted();
    const data = await file
      .slice(index * FILE_CHUNK_SIZE, Math.min(file.size, (index + 1) * FILE_CHUNK_SIZE))
      .arrayBuffer();
    signal?.throwIfAborted();
    yield await encryptFileFrame(key, id, index, file.size, data);
  }
}
export async function* decryptFileStream(
  key: EncryptionKey,
  file: FileManifestEntry,
  stream: ReadableStream<Uint8Array>,
  signal?: AbortSignal,
): AsyncGenerator<Uint8Array<ArrayBuffer>> {
  if (file.encoding !== "chunked-v1" || file.chunk_size !== FILE_CHUNK_SIZE)
    throw new LocalizedError(m("unsupportedFileEncryptionFormat"));
  const expectedContext = context(file.encryption_id);
  wireSize(file.size);
  const reader = stream.getReader();
  let pending: Uint8Array = new Uint8Array(),
    offset = 0;
  async function readExact(length: number) {
    const result = new Uint8Array(length);
    let written = 0;
    while (written < length) {
      signal?.throwIfAborted();
      if (offset === pending.length) {
        const next = await reader.read();
        if (next.done) throw new LocalizedError(m("encryptedFileIsIncomplete"));
        pending = next.value;
        offset = 0;
      }
      const amount = Math.min(length - written, pending.length - offset);
      result.set(pending.subarray(offset, offset + amount), written);
      offset += amount;
      written += amount;
    }
    return result;
  }
  try {
    const count = Math.max(1, Math.ceil(file.size / FILE_CHUNK_SIZE));
    for (let index = 0; index < count; index++) {
      const size = Math.min(FILE_CHUNK_SIZE, file.size - index * FILE_CHUNK_SIZE);
      const frame = await readExact(size + FRAME_OVERHEAD);
      const plain = new Uint8Array(await decrypt(key, frame.buffer));
      const view = new DataView(plain.buffer);
      if (
        plain.length !== size + 32 ||
        expectedContext.some((byte, i) => byte !== plain[i]) ||
        view.getBigUint64(16) !== BigInt(index) ||
        view.getBigUint64(24) !== BigInt(file.size)
      )
        throw new LocalizedError(m("encryptedFileVerificationFailed"));
      signal?.throwIfAborted();
      yield plain.slice(32);
    }
    if (offset !== pending.length || !(await reader.read()).done)
      throw new LocalizedError(m("encryptedFileHasUnexpectedTrailingData"));
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
}

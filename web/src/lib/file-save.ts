import type { FileManifestEntry } from "./crypto";
import { MAX_BUFFERED_BYTES } from "./limits";
import { safeFilename } from "./filenames";
export const LARGE_SAVE_MESSAGE =
  "Large files need HTTPS with a browser that supports saving to disk, or the psst.zip mobile app.";
interface SaveSink {
  write(chunk: Uint8Array<ArrayBuffer>): Promise<void>;
  close(): Promise<void>;
  abort(): Promise<void>;
}
function handoff(blob: Blob, name: string, cleanup = async () => {}) {
  const url = URL.createObjectURL(blob),
    anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = safeFilename(name);
  anchor.click();
  // Let the download consumer acquire its file-backed Blob before removing it.
  setTimeout(() => {
    URL.revokeObjectURL(url);
    void cleanup().catch(() => {});
  }, 60000);
}
/** Called directly from a user gesture before fetching/decrypting. */
export async function createSaveSink(file: FileManifestEntry): Promise<SaveSink> {
  if (file.size <= MAX_BUFFERED_BYTES) {
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let bytes = 0;
    return {
      async write(chunk) {
        bytes += chunk.length;
        if (bytes > file.size || bytes > MAX_BUFFERED_BYTES)
          throw new Error("Unexpected file size");
        chunks.push(chunk);
      },
      async close() {
        if (bytes !== file.size) throw new Error("Incomplete file");
        handoff(new Blob(chunks, { type: "application/octet-stream" }), file.name);
        chunks.length = 0;
      },
      async abort() {
        chunks.length = 0;
      },
    };
  }
  const browser = window as Window & {
    showSaveFilePicker?: (options: { suggestedName: string }) => Promise<FileSystemFileHandle>;
  };
  if (window.isSecureContext && browser.showSaveFilePicker) {
    const handle = await browser.showSaveFilePicker({ suggestedName: safeFilename(file.name) });
    const writer = await handle.createWritable();
    return {
      write: (chunk) => writer.write(chunk),
      close: () => writer.close(),
      abort: () => writer.abort(),
    };
  }
  if (window.isSecureContext && navigator.storage?.getDirectory) {
    const root = await navigator.storage.getDirectory();
    const directory = await root.getDirectoryHandle("psst-downloads", { create: true });
    const name = `${Date.now()}-${crypto.randomUUID()}`;
    const handle = await directory.getFileHandle(name, { create: true });
    let writer: FileSystemWritableFileStream;
    try {
      writer = await handle.createWritable();
    } catch (cause) {
      await directory.removeEntry(name);
      throw cause;
    }
    const cleanup = () => directory.removeEntry(name);
    return {
      write: (chunk) => writer.write(chunk),
      async close() {
        await writer.close();
        handoff(await handle.getFile(), file.name, cleanup);
      },
      async abort() {
        try {
          await writer.abort();
        } finally {
          await cleanup();
        }
      },
    };
  }
  throw new Error(LARGE_SAVE_MESSAGE);
}
/** Remove abandoned OPFS temporary files after crashes; never touch other origin data. */
export async function cleanAbandonedDownloads(): Promise<void> {
  if (!window.isSecureContext || !navigator.storage?.getDirectory) return;
  try {
    const directory = await (
      await navigator.storage.getDirectory()
    ).getDirectoryHandle("psst-downloads");
    const iterable = directory as FileSystemDirectoryHandle & {
      keys(): AsyncIterableIterator<string>;
    };
    for await (const name of iterable.keys()) {
      const timestamp = Number(name.split("-")[0]);
      if (Number.isFinite(timestamp) && timestamp < Date.now() - 24 * 60 * 60 * 1000)
        await directory.removeEntry(name);
    }
  } catch {
    /* Storage may be unavailable; saving reports its own actionable error. */
  }
}

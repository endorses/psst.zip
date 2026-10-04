import type { FileManifestEntry } from "./crypto";
import { MAX_BUFFERED_BYTES, assertFileSize } from "./limits.ts";
import { safeFilename } from "./filenames.ts";
import { checkBrowserStorage, ReceiveStorageError } from "./recipient-policy.ts";
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
  try {
    anchor.click();
  } catch (cause) {
    URL.revokeObjectURL(url);
    void cleanup().catch(() => {});
    throw cause;
  }
  // Let the download consumer acquire its file-backed Blob before removing it.
  setTimeout(() => {
    URL.revokeObjectURL(url);
    void cleanup().catch(() => {});
  }, 60000);
}
/** Called directly from a user gesture before fetching/decrypting. */
export async function createSaveSink(
  file: FileManifestEntry,
  signal?: AbortSignal,
): Promise<SaveSink> {
  assertFileSize(file.size);
  signal?.throwIfAborted();
  const bounded = (sink: SaveSink): SaveSink => {
    let written = 0;
    return {
      async write(chunk) {
        signal?.throwIfAborted();
        if (chunk.byteLength > file.size - written) throw new Error("Unexpected file size");
        try {
          await sink.write(chunk);
        } catch (cause) {
          if (cause instanceof DOMException && cause.name === "QuotaExceededError")
            throw new ReceiveStorageError(
              "Not enough storage to save this file. Free some space and retry, or use the psst.zip mobile app.",
            );
          throw cause;
        }
        written += chunk.byteLength;
      },
      async close() {
        signal?.throwIfAborted();
        if (written !== file.size) throw new Error("Incomplete file");
        await sink.close();
      },
      abort: () => sink.abort(),
    };
  };
  if (file.size <= MAX_BUFFERED_BYTES) {
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let bytes = 0;
    return bounded({
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
    });
  }
  const browser = window as Window & {
    showSaveFilePicker?: (options: { suggestedName: string }) => Promise<FileSystemFileHandle>;
  };
  if (window.isSecureContext && browser.showSaveFilePicker) {
    const handle = await browser.showSaveFilePicker({ suggestedName: safeFilename(file.name) });
    signal?.throwIfAborted();
    const writer = await handle.createWritable();
    return bounded({
      write: (chunk) => writer.write(chunk),
      close: () => writer.close(),
      abort: () => writer.abort(),
    });
  }
  if (window.isSecureContext && navigator.storage?.getDirectory) {
    let initialUsage: number | undefined;
    const checkSpace = async (remaining: number) => {
      const timeout = AbortSignal.timeout(10_000);
      const deadline = signal ? AbortSignal.any([signal, timeout]) : timeout;
      let aborted: (() => void) | undefined;
      try {
        deadline.throwIfAborted();
        const cancelled = new Promise<never>((_, reject) => {
          aborted = () => reject(deadline.reason);
          deadline.addEventListener("abort", aborted, { once: true });
        });
        if (!navigator.storage.estimate) throw new Error("Storage estimate unavailable");
        const estimate = await Promise.race([navigator.storage.estimate(), cancelled]);
        // Estimates may exclude uncommitted writable staging. Validate the raw
        // values first, then conservatively charge our own staged bytes too.
        checkBrowserStorage(estimate, remaining, file.size);
        const usage = Math.max(
          estimate.usage!,
          (initialUsage ?? estimate.usage!) + file.size - remaining,
        );
        checkBrowserStorage({ ...estimate, usage }, remaining, file.size);
        initialUsage ??= estimate.usage;
      } catch (cause) {
        signal?.throwIfAborted();
        if (cause instanceof ReceiveStorageError) throw cause;
        throw new ReceiveStorageError(
          "Could not check browser storage. Use a browser with a save-file picker or the psst.zip mobile app.",
        );
      } finally {
        if (aborted) deadline.removeEventListener("abort", aborted);
      }
    };
    await checkSpace(file.size);
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
    let written = 0,
      checkedAt = 0;
    return bounded({
      async write(chunk) {
        if (written - checkedAt >= 64 * 1024 * 1024) {
          await checkSpace(file.size - written);
          checkedAt = written;
        }
        await writer.write(chunk);
        written += chunk.byteLength;
      },
      async close() {
        await checkSpace(0);
        await writer.close();
        signal?.throwIfAborted();
        const blob = await handle.getFile();
        signal?.throwIfAborted();
        handoff(blob, file.name, cleanup);
      },
      async abort() {
        try {
          await writer.abort();
        } finally {
          await cleanup();
        }
      },
    });
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

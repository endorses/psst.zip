import { encryptFile, wireSize } from "./chunked-files.ts";
import type { EncryptionKey } from "./crypto.ts";

/** One authenticated frame in memory; reconcile a lost PATCH response with tus HEAD. */
export async function uploadEncryptedFile(options: {
  key: EncryptionKey;
  file: Blob;
  encryptionId: string;
  endpoint: string;
  token?: string;
  signal: AbortSignal;
  onProgress: (bytes: number) => void;
}): Promise<string> {
  const { signal } = options;
  const headers: Record<string, string> = {
    "Tus-Resumable": "1.0.0",
    ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
  };
  const creation = await fetch(options.endpoint, {
    method: "POST",
    headers: { ...headers, "Upload-Length": String(wireSize(options.file.size)) },
    signal,
  });
  if (!creation.ok) throw new Error("Could not create file upload");
  const location = creation.headers.get("Location");
  if (!location) throw new Error("Missing file upload location");
  const url = new URL(location, window.location.origin);
  const endpoint = new URL(options.endpoint, window.location.origin);
  if (
    url.origin !== endpoint.origin ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !url.pathname.startsWith(endpoint.pathname + "/")
  )
    throw new Error("Invalid file upload location");
  const id = url.pathname.split("/").pop()!;
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id))
    throw new Error("Invalid file upload location");
  if (url.pathname !== endpoint.pathname + "/" + id)
    throw new Error("Invalid file upload location");
  let offset = 0;
  for await (const frame of encryptFile(options.key, options.file, options.encryptionId, signal)) {
    const start = offset,
      end = start + frame.byteLength;
    let failures = 0;
    while (offset < end) {
      signal.throwIfAborted();
      try {
        const baseOffset = offset;
        const next = await patchFrame(
          url,
          {
            ...headers,
            "Content-Type": "application/offset+octet-stream",
            "Upload-Offset": String(offset),
          },
          frame.slice(offset - start),
          signal,
          (sent) => options.onProgress(baseOffset + sent),
        );
        if (next !== end) throw new Error("Unexpected upload offset");
        offset = next;
        options.onProgress(offset);
      } catch (cause) {
        signal.throwIfAborted();
        if (++failures > 3) throw cause;
        const head = await fetch(url, { method: "HEAD", headers, signal, cache: "no-store" });
        const text = head.headers.get("Upload-Offset"),
          next = Number(text);
        if (!head.ok || text === null || !Number.isSafeInteger(next) || next < offset || next > end)
          throw new Error("Could not resume this upload");
        offset = next;
        options.onProgress(offset);
      }
    }
  }
  return id;
}

function patchFrame(
  url: URL,
  headers: Record<string, string>,
  body: ArrayBuffer,
  signal: AbortSignal,
  progress: (bytes: number) => void,
): Promise<number> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const finish = () => signal.removeEventListener("abort", abort);
    xhr.open("PATCH", url.toString());
    Object.entries(headers).forEach(([key, value]) => xhr.setRequestHeader(key, value));
    xhr.upload.onprogress = (event) => progress(Math.min(body.byteLength, event.loaded));
    xhr.onload = () => {
      finish();
      const offset = xhr.getResponseHeader("Upload-Offset");
      if (xhr.status < 200 || xhr.status >= 300 || offset === null)
        reject(new Error("Upload interrupted"));
      else resolve(Number(offset));
    };
    xhr.onerror = () => {
      finish();
      reject(new Error("Upload interrupted"));
    };
    xhr.onabort = () => {
      finish();
      reject(new DOMException("Upload stopped", "AbortError"));
    };
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) {
      finish();
      reject(new DOMException("Upload stopped", "AbortError"));
      return;
    }
    xhr.send(body);
  });
}

import { encryptFile, wireSize } from "./chunked-files.ts";
import type { EncryptionKey } from "./crypto.ts";
import { ResourceLimitError, resourceLimitError } from "./resource-policy.ts";
import { TransferStateError, transferStateError, detectPublicPause } from "./incident-state.ts";

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
    credentials: options.token ? "omit" : "same-origin",
  });
  if (!creation.ok) {
    const error = await creation.json().catch(() => null);
    const code = error?.code ?? creation.headers.get("X-Psst-Error-Code");
    const policyError = transferStateError(code) ?? resourceLimitError(code);
    if (policyError) throw policyError;
    if (error?.code === "receive_file_limit")
      throw new Error(
        "This receive link has no file allocations left. Ask its owner for a new link.",
      );
    throw new Error("Could not create file upload");
  }
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
        if (cause instanceof ResourceLimitError || cause instanceof TransferStateError) throw cause;
        const paused = await detectPublicPause(signal);
        signal.throwIfAborted();
        if (paused) throw paused;
        if (++failures > 3) throw cause;
        const head = await fetch(url, {
          method: "HEAD",
          headers,
          signal,
          cache: "no-store",
          credentials: options.token ? "omit" : "same-origin",
        });
        const headCode = head.headers.get("X-Psst-Error-Code");
        const stopped = transferStateError(headCode) ?? resourceLimitError(headCode);
        if (stopped) throw stopped;
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
  if (headers.Authorization)
    return fetch(url, { method: "PATCH", headers, body, signal, credentials: "omit" }).then(
      async (response) => {
        if (!response.ok) {
          const error = await response.json().catch(() => null);
          const code = error?.code ?? response.headers.get("X-Psst-Error-Code");
          const policyError = transferStateError(code) ?? resourceLimitError(code);
          if (policyError) throw policyError;
        }
        const offset = response.headers.get("Upload-Offset");
        if (!response.ok || offset === null) throw new Error("Upload interrupted");
        progress(body.byteLength);
        return Number(offset);
      },
    );
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const finish = () => signal.removeEventListener("abort", abort);
    xhr.open("PATCH", url.toString());
    Object.entries(headers).forEach(([key, value]) => xhr.setRequestHeader(key, value));
    xhr.upload.onprogress = (event) => progress(Math.min(body.byteLength, event.loaded));
    xhr.onload = () => {
      finish();
      if (xhr.status < 200 || xhr.status >= 300) {
        try {
          let code = xhr.getResponseHeader("X-Psst-Error-Code");
          try {
            code = JSON.parse(xhr.responseText).code ?? code;
          } catch {
            /* HEAD/proxy responses may carry only the stable header. */
          }
          const policyError = transferStateError(code) ?? resourceLimitError(code);
          if (policyError) {
            reject(policyError);
            return;
          }
        } catch {
          /* Generic status handling below. */
        }
      }
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

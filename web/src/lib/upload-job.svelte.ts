import * as tus from "tus-js-client";
import {
  generateKey,
  exportKey,
  importKey,
  encrypt,
  encryptManifest,
  type FileManifestEntry,
} from "./crypto";
import { assertFileSize } from "./limits";
export function formatSize(bytes: number) {
  if (!bytes) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"];
  const i = Math.min(3, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${(bytes / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
}
export class UploadJob {
  files = $state<File[]>([]);
  state = $state<"idle" | "preparing" | "uploading" | "stopping" | "done" | "error">("idle");
  error = $state("");
  sent = $state(0);
  total = $state(0);
  current = $state("");
  url = $state("");
  transferId = $state("");
  private controller: AbortController | null = null;
  private upload: tus.Upload | null = null;
  private token = "";
  private run = 0;
  get active() {
    return this.state === "preparing" || this.state === "uploading" || this.state === "stopping";
  }
  add(files: File[]) {
    try {
      files.forEach((f) => assertFileSize(f.size));
      this.files = [...this.files, ...files];
      this.error = "";
    } catch {
      this.error = "Files must be no larger than 25 MiB. Choose a smaller file.";
    }
  }
  async cancel() {
    this.run++;
    this.state = "stopping";
    this.controller?.abort();
    await this.upload?.abort();
    this.upload = null;
    await this.cleanup();
    this.state = "idle";
  }
  private async cleanup() {
    if (!this.transferId) return;
    try {
      const response = await fetch(`/api/v1/transfers/${this.transferId}`, {
        method: "DELETE",
        credentials: "omit",
        headers: { Authorization: `Bearer ${this.token}` },
        signal: AbortSignal.timeout(10000),
      });
      if (!response.ok && response.status !== 404) throw Error();
      this.transferId = "";
      this.error = "Upload stopped. Partial server files removed.";
    } catch {
      this.error =
        "Upload stopped, but partial files could not be removed. Retry cleanup or revoke the transfer from History.";
    }
  }
  async retryCleanup() {
    await this.cleanup();
  }
  async start(options: {
    accountId?: string;
    slotId?: string;
    key?: string;
    oncreated?: (id: string, url: string, title: string, size: number) => void;
  }) {
    if (this.active || !this.files.length) return;
    if (this.transferId) {
      await this.cleanup();
      if (this.transferId) return;
    }
    const run = ++this.run,
      files = [...this.files];
    this.controller = new AbortController();
    const signal = this.controller.signal;
    const check = () => {
      if (run !== this.run || signal.aborted) throw new DOMException("Stopped", "AbortError");
    };
    const request = async (path: string, init: RequestInit = {}) => {
      check();
      const res = await fetch(`/api/v1${path}`, { ...init, signal });
      check();
      if (!res.ok)
        throw new Error(
          res.status === 429
            ? "This server is busy. Wait a moment, then retry upload."
            : res.status === 401
              ? "Sign in again to continue."
              : res.status === 404 || res.status === 410
                ? "This link is no longer available. Ask for a new link."
                : "The server could not finish the upload. Check your connection and retry.",
        );
      return res;
    };
    this.state = "preparing";
    this.error = "";
    this.sent = 0;
    this.total = files.reduce((n, f) => n + f.size + 28, 0);
    try {
      if (options.accountId) {
        const me = await (await request("/auth/me")).json();
        check();
        if (me.user.id !== options.accountId)
          throw Error("Your account changed. Sign in again before sending files.");
      }
      const key = options.key ? await importKey(options.key) : await generateKey();
      check();
      const keyString = await exportKey(key);
      check();
      const created = await (
        await request(options.slotId ? `/slots/${options.slotId}/transfers` : "/transfers", {
          method: "POST",
        })
      ).json();
      check();
      this.transferId = created.id;
      this.token = created.delete_token ?? "";
      const url = `${location.origin}/d/${created.id}#${keyString}`;
      options.oncreated?.(
        created.id,
        url,
        files[0].name + (files.length > 1 ? ` + ${files.length - 1}` : ""),
        files.reduce((n, f) => n + f.size, 0),
      );
      const entries: FileManifestEntry[] = [];
      let completed = 0;
      for (const file of files) {
        check();
        this.state = "preparing";
        this.current = file.name;
        const plain = await file.arrayBuffer();
        check();
        const encrypted = await encrypt(key, plain);
        check();
        this.state = "uploading";
        const id = await new Promise<string>((resolve, reject) => {
          const upload = new tus.Upload(new Blob([encrypted]), {
            endpoint: `/api/v1/transfers/${created.id}/files`,
            headers: options.slotId ? { Authorization: `Bearer ${this.token}` } : {},
            chunkSize: 1024 * 1024,
            retryDelays: [0, 1000, 3000, 5000],
            onProgress: (sent) => {
              if (run === this.run) this.sent = completed + sent;
            },
            onError: reject,
            onSuccess: () => resolve(upload.url?.split("/").pop() ?? ""),
          });
          this.upload = upload;
          signal.addEventListener(
            "abort",
            () => reject(new DOMException("Stopped", "AbortError")),
            { once: true },
          );
          upload.start();
        });
        check();
        completed += encrypted.byteLength;
        this.sent = completed;
        entries.push({
          name: file.name,
          size: file.size,
          mime_type: file.type || "application/octet-stream",
          blob_id: id,
        });
      }
      const manifest = await encryptManifest(key, { files: entries });
      check();
      const headers: Record<string, string> = options.slotId
        ? { Authorization: `Bearer ${this.token}` }
        : {};
      await request(`/transfers/${created.id}/manifest`, {
        method: "POST",
        headers,
        body: manifest,
      });
      await request(`/transfers/${created.id}/complete`, { method: "POST", headers });
      check();
      this.url = url;
      this.state = "done";
      this.transferId = "";
      this.upload = null;
    } catch (error) {
      if (run !== this.run) return;
      this.state = "error";
      const safeErrors = [
        "This server is busy. Wait a moment, then retry upload.",
        "Sign in again to continue.",
        "This link is no longer available. Ask for a new link.",
        "The server could not finish the upload. Check your connection and retry.",
        "Your account changed. Sign in again before sending files.",
      ];
      this.error =
        error instanceof Error && safeErrors.includes(error.message)
          ? error.message
          : "Upload interrupted. Check your connection, then retry upload. You can also remove partial files with Retry cleanup.";
    }
  }
}

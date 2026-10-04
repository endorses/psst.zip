import { uploadEncryptedFile } from "./stream-upload";
import { newEncryptionId, wireSize, FILE_CHUNK_SIZE } from "./chunked-files";
import {
  generateKey,
  exportKey,
  importKey,
  encryptManifest,
  type FileManifestEntry,
} from "./crypto";
import { assertFileSize, loadUploadLimit } from "./limits";
export function formatSize(bytes: number) {
  if (!bytes) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"];
  const i = Math.min(3, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${(bytes / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
}
export class UploadJob {
  files = $state<File[]>([]);
  limit = $state<number | null>(null);
  async refreshLimit() {
    try {
      this.limit = await loadUploadLimit();
    } catch (cause) {
      this.limit = null;
      this.error = cause instanceof Error ? cause.message : "Could not load the file limit.";
    }
  }
  state = $state<"idle" | "preparing" | "uploading" | "stopping" | "done" | "error">("idle");
  error = $state("");
  sent = $state(0);
  total = $state(0);
  current = $state("");
  url = $state("");
  transferId = $state("");
  private controller: AbortController | null = null;
  private token = "";
  private run = 0;
  get active() {
    return this.state === "preparing" || this.state === "uploading" || this.state === "stopping";
  }
  async add(files: File[]) {
    try {
      this.limit = await loadUploadLimit();
      if (this.active) return;
      if (this.files.length + files.length > 100)
        throw new Error("Choose no more than 100 files per transfer.");
      files.forEach((f) => assertFileSize(f.size, this.limit!));
      this.files = [...this.files, ...files];
      this.error = "";
    } catch (cause) {
      this.error =
        cause instanceof Error ? cause.message : "Could not check this server’s file limit.";
    }
  }
  async cancel() {
    this.run++;
    this.state = "stopping";
    this.controller?.abort();
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
      if (!res.ok && res.status === 403) {
        const body = await res
          .clone()
          .json()
          .catch(() => null);
        if (body?.code === "password_change_required")
          throw new Error("Change your temporary password before sending files.");
        if (body?.code === "admin_transfer_forbidden")
          throw new Error("Administrator accounts cannot transfer files. Use a regular account.");
      }
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
    this.total = files.reduce((n, f) => n + wireSize(f.size), 0);
    try {
      if (files.length > 100) throw new Error("Choose no more than 100 files per transfer.");
      this.limit = await loadUploadLimit(signal);
      check();
      files.forEach((file) => assertFileSize(file.size, this.limit!));
      if (options.accountId) {
        const me = await (await request("/auth/me")).json();
        check();
        if (me.user.must_change_password)
          throw new Error("Change your temporary password before sending files.");
        if (me.user.role === "admin")
          throw new Error("Administrator accounts cannot transfer files. Use a regular account.");
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
        files[0].name +
          (files.length > 1
            ? ` + ${files.length - 1} ${files.length === 2 ? "file" : "files"}`
            : ""),
        files.reduce((n, f) => n + f.size, 0),
      );
      const entries: FileManifestEntry[] = [];
      let completed = 0;
      for (const file of files) {
        check();
        this.state = "preparing";
        this.current = file.name;
        const encryptionId = newEncryptionId();
        this.state = "uploading";
        const id = await uploadEncryptedFile({
          key,
          file,
          encryptionId,
          endpoint: `/api/v1/transfers/${created.id}/files`,
          token: options.slotId ? this.token : undefined,
          signal,
          onProgress: (bytes) => {
            if (run === this.run) this.sent = completed + bytes;
          },
        });
        check();
        completed += wireSize(file.size);
        this.sent = completed;
        entries.push({
          name: file.name,
          size: file.size,
          mime_type: file.type || "application/octet-stream",
          blob_id: id,
          encoding: "chunked-v1",
          chunk_size: FILE_CHUNK_SIZE,
          encryption_id: encryptionId,
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
    } catch (error) {
      if (run !== this.run) return;
      this.state = "error";
      const safeErrors = [
        "Change your temporary password before sending files.",
        "Administrator accounts cannot transfer files. Use a regular account.",
        "Choose no more than 100 files per transfer.",
        "This server is busy. Wait a moment, then retry upload.",
        "Sign in again to continue.",
        "This link is no longer available. Ask for a new link.",
        "The server could not finish the upload. Check your connection and retry.",
        "Your account changed. Sign in again before sending files.",
      ];
      this.error =
        error instanceof Error &&
        (safeErrors.includes(error.message) ||
          /^Files must be no larger than [0-9.]+ MiB\.$/.test(error.message) ||
          error.message ===
            "Could not load this server's file limit. Check your connection and retry.")
          ? error.message
          : "Upload interrupted. Check your connection, then retry upload. You can also remove partial files with Retry cleanup.";
    }
  }
}

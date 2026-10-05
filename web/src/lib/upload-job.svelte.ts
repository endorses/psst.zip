import {
  message as m,
  number,
  LocalizedError,
  errorText,
  translate,
  type DisplayText,
} from "./i18n/index.ts";
import {
  TrafficLimitError,
  trafficLimitError,
  detectTransferStop,
  type TrafficPolicy,
} from "./traffic-policy";
import { uploadEncryptedFile } from "./stream-upload";
import { newEncryptionId, wireSize } from "./chunked-files";
import { generateKey, exportKey, encryptManifest, type FileManifestEntry } from "./crypto";
import { FileSizeError, assertFileSize, loadUploadLimit, loadServerLimits } from "./limits";
import { ResourceLimitError, resourceLimitError, type ResourcePolicy } from "./resource-policy";
import { getSlotAvailability } from "./api";
import { parseReceiveFragment } from "./receive-keys";
import { sealSubmissionKey, encodeReceiveEnvelope } from "./receive-crypto";
import { validateLinkLimit } from "./link-limits";
import { normalizeLinkTitle } from "./link-title";
import { TransferStateError, transferStateError } from "./incident-state";
import {
  GuestCapacityError,
  CAPACITY_UNAVAILABLE,
  assertGuestKey,
  assertGuestFresh,
  assertGuestSelection,
  selectionWireSize,
  fileManifestEntry,
  type SlotAvailability,
} from "./guest-capacity";
export function formatSize(bytes: number) {
  if (!bytes) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"];
  const i = Math.min(3, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${number(bytes / 1024 ** i, { minimumFractionDigits: i ? 1 : 0, maximumFractionDigits: i ? 1 : 0 })} ${units[i]}`;
}
export class UploadJob {
  constructor(private readonly guest?: { slotId: string; key: string }) {}
  files = $state<File[]>([]);
  limit = $state<number | null>(null);
  trafficPolicy = $state<TrafficPolicy | null>(null);
  resourcePolicy = $state<ResourcePolicy | null>(null);
  availability = $state<SlotAvailability | null>(null);
  availabilityStale = $state(true);
  checking = $state(false);
  private policyQueue: Promise<void> = Promise.resolve();
  private pendingChecks = 0;
  private disposed = false;
  private policyController = new AbortController();
  get guestReady() {
    if (!this.guest) return true;
    if (this.checking || this.availabilityStale || !this.availability || this.limit === null)
      return false;
    try {
      assertGuestSelection(this.availability, this.files, this.limit);
      return true;
    } catch {
      return false;
    }
  }
  private queuePolicy(action: () => Promise<void>): Promise<void> {
    this.pendingChecks++;
    this.checking = true;
    const next = this.policyQueue
      .then(async () => {
        if (this.disposed || this.active) return;
        try {
          await action();
        } catch (cause) {
          if (!this.disposed)
            this.error =
              this.guest &&
              !(cause instanceof GuestCapacityError) &&
              !(cause instanceof FileSizeError)
                ? CAPACITY_UNAVAILABLE
                : cause instanceof Error
                  ? errorText(cause)
                  : CAPACITY_UNAVAILABLE;
        }
      })
      .finally(() => {
        this.pendingChecks--;
        this.checking = this.pendingChecks > 0;
      });
    this.policyQueue = next;
    return next;
  }
  private async readGuestPolicy(guest: { slotId: string; key: string }, signal: AbortSignal) {
    this.availabilityStale = true;
    try {
      const [limits, availability] = await Promise.all([
        loadServerLimits(signal),
        getSlotAvailability(guest.slotId, signal),
      ]);
      signal.throwIfAborted();
      assertGuestKey(availability, parseReceiveFragment(guest.key).encoded);
      assertGuestFresh(availability);
      this.limit = limits.max_file_size;
      this.trafficPolicy = limits.traffic_policy ?? null;
      this.availability = availability;
      this.availabilityStale = false;
      return availability;
    } catch (cause) {
      if (
        cause instanceof GuestCapacityError ||
        cause instanceof TrafficLimitError ||
        cause instanceof TransferStateError ||
        cause instanceof ResourceLimitError
      )
        throw cause;
      throw new GuestCapacityError(CAPACITY_UNAVAILABLE);
    }
  }
  async refreshLimit() {
    if (this.guest)
      return this.queuePolicy(async () => {
        const available = await this.readGuestPolicy(
          this.guest!,
          AbortSignal.any([this.policyController.signal, AbortSignal.timeout(10000)]),
        );
        assertGuestSelection(available, this.files, this.limit!);
        this.error = "";
      });
    try {
      const limits = await loadServerLimits();
      this.limit = limits.max_file_size;
      this.resourcePolicy = limits.resource_policy ?? null;
      this.trafficPolicy = limits.traffic_policy ?? null;
    } catch (cause) {
      this.limit = null;
      this.error = cause instanceof Error ? errorText(cause) : m("couldNotLoadTheFileLimit");
    }
  }
  state = $state<"idle" | "preparing" | "uploading" | "stopping" | "done" | "error">("idle");
  error = $state<DisplayText>("");
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
    return this.queuePolicy(async () => {
      if (!files.length) return;
      let available: SlotAvailability | null = null;
      if (this.guest) {
        available = await this.readGuestPolicy(
          this.guest,
          AbortSignal.any([this.policyController.signal, AbortSignal.timeout(10000)]),
        );
      } else {
        this.limit = await loadUploadLimit();
      }
      if (this.active || this.disposed) return;
      const selection = [...this.files, ...files];
      if (available) {
        try {
          assertGuestSelection(available, selection, this.limit!);
        } catch (cause) {
          if (cause instanceof GuestCapacityError)
            throw new GuestCapacityError(
              m("newFilesWereNotAddedValue", { arg0: errorText(cause) }),
            );
          throw cause;
        }
      }
      if (this.files.length + files.length > 100)
        throw new LocalizedError(m("chooseNoMoreThanFilesPerTransfer"));
      files.forEach((f) => assertFileSize(f.size, this.limit!));
      this.files = selection;
      this.error = "";
    });
  }
  remove(index: number) {
    if (this.active) return;
    this.files = this.files.filter((_, i) => i !== index);
    this.error = "";
    if (this.guest) void this.refreshLimit();
  }
  dispose() {
    this.disposed = true;
    this.policyController.abort();
    void this.cancel();
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
      this.error = m("uploadStoppedPartialServerFilesRemoved");
    } catch {
      this.error = m("uploadStoppedButPartialFilesCouldNotBeRemoved");
    }
  }
  async retryCleanup() {
    await this.cleanup();
  }
  async start(options: {
    accountId?: string;
    slotId?: string;
    key?: string;
    maxDownloads?: number;
    title?: string | null;
    oncreated?: (id: string, url: string, title: string, size: number) => void;
  }) {
    if (this.active || this.checking || this.disposed || !this.files.length) return;
    if (this.transferId) {
      await this.cleanup();
      if (this.transferId) return;
    }
    const run = ++this.run,
      files = [...this.files];
    this.controller = new AbortController();
    const signal = this.controller.signal;
    const check = () => {
      if (run !== this.run || signal.aborted)
        throw new DOMException(translate(m("stopped")), "AbortError");
    };
    const request = async (path: string, init: RequestInit = {}) => {
      check();
      const res = await fetch(`/api/v1${path}`, {
        ...init,
        credentials: options.slotId ? "omit" : "same-origin",
        signal,
      }).catch(async (cause) => {
        if (!this.transferId || signal.aborted) throw cause;
        throw (
          (await detectTransferStop(
            `/transfers/${this.transferId}`,
            options.slotId ? this.token : undefined,
            signal,
            "upload",
          )) ?? cause
        );
      });
      check();
      if (!res.ok) {
        const body = await res
          .clone()
          .json()
          .catch(() => null);
        const code = body?.code ?? res.headers.get("X-Psst-Error-Code");
        const policyError =
          trafficLimitError(code, body?.retry_at ?? res.headers.get("X-Psst-Retry-At")) ??
          transferStateError(code) ??
          resourceLimitError(code);
        if (policyError) throw policyError;
      }
      if (!res.ok && res.status === 403) {
        const body = await res
          .clone()
          .json()
          .catch(() => null);
        if (body?.code === "password_change_required")
          throw new LocalizedError(m("changeYourTemporaryPasswordBeforeSendingFiles"));
        if (body?.code === "admin_transfer_forbidden")
          throw new LocalizedError(m("administratorAccountsCannotTransferFilesUseARegularAccount"));
        if (body?.code === "receive_file_limit")
          throw new LocalizedError(m("thisReceiveLinkHasNoFileAllocationsLeftAsk"));
        if (body?.code === "receive_batch_limit")
          throw new LocalizedError(m("thisReceiveLinkHasReachedItsUploadLimitAsk"));
      }
      if (!res.ok)
        throw new LocalizedError(
          res.status === 429
            ? m("thisServerIsBusyWaitAMomentThenRetry")
            : res.status === 401
              ? m("signInAgainToContinue")
              : res.status === 404 || res.status === 410
                ? m("thisLinkIsNoLongerAvailableAskForA")
                : m("theServerCouldNotFinishTheUploadCheckYour"),
        );
      return res;
    };
    this.state = "preparing";
    this.error = "";
    this.sent = 0;
    this.total = 0;
    try {
      if (files.length > 100) throw new LocalizedError(m("chooseNoMoreThanFilesPerTransfer"));
      if (!options.slotId) this.limit = await loadUploadLimit(signal);
      check();
      this.total = selectionWireSize(files, options.slotId ? undefined : this.limit!);
      if (options.accountId) {
        const me = await (await request("/auth/me")).json();
        check();
        if (me.user.must_change_password)
          throw new LocalizedError(m("changeYourTemporaryPasswordBeforeSendingFiles"));
        if (me.user.role === "admin")
          throw new LocalizedError(m("administratorAccountsCannotTransferFilesUseARegularAccount"));
        if (me.user.id !== options.accountId)
          throw new LocalizedError(m("yourAccountChangedSignInAgainBeforeSendingFiles"));
      }
      const maxDownloads = validateLinkLimit(options.maxDownloads ?? 0);
      const sharedTitle = normalizeLinkTitle(options.title);
      const receiver = options.slotId ? parseReceiveFragment(options.key ?? "") : null;
      const key = await generateKey();
      check();
      const keyString = await exportKey(key);
      check();
      if (options.slotId && receiver) {
        const available = await this.readGuestPolicy(
          { slotId: options.slotId, key: options.key! },
          signal,
        );
        check();
        this.total = assertGuestSelection(available, files, this.limit!);
      }
      const created = await (
        await request(options.slotId ? `/slots/${options.slotId}/transfers` : "/transfers", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            options.slotId ? {} : { max_downloads: maxDownloads, title: sharedTitle },
          ),
        })
      ).json();
      check();
      this.transferId = created.id;
      this.token = created.delete_token ?? "";
      if (!options.slotId && (maxDownloads > 0 || sharedTitle)) {
        const accepted = await (await request(`/transfers/${created.id}`)).json();
        if (accepted.max_downloads !== maxDownloads || (accepted.title ?? null) !== sharedTitle) {
          await this.cleanup();
          throw new LocalizedError(m("thisServerDidNotAcceptTheLinkSettingsAsk"));
        }
      }
      const url = `${location.origin}/d/${created.id}#${keyString}`;
      if (!options.slotId)
        options.oncreated?.(
          created.id,
          url,
          files[0].name,
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
        entries.push(fileManifestEntry(file, id, encryptionId));
      }
      const encryptedManifest = await encryptManifest(key, { files: entries });
      const manifest =
        receiver && options.slotId
          ? encodeReceiveEnvelope(
              await sealSubmissionKey(receiver.publicKey, options.slotId, created.id, key),
              new Uint8Array(encryptedManifest),
            ).buffer
          : encryptedManifest;
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
      if (options.slotId && this.transferId && this.token && !signal.aborted) {
        try {
          const response = await fetch(`/api/v1/transfers/${this.transferId}/upload-status`, {
            credentials: "omit",
            headers: { Authorization: `Bearer ${this.token}` },
            signal: AbortSignal.timeout(5000),
          });
          if (response.ok && (await response.json()).status === "complete") {
            this.state = "done";
            this.transferId = "";
            return;
          }
        } catch {
          /* Keep failed upload and cleanup action visible. */
        }
      }
      this.state = "error";
      this.error = errorText(error, m("uploadInterruptedCheckYourConnectionThenRetryUploadYou"));
    }
  }
}

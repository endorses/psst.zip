import { ApiError } from "./api-error.ts";
import { message as m, LocalizedError } from "./i18n/index.ts";
import { TrafficLimitError, trafficLimitError, detectTransferStop } from "./traffic-policy.ts";
import { wireSize } from "./chunked-files.ts";
import { MAX_BUFFERED_BYTES } from "./limits.ts";
import { validateLinkLimit } from "./link-limits.ts";
import { resourceLimitError } from "./resource-policy.ts";
import { TransferStateError, transferStateError } from "./incident-state.ts";
import { validateSlotAvailability, type SlotAvailability } from "./guest-capacity.ts";
import { decodeReceivePublicKey } from "./receive-keys.ts";
import { normalizeLinkTitle } from "./link-title.ts";
import {
  INBOX_PAGE_SIZE,
  inboxUUID,
  validInboxCursor,
  validateInboxPage,
  type InboxPage,
} from "./inbox-page.ts";
export type { InboxPage } from "./inbox-page.ts";
export type { SlotAvailability } from "./guest-capacity.ts";

/**
 * Thin wrapper around the backend REST API.
 */

const API_BASE = "/api/v1";

function controlSignal(signal?: AbortSignal | null): AbortSignal {
  const timeout = AbortSignal.timeout(10_000);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

/** Read an error only once, with a small bound; never render a raw server body. */
export async function responseError(res: Response, signal?: AbortSignal | null): Promise<Error> {
  let value: { code?: unknown; retry_at?: unknown } | null = null;
  const boundedSignal = controlSignal(signal);
  try {
    value = JSON.parse(
      new TextDecoder("utf-8", { fatal: true }).decode(
        await readBounded(res, 4096, undefined, boundedSignal),
      ),
    );
  } catch {
    boundedSignal.throwIfAborted();
  }
  return (
    trafficLimitError(
      value?.code ?? res.headers.get("X-Psst-Error-Code"),
      value?.retry_at ?? res.headers.get("X-Psst-Retry-At"),
    ) ??
    transferStateError(value?.code ?? res.headers.get("X-Psst-Error-Code")) ??
    resourceLimitError(value?.code ?? res.headers.get("X-Psst-Error-Code")) ??
    new ApiError(
      res.status,
      typeof value?.code === "string"
        ? value.code
        : (res.headers.get("X-Psst-Error-Code") ?? undefined),
    )
  );
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const signal = controlSignal(init?.signal);
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    ...init,
    signal,
  });
  if (!res.ok) {
    throw await responseError(res, signal);
  }
  return JSON.parse(
    new TextDecoder("utf-8", { fatal: true }).decode(
      await readBounded(res, 128 * 1024, undefined, signal),
    ),
  ) as T;
}

async function requestRaw(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    ...init,
  });
  if (!res.ok) {
    throw await responseError(res, init?.signal);
  }
  return res;
}

// ---------------------------------------------------------------------------
// Transfer (send) flow
// ---------------------------------------------------------------------------

export interface CreateTransferResponse {
  id: string;
  delete_token?: string;
}

export async function createTransfer(
  maxDownloads = 0,
  title?: string | null,
): Promise<CreateTransferResponse> {
  return request<CreateTransferResponse>("/transfers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      max_downloads: validateLinkLimit(maxDownloads),
      title: normalizeLinkTitle(title),
    }),
  });
}

export async function uploadManifest(
  transferId: string,
  data: ArrayBuffer,
  token?: string,
): Promise<void> {
  await requestRaw(`/transfers/${transferId}/manifest`, {
    method: "POST",
    headers: { "Content-Type": "application/octet-stream", ...uploadHeaders(token) },
    body: data,
  });
}

export async function completeTransfer(transferId: string, token?: string): Promise<void> {
  await requestRaw(`/transfers/${transferId}/complete`, {
    method: "POST",
    headers: uploadHeaders(token),
  });
}

// ---------------------------------------------------------------------------
// Transfer (download) flow
// ---------------------------------------------------------------------------

export interface TransferInfo {
  id: string;
  title?: string | null;
  inactive_reason?: string | null;
  status: string;
  file_count: number;
  total_size: number;
  expires_at: string;
  downloaded_at: string | null;
  max_downloads?: number;
  files?: {
    id: string;
    size: number;
    download_count?: number | null;
    remaining_downloads?: number | null;
  }[];
}

export async function getTransferInfo(
  transferId: string,
  signal?: AbortSignal,
): Promise<TransferInfo> {
  if (!inboxUUID.test(transferId)) throw new LocalizedError(m("invalidTransferID"));
  return request<TransferInfo>(`/transfers/${transferId}`, { signal });
}

/** Confirm client-side decryption and browser handoff, not a completed disk save. */
export async function acknowledgeDownload(transferId: string): Promise<void> {
  await requestRaw(`/transfers/${transferId}/downloaded`, {
    method: "POST",
    signal: AbortSignal.timeout(10_000),
  });
}

export async function downloadManifest(
  transferId: string,
  signal?: AbortSignal,
): Promise<ArrayBuffer> {
  const boundedSignal = controlSignal(signal);
  try {
    const res = await requestRaw(`/transfers/${transferId}/manifest`, { signal: boundedSignal });
    return await readBounded(res, 1024 * 1024, undefined, boundedSignal);
  } catch (cause) {
    if (
      cause instanceof TrafficLimitError ||
      cause instanceof TransferStateError ||
      boundedSignal.aborted
    )
      throw cause;
    throw (
      (await detectTransferStop(
        `/transfers/${transferId}`,
        undefined,
        boundedSignal,
        "download",
      )) ?? cause
    );
  }
}

export async function downloadFile(
  transferId: string,
  fileId: string,
  onProgress?: (bytes: number) => void,
  signal?: AbortSignal,
): Promise<ArrayBuffer> {
  const res = await requestRaw(`/transfers/${transferId}/files/${fileId}`, { signal });
  return readBounded(res, wireSize(MAX_BUFFERED_BYTES), onProgress);
}

// ---------------------------------------------------------------------------
// Drop slot flow
// ---------------------------------------------------------------------------

export interface CreateSlotResponse {
  id: string;
  delete_token?: string;
}

export async function createSlot(
  publicKey: string,
  maxFiles = 0,
  title?: string | null,
): Promise<CreateSlotResponse> {
  return request<CreateSlotResponse>("/slots", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      receive_protocol: 2,
      recipient_public_key: publicKey,
      max_files: validateLinkLimit(maxFiles),
      title: normalizeLinkTitle(title),
    }),
  });
}

export interface SlotInfo {
  id: string;
  title?: string | null;
  transfers: { transfer_id: string; status: string; file_count: number }[];
  expires_at: string;
  receive_protocol: number;
  recipient_public_key: string;
  max_files: number;
  reserved_files: number;
  completed_files: number;
  remaining_files: number | null;
}

export async function renameLinkTitle(
  kind: "transfers" | "slots",
  id: string,
  title: string | null,
): Promise<{ title: string | null }> {
  if (!inboxUUID.test(id)) throw new LocalizedError(m("invalidLinkID"));
  const normalized = normalizeLinkTitle(title);
  const result = await request<{ title: string | null }>(`/${kind}/${id}/title`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title: normalized }),
  });
  if (result.title !== normalized)
    throw new LocalizedError(m("theServerDidNotSaveThisTitleRefreshAnd"));
  return result;
}

export async function getSlotAvailability(
  slotId: string,
  signal?: AbortSignal,
): Promise<SlotAvailability> {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(slotId))
    throw new LocalizedError(m("invalidReceiveLinkID"));
  const response = await requestRaw(`/slots/${slotId}/availability`, {
    credentials: "omit",
    cache: "no-store",
    signal: signal ?? AbortSignal.timeout(10000),
  });
  const bytes = await readBounded(response, 4096);
  return validateSlotAvailability(JSON.parse(new TextDecoder().decode(bytes)), slotId);
}

export async function getSlotInfo(slotId: string): Promise<SlotInfo> {
  return request<SlotInfo>(`/slots/${slotId}`);
}

/** Read one owner-only inbox page. Never fall back to an unbounded legacy listing. */
export async function getSlotInbox(
  slotId: string,
  after = "",
  signal?: AbortSignal,
): Promise<InboxPage> {
  if (!inboxUUID.test(slotId) || (after && !validInboxCursor(after)))
    throw new LocalizedError(m("invalidInboxPage"));
  const query = new URLSearchParams({ limit: String(INBOX_PAGE_SIZE) });
  if (after) query.set("after", after);
  const timeout = AbortSignal.timeout(10_000);
  const response = await fetch(`${API_BASE}/slots/${slotId}/inbox?${query}`, {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new ApiError(response.status);
  }
  const bytes = await readBounded(response, 32 * 1024);
  return validateInboxPage(
    JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)),
    slotId,
    after,
  );
}

export interface SlotTransferMembership {
  slot_id: string;
  transfer_id: string;
  receive_protocol: 2;
  recipient_public_key: string;
}

/** Owner-only, exact lookup: opening a submission never enumerates the inbox. */
export async function getSlotTransferMembership(
  slotId: string,
  transferId: string,
  signal?: AbortSignal,
): Promise<SlotTransferMembership> {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  if (!uuid.test(slotId) || !uuid.test(transferId))
    throw new LocalizedError(m("invalidInboxMembership"));
  const timeout = AbortSignal.timeout(10_000);
  const response = await fetch(`${API_BASE}/slots/${slotId}/transfers/${transferId}/membership`, {
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new ApiError(response.status);
  }
  const bytes = await readBounded(response, 4096);
  const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new LocalizedError(m("invalidInboxMembership"));
  const result = value as Record<string, unknown>;
  if (
    result.slot_id !== slotId ||
    result.transfer_id !== transferId ||
    result.receive_protocol !== 2 ||
    typeof result.recipient_public_key !== "string"
  )
    throw new LocalizedError(m("invalidInboxMembership"));
  decodeReceivePublicKey(result.recipient_public_key);
  return result as unknown as SlotTransferMembership;
}

/**
 * Subscribe to real-time upload notifications for a drop slot via SSE.
 */
export function subscribeSlotEvents(
  slotId: string,
  onEvent: (event: MessageEvent) => void,
): EventSource {
  const source = new EventSource(`${API_BASE}/slots/${slotId}/events`);
  source.onmessage = onEvent;
  return source;
}

/**
 * Get the tus upload endpoint for a transfer.
 */
export function tusEndpoint(transferId: string): string {
  return `${API_BASE}/transfers/${transferId}/files`;
}

/** Create a transfer owned by a drop slot before uploading its files. */
export async function createSlotTransfer(slotId: string): Promise<CreateTransferResponse> {
  return request<CreateTransferResponse>(`/slots/${slotId}/transfers`, {
    method: "POST",
    credentials: "omit",
  });
}

async function readBounded(
  response: Response,
  limit = wireSize(MAX_BUFFERED_BYTES),
  onProgress?: (bytes: number) => void,
  signal?: AbortSignal,
): Promise<ArrayBuffer> {
  const reader = response.body?.getReader();
  if (!reader) throw new LocalizedError(m("emptyResponseBody"));
  const chunks: Uint8Array[] = [];
  let size = 0;
  const cancel = () => {
    void reader.cancel().catch(() => {});
  };
  signal?.addEventListener("abort", cancel, { once: true });
  try {
    while (true) {
      signal?.throwIfAborted();
      const { done, value } = await reader.read();
      signal?.throwIfAborted();
      if (done) break;
      size += value.byteLength;
      onProgress?.(size);
      if (size > limit) throw new LocalizedError(m("downloadExceedsTheSupportedFileSize"));
      chunks.push(value);
    }
  } finally {
    signal?.removeEventListener("abort", cancel);
    cancel();
    reader.releaseLock();
  }
  const result = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result.buffer;
}

export function uploadHeaders(token?: string): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

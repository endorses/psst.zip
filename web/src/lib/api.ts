import { wireSize } from "./chunked-files.ts";
import { MAX_BUFFERED_BYTES } from "./limits.ts";

/**
 * Thin wrapper around the backend REST API.
 */

const API_BASE = "/api/v1";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, init);
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`API ${res.status}: ${text}`);
  }
  return res.json() as Promise<T>;
}

async function requestRaw(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`${API_BASE}${path}`, init);
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`API ${res.status}: ${text}`);
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

export async function createTransfer(): Promise<CreateTransferResponse> {
  return request<CreateTransferResponse>("/transfers", { method: "POST" });
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
  file_count: number;
  total_size: number;
  expires_at: string;
  downloaded_at: string | null;
}

export async function getTransferInfo(transferId: string): Promise<TransferInfo> {
  return request<TransferInfo>(`/transfers/${transferId}`);
}

/** Confirm client-side decryption and browser handoff, not a completed disk save. */
export async function acknowledgeDownload(transferId: string): Promise<void> {
  await requestRaw(`/transfers/${transferId}/downloaded`, {
    method: "POST",
    signal: AbortSignal.timeout(10_000),
  });
}

export async function downloadManifest(transferId: string): Promise<ArrayBuffer> {
  const res = await requestRaw(`/transfers/${transferId}/manifest`);
  return readBounded(res, 1024 * 1024);
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
}

export async function createSlot(): Promise<CreateSlotResponse> {
  return request<CreateSlotResponse>("/slots", { method: "POST" });
}

export interface SlotInfo {
  id: string;
  transfers: { transfer_id: string; status: string; file_count: number }[];
  expires_at: string;
}

export async function getSlotInfo(slotId: string): Promise<SlotInfo> {
  return request<SlotInfo>(`/slots/${slotId}`);
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
  return request<CreateTransferResponse>(`/slots/${slotId}/transfers`, { method: "POST" });
}

async function readBounded(
  response: Response,
  limit = wireSize(MAX_BUFFERED_BYTES),
  onProgress?: (bytes: number) => void,
): Promise<ArrayBuffer> {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("Empty response body");
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      onProgress?.(size);
      if (size > limit) throw new Error("Download exceeds the supported file size.");
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
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

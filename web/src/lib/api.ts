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
  transferId: string;
}

export async function createTransfer(): Promise<CreateTransferResponse> {
  return request<CreateTransferResponse>("/transfers", { method: "POST" });
}

export async function uploadManifest(transferId: string, data: ArrayBuffer): Promise<void> {
  await requestRaw(`/transfers/${transferId}/manifest`, {
    method: "POST",
    headers: { "Content-Type": "application/octet-stream" },
    body: data,
  });
}

export async function completeTransfer(transferId: string): Promise<void> {
  await requestRaw(`/transfers/${transferId}/complete`, { method: "POST" });
}

// ---------------------------------------------------------------------------
// Transfer (download) flow
// ---------------------------------------------------------------------------

export interface TransferInfo {
  transferId: string;
  fileCount: number;
  totalSize: number;
  expiresAt: string;
}

export async function getTransferInfo(transferId: string): Promise<TransferInfo> {
  return request<TransferInfo>(`/transfers/${transferId}`);
}

export async function downloadManifest(transferId: string): Promise<ArrayBuffer> {
  const res = await requestRaw(`/transfers/${transferId}/manifest`);
  return res.arrayBuffer();
}

export async function downloadFile(transferId: string, fileId: string): Promise<ArrayBuffer> {
  const res = await requestRaw(`/transfers/${transferId}/files/${fileId}`);
  return res.arrayBuffer();
}

// ---------------------------------------------------------------------------
// Drop slot flow
// ---------------------------------------------------------------------------

export interface CreateSlotResponse {
  slotId: string;
}

export async function createSlot(): Promise<CreateSlotResponse> {
  return request<CreateSlotResponse>("/slots", { method: "POST" });
}

export interface SlotInfo {
  slotId: string;
  files: { fileId: string; size: number }[];
  expiresAt: string;
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

/**
 * Get the tus upload endpoint scoped to a drop slot.
 */
export function tusSlotEndpoint(slotId: string): string {
  return `${API_BASE}/slots/${slotId}/files`;
}

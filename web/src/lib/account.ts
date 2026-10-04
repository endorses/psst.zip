import { trafficLimitError } from "./traffic-policy.ts";
import { resourceLimitError } from "./resource-policy.ts";
import { transferStateError } from "./incident-state.ts";

export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
  disabled: boolean;
  must_change_password?: boolean;
}
export interface Session {
  id: string;
  device_name: string;
  expires_at: string;
  current: boolean;
}
export interface Resource {
  id: string;
  status?: string;
  file_count?: number;
  download_count?: number;
  max_downloads?: number;
  max_files?: number;
  reserved_files?: number;
  completed_files?: number;
  remaining_files?: number | null;
  receive_protocol?: number;
  total_size?: number;
  downloaded_at?: string | null;
  expires_at: string;
  created_at?: string;
  owner_id?: string;
  transfers?: { transfer_id: string; status: string; file_count: number }[];
}
/** List endpoints provide aggregate counts; their empty child arrays are not an empty inbox. */
export function resourceFileCount(resource: Resource): number | null {
  if (Number.isSafeInteger(resource.file_count) && resource.file_count! >= 0)
    return resource.file_count!;
  // Compatibility with older detailed responses only when children are present.
  if (resource.transfers?.length)
    return resource.transfers.reduce((sum, child) => sum + child.file_count, 0);
  return null;
}
export function receivedFileCount(resource: Resource): number | null {
  if (Number.isSafeInteger(resource.completed_files) && resource.completed_files! >= 0)
    return resource.completed_files!;
  if (resource.transfers?.length)
    return resource.transfers
      .filter((child) => child.status === "complete")
      .reduce((sum, child) => sum + child.file_count, 0);
  return null;
}
export class AccountError extends Error {
  status: number;
  code?: string;
  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
export async function accountRequest<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  });
  if (!response.ok) {
    let code = response.headers.get("X-Psst-Error-Code") ?? undefined;
    let detail = (await response.text()).slice(0, 300);
    try {
      const parsed: unknown = JSON.parse(detail);
      if (
        parsed &&
        typeof parsed === "object" &&
        "code" in parsed &&
        typeof parsed.code === "string"
      )
        code = parsed.code;
      if (
        parsed &&
        typeof parsed === "object" &&
        "error" in parsed &&
        typeof parsed.error === "string"
      )
        detail = parsed.error;
    } catch {
      /* Plain text errors remain useful. */
    }
    throw new AccountError(
      response.status,
      trafficLimitError(code, response.headers.get("X-Psst-Retry-At"))?.message ||
        transferStateError(code)?.message ||
        resourceLimitError(code)?.message ||
        detail ||
        `Request failed (${response.status})`,
      code,
    );
  }
  return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}
// Only encryption links, never login credentials; separate accounts on shared browsers.
export function loadLinks(id: string): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(`psst.links.${id}`) || "{}");
  } catch {
    return {};
  }
}
export function saveLinks(id: string, links: Record<string, string>) {
  try {
    localStorage.setItem(`psst.links.${id}`, JSON.stringify(links));
  } catch {
    /* Sharing remains available without persistent storage. */
  }
}

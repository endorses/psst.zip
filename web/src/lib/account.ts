import { apiErrorMessage } from "./api-error.ts";
import { message as m, LocalizedError, type DisplayText } from "./i18n/index.ts";
import {
  administratorSecurityMessage,
  securityIdentityGeneration,
  recentAuthenticationRequired,
} from "./admin-security.ts";
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
  title?: string | null;
  inactive_reason?: string | null;
  status?: string;
  file_count?: number | null;
  download_count?: number;
  max_downloads?: number;
  max_files?: number;
  reserved_files?: number;
  completed_files?: number | null;
  remaining_files?: number | null;
  receive_protocol?: number;
  total_size?: number | null;
  summary?: {
    state: "ready" | "updating";
    completed_files: number | null;
    file_count: number | null;
    total_size: number | null;
  };
  downloaded_at?: string | null;
  expires_at: string;
  created_at?: string;
  owner_id?: string;
  transfers?: { transfer_id: string; status: string; file_count: number }[];
}
/** List endpoints provide aggregate counts; their empty child arrays are not an empty inbox. */
export function resourceFileCount(resource: Resource): number | null {
  if (resource.summary)
    return resource.summary.state === "ready" ? resource.summary.file_count : null;
  if (Number.isSafeInteger(resource.file_count) && resource.file_count! >= 0)
    return resource.file_count!;
  // Compatibility with older detailed responses only when children are present.
  if (resource.transfers?.length)
    return resource.transfers.reduce((sum, child) => sum + child.file_count, 0);
  return null;
}
export function receivedFileCount(resource: Resource): number | null {
  if (resource.summary)
    return resource.summary.state === "ready" ? resource.summary.completed_files : null;
  if (Number.isSafeInteger(resource.completed_files) && resource.completed_files! >= 0)
    return resource.completed_files!;
  if (resource.transfers?.length)
    return resource.transfers
      .filter((child) => child.status === "complete")
      .reduce((sum, child) => sum + child.file_count, 0);
  return null;
}
export class AccountError extends LocalizedError {
  status: number;
  code?: string;
  constructor(status: number, message: DisplayText, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
/** Stop reading at the byte boundary, including chunked or untrusted error responses. */
async function accountBody(response: Response, limit: number): Promise<string> {
  const reader = response.body?.getReader();
  if (!reader) throw new LocalizedError(m("emptyAccountResponse"));
  let size = 0;
  const chunks: Uint8Array[] = [];
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) throw new LocalizedError(m("accountResponseExceedsTheSupportedSize"));
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
}
export async function accountRequest<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
  maxResponseBytes = 1024 * 1024,
): Promise<T> {
  const generation = securityIdentityGeneration();
  const response = await fetch(`/api/v1${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal
      ? AbortSignal.any([signal, AbortSignal.timeout(15000)])
      : AbortSignal.timeout(15000),
  });
  if (!response.ok) {
    let code = response.headers.get("X-Psst-Error-Code") ?? undefined;
    let detail = await accountBody(response, 4096).catch(() => "");
    try {
      const parsed: unknown = JSON.parse(detail);
      if (
        parsed &&
        typeof parsed === "object" &&
        "code" in parsed &&
        typeof parsed.code === "string"
      )
        code = parsed.code;
    } catch {
      /* Unknown response bodies are never displayed. */
    }
    if (code === "recent_authentication_required") recentAuthenticationRequired(generation);
    throw new AccountError(
      response.status,
      administratorSecurityMessage(code) ||
        trafficLimitError(code, response.headers.get("X-Psst-Retry-At"))?.presentation ||
        transferStateError(code)?.presentation ||
        resourceLimitError(code)?.presentation ||
        apiErrorMessage(response.status, code),
      code,
    );
  }
  return response.status === 204
    ? (undefined as T)
    : (JSON.parse(await accountBody(response, maxResponseBytes)) as T);
}

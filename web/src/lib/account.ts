export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
  disabled: boolean;
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
  total_size?: number;
  downloaded_at?: string | null;
  expires_at: string;
  transfers?: { transfer_id: string; status: string; file_count: number }[];
}
export class AccountError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
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
    let detail = (await response.text()).slice(0, 300);
    try {
      const parsed: unknown = JSON.parse(detail);
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
    throw new AccountError(response.status, detail || `Request failed (${response.status})`);
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

import { base64urlDecode, base64urlEncode } from "./crypto.ts";
import { decodeReceivePublicKey } from "./receive-keys.ts";
import { validSharedTitle } from "./link-title.ts";

export const INBOX_PAGE_SIZE = 50;
export const INBOX_PREVIOUS_WINDOW = 100;
export const inboxUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export interface InboxPage {
  id: string;
  title?: string | null;
  status: "waiting" | "has_uploads";
  expires_at: string;
  created_at: string;
  receive_protocol: 1 | 2;
  recipient_public_key: string;
  max_files: number;
  reserved_files: number;
  remaining_files: number | null;
  transfers: { transfer_id: string; status: "pending" | "complete"; file_count: number }[];
  paginated: true;
  next_cursor: string | null;
  summary: {
    state: "ready" | "updating";
    completed_files: number | null;
    file_count: number | null;
    total_size: number | null;
  };
}
const unsupported = () =>
  new Error(
    "This server returned an unsupported inbox page. Refresh or contact its administrator.",
  );
export function validInboxCursor(value: unknown): value is string {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]{1,512}$/.test(value)) return false;
  try {
    return base64urlEncode(base64urlDecode(value)) === value;
  } catch {
    return false;
  }
}
const integer = (value: unknown, max = Number.MAX_SAFE_INTEGER): value is number =>
  Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= max;
const timestamp = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(value) &&
  Number.isFinite(Date.parse(value));
export function validateInboxPage(value: unknown, slotId: string, after = ""): InboxPage {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw unsupported();
  const p = value as InboxPage;
  if (
    p.id !== slotId ||
    !validSharedTitle(p.title) ||
    !["waiting", "has_uploads"].includes(p.status) ||
    p.paginated !== true ||
    !timestamp(p.expires_at) ||
    !timestamp(p.created_at) ||
    ![1, 2].includes(p.receive_protocol) ||
    typeof p.recipient_public_key !== "string" ||
    !integer(p.max_files, 2147483647) ||
    !integer(p.reserved_files) ||
    (p.max_files === 0
      ? p.remaining_files !== null
      : p.remaining_files !== Math.max(0, p.max_files - p.reserved_files)) ||
    !Array.isArray(p.transfers) ||
    p.transfers.length > INBOX_PAGE_SIZE ||
    (p.next_cursor !== null && (!validInboxCursor(p.next_cursor) || p.next_cursor === after))
  )
    throw unsupported();
  if (p.receive_protocol === 2) {
    try {
      decodeReceivePublicKey(p.recipient_public_key);
    } catch {
      throw unsupported();
    }
  } else if (p.recipient_public_key !== "") throw unsupported();
  if (
    p.transfers.some(
      (t) =>
        !t ||
        !inboxUUID.test(t.transfer_id) ||
        !["pending", "complete"].includes(t.status) ||
        !integer(t.file_count, 100),
    ) ||
    new Set(p.transfers.map((t) => t.transfer_id)).size !== p.transfers.length
  )
    throw unsupported();
  const s = p.summary;
  if (!s || !["ready", "updating"].includes(s.state)) throw unsupported();
  if (s.state === "updating") {
    if (s.completed_files !== null || s.file_count !== null || s.total_size !== null)
      throw unsupported();
  } else if (
    !integer(s.completed_files) ||
    !integer(s.file_count) ||
    !integer(s.total_size) ||
    s.completed_files > s.file_count ||
    s.completed_files <
      p.transfers.reduce(
        (sum, transfer) => sum + (transfer.status === "complete" ? transfer.file_count : 0),
        0,
      ) ||
    s.file_count < p.transfers.reduce((sum, transfer) => sum + transfer.file_count, 0)
  )
    throw unsupported();
  return p;
}

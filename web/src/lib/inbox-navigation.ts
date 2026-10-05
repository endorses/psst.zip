import { INBOX_PREVIOUS_WINDOW, inboxUUID, validInboxCursor } from "./inbox-page.ts";

const KEY = "psst.inbox-return";
export interface InboxViewPosition {
  cursor: string;
  previous: string[];
  page: number;
  scroll: number;
}
type Checkpoint = InboxViewPosition & { account: string; inbox: string; at: number };
type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

/** One short-lived view checkpoint per tab; contains neither keys nor file metadata. */
export function rememberInboxPosition(
  storage: StorageLike,
  account: string,
  inbox: string,
  position: InboxViewPosition,
  now = Date.now(),
) {
  const value: Checkpoint = { ...position, account, inbox, at: now };
  if (!valid(value)) return;
  try {
    storage.setItem(KEY, JSON.stringify(value));
  } catch {
    /* Returning to the first page is safe. */
  }
}
export function restoreInboxPosition(
  storage: StorageLike,
  account: string,
  inbox: string,
  now = Date.now(),
): InboxViewPosition | null {
  try {
    const raw = storage.getItem(KEY);
    if (!raw) return null;
    if (raw.length > 60 * 1024) {
      storage.removeItem(KEY);
      return null;
    }
    const value = JSON.parse(raw) as Checkpoint;
    if (!valid(value) || value.at > now || now - value.at > 24 * 60 * 60 * 1000) {
      storage.removeItem(KEY);
      return null;
    }
    if (value.account !== account || value.inbox !== inbox) return null;
    storage.removeItem(KEY);
    return {
      cursor: value.cursor,
      previous: value.previous,
      page: value.page,
      scroll: value.scroll,
    };
  } catch {
    try {
      storage.removeItem(KEY);
    } catch {
      /* Storage can be unavailable. */
    }
    return null;
  }
}
function valid(value: Checkpoint): boolean {
  return (
    !!value &&
    inboxUUID.test(value.account) &&
    inboxUUID.test(value.inbox) &&
    (value.cursor === "" || validInboxCursor(value.cursor)) &&
    Array.isArray(value.previous) &&
    value.previous.length <= INBOX_PREVIOUS_WINDOW &&
    value.previous.every((cursor) => cursor === "" || validInboxCursor(cursor)) &&
    Number.isSafeInteger(value.page) &&
    value.page >= 1 &&
    value.page <= Number.MAX_SAFE_INTEGER &&
    Number.isFinite(value.scroll) &&
    value.scroll >= 0 &&
    value.scroll <= 10_000_000 &&
    Number.isSafeInteger(value.at) &&
    value.at >= 0
  );
}

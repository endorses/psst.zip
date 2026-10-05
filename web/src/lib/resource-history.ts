import { message as m, t, LocalizedError } from "./i18n/index.ts";
import { accountRequest, type Resource, type User } from "./account.ts";
import { inboxUUID, validInboxCursor } from "./inbox-page.ts";
import { decodeReceivePublicKey } from "./receive-keys.ts";
import { validSharedTitle } from "./link-title.ts";

export const HISTORY_PAGE_SIZE = 50;
export const HISTORY_PREVIOUS_WINDOW = 100;
export interface ResourcePage {
  transfers: Resource[];
  slots: Resource[];
  paginated: true;
  next_cursor: string | null;
}
const invalidPage = () =>
  new LocalizedError(m("thisServerReturnedAnUnsupportedResourcePageRefreshOr"));
const nonnegative = (value: unknown, maximum = Number.MAX_SAFE_INTEGER): value is number =>
  Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum;
const timestamp = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(value) &&
  Number.isFinite(Date.parse(value));
function validResource(value: unknown, slot: boolean): value is Resource {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const r = value as Record<string, unknown>;
  if (
    typeof r.id !== "string" ||
    !inboxUUID.test(r.id) ||
    !validSharedTitle(r.title) ||
    !timestamp(r.created_at) ||
    !timestamp(r.expires_at) ||
    (r.owner_id !== undefined && (typeof r.owner_id !== "string" || !inboxUUID.test(r.owner_id))) ||
    !(
      slot ? ["waiting", "has_uploads", "revoked"] : ["pending", "complete", "revoked", "exhausted"]
    ).includes(r.status as string)
  )
    return false;
  if (!r.summary || typeof r.summary !== "object" || Array.isArray(r.summary)) return false;
  const summary = r.summary as Record<string, unknown>;
  if (summary.state === "updating") {
    if (
      summary.completed_files !== null ||
      summary.file_count !== null ||
      summary.total_size !== null
    )
      return false;
  } else if (
    summary.state !== "ready" ||
    !nonnegative(summary.completed_files) ||
    !nonnegative(summary.file_count) ||
    !nonnegative(summary.total_size) ||
    summary.completed_files > summary.file_count
  )
    return false;
  if (r.file_count !== summary.file_count || r.total_size !== summary.total_size) return false;
  if (slot) {
    if (
      r.completed_files !== summary.completed_files ||
      !Array.isArray(r.transfers) ||
      r.transfers.length !== 0 ||
      ![1, 2].includes(r.receive_protocol as number) ||
      typeof r.recipient_public_key !== "string" ||
      !nonnegative(r.max_files, 2147483647) ||
      !nonnegative(r.reserved_files) ||
      (r.max_files === 0
        ? r.remaining_files !== null
        : r.remaining_files !== Math.max(0, r.max_files - r.reserved_files))
    )
      return false;
    if (r.receive_protocol === 2) {
      try {
        decodeReceivePublicKey(r.recipient_public_key);
      } catch {
        return false;
      }
    } else if (r.recipient_public_key !== "") return false;
  } else if (
    !Array.isArray(r.files) ||
    r.files.length !== 0 ||
    !nonnegative(r.max_downloads, 2147483647) ||
    !nonnegative(r.download_count) ||
    typeof r.has_manifest !== "boolean" ||
    (r.downloaded_at !== null && !timestamp(r.downloaded_at)) ||
    (r.completed_at !== undefined && r.completed_at !== null && !timestamp(r.completed_at))
  )
    return false;
  return true;
}
export function validateResourcePage(value: unknown, after = ""): ResourcePage {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalidPage();
  const result = value as ResourcePage;
  if (
    result.paginated !== true ||
    !Array.isArray(result.transfers) ||
    !Array.isArray(result.slots) ||
    result.transfers.length + result.slots.length > HISTORY_PAGE_SIZE ||
    (result.next_cursor !== null &&
      (!validInboxCursor(result.next_cursor) || result.next_cursor === after)) ||
    !result.transfers.every((item) => validResource(item, false)) ||
    !result.slots.every((item) => validResource(item, true)) ||
    new Set([
      ...result.transfers.map((t) => `transfer:${t.id}`),
      ...result.slots.map((t) => `slot:${t.id}`),
    ]).size !==
      result.transfers.length + result.slots.length
  )
    throw invalidPage();
  return result;
}
/** Fetch exactly one bounded page. Empty filtered pages can still have a next cursor. */
export async function loadResourcePage(
  after = "",
  all = false,
  signal?: AbortSignal,
  kind?: "transfer" | "slot",
): Promise<ResourcePage> {
  if (after && !validInboxCursor(after)) throw invalidPage();
  const query = new URLSearchParams({ limit: String(HISTORY_PAGE_SIZE) });
  if (all) query.set("all", "true");
  if (kind) query.set("kind", kind);
  if (after) query.set("after", after);
  const timeout = AbortSignal.timeout(10000);
  const result = await accountRequest<unknown>(
    `/auth/resources?${query}`,
    "GET",
    undefined,
    signal ? AbortSignal.any([signal, timeout]) : timeout,
    64 * 1024,
  );
  const page = validateResourcePage(result, after);
  if ((kind === "transfer" && page.slots.length) || (kind === "slot" && page.transfers.length))
    throw invalidPage();
  return page;
}

export async function loadUsersPage(
  after = "",
): Promise<{ users: User[]; next_cursor: string | null }> {
  const query = new URLSearchParams({ limit: String(HISTORY_PAGE_SIZE) });
  if (after) query.set("after", after);
  const result = await accountRequest<{ users: User[]; next_cursor?: string | null }>(
    `/admin/users?${query}`,
  );
  if (
    !Array.isArray(result.users) ||
    result.users.length > HISTORY_PAGE_SIZE ||
    (result.next_cursor != null &&
      (typeof result.next_cursor !== "string" ||
        result.next_cursor.length > 2048 ||
        result.next_cursor === after))
  )
    throw new LocalizedError(m("thisServerReturnedAnUnsupportedAccountsPageRefreshOr"));
  return { users: result.users, next_cursor: result.next_cursor || null };
}

import { accountRequest, type Resource, type User } from "./account.ts";

export const HISTORY_PAGE_SIZE = 50;
export interface ResourcePage {
  transfers: Resource[];
  slots: Resource[];
  next_cursor: string | null;
}
/** Fetch exactly one bounded page. The caller decides when to request another. */
export async function loadResourcePage(after = "", all = false): Promise<ResourcePage> {
  const query = new URLSearchParams({ limit: String(HISTORY_PAGE_SIZE) });
  if (all) query.set("all", "true");
  if (after) query.set("after", after);
  const result = await accountRequest<Partial<ResourcePage>>(`/auth/resources?${query}`);
  const transfers = result.transfers ?? [],
    slots = result.slots ?? [];
  if (
    !Array.isArray(transfers) ||
    !Array.isArray(slots) ||
    transfers.length + slots.length > HISTORY_PAGE_SIZE ||
    (result.next_cursor != null &&
      (typeof result.next_cursor !== "string" ||
        result.next_cursor.length > 2048 ||
        result.next_cursor === after))
  )
    throw new Error(
      "This server returned an unsupported resource page. Refresh or contact its administrator.",
    );
  return { transfers, slots, next_cursor: result.next_cursor || null };
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
    throw new Error(
      "This server returned an unsupported accounts page. Refresh or contact its administrator.",
    );
  return { users: result.users, next_cursor: result.next_cursor || null };
}

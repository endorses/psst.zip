import type { InboxPage } from "../src/lib/inbox-page.ts";
export const inboxID = "11111111-1111-4111-8111-111111111111";
export const inboxOwner = "33333333-3333-4333-8333-333333333333";
export const inboxKey = "A".repeat(43);
export const inboxChild = (n: number) => `22222222-2222-4222-8222-${String(n).padStart(12, "0")}`;
export const inboxCursor = (n: number) => Buffer.from(JSON.stringify({ n })).toString("base64url");
export function inboxPage(overrides: Partial<InboxPage> = {}): InboxPage {
  return {
    id: inboxID,
    status: "waiting",
    expires_at: "2099-01-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    receive_protocol: 2,
    recipient_public_key: inboxKey,
    max_files: 0,
    reserved_files: 200,
    remaining_files: null,
    transfers: [{ transfer_id: inboxChild(1), status: "complete", file_count: 1 }],
    paginated: true,
    next_cursor: null,
    summary: { state: "ready", completed_files: 151, file_count: 160, total_size: 9600 },
    ...overrides,
  };
}

import type { AdminResource, CleanupOverview } from "../src/lib/admin-resources.ts";
export const resourceID = "11111111-1111-4111-8111-111111111111";
export const receiveID = "22222222-2222-4222-8222-222222222222";
export const ownerID = "33333333-3333-4333-8333-333333333333";
export function adminResource(overrides: Partial<AdminResource> = {}): AdminResource {
  return {
    type: "transfer",
    id: resourceID,
    parent_slot_id: null,
    owner_id: ownerID,
    owner_username: "Member",
    owner_disabled: false,
    created_at: "2026-10-04T12:00:00Z",
    expires_at: "2030-10-04T12:00:00Z",
    pending_expires_at: null,
    status: "complete",
    file_count: 2,
    child_transfer_count: 0,
    reserved_bytes: 2097152,
    occupied_bytes_estimate: 1048576,
    manifest_bytes: 128,
    cleanup: { state: "none", attempt_count: 0 },
    ...overrides,
  };
}
export const cleanupOverview: CleanupOverview = {
  pending_count: 2,
  failed_count: 1,
  busy_count: 1,
  oldest_pending_at: "2026-10-04T11:00:00Z",
  last_discovery_at: "2026-10-04T12:00:00Z",
  discovery_pending: true,
};

export const storageChecks = {
  state: "checked",
  issue_count: 0,
  busy_count: 0,
  unavailable_count: 0,
  failed_count: 0,
  last_scan_completed_at: "2026-10-04T12:00:00Z",
  scan_pending: false,
};

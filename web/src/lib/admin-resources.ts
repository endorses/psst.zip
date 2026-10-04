import { accountRequest } from "./account.ts";

export type ResourceType = "transfer" | "slot";
export interface ResourceCleanup {
  state: "none" | "pending" | "busy" | "waiting_children" | "failed";
  reason?: string;
  pending_since?: string;
  last_attempt_at?: string;
  next_retry_at?: string;
  attempt_count: number;
  failure_code?: string;
  last_failure_at?: string;
}
export interface AdminResource {
  type: ResourceType;
  id: string;
  parent_slot_id: string | null;
  owner_id: string | null;
  owner_username: string | null;
  owner_disabled: boolean;
  created_at: string;
  expires_at: string;
  pending_expires_at: string | null;
  status: string;
  file_count: number;
  child_transfer_count: number;
  reserved_bytes: number;
  occupied_bytes_estimate: number;
  manifest_bytes: number;
  cleanup: ResourceCleanup;
}
export interface AdminResourcePage {
  resources: AdminResource[];
  next_cursor: string | null;
}
export interface CleanupOverview {
  pending_count: number;
  failed_count: number;
  busy_count: number;
  oldest_pending_at?: string;
  last_discovery_at?: string;
  discovery_pending: boolean;
}
export interface ResourceFilters {
  type: string;
  owner_id: string;
  status: string;
}
export type ResourceMutation =
  | { state: "removed"; type: ResourceType; id: string }
  | { state: "pending" | "complete"; type: ResourceType; id: string; cleanup: ResourceCleanup };
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const isResourceID = (value: string) => uuid.test(value);
export function resourcePath(type: ResourceType, id: string) {
  if ((type !== "transfer" && type !== "slot") || !isResourceID(id))
    throw new Error("Choose a resource type and enter its ID only, without a URL or link secret.");
  return `/admin/resources/${type}/${id}`;
}
const natural = (n: unknown) => typeof n === "number" && Number.isSafeInteger(n) && n >= 0;
const timestamp = (s: unknown) =>
  typeof s === "string" && s.length <= 64 && Number.isFinite(Date.parse(s));
const optionalTime = (s: unknown) => s == null || timestamp(s);
const boundedText = (s: unknown) => typeof s === "string" && s.length <= 128;
function validCleanup(value: ResourceCleanup): boolean {
  return (
    !!value &&
    ["none", "pending", "busy", "waiting_children", "failed"].includes(value.state) &&
    natural(value.attempt_count) &&
    [value.pending_since, value.last_attempt_at, value.next_retry_at, value.last_failure_at].every(
      optionalTime,
    ) &&
    (value.reason == null || boundedText(value.reason)) &&
    (value.failure_code == null || boundedText(value.failure_code))
  );
}
function validResource(value: AdminResource): boolean {
  return (
    !!value &&
    (value.type === "transfer" || value.type === "slot") &&
    isResourceID(value.id) &&
    (value.parent_slot_id === null || isResourceID(value.parent_slot_id)) &&
    (value.owner_id === null || isResourceID(value.owner_id)) &&
    (value.owner_username === null || boundedText(value.owner_username)) &&
    typeof value.owner_disabled === "boolean" &&
    timestamp(value.created_at) &&
    timestamp(value.expires_at) &&
    optionalTime(value.pending_expires_at) &&
    ["pending", "complete", "waiting", "revoked"].includes(value.status) &&
    [
      value.file_count,
      value.child_transfer_count,
      value.reserved_bytes,
      value.occupied_bytes_estimate,
      value.manifest_bytes,
    ].every(natural) &&
    validCleanup(value.cleanup)
  );
}
export async function loadAdminResources(
  filters: ResourceFilters,
  after = "",
  signal?: AbortSignal,
): Promise<AdminResourcePage> {
  const query = new URLSearchParams({ limit: "50" });
  if (filters.type) {
    if (!["transfer", "slot"].includes(filters.type)) throw new Error("Invalid resource type");
    query.set("type", filters.type);
  }
  if (filters.owner_id) {
    if (!isResourceID(filters.owner_id)) throw new Error("Enter the account ID only.");
    query.set("owner_id", filters.owner_id);
  }
  if (filters.status) {
    if (!["pending", "complete", "waiting", "revoked"].includes(filters.status))
      throw new Error("Invalid resource status");
    query.set("status", filters.status);
  }
  if (after) {
    if (after.length > 2048) throw new Error("Invalid resource page");
    query.set("after", after);
  }
  const page = await accountRequest<AdminResourcePage>(
    `/admin/resources?${query}`,
    "GET",
    undefined,
    signal,
  );
  if (
    !page ||
    !Array.isArray(page.resources) ||
    page.resources.length > 50 ||
    !page.resources.every(validResource) ||
    (page.next_cursor !== null &&
      (typeof page.next_cursor !== "string" ||
        !page.next_cursor ||
        page.next_cursor.length > 2048 ||
        page.next_cursor === after))
  )
    throw new Error("Invalid resource response");
  return page;
}
export async function loadAdminResource(
  type: ResourceType,
  id: string,
  signal?: AbortSignal,
): Promise<AdminResource> {
  const value = await accountRequest<AdminResource>(
    resourcePath(type, id),
    "GET",
    undefined,
    signal,
  );
  if (!validResource(value) || value.type !== type || value.id !== id)
    throw new Error("Invalid resource response");
  return value;
}
export async function loadCleanupOverview(signal?: AbortSignal): Promise<CleanupOverview> {
  const value = await accountRequest<CleanupOverview>("/admin/cleanup", "GET", undefined, signal);
  if (
    !value ||
    ![value.pending_count, value.failed_count, value.busy_count].every(natural) ||
    !optionalTime(value.oldest_pending_at) ||
    !optionalTime(value.last_discovery_at) ||
    typeof value.discovery_pending !== "boolean"
  )
    throw new Error("Invalid cleanup response");
  return value;
}
export async function mutateAdminResource(
  type: ResourceType,
  id: string,
  action: "revoke" | "cleanup",
  signal?: AbortSignal,
): Promise<ResourceMutation> {
  const value = await accountRequest<ResourceMutation>(
    `${resourcePath(type, id)}/${action}`,
    "POST",
    {},
    signal,
  );
  if (
    !value ||
    value.type !== type ||
    value.id !== id ||
    (value.state !== "removed" &&
      ((value.state !== "pending" && value.state !== "complete") ||
        !validCleanup(value.cleanup) ||
        (value.state === "complete" && (action !== "cleanup" || value.cleanup.state !== "none"))))
  )
    throw new Error("Invalid resource response");
  return value;
}
const cleanupLabels = new Map([
  ["none", "No cleanup queued"],
  ["pending", "Cleanup pending"],
  ["busy", "Waiting for active file operations"],
  ["waiting_children", "Waiting for received transfers to be removed"],
  ["failed", "Cleanup failed"],
]);
const failureLabels = new Map([
  [
    "storage_delete_failed",
    "Encrypted files could not be removed. Check storage access and available disk space.",
  ],
  [
    "metadata_delete_failed",
    "Resource metadata could not be removed. Check database availability.",
  ],
]);
const reasonLabels = new Map([
  ["revoked", "Link revoked"],
  ["expired", "Link expired"],
  ["unfinished_expired", "Unfinished upload expired"],
  ["download_limit", "Download limit reached"],
]);
export const cleanupLabel = (value: ResourceCleanup) =>
  cleanupLabels.get(value.state) ?? "Cleanup status unavailable";
export const cleanupFailure = (value: ResourceCleanup) =>
  value.failure_code
    ? (failureLabels.get(value.failure_code) ??
      "The last cleanup attempt failed. Check server storage and database availability.")
    : "";
export const cleanupReason = (value: ResourceCleanup) => reasonLabels.get(value.reason ?? "") ?? "";
export const resourceLabel = (value: AdminResource) =>
  value.type === "slot"
    ? "Receive link"
    : value.parent_slot_id
      ? "Received transfer"
      : "Sent transfer";
export const resourceStatus = (value: AdminResource) =>
  value.status === "revoked"
    ? "Revoked"
    : Date.parse(value.expires_at) <= Date.now()
      ? "Expired"
      : value.status === "complete"
        ? "Ready to download"
        : value.status === "pending"
          ? "Upload unfinished"
          : value.file_count > 0
            ? "Files received"
            : "Waiting for files";

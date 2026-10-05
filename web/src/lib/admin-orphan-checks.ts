import { message as m, LocalizedError } from "./i18n/index.ts";
import { accountRequest } from "./account.ts";

export interface OrphanChecks {
  state: "pending" | "checked" | "degraded";
  scan_pending: boolean;
  last_scan_completed_at?: string;
  pending_directories: number;
  pending_candidates: number;
  busy_count: number;
  failed_count: number;
  unsupported_count: number;
  saturated: boolean;
  unstable: boolean;
  oldest_pending_at?: string;
  scan_error_code?: "scan_failed";
}

const count = (value: unknown, maximum: number) =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= maximum;
const optionalTime = (value: unknown) =>
  value === undefined ||
  (typeof value === "string" &&
    value.length <= 64 &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value) &&
    Number.isFinite(Date.parse(value)));

export async function loadOrphanChecks(signal?: AbortSignal): Promise<OrphanChecks> {
  const value = await accountRequest<OrphanChecks>(
    "/admin/orphan-checks",
    "GET",
    undefined,
    signal,
  );
  if (
    !value ||
    !["pending", "checked", "degraded"].includes(value.state) ||
    typeof value.scan_pending !== "boolean" ||
    typeof value.saturated !== "boolean" ||
    typeof value.unstable !== "boolean" ||
    !count(value.pending_directories, 64) ||
    !count(value.pending_candidates, 256) ||
    !count(value.busy_count, 320) ||
    !count(value.failed_count, 320) ||
    !count(value.unsupported_count, 256) ||
    !optionalTime(value.last_scan_completed_at) ||
    !optionalTime(value.oldest_pending_at) ||
    (value.scan_error_code !== undefined && value.scan_error_code !== "scan_failed")
  )
    throw new LocalizedError(m("invalidOrphanCheckResponse"));
  return value;
}

import { accountRequest } from "./account.ts";

export interface StorageChecks {
  state: "pending" | "checked" | "degraded";
  issue_count: number;
  busy_count: number;
  unavailable_count: number;
  failed_count: number;
  last_scan_completed_at?: string;
  scan_pending: boolean;
  scan_error_code?: "scan_failed";
}

export async function loadStorageChecks(signal?: AbortSignal): Promise<StorageChecks> {
  const value = await accountRequest<StorageChecks>(
    "/admin/storage-checks",
    "GET",
    undefined,
    signal,
  );
  const counts = value && [
    value.issue_count,
    value.busy_count,
    value.unavailable_count,
    value.failed_count,
  ];
  if (
    !value ||
    !["pending", "checked", "degraded"].includes(value.state) ||
    !counts.every((n) => typeof n === "number" && Number.isSafeInteger(n) && n >= 0) ||
    value.issue_count !== value.busy_count + value.unavailable_count + value.failed_count ||
    typeof value.scan_pending !== "boolean" ||
    (value.scan_error_code !== undefined && value.scan_error_code !== "scan_failed") ||
    (value.last_scan_completed_at !== undefined &&
      (typeof value.last_scan_completed_at !== "string" ||
        value.last_scan_completed_at.length > 64 ||
        !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value.last_scan_completed_at) ||
        !Number.isFinite(Date.parse(value.last_scan_completed_at))))
  )
    throw new Error("Invalid file-check response");
  return value;
}

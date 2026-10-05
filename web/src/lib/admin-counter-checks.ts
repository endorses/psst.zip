import { message as m, LocalizedError } from "./i18n/index.ts";
import { accountRequest } from "./account.ts";

export interface CounterChecks {
  state: "pending" | "checked" | "degraded";
  scan_pending: boolean;
  last_scan_completed_at?: string;
  pending_count: number;
  failed_count: number;
  busy_count: number;
  scan_error_code?: "scan_failed";
}

export async function loadCounterChecks(signal?: AbortSignal): Promise<CounterChecks> {
  const value = await accountRequest<CounterChecks>(
    "/admin/counter-checks",
    "GET",
    undefined,
    signal,
  );
  if (
    !value ||
    !["pending", "checked", "degraded"].includes(value.state) ||
    typeof value.scan_pending !== "boolean" ||
    ![value.pending_count, value.failed_count, value.busy_count].every(
      (n) => typeof n === "number" && Number.isSafeInteger(n) && n >= 0 && n <= 64,
    ) ||
    value.failed_count > value.pending_count ||
    value.busy_count > value.pending_count ||
    (value.scan_error_code !== undefined && value.scan_error_code !== "scan_failed") ||
    (value.last_scan_completed_at !== undefined &&
      (typeof value.last_scan_completed_at !== "string" ||
        value.last_scan_completed_at.length > 64 ||
        !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value.last_scan_completed_at) ||
        !Number.isFinite(Date.parse(value.last_scan_completed_at))))
  )
    throw new LocalizedError(m("invalidCounterCheckResponse"));
  return value;
}

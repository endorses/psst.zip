import { message as m, date, number, dateArgument, LocalizedError } from "./i18n/index.ts";
import { transferStateError } from "./incident-state.ts";

/** Enforcement is separate from the traffic chart's optional monitoring allowance. */
export interface TrafficPolicy {
  enforcement_enabled: boolean;
  server_budget_bytes: number;
  default_account_budget_bytes: number;
  basis: "outbound" | "combined";
  cycle_start_day: number;
  upload_bytes_per_second: number;
  download_bytes_per_second: number;
  max_active_streams: number;
  max_streams_per_account: number;
  max_streams_per_ip: number;
  max_streams_per_transfer: number;
  max_streams_per_slot: number;
}
export interface TrafficUsage {
  observed_uploaded_bytes: number;
  observed_downloaded_bytes: number;
  reserved_uploaded_bytes: number;
  reserved_downloaded_bytes: number;
  conservative_uploaded_bytes: number;
  conservative_downloaded_bytes: number;
  charged_bytes: number;
  budget_bytes: number;
  remaining_bytes: number;
}
export interface TrafficStatus {
  usage: TrafficUsage;
  recording_started_at: string;
  cycle: { start: string; end: string };
  state: "ready" | "exhausted" | "unavailable";
}
export interface TrafficSnapshot extends TrafficStatus {
  policy: TrafficPolicy;
  lease_bytes: number;
}
export interface AccountTrafficSnapshot extends TrafficStatus {
  policy: TrafficPolicy;
  account_budget_bytes: number | null;
  effective_budget_bytes: number;
}
export class TrafficLimitError extends LocalizedError {
  code: "traffic_budget_exhausted" | "traffic_accounting_unavailable" | "traffic_policy_changed";
  retryAt: string | null;
  constructor(
    code: "traffic_budget_exhausted" | "traffic_accounting_unavailable" | "traffic_policy_changed",
    retryAt: string | null,
  ) {
    super(
      code === "traffic_policy_changed"
        ? m("serverTransferLimitsChangedSavedFilesAreSafeRetry")
        : code === "traffic_budget_exhausted"
          ? m("thisTransferSTrafficBudgetIsExhaustedValueWait", {
              arg0: retryAt ? m("nextTrafficCycle", { date: dateArgument(retryAt, true) }) : "",
            })
          : m("trafficAccountingIsUnavailableTransfersHaveStoppedUntilThe"),
    );
    this.code = code;
    this.retryAt = retryAt;
  }
}
export function trafficLimitError(code: unknown, retryAt?: unknown): TrafficLimitError | null {
  if (
    code !== "traffic_budget_exhausted" &&
    code !== "traffic_accounting_unavailable" &&
    code !== "traffic_policy_changed"
  )
    return null;
  const date = validUTC(retryAt) ? new Date(retryAt).toISOString() : null;
  return new TrafficLimitError(code, date);
}
const validUTC = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 64 &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(value) &&
  Number.isFinite(Date.parse(value));
const positive = (value: unknown): value is number =>
  Number.isSafeInteger(value) && (value as number) > 0;
const nonnegative = (value: unknown): value is number =>
  Number.isSafeInteger(value) && (value as number) >= 0;
export function validateTrafficPolicy(value: unknown): TrafficPolicy {
  const p = value as TrafficPolicy;
  if (
    !p ||
    typeof p.enforcement_enabled !== "boolean" ||
    !["outbound", "combined"].includes(p.basis) ||
    !positive(p.cycle_start_day) ||
    p.cycle_start_day > 31 ||
    ![
      p.server_budget_bytes,
      p.default_account_budget_bytes,
      p.upload_bytes_per_second,
      p.download_bytes_per_second,
      p.max_active_streams,
      p.max_streams_per_account,
      p.max_streams_per_ip,
      p.max_streams_per_transfer,
      p.max_streams_per_slot,
    ].every(positive) ||
    p.default_account_budget_bytes > p.server_budget_bytes ||
    p.upload_bytes_per_second > 10737418240 ||
    p.download_bytes_per_second > 10737418240 ||
    [
      p.max_active_streams,
      p.max_streams_per_account,
      p.max_streams_per_ip,
      p.max_streams_per_transfer,
      p.max_streams_per_slot,
    ].some((n) => n > 4096)
  )
    throw new LocalizedError(m("enterPositiveWholeByteBudgetsAndRatesStreamLimits"));
  return p;
}
export function validateTrafficStatus(value: unknown): TrafficStatus {
  const s = value as TrafficStatus;
  if (
    !s ||
    !s.usage ||
    !["ready", "exhausted", "unavailable"].includes(s.state) ||
    !validUTC(s.recording_started_at) ||
    !s.cycle ||
    !validUTC(s.cycle.start) ||
    !validUTC(s.cycle.end) ||
    Date.parse(s.cycle.start) >= Date.parse(s.cycle.end) ||
    ![
      s.usage.observed_uploaded_bytes,
      s.usage.observed_downloaded_bytes,
      s.usage.reserved_uploaded_bytes,
      s.usage.reserved_downloaded_bytes,
      s.usage.conservative_uploaded_bytes,
      s.usage.conservative_downloaded_bytes,
      s.usage.charged_bytes,
      s.usage.remaining_bytes,
    ].every(nonnegative) ||
    !positive(s.usage.budget_bytes) ||
    s.usage.remaining_bytes > s.usage.budget_bytes
  )
    throw new LocalizedError(m("theServerReturnedAnUnsupportedTrafficBudgetStatusRefresh"));
  return s;
}
export function validateTrafficSnapshot(value: unknown): TrafficSnapshot {
  const s = value as TrafficSnapshot;
  validateTrafficStatus(s);
  validateTrafficPolicy(s.policy);
  if (!positive(s.lease_bytes) || s.lease_bytes > 65536)
    throw new LocalizedError(m("theServerReturnedAnUnsupportedTrafficReservationSize"));
  return s;
}
export function validateAccountTrafficSnapshot(value: unknown): AccountTrafficSnapshot {
  const s = value as AccountTrafficSnapshot;
  validateTrafficStatus(s);
  validateTrafficPolicy(s.policy);
  if (
    (s.account_budget_bytes !== null && !positive(s.account_budget_bytes)) ||
    !positive(s.effective_budget_bytes)
  )
    throw new LocalizedError(m("theServerReturnedAnUnsupportedAccountTrafficPolicy"));
  return s;
}

/** Interrupted payloads cannot carry structured errors. One control read never retries bytes. */
export async function detectTransferStop(
  resource: string,
  token?: string,
  signal?: AbortSignal,
  direction: "upload" | "download" = "download",
): Promise<Error | null> {
  if (signal?.aborted) return null;
  if (!/^\/(transfers|slots)\/[^/?#]+$/.test(resource)) return null;
  try {
    const response = await fetch(`/api/v1${resource}/traffic-status?direction=${direction}`, {
      credentials: token ? "omit" : "same-origin",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      cache: "no-store",
      signal: AbortSignal.timeout(2500),
    });
    if (signal?.aborted) return null;
    const status = await response.json().catch(() => null);
    const error =
      trafficLimitError(
        status?.code ?? response.headers.get("X-Psst-Error-Code"),
        status?.retry_at ?? response.headers.get("X-Psst-Retry-At"),
      ) ?? transferStateError(status?.code ?? response.headers.get("X-Psst-Error-Code"));
    if (error) return error;
    if (!response.ok || !status) return null;
    if (status.state === "exhausted")
      return trafficLimitError("traffic_budget_exhausted", status.retry_at);
    if (status.state === "unavailable") return trafficLimitError("traffic_accounting_unavailable");
    if (status.state === "paused") return transferStateError("public_transfers_paused");
    if (status.state === "suspended" || status.state === "revoked")
      return transferStateError("resource_revoked");
    return null;
  } catch {
    return null;
  }
}

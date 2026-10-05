import { message as m, number, LocalizedError, type DisplayText } from "./i18n/index.ts";
export interface ResourcePolicy {
  server_storage_bytes: number;
  account_storage_bytes: number;
  server_files: number;
  account_files: number;
  server_transfers: number;
  account_transfers: number;
  server_slots: number;
  account_slots: number;
  max_retention_seconds: number;
  pending_upload_seconds: number;
  reserve_disk_bytes: number;
  reserve_disk_percent: number;
}
export interface ResourceUsage {
  reserved_bytes: number;
  occupied_bytes: number;
  occupied_bytes_estimate?: boolean;
  files: number;
  transfers: number;
  slots: number;
}
export interface CapacitySnapshot {
  checked_at: string;
  state: "ready" | "blocked" | "unknown";
  reason?: string;
  scope: "server" | "account";
  available_wire_bytes: number | null;
  available_files: number;
  available_transfers: number;
  available_slots: number;
}
export interface ResourceSnapshot {
  policy: ResourcePolicy;
  usage: ResourceUsage;
  capacity?: CapacitySnapshot;
}
export const policyFields: {
  key: keyof ResourcePolicy;
  label: DisplayText;
  factor: number;
  min: number;
  max: number;
  group: string;
}[] = [
  {
    key: "server_storage_bytes",
    label: m("serverStorageMiB"),
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 3,
    group: "Storage",
  },
  {
    key: "account_storage_bytes",
    label: m("storagePerAccountMiB"),
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 3,
    group: "Storage",
  },
  {
    key: "server_files",
    label: m("filesOnServer"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_files",
    label: m("filesPerAccount"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "server_transfers",
    label: m("transfersOnServer"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_transfers",
    label: m("transfersPerAccount"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "server_slots",
    label: m("receiveLinksOnServer"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_slots",
    label: m("receiveLinksPerAccount"),
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "max_retention_seconds",
    label: m("maximumRetentionHours"),
    factor: 3600,
    min: 1 / 60,
    max: 365 * 24,
    group: "Retention",
  },
  {
    key: "pending_upload_seconds",
    label: m("unfinishedUploadLifetimeHours"),
    factor: 3600,
    min: 1 / 60,
    max: 365 * 24,
    group: "Retention",
  },
  {
    key: "reserve_disk_bytes",
    label: m("minimumFreeDiskSpaceMiB"),
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 2,
    group: "disk-safety",
  },
  {
    key: "reserve_disk_percent",
    label: m("minimumFreeDiskSpace"),
    factor: 1,
    min: 1,
    max: 50,
    group: "disk-safety",
  },
];

export function validateResourcePolicy(value: unknown): ResourcePolicy {
  if (!value || typeof value !== "object")
    throw new LocalizedError(m("resourcePolicyIsUnavailable"));
  const policy = value as ResourcePolicy;
  for (const field of policyFields) {
    const number = policy[field.key];
    if (
      !Number.isSafeInteger(number) ||
      number < field.min * field.factor ||
      number > field.max * field.factor
    )
      throw new LocalizedError(
        m("chooseASupportedWholeNumberValueForValue", { arg0: field.label }),
      );
  }
  if (policy.pending_upload_seconds > policy.max_retention_seconds)
    throw new LocalizedError(m("unfinishedUploadLifetimeCannotExceedMaximumRetention"));
  return { ...policy };
}

export function validateResourceSnapshot(value: unknown): ResourceSnapshot {
  const snapshot = value as ResourceSnapshot;
  const policy = validateResourcePolicy(snapshot?.policy);
  for (const field of ["reserved_bytes", "occupied_bytes", "files", "transfers", "slots"] as const)
    if (!Number.isSafeInteger(snapshot?.usage?.[field]) || snapshot.usage[field] < 0)
      throw new LocalizedError(m("resourceUsageIsUnavailableRetryToRefreshTheMeasurements"));
  if (snapshot.usage.occupied_bytes > snapshot.usage.reserved_bytes)
    throw new LocalizedError(m("resourceUsageNeedsReconciliationRetryToRefreshTheMeasurements"));
  const capacity = snapshot.capacity;
  if (capacity !== undefined) {
    if (
      !capacity ||
      !["ready", "blocked", "unknown"].includes(capacity.state) ||
      !["server", "account"].includes(capacity.scope) ||
      !Number.isFinite(Date.parse(capacity.checked_at))
    )
      throw new LocalizedError(m("currentUploadCapacityIsUnavailableRetryToRefreshIt"));
    for (const field of ["available_files", "available_transfers", "available_slots"] as const)
      if (!Number.isSafeInteger(capacity[field]) || capacity[field] < 0)
        throw new LocalizedError(m("currentUploadCapacityIsUnavailableRetryToRefreshIt"));
    if (
      capacity.state === "unknown"
        ? capacity.available_wire_bytes !== null
        : !Number.isSafeInteger(capacity.available_wire_bytes) || capacity.available_wire_bytes! < 0
    )
      throw new LocalizedError(m("currentUploadCapacityIsUnavailableRetryToRefreshIt"));
    if (capacity.state === "blocked" && capacity.available_wire_bytes !== 0)
      throw new LocalizedError(m("currentUploadCapacityIsInconsistentRetryToRefreshIt"));
  }
  return {
    policy,
    usage: { ...snapshot.usage },
    ...(capacity ? { capacity: { ...capacity } } : {}),
  };
}

export function remainingCapacity(limit: number, reserved: number): number {
  return Math.max(0, limit - reserved);
}
export function capacityLabel(bytes: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const index =
    bytes > 0 ? Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024))) : 0;
  return `${number(bytes / 1024 ** index, { maximumFractionDigits: 2 })} ${units[index]}`;
}
export function durationLabel(seconds: number): DisplayText {
  if (seconds % 86400 === 0) return m("valueDays", { arg0: seconds / 86400 });
  if (seconds % 3600 === 0) return m("valueHours", { arg0: seconds / 3600 });
  return m("valueMinutes", { arg0: Math.round(seconds / 60) });
}

export class ResourceLimitError extends LocalizedError {}
export function resourceLimitError(code: unknown): ResourceLimitError | null {
  const messages: Record<string, DisplayText> = {
    resource_limit: m("thisAccountOrServerHasReachedItsStorageOr"),
    disk_capacity: m("theServerHasReachedItsFreeDiskSafetyReserve"),
    retention_limit: m("theRequestedLifetimeExceedsThisServerSRetentionPolicy"),
  };
  return typeof code === "string" && messages[code] ? new ResourceLimitError(messages[code]) : null;
}

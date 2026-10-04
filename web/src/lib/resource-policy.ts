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
  label: string;
  factor: number;
  min: number;
  max: number;
  group: string;
}[] = [
  {
    key: "server_storage_bytes",
    label: "Server storage (MiB)",
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 3,
    group: "Storage",
  },
  {
    key: "account_storage_bytes",
    label: "Storage per account (MiB)",
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 3,
    group: "Storage",
  },
  {
    key: "server_files",
    label: "Files on server",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_files",
    label: "Files per account",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "server_transfers",
    label: "Transfers on server",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_transfers",
    label: "Transfers per account",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "server_slots",
    label: "Receive links on server",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "account_slots",
    label: "Receive links per account",
    factor: 1,
    min: 1,
    max: 1000000,
    group: "Objects",
  },
  {
    key: "max_retention_seconds",
    label: "Maximum retention (hours)",
    factor: 3600,
    min: 1 / 60,
    max: 365 * 24,
    group: "Retention",
  },
  {
    key: "pending_upload_seconds",
    label: "Unfinished upload lifetime (hours)",
    factor: 3600,
    min: 1 / 60,
    max: 365 * 24,
    group: "Retention",
  },
  {
    key: "reserve_disk_bytes",
    label: "Minimum free disk space (MiB)",
    factor: 1024 ** 2,
    min: 1,
    max: 1024 ** 2,
    group: "Disk safety",
  },
  {
    key: "reserve_disk_percent",
    label: "Minimum free disk space (%)",
    factor: 1,
    min: 1,
    max: 50,
    group: "Disk safety",
  },
];

export function validateResourcePolicy(value: unknown): ResourcePolicy {
  if (!value || typeof value !== "object") throw new Error("Resource policy is unavailable.");
  const policy = value as ResourcePolicy;
  for (const field of policyFields) {
    const number = policy[field.key];
    if (
      !Number.isSafeInteger(number) ||
      number < field.min * field.factor ||
      number > field.max * field.factor
    )
      throw new Error(`Choose a supported whole-number value for ${field.label.toLowerCase()}.`);
  }
  if (policy.pending_upload_seconds > policy.max_retention_seconds)
    throw new Error("Unfinished upload lifetime cannot exceed maximum retention.");
  return { ...policy };
}

export function validateResourceSnapshot(value: unknown): ResourceSnapshot {
  const snapshot = value as ResourceSnapshot;
  const policy = validateResourcePolicy(snapshot?.policy);
  for (const field of ["reserved_bytes", "occupied_bytes", "files", "transfers", "slots"] as const)
    if (!Number.isSafeInteger(snapshot?.usage?.[field]) || snapshot.usage[field] < 0)
      throw new Error("Resource usage is unavailable. Retry to refresh the measurements.");
  if (snapshot.usage.occupied_bytes > snapshot.usage.reserved_bytes)
    throw new Error("Resource usage needs reconciliation. Retry to refresh the measurements.");
  const capacity = snapshot.capacity;
  if (capacity !== undefined) {
    if (
      !capacity ||
      !["ready", "blocked", "unknown"].includes(capacity.state) ||
      !["server", "account"].includes(capacity.scope) ||
      !Number.isFinite(Date.parse(capacity.checked_at))
    )
      throw new Error("Current upload capacity is unavailable. Retry to refresh it.");
    for (const field of ["available_files", "available_transfers", "available_slots"] as const)
      if (!Number.isSafeInteger(capacity[field]) || capacity[field] < 0)
        throw new Error("Current upload capacity is unavailable. Retry to refresh it.");
    if (
      capacity.state === "unknown"
        ? capacity.available_wire_bytes !== null
        : !Number.isSafeInteger(capacity.available_wire_bytes) || capacity.available_wire_bytes! < 0
    )
      throw new Error("Current upload capacity is unavailable. Retry to refresh it.");
    if (capacity.state === "blocked" && capacity.available_wire_bytes !== 0)
      throw new Error("Current upload capacity is inconsistent. Retry to refresh it.");
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
  return `${Number((bytes / 1024 ** index).toFixed(2))} ${units[index]}`;
}
export function durationLabel(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400} days`;
  if (seconds % 3600 === 0) return `${seconds / 3600} hours`;
  return `${Math.round(seconds / 60)} minutes`;
}

export class ResourceLimitError extends Error {}
export function resourceLimitError(code: unknown): ResourceLimitError | null {
  const messages: Record<string, string> = {
    resource_limit:
      "This account or server has reached its storage or object limit. Remove unused transfers or contact the administrator before retrying.",
    disk_capacity:
      "The server has reached its free-disk safety reserve. Contact its administrator before retrying.",
    retention_limit:
      "The requested lifetime exceeds this server's retention policy. Refresh the server limits before retrying.",
  };
  return typeof code === "string" && messages[code] ? new ResourceLimitError(messages[code]) : null;
}

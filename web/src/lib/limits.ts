// Whole-file AES-GCM remains bounded. The server controls uploads; a lower new
// upload setting must not make already-created links unreadable.
export const MAX_FILE_BYTES = 1024 ** 4;
export const DEFAULT_FILE_BYTES = 25 * 1024 * 1024;
export const MAX_BUFFERED_BYTES = 25 * 1024 * 1024;
export const MAX_ZIP_BYTES = 25 * 1024 * 1024;
export function fileLimitLabel(bytes: number): string {
  return `${Number((bytes / 1024 / 1024).toFixed(2))} MiB`;
}
export function assertFileSize(size: number, limit = MAX_FILE_BYTES): void {
  if (!Number.isSafeInteger(size) || size < 0 || size > limit || size > MAX_FILE_BYTES) {
    throw new Error(`Files must be no larger than ${fileLimitLabel(limit)}.`);
  }
}
export interface ServerLimits {
  max_file_size: number;
  max_file_size_ceiling: number;
  resource_policy?: ResourcePolicy;
}
export async function loadServerLimits(signal?: AbortSignal): Promise<ServerLimits> {
  const response = await fetch("/api/v1/config", {
    credentials: "omit",
    cache: "no-store",
    signal: signal ?? AbortSignal.timeout(10000),
  });
  if (!response.ok)
    throw new Error("Could not load this server's file limit. Check your connection and retry.");
  const config = await response.json();
  const value: unknown = config.max_file_size;
  const ceiling: unknown = config.max_file_size_ceiling ?? value;
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < 1 ||
    typeof ceiling !== "number" ||
    !Number.isSafeInteger(ceiling) ||
    ceiling < value ||
    ceiling > MAX_FILE_BYTES
  )
    throw new Error("This server's file limit is not supported by this app.");
  return {
    max_file_size: value,
    max_file_size_ceiling: ceiling as number,
    ...(config.resource_policy === undefined
      ? {}
      : { resource_policy: validateResourcePolicy(config.resource_policy) }),
  };
}

export async function loadUploadLimit(signal?: AbortSignal): Promise<number> {
  return (await loadServerLimits(signal)).max_file_size;
}
import { validateResourcePolicy, type ResourcePolicy } from "./resource-policy.ts";

/** Recipient policy is independent of a scanned server's upload settings. */
export const MAX_RECEIVE_TOTAL_BYTES = 1024 ** 4;
export const RECEIVE_RESERVE_BYTES = 256 * 1024 * 1024;
export const DESTINATION_SPACE_NOTICE =
  "This browser cannot check free space in your download folder. Allow room for the files and temporary copies, with at least 256 MiB left free.";

export class ReceiveStorageError extends Error {}

/** Origin quota is advisory browser storage headroom, not destination disk space. */
export function checkBrowserStorage(
  estimate: StorageEstimate,
  remaining: number,
  handoffBytes = remaining,
): void {
  const { quota, usage } = estimate;
  if (
    !Number.isSafeInteger(remaining) ||
    remaining < 0 ||
    remaining > MAX_RECEIVE_TOTAL_BYTES ||
    !Number.isSafeInteger(handoffBytes) ||
    handoffBytes < remaining ||
    handoffBytes > MAX_RECEIVE_TOTAL_BYTES ||
    typeof quota !== "number" ||
    !Number.isSafeInteger(quota) ||
    quota < 0 ||
    typeof usage !== "number" ||
    !Number.isSafeInteger(usage) ||
    usage < 0 ||
    usage > quota
  )
    throw new ReceiveStorageError(
      "Could not check browser storage. Use a browser with a save-file picker or the psst.zip mobile app.",
    );
  // Writable staging and browser handoff may need two copies. Existing origin
  // usage includes earlier saves waiting for handoff/cleanup.
  if (remaining + handoffBytes > quota - usage - RECEIVE_RESERVE_BYTES)
    throw new ReceiveStorageError(
      "Not enough browser storage to save this file while keeping 256 MiB available. Free storage or use the psst.zip mobile app.",
    );
}

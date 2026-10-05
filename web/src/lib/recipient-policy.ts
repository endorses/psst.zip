import { message as m, LocalizedError } from "./i18n/index.ts";
/** Recipient policy is independent of a scanned server's upload settings. */
export const MAX_RECEIVE_TOTAL_BYTES = 1024 ** 4;
export const RECEIVE_RESERVE_BYTES = 256 * 1024 * 1024;
export const DESTINATION_SPACE_NOTICE = m("thisBrowserCannotCheckFreeSpaceInYourDownload");

export class ReceiveStorageError extends LocalizedError {}

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
    throw new ReceiveStorageError(m("couldNotCheckBrowserStorageUseABrowserWith"));
  // Writable staging and browser handoff may need two copies. Existing origin
  // usage includes earlier saves waiting for handoff/cleanup.
  if (remaining + handoffBytes > quota - usage - RECEIVE_RESERVE_BYTES)
    throw new ReceiveStorageError(m("notEnoughBrowserStorageToSaveThisFileWhile"));
}

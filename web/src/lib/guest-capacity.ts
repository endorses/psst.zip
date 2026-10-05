import { message as m, LocalizedError } from "./i18n/index.ts";
import { wireSize, FILE_CHUNK_SIZE } from "./chunked-files.ts";
import type { FileManifestEntry } from "./crypto.ts";
import { assertFileSize, MAX_FILE_BYTES } from "./limits.ts";
import { validSharedTitle } from "./link-title.ts";

export interface GuestCapacity {
  checked_at: string;
  state: "ready" | "blocked" | "unknown";
  reason?: "link_limit" | "capacity_limit" | "capacity_unavailable";
  available_wire_bytes: number | null;
  available_files: number | null;
  manifest_reserve_bytes: number;
}
export interface SlotAvailability {
  id: string;
  title?: string | null;
  receive_protocol: number;
  recipient_public_key: string;
  max_files: number;
  remaining_files: number | null;
  remaining_bytes: number;
  remaining_transfers: number;
  available: boolean;
  upload_capacity: GuestCapacity;
}
export class GuestCapacityError extends LocalizedError {}
type SelectedFile = { size: number; name?: string; type?: string };
export function fileManifestEntry(
  file: SelectedFile,
  blobID: string,
  encryptionID: string,
): FileManifestEntry {
  return {
    name: file.name ?? "",
    size: file.size,
    mime_type: file.type || "application/octet-stream",
    blob_id: blobID,
    encoding: "chunked-v1",
    chunk_size: FILE_CHUNK_SIZE,
    encryption_id: encryptionID,
  };
}
/** UUID/context contents vary, but their encoded lengths are fixed before allocation. */
export function guestManifestWireSize(files: readonly SelectedFile[]): number {
  const manifest = {
    files: files.map((file) =>
      fileManifestEntry(file, "00000000-0000-0000-0000-000000000000", "0".repeat(32)),
    ),
  };
  return new TextEncoder().encode(JSON.stringify(manifest)).length + 116;
}
export const CAPACITY_UNAVAILABLE = m("couldNotCheckUploadSpaceRefreshAvailabilityAndTry");
const integer = (value: unknown, max = Number.MAX_SAFE_INTEGER): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= max;

export function validateSlotAvailability(value: unknown, slotId: string): SlotAvailability {
  const v = value as SlotAvailability | null;
  const c = v?.upload_capacity;
  if (
    !v ||
    v.id !== slotId ||
    !validSharedTitle(v.title) ||
    v.receive_protocol !== 2 ||
    typeof v.recipient_public_key !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/.test(v.recipient_public_key) ||
    typeof v.available !== "boolean" ||
    !integer(v.max_files, 2147483647) ||
    !(v.remaining_files === null || integer(v.remaining_files, 2147483647)) ||
    (v.max_files === 0
      ? v.remaining_files !== null
      : v.remaining_files === null || v.remaining_files > v.max_files) ||
    !integer(v.remaining_bytes) ||
    !integer(v.remaining_transfers, 2147483647) ||
    !c ||
    typeof c.checked_at !== "string" ||
    c.checked_at.length > 64 ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(c.checked_at) ||
    !Number.isFinite(Date.parse(c.checked_at)) ||
    !integer(c.manifest_reserve_bytes, 1048576) ||
    c.manifest_reserve_bytes < 1 ||
    !(
      (c.state === "ready" &&
        c.reason === undefined &&
        integer(c.available_files, 100) &&
        c.available_files > 0 &&
        integer(c.available_wire_bytes, 2 ** 50) &&
        c.available_wire_bytes >= 60 &&
        c.available_files <= Math.floor(c.available_wire_bytes / 60)) ||
      (c.state === "blocked" &&
        ["link_limit", "capacity_limit"].includes(c.reason ?? "") &&
        c.available_files === 0 &&
        c.available_wire_bytes === 0) ||
      (c.state === "unknown" &&
        c.reason === "capacity_unavailable" &&
        c.available_files === null &&
        c.available_wire_bytes === null)
    )
  )
    throw new GuestCapacityError(CAPACITY_UNAVAILABLE);
  return v;
}

export function assertGuestFresh(availability: SlotAvailability, now = Date.now()) {
  const timestamp = Date.parse(availability.upload_capacity.checked_at);
  if (!Number.isFinite(timestamp) || Math.abs(now - timestamp) > 120000)
    throw new GuestCapacityError(m("uploadAvailabilityIsOutOfDateRefreshAvailabilityBefore"));
}

export function assertGuestKey(availability: SlotAvailability, publicKey: string) {
  if (availability.recipient_public_key !== publicKey)
    throw new GuestCapacityError(m("theReceiveLinkSEncryptionKeyDoesNotMatch"));
}

export function selectionWireSize(files: readonly SelectedFile[], limit = MAX_FILE_BYTES): number {
  if (files.length > 100) throw new GuestCapacityError(m("chooseNoMoreThanFilesPerTransfer"));
  let total = 0;
  for (const file of files) {
    assertFileSize(file.size, limit);
    total += wireSize(file.size);
    if (!Number.isSafeInteger(total))
      throw new GuestCapacityError(m("thisSelectionIsTooLargeRemoveSomeFilesAnd"));
  }
  return total;
}

export function assertGuestSelection(
  availability: SlotAvailability,
  files: readonly SelectedFile[],
  limit: number,
): number {
  assertGuestFresh(availability);
  const total = selectionWireSize(files, limit);
  const c = availability.upload_capacity;
  if (
    !availability.available ||
    availability.remaining_transfers === 0 ||
    c.reason === "link_limit"
  )
    throw new GuestCapacityError(m("thisReceiveLinkHasReachedItsUploadAllowanceAsk"));
  if (c.state === "unknown") throw new GuestCapacityError(CAPACITY_UNAVAILABLE);
  if (c.state === "blocked")
    throw new GuestCapacityError(m("uploadSpaceIsCurrentlyUnavailableRefreshAvailabilityLaterOr"));
  if (
    files.length > c.available_files! ||
    (availability.remaining_files !== null && files.length > availability.remaining_files)
  )
    throw new GuestCapacityError(m("tooManyFilesForThisReceiveLinkSCurrent"));
  if (total > c.available_wire_bytes! || total > availability.remaining_bytes)
    throw new GuestCapacityError(m("theseFilesExceedTheSpaceCurrentlyAvailableRemoveSome"));
  if (files.length && guestManifestWireSize(files) > c.manifest_reserve_bytes)
    throw new GuestCapacityError(m("theseFileDetailsExceedThisLinkSAllowanceRemove"));
  return total;
}

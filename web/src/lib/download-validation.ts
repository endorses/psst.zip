import { message as m, LocalizedError } from "./i18n/index.ts";
import type { Manifest } from "./crypto.ts";
import type { TransferInfo } from "./api.ts";
import { wireSize } from "./chunked-files.ts";
import { validSharedTitle } from "./link-title.ts";

/** Cross-check server metadata against the authenticated, bounded manifest. */
export function validateDownload(info: TransferInfo, id: string, manifest: Manifest): void {
  const invalid = () => {
    throw new LocalizedError(m("manifestSizeMismatchOrInvalidTransferDetails"));
  };
  if (
    !info ||
    typeof info.id !== "string" ||
    info.id.toLowerCase() !== id.toLowerCase() ||
    !["complete", "exhausted"].includes(info.status) ||
    !validSharedTitle(info.title) ||
    info.file_count !== manifest.files.length ||
    !Number.isSafeInteger(info.total_size) ||
    info.total_size !== manifest.files.reduce((sum, file) => sum + wireSize(file.size), 0)
  )
    invalid();
  const limit = info.max_downloads ?? 0;
  if (!Number.isSafeInteger(limit) || limit < 0 || limit > 2147483647) invalid();
  if (info.files === undefined && limit === 0) return; // Additive counters on older unlimited servers.
  if (!Array.isArray(info.files) || info.files.length !== manifest.files.length) invalid();
  const actual = new Map(
    info.files!.map((file) => [typeof file?.id === "string" ? file.id.toLowerCase() : "", file]),
  );
  if (actual.size !== manifest.files.length) invalid();
  for (const entry of manifest.files) {
    const file = actual.get(entry.blob_id.toLowerCase());
    if (
      !file ||
      file.size !== wireSize(entry.size) ||
      (file.download_count != null &&
        (!Number.isSafeInteger(file.download_count) || file.download_count < 0)) ||
      (file.remaining_downloads != null &&
        (!Number.isSafeInteger(file.remaining_downloads) ||
          limit === 0 ||
          file.remaining_downloads < 0 ||
          file.remaining_downloads > limit))
    )
      invalid();
  }
}

// The current wire format uses whole-file AES-GCM. Keep buffering bounded until
// all clients support an authenticated streaming format.
export const MAX_BUFFERED_BYTES = 25 * 1024 * 1024;
export const FILE_SIZE_NOTICE = "Up to 25 MiB per file.";

export function assertFileSize(size: number): void {
  if (!Number.isSafeInteger(size) || size < 0 || size > MAX_BUFFERED_BYTES) {
    throw new Error("Files must be no larger than 25 MiB.");
  }
}

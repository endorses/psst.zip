/** Shared descriptive metadata, never an automatic decrypted filename. */
export function normalizeLinkTitle(value: string | null | undefined): string | null {
  if (value == null) return null;
  if (typeof value !== "string") throw new Error("Enter a valid link title.");
  if (/\p{Cc}|\p{Cs}/u.test(value))
    throw new Error("Link titles cannot contain control characters.");
  const title = value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
  if (!title) return null;
  if (Array.from(title).length > 200 || new TextEncoder().encode(title).length > 800)
    throw new Error("Use no more than 200 characters for the link title.");
  return title;
}

export function validSharedTitle(value: unknown): boolean {
  try {
    return value == null || (typeof value === "string" && normalizeLinkTitle(value) === value);
  } catch {
    return false;
  }
}

export function downloadLinkExhausted(value: {
  status?: string;
  inactive_reason?: string | null;
}): boolean {
  return value.status === "exhausted" || value.inactive_reason === "download_limit";
}

export const DOWNLOAD_LINK_CLOSED =
  "This link has reached its download limit. Ask the sender for a new link.";

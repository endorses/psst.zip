import { message as m, LocalizedError } from "./i18n/index.ts";
/** Shared descriptive metadata, never an automatic decrypted filename. */
export function normalizeLinkTitle(value: string | null | undefined): string | null {
  if (value == null) return null;
  if (typeof value !== "string") throw new LocalizedError(m("enterAValidLinkTitle"));
  if (/\p{Cc}|\p{Cs}/u.test(value))
    throw new LocalizedError(m("linkTitlesCannotContainControlCharacters"));
  const title = value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
  if (!title) return null;
  if (Array.from(title).length > 200 || new TextEncoder().encode(title).length > 800)
    throw new LocalizedError(m("useNoMoreThanCharactersForTheLinkTitle"));
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

export const DOWNLOAD_LINK_CLOSED = m("thisLinkHasReachedItsDownloadLimitAskThe");

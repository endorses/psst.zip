import { accountRequest, type Resource } from "./account.ts";
import { message as m, LocalizedError } from "./i18n/index.ts";
import { inboxUUID, validInboxCursor } from "./inbox-page.ts";
import { validHistoryResource } from "./resource-history.ts";

export type HistoryKind = "transfer" | "slot";
export type HistoryFilter = HistoryKind | undefined;
export interface HistoryChange {
  kind: HistoryKind;
  id: string;
  revision: number;
  action: "upsert" | "remove";
  resource?: Resource;
}
export interface HistoryChanges {
  version: 1;
  generation: string;
  changes: HistoryChange[];
  next_cursor: string;
  has_more: boolean;
}
export const HISTORY_SYNC_INTERVAL = 10_000;
export const HISTORY_SYNC_BATCHES = 4;
export const HISTORY_RESPONSE_BYTES = 1024 * 1024;
export const validRevision = (value: unknown): value is number =>
  Number.isSafeInteger(value) && (value as number) >= 0;
const invalid = () => new LocalizedError(m("thisServerReturnedAnUnsupportedResourcePageRefreshOr"));
export function validateHistoryChanges(
  value: unknown,
  cursor: string,
  generation: string,
): HistoryChanges {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalid();
  const result = value as HistoryChanges;
  if (
    result.version !== 1 ||
    result.generation !== generation ||
    !inboxUUID.test(result.generation) ||
    !validInboxCursor(result.next_cursor) ||
    typeof result.has_more !== "boolean" ||
    !Array.isArray(result.changes) ||
    result.changes.length > 50 ||
    (result.has_more && result.next_cursor === cursor)
  )
    throw invalid();
  const identities = new Set<string>();
  for (const change of result.changes) {
    if (
      !change ||
      !["transfer", "slot"].includes(change.kind) ||
      !inboxUUID.test(change.id) ||
      !validRevision(change.revision) ||
      change.revision === 0 ||
      !["upsert", "remove"].includes(change.action)
    )
      throw invalid();
    const identity = `${change.kind}:${change.id}`;
    if (identities.has(identity)) throw invalid();
    identities.add(identity);
    if (change.action === "upsert") {
      if (
        !validHistoryResource(change.resource, change.kind === "slot") ||
        change.resource!.id !== change.id ||
        change.resource!.revision !== change.revision
      )
        throw invalid();
    } else if (change.resource !== undefined && change.resource !== null) throw invalid();
  }
  if (result.changes.length && result.next_cursor === cursor) throw invalid();
  return result;
}
export async function loadHistoryChanges(
  cursor: string,
  generation: string,
  signal: AbortSignal,
): Promise<HistoryChanges> {
  if (!validInboxCursor(cursor) || !inboxUUID.test(generation)) throw invalid();
  const query = new URLSearchParams({ cursor, limit: "50" });
  return validateHistoryChanges(
    await accountRequest<unknown>(
      `/auth/history/changes?${query}`,
      "GET",
      undefined,
      AbortSignal.any([signal, AbortSignal.timeout(10_000)]),
      HISTORY_RESPONSE_BYTES,
    ),
    cursor,
    generation,
  );
}
/** Capability failure is a transport failure, never permission to silently downgrade. */
export async function historySyncSupported(signal: AbortSignal): Promise<boolean> {
  const config = await accountRequest<{ history_sync_version?: unknown }>(
    "/config",
    "GET",
    undefined,
    signal,
    128 * 1024,
  );
  if (!config || typeof config !== "object" || Array.isArray(config)) throw invalid();
  if (config.history_sync_version === undefined) return false;
  if (config.history_sync_version !== 1) throw invalid();
  return true;
}
export function historyRetryDelay(failures: number, retryAfter = 0, random = Math.random): number {
  return Math.max(
    retryAfter,
    Math.min(60_000, 10_000 * 2 ** Math.min(3, Math.max(0, failures - 1))) +
      Math.floor(random() * 500),
  );
}

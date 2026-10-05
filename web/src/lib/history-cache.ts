import { message as m, LocalizedError } from "./i18n/index.ts";
import { type Resource } from "./account.ts";
import { inboxUUID, validInboxCursor } from "./inbox-page.ts";
import {
  validHistoryResource,
  validateResourcePage,
  type ResourcePage,
} from "./resource-history.ts";
import {
  validateHistoryChanges,
  type HistoryChanges,
  type HistoryKind,
  type HistoryFilter,
} from "./history-sync.ts";

// This database contains disposable server facts only. Private links/keys stay in
// their existing stores and are never included in eviction/reset transactions.
const NAME = "psst.server-history.v1";
const ACCOUNT_LIMIT = 2000,
  GLOBAL_LIMIT = 10_000,
  WINDOW_LIMIT = 100,
  WINDOW_ROWS = 200,
  ROW_BYTES = 16 * 1024;
export type HistoryScope = string;
export function historyScope(server: string, account: string): HistoryScope {
  return JSON.stringify([new URL(server).origin, account]);
}
interface Fact {
  scope: string;
  generation?: string;
  key: [string, HistoryKind, string];
  revision: number;
  resource?: Resource;
  touched: number;
}
interface Key {
  kind: HistoryKind;
  id: string;
  created: string;
  allAfter?: string;
  kindAfter?: string;
}
interface Window {
  scope: string;
  key: [string, string, string];
  keys: Key[];
  next: string | null;
  generation?: string;
  stale: boolean;
  touched: number;
}
export interface HistorySyncState {
  scope: string;
  cursor: string;
  generation: string;
}
export interface CachedHistoryPage extends ResourcePage {
  stale: boolean;
}
let opened: Promise<IDBDatabase> | undefined;
function compactResource(resource: Resource): Resource {
  const allowed = [
    "id",
    "revision",
    "history_after",
    "history_after_kind",
    "title",
    "inactive_reason",
    "status",
    "file_count",
    "download_count",
    "max_downloads",
    "max_files",
    "reserved_files",
    "completed_files",
    "remaining_files",
    "receive_protocol",
    "recipient_public_key",
    "total_size",
    "summary",
    "downloaded_at",
    "completed_at",
    "expires_at",
    "created_at",
    "owner_id",
    "transfers",
    "files",
    "has_manifest",
  ];
  const result = Object.fromEntries(
    Object.entries(resource).filter(([key]) => allowed.includes(key)),
  ) as unknown as Resource;
  result.summary = Object.fromEntries(
    Object.entries(resource.summary!).filter(([key]) =>
      ["state", "completed_files", "file_count", "total_size"].includes(key),
    ),
  ) as Resource["summary"];
  if (new TextEncoder().encode(JSON.stringify(result)).byteLength > ROW_BYTES) throw failure();
  return result;
}
export class HistoryCacheCorruption extends LocalizedError {
  constructor() {
    super(m("couldNotSaveOrReadLocalHistoryOnThis"));
  }
}
const corrupt = () => new HistoryCacheCorruption();
const failure = () => new LocalizedError(m("couldNotSaveOrReadLocalHistoryOnThis"));
function database(): Promise<IDBDatabase> {
  if (opened) return opened;
  opened = new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(NAME, 1);
    const timer = setTimeout(() => reject(failure()), 10_000);
    request.onupgradeneeded = () => {
      const db = request.result;
      const facts = db.createObjectStore("facts", { keyPath: "key" });
      facts.createIndex("scope", "scope");
      facts.createIndex("touched", "touched");
      facts.createIndex("scopeTouched", ["scope", "touched"]);
      const windows = db.createObjectStore("windows", { keyPath: "key" });
      windows.createIndex("scope", "scope");
      windows.createIndex("touched", "touched");
      db.createObjectStore("sync", { keyPath: "scope" }).createIndex("touched", "touched");
    };
    request.onerror = request.onblocked = () => {
      clearTimeout(timer);
      reject(failure());
    };
    request.onsuccess = () => {
      clearTimeout(timer);
      request.result.onversionchange = () => {
        request.result.close();
        opened = undefined;
      };
      resolve(request.result);
    };
  }).catch((error) => {
    opened = undefined;
    throw error;
  });
  return opened;
}
const get = <T>(request: IDBRequest<T>): Promise<T> =>
  new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(failure());
  });
async function transaction<T>(
  mode: IDBTransactionMode,
  operation: (tx: IDBTransaction) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  signal?.throwIfAborted();
  const db = await database();
  signal?.throwIfAborted();
  const tx = db.transaction(["facts", "windows", "sync"], mode);
  const abort = () => {
    try {
      tx.abort();
    } catch {}
  };
  signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(abort, 10_000);
  const completed = new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onabort = tx.onerror = () => reject(failure());
  });
  // Register rejection handling before scheduling requests; rollback can precede
  // operation completion (quota failures, cancellation, malformed cache data).
  void completed.catch(() => {});
  try {
    const result = await operation(tx);
    await completed;
    signal?.throwIfAborted();
    return result;
  } catch (error) {
    abort();
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}
function windowKey(scope: string, filter: HistoryFilter, cursor: string): [string, string, string] {
  return [scope, filter ?? "", cursor];
}
const order = (a: Key, b: Key) =>
  b.created.localeCompare(a.created) || b.id.localeCompare(a.id) || b.kind.localeCompare(a.kind);
function keys(page: ResourcePage): Key[] {
  return [
    ...page.transfers.map((r) => ({
      kind: "transfer" as const,
      id: r.id,
      created: r.created_at!,
      allAfter: r.history_after,
      kindAfter: r.history_after_kind,
    })),
    ...page.slots.map((r) => ({
      kind: "slot" as const,
      id: r.id,
      created: r.created_at!,
      allAfter: r.history_after,
      kindAfter: r.history_after_kind,
    })),
  ].sort(order);
}
// Overflow is paged locally before following the server's original continuation.
// Thus prepending arrivals cannot skip the tail of an already loaded page.
function virtualCursor(base: string, after: Key): string {
  return "local." + btoa(JSON.stringify({ base, after }));
}
function validKey(key: Key): boolean {
  return (
    !!key &&
    ["transfer", "slot"].includes(key.kind) &&
    typeof key.id === "string" &&
    inboxUUID.test(key.id) &&
    typeof key.created === "string" &&
    key.created.length <= 40 &&
    Number.isFinite(Date.parse(key.created)) &&
    (key.allAfter === undefined || validInboxCursor(key.allAfter)) &&
    (key.kindAfter === undefined || validInboxCursor(key.kindAfter))
  );
}
function validateWindow(window: Window, scope: string): void {
  if (
    !window ||
    window.scope !== scope ||
    !Array.isArray(window.key) ||
    window.key[0] !== scope ||
    window.key.length !== 3 ||
    !["", "transfer", "slot"].includes(window.key[1]) ||
    typeof window.key[2] !== "string" ||
    window.key[2].length > 2048 ||
    !Array.isArray(window.keys) ||
    window.keys.length > WINDOW_ROWS ||
    !window.keys.every(validKey) ||
    (window.next !== null && !validInboxCursor(window.next)) ||
    typeof window.stale !== "boolean" ||
    !Number.isSafeInteger(window.touched) ||
    (window.generation !== undefined && !inboxUUID.test(window.generation))
  )
    throw corrupt();
}
function validateState(state: HistorySyncState | undefined, scope: string): void {
  if (
    state &&
    (state.scope !== scope ||
      !validInboxCursor(state.cursor) ||
      typeof state.generation !== "string" ||
      !inboxUUID.test(state.generation))
  )
    throw corrupt();
}
function validateFact(fact: Fact | undefined, scope: string, key: Key): void {
  if (!fact) return;
  if (
    fact.scope !== scope ||
    !Array.isArray(fact.key) ||
    fact.key.length !== 3 ||
    fact.key[0] !== scope ||
    fact.key[1] !== key.kind ||
    fact.key[2] !== key.id ||
    !Number.isSafeInteger(fact.revision) ||
    fact.revision < 0 ||
    !Number.isSafeInteger(fact.touched) ||
    fact.touched < 0 ||
    (fact.resource && fact.generation !== undefined && fact.resource.revision !== fact.revision) ||
    (fact.generation !== undefined && !inboxUUID.test(fact.generation)) ||
    (fact.resource &&
      (fact.resource.id !== key.id ||
        !validHistoryResource(fact.resource, key.kind === "slot") ||
        new TextEncoder().encode(JSON.stringify(fact.resource)).byteLength > ROW_BYTES))
  )
    throw corrupt();
}
function cursorParts(cursor: string): { base: string; after?: Key } {
  if (!cursor.startsWith("local.")) return { base: cursor };
  if (cursor.length > 2048) throw failure();
  const value = JSON.parse(atob(cursor.slice(6))) as { base: string; after: Key };
  if (typeof value.base !== "string" || value.base.length > 512 || !validKey(value.after))
    throw corrupt();
  return value;
}
export async function cachedHistoryState(
  scope: string,
  signal?: AbortSignal,
): Promise<HistorySyncState | undefined> {
  return transaction(
    "readonly",
    async (tx) => {
      const state: HistorySyncState | undefined = await get(tx.objectStore("sync").get(scope));
      validateState(state, scope);
      return state;
    },
    signal,
  );
}
export async function cachedHistoryPage(
  scope: string,
  filter: HistoryFilter,
  cursor: string,
  signal?: AbortSignal,
): Promise<CachedHistoryPage | null> {
  const { base, after } = cursorParts(cursor);
  return transaction(
    "readonly",
    async (tx) => {
      const exact: Window | undefined = cursor.startsWith("local.")
        ? await get(tx.objectStore("windows").get(windowKey(scope, filter, cursor)))
        : undefined;
      const window: Window | undefined =
        exact ?? (await get(tx.objectStore("windows").get(windowKey(scope, filter, base))));
      if (!window) return null;
      validateWindow(window, scope);
      const selected = window.keys
        .filter((key) => !!exact || !after || order(key, after) > 0)
        .slice(0, 51);
      // A rebuilt newest window does not prove coverage beyond its own tail.
      // Keep the anchor carried by the local cursor and seek that bounded page.
      if (after && !exact && selected.length === 0 && window.next !== null) return null;
      const values: (Fact | undefined)[] = await Promise.all(
        selected
          .slice(0, 50)
          .map((key) => get(tx.objectStore("facts").get([scope, key.kind, key.id]))),
      );
      const page: CachedHistoryPage = {
        transfers: [],
        slots: [],
        paginated: true,
        next_cursor:
          selected.length > 50
            ? exact
              ? ((filter ? selected[49].kindAfter : selected[49].allAfter) ?? window.next)
              : virtualCursor(base, selected[49])
            : window.next,
        stale: window.stale,
        generation: window.generation,
      };
      values.forEach((fact, index) => validateFact(fact, scope, selected[index]));
      if (values.some((fact) => !fact)) page.stale = true;
      values.forEach((fact, index) => {
        if (fact?.resource)
          (selected[index].kind === "transfer" ? page.transfers : page.slots).push(fact.resource);
      });
      return page;
    },
    signal,
  );
}
async function scopedWindows(tx: IDBTransaction, scope: string): Promise<Window[]> {
  const windows: Window[] = await get(
    tx
      .objectStore("windows")
      .index("scope")
      .getAll(scope, WINDOW_LIMIT + 1),
  );
  windows.forEach((window) => validateWindow(window, scope));
  return windows;
}
async function prune(tx: IDBTransaction, scope: string) {
  const facts = tx.objectStore("facts");
  const scopedCount = await get(facts.index("scope").count(scope));
  const globalCount = await get(facts.count());
  const remove = async (
    index: IDBIndex,
    query: IDBValidKey | IDBKeyRange | null,
    count: number,
  ) => {
    if (count <= 0) return;
    const doomed: Fact[] = await get(index.getAll(query, Math.min(count, 256)));
    const scopes = new Set(doomed.map((fact) => fact.key[0]));
    for (const fact of doomed) facts.delete(fact.key);
    // Eviction invalidates coverage and the checkpoint. Bootstrap on next sync
    // prevents forgetting a removal revision and accepting a stale page later.
    for (const owner of scopes) {
      tx.objectStore("sync").delete(owner);
      for (const window of await scopedWindows(tx, owner)) {
        window.stale = true;
        window.keys = window.keys.filter(
          (key) =>
            !doomed.some(
              (fact) => fact.key[0] === owner && fact.key[1] === key.kind && fact.key[2] === key.id,
            ),
        );
        tx.objectStore("windows").put(window);
      }
    }
  };
  await remove(
    facts.index("scopeTouched"),
    IDBKeyRange.bound([scope, 0], [scope, Number.MAX_SAFE_INTEGER]),
    Math.max(0, scopedCount - ACCOUNT_LIMIT),
  );
  await remove(facts.index("touched"), null, Math.max(0, globalCount - GLOBAL_LIMIT));
  const count = await get(tx.objectStore("windows").index("scope").count(scope));
  if (count > WINDOW_LIMIT) {
    const old: Window[] = await get(
      tx
        .objectStore("windows")
        .index("scope")
        .getAll(scope, WINDOW_LIMIT + 1),
    );
    old.sort((a, b) => a.touched - b.touched);
    for (const window of old.slice(0, count - WINDOW_LIMIT))
      tx.objectStore("windows").delete(window.key);
  }
  const totalWindows = await get(tx.objectStore("windows").count());
  if (totalWindows > 500) {
    const old: Window[] = await get(
      tx
        .objectStore("windows")
        .index("touched")
        .getAll(null, Math.min(256, totalWindows - 500)),
    );
    for (const window of old) tx.objectStore("windows").delete(window.key);
  }
  const states = tx.objectStore("sync"),
    totalStates = await get(states.count());
  if (totalStates > 500) {
    const old: HistorySyncState[] = await get(
      states.index("touched").getAll(null, Math.min(256, totalStates - 500)),
    );
    for (const state of old) states.delete(state.scope);
  }
}
function sameState(a?: HistorySyncState, b?: HistorySyncState) {
  return a?.cursor === b?.cursor && a?.generation === b?.generation;
}
export async function cacheHistorySnapshot(
  scope: string,
  filter: HistoryFilter,
  cursor: string,
  page: ResourcePage,
  expected?: HistorySyncState,
  reset = false,
  signal?: AbortSignal,
): Promise<boolean> {
  validateResourcePage(page, serverHistoryCursor(cursor, filter));
  return transaction(
    "readwrite",
    async (tx) => {
      const state: HistorySyncState | undefined = await get(tx.objectStore("sync").get(scope));
      validateState(state, scope);
      if (!sameState(state, expected)) return false;
      const changedGeneration = !!page.generation && page.generation !== state?.generation;
      if (reset || changedGeneration) {
        for (const window of await scopedWindows(tx, scope)) {
          window.stale = true;
          tx.objectStore("windows").put(window);
        }
        // Old facts remain readable in stale older windows until revalidation.
        // Generation-scoped revisions prevent them from rejecting fresh rows.
      }
      const incoming = keys(page);
      for (const key of incoming) {
        const resource = (key.kind === "transfer" ? page.transfers : page.slots).find(
          (r) => r.id === key.id,
        )!;
        const previous: Fact | undefined = await get(
          tx.objectStore("facts").get([scope, key.kind, key.id]),
        );
        validateFact(previous, scope, key);
        if (
          !previous ||
          previous.generation !== page.generation ||
          (resource.revision ?? 0) > previous.revision ||
          (!!previous.resource && (resource.revision ?? 0) === previous.revision)
        )
          tx.objectStore("facts").put({
            scope,
            key: [scope, key.kind, key.id],
            revision: resource.revision ?? 0,
            generation: page.generation,
            resource: compactResource(resource),
            touched: Date.now(),
          } satisfies Fact);
      }
      tx.objectStore("windows").put({
        scope,
        key: windowKey(scope, filter, cursor),
        keys: incoming,
        next: page.next_cursor,
        generation: page.generation,
        stale: false,
        touched: Date.now(),
      } satisfies Window);
      // Only a full account bootstrap can set its sync cursor. Filtered/older
      // snapshots enrich metadata without skipping changes to other resources.
      if (
        !filter &&
        !cursor &&
        page.sync_cursor &&
        page.generation &&
        (!state || reset || changedGeneration)
      )
        tx.objectStore("sync").put({
          scope,
          cursor: page.sync_cursor,
          generation: page.generation,
          touched: Date.now(),
        });
      await prune(tx, scope);
      return true;
    },
    signal,
  );
}
export async function cacheHistoryChanges(
  scope: string,
  expected: HistorySyncState,
  batch: HistoryChanges,
  signal?: AbortSignal,
): Promise<boolean> {
  validateHistoryChanges(batch, expected.cursor, expected.generation);
  return transaction(
    "readwrite",
    async (tx) => {
      const state: HistorySyncState | undefined = await get(tx.objectStore("sync").get(scope));
      validateState(state, scope);
      if (!sameState(state, expected) || batch.generation !== state?.generation) return false;
      if (!batch.changes.length && batch.next_cursor === state.cursor) return true;
      const windows = await scopedWindows(tx, scope);
      for (const change of batch.changes) {
        const identity: [string, HistoryKind, string] = [scope, change.kind, change.id];
        const previous: Fact | undefined = await get(tx.objectStore("facts").get(identity));
        validateFact(previous, scope, {
          kind: change.kind,
          id: change.id,
          created: change.resource?.created_at ?? "1970-01-01T00:00:00Z",
        });
        if (previous?.generation === batch.generation && previous.revision >= change.revision)
          continue;
        tx.objectStore("facts").put({
          scope,
          key: identity,
          revision: change.revision,
          generation: batch.generation,
          resource: change.action === "upsert" ? compactResource(change.resource!) : undefined,
          touched: Date.now(),
        } satisfies Fact);
        for (const window of windows) {
          if (window.generation !== batch.generation || window.stale) continue;
          const existed = window.keys.some(
            (key) => key.id === change.id && key.kind === change.kind,
          );
          const oldest = window.keys.at(-1),
            newest = window.keys[0];
          window.keys = window.keys.filter(
            (key) => !(key.id === change.id && key.kind === change.kind),
          );
          if (change.action === "upsert" && (!window.key[1] || window.key[1] === change.kind)) {
            const key: Key = {
              kind: change.kind,
              id: change.id,
              created: change.resource!.created_at!,
              allAfter: change.resource!.history_after,
              kindAfter: change.resource!.history_after_kind,
            };
            // New identities only enter the newest page, or an older page whose
            // known interval contains them. Existing identities retain their page.
            const inRange =
              existed ||
              (!window.key[2]
                ? window.next === null || (!!oldest && order(key, oldest) <= 0)
                : !!oldest && !!newest && order(key, newest) >= 0 && order(key, oldest) <= 0);
            if (inRange) window.keys.push(key);
          }
          window.keys.sort(order);
          if (window.keys.length > WINDOW_ROWS) {
            window.keys = window.keys.slice(0, WINDOW_ROWS);
            window.stale = true;
          }
        }
      }
      if (batch.changes.length) for (const window of windows) tx.objectStore("windows").put(window);
      tx.objectStore("sync").put({ ...expected, cursor: batch.next_cursor, touched: Date.now() });
      await prune(tx, scope);
      return true;
    },
    signal,
  );
}
export function serverHistoryCursor(cursor: string, filter?: HistoryFilter): string {
  const { base, after } = cursorParts(cursor);
  return (filter ? after?.kindAfter : after?.allAfter) ?? base;
}

/** Repair disposable metadata only; existing private databases are never opened. */
export async function resetHistoryCache(scope: string, signal?: AbortSignal): Promise<void> {
  // Bound each transaction, including recovery from an oversized/corrupt store.
  for (let batch = 0; batch < 40; batch++) {
    const more = await transaction(
      "readwrite",
      async (tx) => {
        let remaining = false;
        for (const name of ["facts", "windows"]) {
          const store = tx.objectStore(name);
          const keys = await get(store.getAllKeys(IDBKeyRange.bound([scope], [scope, []]), 256));
          for (const key of keys) store.delete(key);
          remaining ||= keys.length === 256;
        }
        tx.objectStore("sync").delete(scope);
        return remaining;
      },
      signal,
    );
    if (!more) return;
  }
  throw failure();
}

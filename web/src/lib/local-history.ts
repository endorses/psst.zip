import { message as m, LocalizedError } from "./i18n/index.ts";
import { legacyHistoryEntries } from "./legacy-history.ts";
import { labelKey, type HistoryLabel, type HistoryLabels } from "./history-labels.ts";
export type LocalResource = { kind: "transfers" | "slots"; id: string };
type Kind = "link" | "label" | "migration";
type Entry = {
  key: [string, Kind, string];
  value?: unknown;
  deleted?: boolean;
  source: "legacy" | "current";
};
export type LocalHistoryPage = { links: Record<string, string>; labels: HistoryLabels };
const databaseName = "psst.local-history.v1";
let opened: Promise<IDBDatabase> | undefined;
const importing = new Map<string, Promise<void>>();
const storageFailure = () => new LocalizedError(m("couldNotSaveOrReadLocalHistoryOnThis"));
function identity(account: string, key: string) {
  if (!account || account.length > 256 || !key || key.length > 256) throw storageFailure();
}
function validate(kind: "link" | "label", value: unknown) {
  if (kind === "link") {
    if (typeof value !== "string" || value.length > 8192) throw storageFailure();
  } else {
    if (!value || typeof value !== "object" || Array.isArray(value)) throw storageFailure();
    const label = value as HistoryLabel;
    if (
      (label.title !== undefined &&
        (typeof label.title !== "string" || label.title.length > 8192)) ||
      (label.custom !== undefined &&
        (typeof label.custom !== "string" || label.custom.length > 8192)) ||
      (label.size !== undefined && (!Number.isSafeInteger(label.size) || label.size < 0))
    )
      throw storageFailure();
    if (JSON.stringify(value).length > 64 * 1024) throw storageFailure();
  }
}
function database(): Promise<IDBDatabase> {
  if (opened) return opened;
  opened = new Promise<IDBDatabase>((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      reject(storageFailure());
      return;
    }
    let settled = false;
    const fail = () => {
      if (!settled) {
        settled = true;
        reject(storageFailure());
      }
    };
    const timer = setTimeout(fail, 10000);
    const request = indexedDB.open(databaseName, 1);
    request.onupgradeneeded = () => {
      request.result.createObjectStore("entries", { keyPath: "key" });
    };
    request.onerror = () => {
      clearTimeout(timer);
      fail();
    };
    request.onblocked = () => {
      clearTimeout(timer);
      fail();
    };
    request.onsuccess = () => {
      clearTimeout(timer);
      if (settled) {
        request.result.close();
        return;
      }
      settled = true;
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
function transaction<T>(
  db: IDBDatabase,
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore, finish: (value: T) => void) => void,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction("entries", mode);
    let result: T;
    const timer = setTimeout(() => {
      try {
        tx.abort();
      } catch {}
    }, 10000);
    tx.oncomplete = () => {
      clearTimeout(timer);
      resolve(result);
    };
    tx.onerror = tx.onabort = () => {
      clearTimeout(timer);
      reject(storageFailure());
    };
    try {
      operation(tx.objectStore("entries"), (value) => {
        result = value;
      });
    } catch (error) {
      try {
        tx.abort();
      } catch {}
      clearTimeout(timer);
      reject(error);
    }
  });
}
async function readEntries(
  account: string,
  keys: [Kind, string][],
): Promise<(Entry | undefined)[]> {
  const db = await database();
  return transaction(db, "readonly", (store, finish) => {
    const values: (Entry | undefined)[] = new Array(keys.length);
    let remaining = keys.length;
    if (!remaining) {
      finish(values);
      return;
    }
    keys.forEach(([kind, key], index) => {
      identity(account, key);
      const get = store.get([account, kind, key]);
      get.onsuccess = () => {
        values[index] = get.result;
        if (!--remaining) finish(values);
      };
    });
  });
}
async function importBatch(account: string, kind: "link" | "label", batch: [string, unknown][]) {
  const db = await database();
  await transaction<void>(db, "readwrite", (store, finish) => {
    for (const [key, value] of batch) {
      identity(account, key);
      validate(kind, value);
      const get = store.get([account, kind, key]);
      get.onsuccess = () => {
        const old: Entry | undefined = get.result;
        if (!old || old.source === "legacy")
          store.put({ key: [account, kind, key], value, source: "legacy" } satisfies Entry);
      };
    }
    finish();
  });
}
async function migrate(account: string, signal?: AbortSignal) {
  for (const kind of ["link", "label"] as const) {
    signal?.throwIfAborted();
    const [marker] = await readEntries(account, [["migration", kind]]);
    if (marker?.value === true) continue;
    // The browser API cannot stream getItem: this one-time whole-string allocation
    // (and final unchanged-source comparison) is the explicit legacy exception.
    const name = `psst.${kind === "link" ? "links" : "labels"}.${account}`;
    const source = localStorage.getItem(name);
    let batch: [string, unknown][] = [];
    if (source !== null)
      for (const entry of legacyHistoryEntries(source)) {
        signal?.throwIfAborted();
        batch.push(entry);
        if (batch.length === 25) {
          await importBatch(account, kind, batch);
          batch = [];
          await new Promise((resolve) => setTimeout(resolve, 0));
        }
      }
    if (batch.length) await importBatch(account, kind, batch);
    signal?.throwIfAborted();
    if (localStorage.getItem(name) !== source)
      throw new LocalizedError(m("existingLocalHistoryChangedDuringImportCloseOlderPsst"));
    const db = await database();
    await transaction<void>(db, "readwrite", (store, finish) => {
      store.put({
        key: [account, "migration", kind],
        value: true,
        source: "current",
      } satisfies Entry);
      finish();
    });
  }
}
async function ensureMigrated(account: string, signal?: AbortSignal) {
  let pending = importing.get(account);
  if (!pending) {
    pending = migrate(account, signal).finally(() => importing.delete(account));
    importing.set(account, pending);
  }
  await pending;
  signal?.throwIfAborted();
}
/** Normal reads are exact indexed gets: at most51 resources, with typed/legacy label keys. */
export async function loadLocalHistory(
  account: string,
  resources: LocalResource[],
  signal?: AbortSignal,
): Promise<LocalHistoryPage> {
  if (resources.length > 51) throw storageFailure();
  await ensureMigrated(account, signal);
  const keys: [Kind, string][] = [];
  for (const resource of resources)
    keys.push(
      ["link", resource.id],
      ["label", labelKey(resource.kind, resource.id)],
      ["label", resource.id],
    );
  const entries = await readEntries(account, keys);
  signal?.throwIfAborted();
  const links: Record<string, string> = {},
    labels: HistoryLabels = {};
  entries.forEach((entry, index) => {
    if (!entry || entry.deleted) return;
    const [kind, key] = keys[index];
    validate(kind as "link" | "label", entry.value);
    if (kind === "link") links[key] = entry.value as string;
    else labels[key] = entry.value as HistoryLabel;
  });
  return { links, labels };
}
export async function saveLocalLink(account: string, id: string, url: string): Promise<void> {
  identity(account, id);
  validate("link", url);
  const db = await database();
  await transaction<void>(db, "readwrite", (store, finish) => {
    store.put({ key: [account, "link", id], value: url, source: "current" } satisfies Entry);
    finish();
  });
}
export async function removeLocalLink(account: string, id: string): Promise<void> {
  identity(account, id);
  const db = await database();
  await transaction<void>(db, "readwrite", (store, finish) => {
    store.put({ key: [account, "link", id], deleted: true, source: "current" } satisfies Entry);
    finish();
  });
}
export async function updateLocalLabel(
  account: string,
  resource: LocalResource,
  patch: HistoryLabel,
): Promise<HistoryLabel> {
  const key = labelKey(resource.kind, resource.id);
  identity(account, key);
  validate("label", patch);
  await ensureMigrated(account);
  const db = await database();
  return transaction<HistoryLabel>(db, "readwrite", (store, finish) => {
    const typed = store.get([account, "label", key]),
      legacy = store.get([account, "label", resource.id]);
    let done = 0;
    const update = () => {
      if (++done !== 2) return;
      const old: Entry | undefined = typed.result ?? legacy.result;
      const value = { ...(old && !old.deleted ? (old.value as HistoryLabel) : {}), ...patch };
      validate("label", value);
      store.put({ key: [account, "label", key], value, source: "current" } satisfies Entry);
      finish(value);
    };
    typed.onsuccess = legacy.onsuccess = () => {
      try {
        update();
      } catch {
        store.transaction.abort();
      }
    };
  });
}

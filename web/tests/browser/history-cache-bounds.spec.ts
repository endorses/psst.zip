import type { ResourcePage } from "../../src/lib/resource-history.ts";
import { test, expect, type Page } from "@playwright/test";
import { historyPage, historyTransfer, historyID, historyCursor } from "../history-page-fixture";

const owner = historyID(900),
  generation = historyID(999),
  initial = historyCursor(1);
async function session(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: { user: { id: owner, username: "cache-owner", role: "user", disabled: false } },
    }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({
      json: { max_file_size: 25000000, history_sync_version: 1 },
    }),
  );
  await page.goto("/?view=settings");
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
}

test("account eviction bounds live facts and removals, invalidates coverage and retains private links", async ({
  page,
}) => {
  test.setTimeout(120000);
  await session(page);
  const result = await page.evaluate(
    async ({ owner, generation, initial, row, id }) => {
      const cursor = (value: unknown) =>
        btoa(JSON.stringify(value)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
      const cachePath = "/src/lib/history-cache.ts",
        localPath = "/src/lib/local-history.ts";
      const cache = await import(cachePath),
        local = await import(localPath);
      const scope = cache.historyScope(location.origin, owner),
        link = `${location.origin}/d/${id}#${"A".repeat(43)}`;
      await local.saveLocalLink(owner, id, link);
      await cache.cacheHistorySnapshot(scope, undefined, "", {
        transfers: [row],
        slots: [],
        paginated: true,
        next_cursor: cursor({ page: 2 }),
        sync_cursor: initial,
        generation,
      });
      await cache.cacheHistoryChanges(scope, await cache.cachedHistoryState(scope), {
        version: 1,
        generation,
        changes: [{ kind: "transfer", id, revision: 2, action: "remove" }],
        next_cursor: cursor({ revision: 2 }),
        has_more: false,
      });
      for (let batch = 0; batch < 40; batch++) {
        const transfers = Array.from({ length: 50 }, (_, index) => ({
          ...row,
          id: `11111111-1111-4111-8111-${String(10000 + batch * 50 + index).padStart(12, "0")}`,
          created_at: new Date(Date.UTC(2026, 0, 2) + (batch * 50 + index) * 1000).toISOString(),
        }));
        await cache.cacheHistorySnapshot(
          scope,
          undefined,
          cursor({ page: batch + 10 }),
          {
            transfers,
            slots: [],
            paginated: true,
            next_cursor: null,
            sync_cursor: initial,
            generation,
          },
          await cache.cachedHistoryState(scope),
        );
      }
      const counts = await new Promise<{ facts: number; windows: number; tombstones: number }>(
        (resolve, reject) => {
          const request = indexedDB.open("psst.server-history.v1");
          request.onsuccess = () => {
            const db = request.result,
              tx = db.transaction(["facts", "windows"], "readonly");
            const facts = tx.objectStore("facts").index("scope").getAll(scope),
              windows = tx.objectStore("windows").index("scope").count(scope);
            tx.oncomplete = () => {
              db.close();
              resolve({
                facts: facts.result.length,
                windows: windows.result,
                tombstones: facts.result.filter((f) => !f.resource).length,
              });
            };
            tx.onabort = () => reject(tx.error);
          };
          request.onerror = () => reject(request.error);
        },
      );
      return {
        ...counts,
        page: await cache.cachedHistoryPage(scope, undefined, ""),
        state: await cache.cachedHistoryState(scope),
        privateLink: (await local.loadLocalHistory(owner, [{ kind: "transfers", id }])).links[id],
        link,
      };
    },
    { owner, generation, initial, row: historyTransfer({ revision: 1 }), id: historyID(1) },
  );
  expect(result.facts).toBe(2000);
  expect(result.windows).toBeLessThanOrEqual(100);
  expect(result.tombstones).toBe(0);
  expect(result.page?.stale).toBe(true);
  expect(result.state).toBeUndefined();
  expect(result.privateLink).toBe(result.link);
});

test("global eviction bounds 10000 facts across account scopes and preserves private data", async ({
  page,
}) => {
  test.setTimeout(120000);
  await session(page);
  const result = await page.evaluate(
    async ({ owner, generation, initial, row, id }) => {
      const cursor = (value: unknown) =>
        btoa(JSON.stringify(value)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
      const cachePath = "/src/lib/history-cache.ts",
        localPath = "/src/lib/local-history.ts";
      const cache = await import(cachePath),
        local = await import(localPath);
      const link = `${location.origin}/d/${id}#${"B".repeat(43)}`;
      await local.saveLocalLink(owner, id, link);
      const scopes: string[] = [];
      for (let account = 0; account < 6; account++) {
        const accountID =
          account === 0
            ? owner
            : `11111111-1111-4111-8111-${String(900 + account).padStart(12, "0")}`;
        const scope = cache.historyScope(location.origin, accountID);
        scopes.push(scope);
        for (let batch = 0; batch < (account === 5 ? 1 : 40); batch++) {
          const transfers = Array.from({ length: 50 }, (_, index) => ({
            ...row,
            id:
              account === 0 && batch === 0 && index === 0
                ? id
                : `11111111-1111-4111-8111-${String(100000 + account * 10000 + batch * 50 + index).padStart(12, "0")}`,
            created_at: new Date(Date.UTC(2026, 0, 1) + (batch * 50 + index) * 1000).toISOString(),
          }));
          await cache.cacheHistorySnapshot(
            scope,
            undefined,
            batch === 0 ? "" : cursor({ page: batch }),
            {
              transfers,
              slots: [],
              paginated: true,
              next_cursor: cursor({ page: 99 }),
              sync_cursor: initial,
              generation,
            },
            await cache.cachedHistoryState(scope),
          );
        }
      }
      const counts = await new Promise<{ total: number; scoped: number[]; windows: number }>(
        (resolve, reject) => {
          const request = indexedDB.open("psst.server-history.v1");
          request.onsuccess = () => {
            const db = request.result,
              tx = db.transaction(["facts", "windows"], "readonly");
            const total = tx.objectStore("facts").count(),
              windows = tx.objectStore("windows").count();
            const scoped = scopes.map((scope) =>
              tx.objectStore("facts").index("scope").count(scope),
            );
            tx.oncomplete = () => {
              db.close();
              resolve({
                total: total.result,
                windows: windows.result,
                scoped: scoped.map((request) => request.result),
              });
            };
            tx.onabort = () => reject(tx.error);
          };
          request.onerror = () => reject(request.error);
        },
      );
      return {
        ...counts,
        page: await cache.cachedHistoryPage(scopes[0], undefined, ""),
        state: await cache.cachedHistoryState(scopes[0]),
        privateLink: (await local.loadLocalHistory(owner, [{ kind: "transfers", id }])).links[id],
        link,
      };
    },
    { owner, generation, initial, row: historyTransfer({ revision: 1 }), id: historyID(1) },
  );
  expect(result.total).toBe(10000);
  expect(result.scoped).toEqual([1950, 2000, 2000, 2000, 2000, 50]);
  expect(result.windows).toBeLessThanOrEqual(500);
  expect(result.page?.stale).toBe(true);
  expect(result.state).toBeUndefined();
  expect(result.privateLink).toBe(result.link);
});

test("unchanged empty feed batch performs no IndexedDB puts or deletes", async ({ page }) => {
  await session(page);
  const result = await page.evaluate(
    async ({ owner, generation, initial, snapshot }) => {
      const path = "/src/lib/history-cache.ts",
        cache = await import(path);
      const scope = cache.historyScope(location.origin, owner);
      await cache.cacheHistorySnapshot(scope, undefined, "", snapshot);
      const state = await cache.cachedHistoryState(scope);
      const originalPut = IDBObjectStore.prototype.put,
        originalDelete = IDBObjectStore.prototype.delete;
      let puts = 0,
        deletes = 0;
      IDBObjectStore.prototype.put = function (...args: Parameters<typeof originalPut>) {
        puts++;
        return originalPut.apply(this, args);
      };
      IDBObjectStore.prototype.delete = function (...args: Parameters<typeof originalDelete>) {
        deletes++;
        return originalDelete.apply(this, args);
      };
      let applied: boolean;
      try {
        applied = await cache.cacheHistoryChanges(scope, state, {
          version: 1,
          generation,
          changes: [],
          next_cursor: initial,
          has_more: false,
        });
      } finally {
        IDBObjectStore.prototype.put = originalPut;
        IDBObjectStore.prototype.delete = originalDelete;
      }
      return {
        applied,
        puts,
        deletes,
        state: await cache.cachedHistoryState(scope),
        rows: (await cache.cachedHistoryPage(scope, undefined, "")).transfers.length,
      };
    },
    {
      owner,
      generation,
      initial,
      snapshot: historyPage({
        transfers: [historyTransfer({ revision: 1 })],
        sync_cursor: initial,
        generation,
      }),
    },
  );
  expect(result.applied).toBe(true);
  expect(result.puts).toBe(0);
  expect(result.deletes).toBe(0);
  expect(result.state?.cursor).toBe(initial);
  expect(result.rows).toBe(1);
});

for (const damage of ["reset", "evicted coverage"])
  test(`virtual-page navigation uses its server-issued anchor after ${damage}`, async ({
    page,
  }) => {
    await session(page);
    const rows = Array.from({ length: 100 }, (_, index) =>
      historyTransfer({
        id: historyID(index + 1),
        revision: index < 50 ? 1 : 2,
        created_at: new Date(Date.UTC(2026, 0, 1) + index * 1000).toISOString(),
        history_after: historyCursor(index + 1000),
        history_after_kind: historyCursor(index + 2000),
      }),
    ).reverse();
    const anchor = historyCursor(1050);
    const requests: string[] = [];
    await page.route("**/api/v1/auth/resources?*", (route) => {
      const after = new URL(route.request().url()).searchParams.get("after") ?? "";
      requests.push(after);
      return route.fulfill({
        json: historyPage({
          transfers: after === anchor ? rows.slice(50) : rows.slice(0, 50),
          sync_cursor: initial,
          generation,
          next_cursor: after === anchor ? null : historyCursor(90),
        }),
      });
    });
    await page.route("**/api/v1/auth/history/changes?*", (route) =>
      route.fulfill({
        json: {
          version: 1,
          generation,
          changes: [],
          next_cursor: new URL(route.request().url()).searchParams.get("cursor"),
          has_more: false,
        },
      }),
    );
    const result = await page.evaluate(
      async ({ owner, generation, initial, rows, anchor, damage }) => {
        const cursor = (value: unknown) =>
          btoa(JSON.stringify(value)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
        const cachePath = "/src/lib/history-cache.ts",
          controllerPath = "/src/lib/history-controller.ts";
        const cache = await import(cachePath),
          { HistoryController } = await import(controllerPath);
        const scope = cache.historyScope(location.origin, owner);
        await cache.cacheHistorySnapshot(scope, undefined, "", {
          transfers: rows.slice(50),
          slots: [],
          paginated: true,
          next_cursor: cursor({ page: 90 }),
          sync_cursor: initial,
          generation,
        });
        await cache.cacheHistoryChanges(scope, await cache.cachedHistoryState(scope), {
          version: 1,
          generation,
          changes: rows.slice(0, 50).map((resource) => ({
            kind: "transfer",
            id: resource.id,
            revision: 2,
            action: "upsert",
            resource,
          })),
          next_cursor: cursor({ revision: 2 }),
          has_more: false,
        });
        const next = (await cache.cachedHistoryPage(scope, undefined, "")).next_cursor;
        if (damage === "reset") await cache.resetHistoryCache(scope);
        else
          await new Promise<void>((resolve, reject) => {
            const request = indexedDB.open("psst.server-history.v1");
            request.onsuccess = () => {
              const db = request.result,
                tx = db.transaction("windows", "readwrite");
              tx.objectStore("windows").delete([scope, "", ""]);
              tx.oncomplete = () => {
                db.close();
                resolve();
              };
              tx.onabort = () => reject(tx.error);
            };
            request.onerror = () => reject(request.error);
          });
        const absent = await cache.cachedHistoryPage(scope, undefined, next);
        const seen: string[][] = [],
          errors: string[] = [];
        const controller = new HistoryController({
          scope,
          busy: () => {},
          page: (page: ResourcePage) => {
            seen.push(page.transfers.map((row) => row.id));
          },
          error: (error: unknown) => {
            errors.push(String(error));
          },
        });
        let success: boolean;
        try {
          success = await controller.enter(undefined, next);
        } finally {
          controller.dispose();
        }
        return {
          next,
          translated: cache.serverHistoryCursor(next),
          anchor,
          absent,
          success,
          seen,
          errors,
        };
      },
      { owner, generation, initial, rows, anchor, damage },
    );
    expect(result.next).toMatch(/^local\./);
    expect(result.translated).toBe(anchor);
    expect(result.absent).toBeNull();
    expect(result.success).toBe(true);
    expect(result.errors).toEqual([]);
    expect(result.seen.at(-1)).toEqual(rows.slice(50).map((row) => row.id));
    expect(requests).toEqual(damage === "reset" ? ["", anchor] : [anchor]);
  });

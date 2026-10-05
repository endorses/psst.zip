import { test, expect, type Page } from "@playwright/test";
import { historyPage, historyTransfer, historyID, historyCursor } from "../history-page-fixture";
const generation = historyID(999),
  owner = historyID(900),
  initial = historyCursor(1);
async function session(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: { user: { id: owner, username: "sync-owner", role: "user", disabled: false } },
    }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25000000, history_sync_version: 1 } }),
  );
}
const snapshot = (rows: unknown[], next: string | null = null) =>
  historyPage({ transfers: rows, sync_cursor: initial, generation, next_cursor: next });
test("cache-first reload, incremental older updates/removals and no repeated snapshot polling", async ({
  page,
}) => {
  await session(page);
  let snapshots = 0,
    feeds = 0,
    update = false,
    removed = false;
  await page.route("**/api/v1/auth/resources?*", (route) => {
    snapshots++;
    return route.fulfill({
      json: snapshot([historyTransfer({ title: "Cached original", revision: 1 })]),
    });
  });
  await page.route("**/api/v1/auth/history/changes?*", (route) => {
    feeds++;
    const cursor = new URL(route.request().url()).searchParams.get("cursor")!;
    const changes = removed
      ? [{ kind: "transfer", id: historyID(1), action: "remove", revision: 3 }]
      : update
        ? [
            {
              kind: "transfer",
              id: historyID(1),
              action: "upsert",
              revision: 2,
              resource: historyTransfer({ title: "Updated on another device", revision: 2 }),
            },
          ]
        : [];
    update = false;
    removed = false;
    return route.fulfill({
      json: {
        version: 1,
        generation,
        changes,
        next_cursor: changes.length ? historyCursor(feeds + 1) : cursor,
        has_more: false,
      },
    });
  });
  await page.goto("/?view=history");
  await expect(page.locator(".resource")).toContainText("Cached original");
  await expect.poll(() => feeds).toBe(1);
  update = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.locator(".resource")).toContainText("Updated on another device");
  expect(snapshots).toBe(1);
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/v1/auth/history/changes?*", async (route) => {
    await held;
    return route.fulfill({
      json: {
        version: 1,
        generation,
        changes: [],
        next_cursor: new URL(route.request().url()).searchParams.get("cursor"),
        has_more: false,
      },
    });
  });
  await page.reload();
  await expect(page.locator(".resource")).toContainText("Updated on another device");
  expect(snapshots).toBe(1);
  release();
  await page.unroute("**/api/v1/auth/history/changes?*");
  await page.route("**/api/v1/auth/history/changes?*", (route) =>
    route.fulfill({
      json: {
        version: 1,
        generation,
        changes: [{ kind: "transfer", id: historyID(1), action: "remove", revision: 3 }],
        next_cursor: historyCursor(50),
        has_more: false,
      },
    }),
  );
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.locator(".resource")).toHaveCount(0);
});
test("arrivals preserve overflow, old-page position and navigation stops sync", async ({
  page,
}) => {
  await session(page);
  const rows = Array.from({ length: 50 }, (_, i) =>
    historyTransfer({
      id: historyID(i + 1),
      created_at: `2026-01-${String(31 - Math.floor(i / 2)).padStart(2, "0")}T00:00:00Z`,
      revision: 1,
    }),
  );
  let snapshots = 0,
    feeds = 0,
    arrival = false;
  await page.route("**/api/v1/auth/resources?*", (route) => {
    snapshots++;
    return route.fulfill({ json: snapshot(rows, historyCursor(100)) });
  });
  await page.route("**/api/v1/auth/history/changes?*", (route) => {
    feeds++;
    const cursor = new URL(route.request().url()).searchParams.get("cursor")!;
    const changes = arrival
      ? [
          {
            kind: "transfer",
            id: historyID(100),
            action: "upsert",
            revision: 2,
            resource: historyTransfer({
              id: historyID(100),
              title: "New arrival",
              created_at: "2026-02-01T00:00:00Z",
              revision: 2,
            }),
          },
        ]
      : [];
    arrival = false;
    return route.fulfill({
      json: {
        version: 1,
        generation,
        changes,
        next_cursor: changes.length ? historyCursor(2) : cursor,
        has_more: false,
      },
    });
  });
  await page.goto("/?view=history");
  await expect(page.locator(".resource")).toHaveCount(50);
  await expect.poll(() => feeds).toBe(1);
  arrival = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.locator(".resource").first()).toContainText("New arrival");
  await page.getByRole("button", { name: "Older transfers" }).click();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
  await expect(page.locator(".resource")).toHaveCount(1);
  expect(snapshots).toBe(1);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  const count = feeds;
  await page.clock.install();
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 60000));
  await page.clock.fastForward(20000);
  expect(feeds).toBe(count);
});
test("generation reset reboots a bounded snapshot and preserves private local links", async ({
  page,
}) => {
  await session(page);
  await page.addInitScript(
    ({ owner, id }) =>
      localStorage.setItem(
        `psst.links.${owner}`,
        JSON.stringify({ [id]: `${location.origin}/d/${id}#${"A".repeat(43)}` }),
      ),
    { owner, id: historyID(1) },
  );
  let snapshots = 0,
    reset = false;
  await page.route("**/api/v1/auth/resources?*", (route) => {
    snapshots++;
    return route.fulfill({
      json: snapshot([
        historyTransfer({ title: reset ? "Restored server" : "Before restart", revision: 1 }),
      ]),
    });
  });
  await page.route("**/api/v1/auth/history/changes?*", (route) =>
    reset
      ? route.fulfill({ status: 409, json: { code: "history_sync_reset_required" } })
      : route.fulfill({
          json: { version: 1, generation, changes: [], next_cursor: initial, has_more: false },
        }),
  );
  await page.goto("/?view=history");
  const row = page.locator(".resource");
  await expect(row.getByRole("link", { name: "Open", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  reset = true;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(row).toContainText("Restored server");
  await expect(row.getByRole("link", { name: "Open", exact: true })).toHaveAttribute(
    "href",
    /#A{43}$/,
  );
  expect(snapshots).toBe(2);
});

test("database commits changes with their cursor, rolls back storage failures and preserves private links", async ({
  page,
}) => {
  await session(page);
  await page.goto("/?view=settings");
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  const result = await page.evaluate(
    async ({ snapshot, initial, generation, owner, id, next }) => {
      const modulePath = "/src/lib/history-cache.ts",
        localPath = "/src/lib/local-history.ts";
      const cache = await import(modulePath),
        local = await import(localPath);
      const scope = cache.historyScope(location.origin, owner);
      await local.saveLocalLink(owner, id, `${location.origin}/d/${id}#${"A".repeat(43)}`);
      await cache.cacheHistorySnapshot(scope, undefined, "", snapshot);
      const state = await cache.cachedHistoryState(scope);
      const batch = {
        version: 1,
        generation,
        changes: [{ kind: "transfer", id, revision: 2, action: "remove" }],
        next_cursor: next,
        has_more: false,
      };
      const original = IDBObjectStore.prototype.put;
      IDBObjectStore.prototype.put = function (...args: Parameters<typeof original>) {
        if (this.name === "sync")
          throw new DOMException("Test storage failure", "QuotaExceededError");
        return original.apply(this, args);
      };
      let rolledBack = false;
      try {
        await cache.cacheHistoryChanges(scope, state, batch);
      } catch {
        rolledBack = true;
      } finally {
        IDBObjectStore.prototype.put = original;
      }
      const unchanged = await cache.cachedHistoryPage(scope, undefined, "");
      const sameCursor = (await cache.cachedHistoryState(scope)).cursor === initial;
      await cache.cacheHistoryChanges(scope, state, batch);
      const empty = await cache.cachedHistoryPage(scope, undefined, "");
      // Late stale snapshot must not resurrect a removal or roll back the cursor.
      const staleAccepted = await cache.cacheHistorySnapshot(scope, undefined, "", snapshot, state);
      const privateLink = (await local.loadLocalHistory(owner, [{ kind: "transfers", id }])).links[
        id
      ];
      const other = await cache.cachedHistoryPage(
        cache.historyScope("https://other.example", owner),
        undefined,
        "",
      );
      return {
        rolledBack,
        count: unchanged.transfers.length,
        sameCursor,
        empty: empty.transfers.length,
        staleAccepted,
        privateLink,
        other,
      };
    },
    {
      snapshot: snapshot([historyTransfer({ revision: 1 })]),
      initial,
      generation,
      owner: historyID(901),
      id: historyID(1),
      next: historyCursor(2),
    },
  );
  expect(result.rolledBack).toBe(true);
  expect(result.count).toBe(1);
  expect(result.sameCursor).toBe(true);
  expect(result.empty).toBe(0);
  expect(result.staleAccepted).toBe(false);
  expect(result.privateLink).toMatch(/#A{43}$/);
  expect(result.other).toBeNull();
});

test("quiet History polls at ten seconds, pauses hidden and honors server retry instructions", async ({
  page,
}) => {
  await session(page);
  await page.clock.install();
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 60000));
  let feeds = 0,
    limited = false;
  await page.route("**/api/v1/auth/resources?*", (route) =>
    route.fulfill({ json: snapshot([historyTransfer({ revision: 1 })]) }),
  );
  await page.route("**/api/v1/auth/history/changes?*", (route) => {
    feeds++;
    return limited
      ? route.fulfill({
          status: 429,
          headers: { "Retry-After": "120" },
          json: { code: "rate_limited" },
        })
      : route.fulfill({
          json: { version: 1, generation, changes: [], next_cursor: initial, has_more: false },
        });
  });
  await page.goto("/?view=history");
  await expect.poll(() => feeds).toBe(1);
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  await page.clock.fastForward(9999);
  expect(feeds).toBe(1);
  await page.clock.fastForward(1);
  await expect.poll(() => feeds).toBe(2);
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(60000);
  expect(feeds).toBe(2);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => feeds).toBe(3);
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  limited = true;
  await page.clock.fastForward(10000);
  await expect.poll(() => feeds).toBe(4);
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  await page.clock.fastForward(60000);
  expect(feeds).toBe(4);
  await page.clock.fastForward(60000);
  await expect.poll(() => feeds).toBe(5);
});

for (const damage of ["checkpoint", "revision", "timestamp", "coverage"])
  test(`corrupt ${damage} metadata reboots without deleting private links`, async ({ page }) => {
    await session(page);
    let snapshots = 0;
    const seed = snapshot([historyTransfer({ revision: 1, title: "Recovered" })]);
    await page.route("**/api/v1/auth/resources?*", (route) => {
      snapshots++;
      return route.fulfill({ json: seed });
    });
    await page.route("**/api/v1/auth/history/changes?*", (route) =>
      route.fulfill({
        json: { version: 1, generation, changes: [], next_cursor: initial, has_more: false },
      }),
    );
    await page.goto("/?view=settings");
    await page.evaluate(
      async ({ owner, id, generation, seed, damage }) => {
        const cachePath = "/src/lib/history-cache.ts",
          localPath = "/src/lib/local-history.ts";
        const cache = await import(cachePath),
          local = await import(localPath);
        const scope = cache.historyScope(location.origin, owner);
        await local.saveLocalLink(owner, id, `${location.origin}/d/${id}#${"A".repeat(43)}`);
        await cache.cacheHistorySnapshot(scope, undefined, "", seed);
        await new Promise<void>((resolve, reject) => {
          const open = indexedDB.open("psst.server-history.v1");
          open.onsuccess = () => {
            const db = open.result,
              name = damage === "checkpoint" ? "sync" : damage === "coverage" ? "windows" : "facts";
            const tx = db.transaction(name, "readwrite");
            if (name === "sync")
              tx.objectStore(name).put({
                scope,
                cursor: { damaged: true },
                generation,
                touched: Date.now(),
              });
            else if (name === "windows")
              tx.objectStore(name).put({
                scope,
                key: [scope, "", ""],
                keys: { damaged: true },
                next: null,
                generation,
                touched: Date.now(),
                stale: false,
              });
            else
              tx.objectStore(name).put({
                scope,
                key: [scope, "transfer", id],
                revision: damage === "revision" ? 999 : 1,
                resource: seed.transfers[0],
                generation,
                touched: damage === "timestamp" ? "broken" : Date.now(),
              });
            tx.oncomplete = () => {
              db.close();
              resolve();
            };
            tx.onabort = () => reject(tx.error);
          };
        });
      },
      { owner, id: historyID(1), generation, seed, damage },
    );
    await page.goto("/?view=history");
    await expect(page.locator(".resource")).toContainText("Recovered");
    await expect(
      page.locator(".resource").getByRole("button", { name: "Copy link", exact: true }),
    ).toBeVisible();
    await expect.poll(() => snapshots).toBe(1);
  });

for (const mode of ["denied", "quota"])
  test(`history remains usable with ${mode} metadata storage and never saves a false checkpoint`, async ({
    page,
  }) => {
    await session(page);
    let feeds = 0;
    await page.route("**/api/v1/auth/resources?*", (route) =>
      route.fulfill({
        json: snapshot([historyTransfer({ revision: 1, title: "Online only" })]),
      }),
    );
    await page.route("**/api/v1/auth/history/changes?*", (route) => {
      feeds++;
      return route.fulfill({
        json: { version: 1, generation, changes: [], next_cursor: initial, has_more: false },
      });
    });
    await page.goto("/?view=settings");
    await page.evaluate((mode) => {
      if (mode === "denied")
        Object.defineProperty(window, "indexedDB", {
          configurable: true,
          get() {
            throw new DOMException("Denied", "SecurityError");
          },
        });
      else {
        const put = IDBObjectStore.prototype.put;
        IDBObjectStore.prototype.put = function (...args: Parameters<typeof put>) {
          if (this.name === "windows") throw new DOMException("Full", "QuotaExceededError");
          return put.apply(this, args);
        };
      }
    }, mode);
    await page.getByRole("link", { name: "History", exact: true }).click();
    await expect(page.locator(".resource")).toContainText("Online only");
    await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
    expect(feeds).toBe(0);
  });

test("coalesced mutations respect Retry-After beyond the browser timer boundary", async ({
  page,
}) => {
  await session(page);
  await page.clock.install();
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 60000));
  let feeds = 0,
    release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/v1/auth/resources?*", (route) =>
    route.fulfill({ json: snapshot([historyTransfer({ revision: 1 })]) }),
  );
  await page.route("**/api/v1/auth/history/changes?*", async (route) => {
    feeds++;
    if (feeds === 2) {
      await held;
      return route.fulfill({
        status: 429,
        headers: { "Retry-After": "2147484" },
        json: { code: "rate_limited" },
      });
    }
    return route.fulfill({
      json: { version: 1, generation, changes: [], next_cursor: initial, has_more: false },
    });
  });
  await page.goto("/?view=history");
  await expect.poll(() => feeds).toBe(1);
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect.poll(() => feeds).toBe(2);
  await page.evaluate(async () => {
    const path = "/src/lib/history-notifications.ts";
    (await import(path)).notifyHistoryMutation();
  });
  await page.clock.runFor(1);
  release();
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
  await page.evaluate(async () => {
    const path = "/src/lib/history-notifications.ts";
    (await import(path)).notifyHistoryMutation();
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.clock.fastForward(10000);
  expect(feeds).toBe(2);
  await page.clock.fastForward(2147473647);
  expect(feeds).toBe(2);
  await page.clock.fastForward(353);
  await expect.poll(() => feeds).toBe(3);
});

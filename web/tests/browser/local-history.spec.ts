import { test, expect, type Page } from "@playwright/test";
async function open(page: Page) {
  await page.goto("/");
}
test("legacy migration preserves every record, scoped keys, current edits and deletion tombstones", async ({
  page,
}) => {
  await open(page);
  const result = await page.evaluate(async () => {
    const path = "/src/lib/local-history.ts",
      store = await import(path);
    const links = Object.fromEntries(
      Array.from({ length: 200 }, (_, i) => [`id${i}`, `https://example.test/d/id${i}#secret${i}`]),
    );
    const raw = JSON.stringify(links),
      labels = JSON.stringify({
        id199: { title: "old title", size: 42 },
        "slots:id199": { custom: "inbox name" },
      });
    localStorage.setItem("psst.links.alice", raw);
    localStorage.setItem("psst.labels.alice", labels);
    localStorage.setItem(
      "psst.receive-key.v2.alice.inbox",
      JSON.stringify({ publicKey: "A".repeat(43), privateKey: "B".repeat(43) }),
    );
    await store.saveLocalLink("alice", "id0", "https://new.test/d/id0#new-secret");
    await store.removeLocalLink("alice", "id1");
    const loaded = await store.loadLocalHistory("alice", [
      { kind: "transfers", id: "id0" },
      { kind: "transfers", id: "id1" },
      { kind: "transfers", id: "id199" },
      { kind: "slots", id: "id199" },
    ]);
    const bob = await store.loadLocalHistory("bob", [{ kind: "transfers", id: "id199" }]);
    const sourcePreserved =
      localStorage.getItem("psst.links.alice") === raw &&
      localStorage.getItem("psst.labels.alice") === labels;
    await Promise.all([
      store.updateLocalLabel("alice", { kind: "transfers", id: "id199" }, { custom: "new custom" }),
      store.updateLocalLabel(
        "alice",
        { kind: "transfers", id: "id199" },
        { title: "updated title" },
      ),
    ]);
    let reads = 0,
      legacyReads = 0;
    const get = IDBObjectStore.prototype.get,
      getItem = Storage.prototype.getItem;
    IDBObjectStore.prototype.get = function (key) {
      reads++;
      return get.call(this, key);
    };
    IDBObjectStore.prototype.getAll = function () {
      throw new Error("Unbounded read forbidden");
    };
    IDBObjectStore.prototype.openCursor = function () {
      throw new Error("Unbounded cursor forbidden");
    };
    Storage.prototype.getItem = function (key) {
      if (key.startsWith("psst.links.") || key.startsWith("psst.labels.")) {
        legacyReads++;
        throw new Error("Completed migration must not reread full maps");
      }
      return getItem.call(this, key);
    };
    const final = await store.loadLocalHistory("alice", [{ kind: "transfers", id: "id199" }]);
    return {
      loaded,
      bob,
      sourcePreserved,
      final,
      reads,
      legacyReads,
      privateKey: localStorage.getItem("psst.receive-key.v2.alice.inbox"),
    };
  });
  expect(result.loaded.links).toEqual({
    id0: "https://new.test/d/id0#new-secret",
    id199: "https://example.test/d/id199#secret199",
  });
  expect(result.loaded.labels["slots:id199"].custom).toBe("inbox name");
  expect(result.bob).toEqual({ links: {}, labels: {} });
  expect(result.sourcePreserved).toBe(true);
  expect(result.final.labels["transfers:id199"]).toEqual({
    title: "updated title",
    custom: "new custom",
    size: 42,
  });
  expect(result.legacyReads).toBe(0);
  expect(result.reads).toBeLessThanOrEqual(5);
  expect(result.privateKey).toContain("B".repeat(43));
});
test("interrupted migration resumes nondestructively and exact reads work after reload", async ({
  page,
}) => {
  await open(page);
  const interrupted = await page.evaluate(async () => {
    const path = "/src/lib/local-history.ts",
      store = await import(path);
    const raw = JSON.stringify(
      Object.fromEntries(Array.from({ length: 2500 }, (_, i) => [`id${i}`, `link${i}`])),
    );
    localStorage.setItem("psst.links.restart", raw);
    const controller = new AbortController();
    setTimeout(() => controller.abort(), 20);
    try {
      await store.loadLocalHistory(
        "restart",
        [{ kind: "transfers", id: "id2499" }],
        controller.signal,
      );
      return false;
    } catch {
      return localStorage.getItem("psst.links.restart") === raw;
    }
  });
  expect(interrupted).toBe(true);
  await page.reload();
  const result = await page.evaluate(async () => {
    const path = "/src/lib/local-history.ts",
      store = await import(path);
    return store.loadLocalHistory("restart", [
      { kind: "transfers", id: "id0" },
      { kind: "transfers", id: "id2499" },
    ]);
  });
  expect(result.links).toEqual({ id0: "link0", id2499: "link2499" });
});
test("malformed legacy data fails explicitly without altering the source", async ({ page }) => {
  await open(page);
  const result = await page.evaluate(async () => {
    const path = "/src/lib/local-history.ts",
      store = await import(path);
    const raw = '{"valid":"preserved","broken":';
    localStorage.setItem("psst.links.corrupt", raw);
    let failed = false;
    try {
      await store.loadLocalHistory("corrupt", [{ kind: "transfers", id: "valid" }]);
    } catch {
      failed = true;
    }
    const unchanged = localStorage.getItem("psst.links.corrupt") === raw;
    localStorage.setItem(
      "psst.links.corrupt",
      JSON.stringify({ valid: "preserved", repaired: "also preserved" }),
    );
    const restored = await store.loadLocalHistory("corrupt", [
      { kind: "transfers", id: "valid" },
      { kind: "transfers", id: "repaired" },
    ]);
    return { failed, unchanged, restored };
  });
  expect(result.failed).toBe(true);
  expect(result.unchanged).toBe(true);
  expect(result.restored.links).toEqual({ valid: "preserved", repaired: "also preserved" });
});

import { test as authenticatedTest, signIn } from "./auth-fixture";
authenticatedTest(
  "failed local link persistence warns before leaving and can be retried without losing the transfer",
  async ({ page }) => {
    await signIn(page);
    await page.evaluate(() => {
      const factory = indexedDB,
        open = factory.open.bind(factory);
      (window as any).restoreHistoryStorage = () => {
        factory.open = open;
      };
      factory.open = () => {
        throw new DOMException("Storage blocked", "SecurityError");
      };
    });
    await page.getByLabel("Choose files").setInputFiles({
      name: "keep-link.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("keep my transfer key"),
    });
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
    const link = await page.getByLabel("Full link").inputValue();
    await expect(page.getByRole("alert")).toContainText("Copy the full link before leaving");
    await page.evaluate(() => (window as any).restoreHistoryStorage());
    await page.getByRole("button", { name: "Retry saving link", exact: true }).click();
    await expect(page.getByRole("button", { name: "Retry saving link", exact: true })).toHaveCount(
      0,
    );
    await page.getByRole("link", { name: "History", exact: true }).click();
    const row = page.locator(`[data-resource-id="${new URL(link).pathname.split("/").pop()}"]`);
    await expect(row.getByRole("link", { name: "Open", exact: true })).toHaveAttribute(
      "href",
      link,
    );
    await expect(row).toContainText("keep-link.txt");
  },
);
authenticatedTest(
  "a receive invitation remains copyable when link storage fails",
  async ({ page }) => {
    await signIn(page);
    await page.getByRole("link", { name: "Receive", exact: true }).click();
    await page.evaluate(() => {
      const factory = indexedDB,
        open = factory.open.bind(factory);
      (window as any).restoreHistoryStorage = () => {
        factory.open = open;
      };
      factory.open = () => {
        throw new DOMException("Storage blocked", "SecurityError");
      };
    });
    await page.getByRole("button", { name: "Create receive link", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("Copy the full link before leaving");
    const link = await page.getByLabel("Full link").inputValue();
    expect(link).toMatch(/\/u\/[0-9a-f-]+#v2\./);
    await expect(page.getByRole("button", { name: "Copy link", exact: true })).toBeVisible();
    await page.evaluate(() => (window as any).restoreHistoryStorage());
    await page.getByRole("button", { name: "Retry saving link", exact: true }).click();
    await expect(page.getByRole("button", { name: "Retry saving link", exact: true })).toHaveCount(
      0,
    );
    await page.reload();
    await expect(page.getByLabel("Full link")).toHaveValue(link);
  },
);

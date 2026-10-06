import { test, expect, type Page } from "@playwright/test";
import {
  historyPage,
  historySlot,
  historyTransfer,
  historyID,
  historyCursor,
} from "../history-page-fixture";
async function session(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: { id: historyID(900), username: "history-owner", role: "user", disabled: false },
      },
    }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25000000 } }),
  );
}
test("empty filtered history pages stay navigable and unknown counts never become zero", async ({
  page,
}) => {
  await page.clock.install();
  await session(page);
  await page.setViewportSize({ width: 390, height: 844 });
  const reads: string[] = [];
  await page.route("**/api/v1/auth/resources?*", (route) => {
    const query = new URL(route.request().url()).searchParams;
    expect(query.get("limit")).toBe("50");
    const after = query.get("after") || "";
    reads.push(after);
    return route.fulfill({
      json: after
        ? historyPage({
            slots: [historySlot({ file_count: null, completed_files: null, total_size: null })],
          })
        : historyPage({ next_cursor: historyCursor(1) }),
    });
  });
  await page.goto("/?view=history");
  await expect(page.getByText("No transfers on this page.", { exact: true })).toBeVisible();
  expect(reads).toEqual([""]);
  await page.getByRole("button", { name: "Older transfers" }).click();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
  const row = page.locator(".resource");
  await expect(row).toContainText("File totals updating");
  await expect(row).not.toContainText("0 files received");
  await expect(page.getByRole("button", { name: "Older transfers" })).toBeDisabled();
  // Exercise the real polling request without waiting for its browser deadline.
  await page.clock.fastForward(12_000);
  await expect.poll(() => reads.length, { timeout: 15000 }).toBeGreaterThan(2);
  expect(reads.slice(1).every((cursor) => cursor === historyCursor(1))).toBe(true);
  await page.getByRole("button", { name: "First page", exact: true }).click();
  await expect(page.getByText("Page 1", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
test("malformed page preserves names, keys and the last loaded history rows", async ({ page }) => {
  await session(page);
  const id = historyID(1);
  await page.addInitScript(
    ({ owner, id }) => {
      localStorage.setItem(
        `psst.links.${owner}`,
        JSON.stringify({ [id]: `${location.origin}/d/${id}#${"A".repeat(43)}` }),
      );
    },
    { owner: historyID(900), id },
  );
  let bad = false;
  let title: string | null = null;
  await page.route(`**/api/v1/transfers/${id}/title`, (route) => {
    title = route.request().postDataJSON().title;
    return route.fulfill({ json: { title } });
  });
  await page.route("**/api/v1/auth/resources?*", (route) =>
    route.fulfill({
      json: bad
        ? { transfers: [], slots: [] }
        : historyPage({ transfers: [historyTransfer({ title })], next_cursor: historyCursor(1) }),
    }),
  );
  await page.goto("/?view=history");
  const row = page.locator(`[data-resource-id="${id}"]`);
  await expect(row.getByRole("link", { name: "Open", exact: true })).toBeVisible();
  await row.getByRole("button", { name: "Rename", exact: true }).click();
  await row.getByLabel("Link title").fill("Keep my label");
  await row.getByRole("button", { name: "Save name", exact: true }).click();
  bad = true;
  await page.getByRole("button", { name: "Older transfers" }).click();
  await expect(page.getByRole("alert")).toContainText("unsupported resource page");
  await expect(row).toContainText("Keep my label");
  await expect(row.getByRole("link", { name: "Open", exact: true })).toHaveAttribute(
    "href",
    new RegExp(`#${"A".repeat(43)}$`),
  );
  await expect(page.getByText("Page 1", { exact: true })).toBeVisible();
});
test("a stale unauthorized history response cannot sign out a user after navigation", async ({
  page,
}) => {
  await session(page);
  let calls = 0,
    release!: () => void,
    entered!: () => void;
  const delayed = new Promise<void>((resolve) => (release = resolve)),
    started = new Promise<void>((resolve) => (entered = resolve));
  await page.route("**/api/v1/auth/resources?*", async (route) => {
    if (++calls === 1)
      return route.fulfill({ json: historyPage({ transfers: [historyTransfer()] }) });
    entered();
    await delayed;
    await route.fulfill({ status: 401, json: { error: "stale unauthorized" } }).catch(() => {});
  });
  await page.goto("/?view=history");
  await expect(page.locator(".resource")).toHaveCount(1);
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await started;
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  release();
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
  await expect(page.getByRole("alert").filter({ hasText: "stale unauthorized" })).toHaveCount(0);
});

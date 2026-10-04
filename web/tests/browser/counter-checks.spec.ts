import { test, expect, type Page } from "@playwright/test";
import { cleanupOverview, storageChecks, counterChecks } from "../admin-resource-fixture";

async function admin(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "admin", role: "admin", username: "admin" } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25 * 1024 ** 2 } }),
  );
  await page.route("**/api/v1/admin/security", (route) =>
    route.fulfill({ json: { enabled: true, recovery_codes_remaining: 10, recent_until: null } }),
  );
  await page.route("**/api/v1/admin/cleanup", (route) => route.fulfill({ json: cleanupOverview }));
  await page.route("**/api/v1/admin/storage-checks", (route) =>
    route.fulfill({ json: storageChecks }),
  );
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [], next_cursor: null } }),
  );
}

test("counter checks distinguish discovery, retries and a completed database pass", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await admin(page);
  let response: Record<string, unknown> = {
    ...counterChecks,
    state: "pending",
    last_scan_completed_at: undefined,
    scan_pending: true,
  };
  await page.route("**/api/v1/admin/counter-checks", (route) => route.fulfill({ json: response }));
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Counter checks", exact: true });
  await expect(panel).toContainText("Counter checks pending");
  await expect(panel).toContainText("No completed counter pass recorded yet");
  await expect(panel).toContainText("Additional work may await discovery or retry");
  response = {
    ...counterChecks,
    state: "degraded",
    pending_count: 5,
    failed_count: 2,
    busy_count: 1,
    scan_pending: true,
    scan_error_code: "scan_failed",
  };
  await panel.getByRole("button", { name: "Refresh counter checks" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "The counter scan could not complete. It will retry.",
  );
  await expect(
    panel.locator("dl > div").filter({ hasText: "Queued checks, including retries" }),
  ).toContainText("5");
  await expect(panel.locator("dl > div").filter({ hasText: "Queued failures" })).toContainText("2");
  await expect(
    panel.locator("dl > div").filter({ hasText: "Queued checks awaiting stable data" }),
  ).toContainText("1");
  response = counterChecks;
  await panel.getByRole("button", { name: "Refresh counter checks" }).click();
  await expect(panel).toContainText("Last counter pass completed");
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await expect(panel).toContainText("Upload quotas use live database records");
  await expect(panel).toContainText("do not verify stored file contents");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await panel.screenshot({ path: testInfo.outputPath("counter-checks-mobile.png") });
});

test("malformed or unavailable counter snapshots preserve known status independently", async ({
  page,
}) => {
  await admin(page);
  let response: Record<string, unknown> = counterChecks;
  let offline = false;
  let storageReads = 0;
  await page.route("**/api/v1/admin/storage-checks", (route) => {
    storageReads++;
    return route.fulfill({ json: storageChecks });
  });
  await page.route("**/api/v1/admin/counter-checks", (route) => {
    expect(route.request().method()).toBe("GET");
    return route.fulfill(
      offline ? { status: 503, json: { error: "/private/DO_NOT_DISPLAY" } } : { json: response },
    );
  });
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Counter checks", exact: true });
  await expect(panel).toContainText("Last counter pass completed");
  for (const invalid of [
    { pending_count: -1 },
    { pending_count: 65 },
    { failed_count: 1 },
    { busy_count: 0.5 },
    { state: "unsafe" },
    { scan_pending: "yes" },
    { last_scan_completed_at: "/private/DO_NOT_DISPLAY" },
    { scan_error_code: "/private/DO_NOT_DISPLAY" },
  ]) {
    response = { ...counterChecks, ...invalid };
    await panel.getByRole("button", { name: "Refresh counter checks" }).click();
    await expect(panel.getByRole("alert")).toContainText(
      "previous snapshot remains below and may be stale",
    );
    await expect(panel).toContainText("Last counter pass completed");
    await expect(panel).not.toContainText("DO_NOT_DISPLAY");
  }
  offline = true;
  await panel.getByRole("button", { name: "Refresh counter checks" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "previous snapshot remains below and may be stale",
  );
  expect(storageReads).toBe(1);
  offline = false;
  response = { ...counterChecks, state: "degraded", scan_error_code: "scan_failed" };
  await panel.getByRole("button", { name: "Refresh counter checks" }).click();
  await expect(panel).toContainText("Counter checks need retry");
  await expect(panel).not.toContainText("previous snapshot");
});

import { test, expect, type Page } from "@playwright/test";
import {
  cleanupOverview,
  storageChecks,
  counterChecks,
  orphanChecks,
} from "../admin-resource-fixture";

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
  await page.route("**/api/v1/admin/counter-checks", (route) =>
    route.fulfill({ json: counterChecks }),
  );
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [], next_cursor: null } }),
  );
}

test("orphan checks show queued progress, observation grace and retained unsupported entries on mobile", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await admin(page);
  let response: Record<string, unknown> = {
    ...orphanChecks,
    state: "pending",
    scan_pending: true,
    last_scan_completed_at: undefined,
  };
  await page.route("**/api/v1/admin/orphan-checks", (route) => route.fulfill({ json: response }));
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Orphan file checks", exact: true });
  await expect(panel).toContainText("Orphan checks pending");
  await expect(panel).toContainText("No completed orphan pass recorded yet");
  await expect(panel).toContainText("observed for at least one hour");
  await expect(panel).toContainText("Queued candidates are not confirmed orphans");
  response = {
    ...orphanChecks,
    state: "degraded",
    scan_pending: true,
    pending_directories: 3,
    pending_candidates: 7,
    busy_count: 2,
    failed_count: 1,
    unsupported_count: 4,
    oldest_pending_at: "2026-10-04T10:00:00Z",
  };
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  for (const [label, count] of [
    ["Queued directories", "3"],
    ["Queued candidates", "7"],
    ["Queued checks awaiting safe access", "2"],
    ["Queued failures", "1"],
    ["Recorded unsupported entries", "4"],
  ]) {
    await expect(panel.locator("dl > div").filter({ hasText: label })).toContainText(count);
  }
  await expect(panel).toContainText(
    "Unknown or suspicious entries are retained for operator review",
  );
  await expect(panel).toContainText("not scanned recursively");
  await expect(panel).toContainText("Oldest queued observation");
  await expect(panel).toContainText("not a complete disk inventory");
  await expect(panel).toContainText("do not verify encryption");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await panel.screenshot({ path: testInfo.outputPath("orphan-checks-mobile.png") });
});

test("saturated and unstable orphan passes remain incomplete, including global failure with no queued work", async ({
  page,
}) => {
  await admin(page);
  let response: Record<string, unknown> = {
    ...orphanChecks,
    state: "pending",
    scan_pending: true,
    saturated: true,
  };
  await page.route("**/api/v1/admin/orphan-checks", (route) => route.fulfill({ json: response }));
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Orphan file checks", exact: true });
  await expect(panel).toContainText("Orphan checks incomplete");
  await expect(panel).toContainText("work queue reached its limit");
  await expect(panel).not.toContainText("Last orphan pass completed");
  response = { ...orphanChecks, state: "pending", scan_pending: true, unstable: true };
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  await expect(panel).toContainText("Storage changed during scanning");
  await expect(panel).not.toContainText("work queue reached its limit");
  response = {
    ...orphanChecks,
    state: "degraded",
    scan_pending: true,
    scan_error_code: "scan_failed",
  };
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "The orphan scan could not complete. It will retry.",
  );
  response = orphanChecks;
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await expect(panel).toContainText("Last orphan pass completed");
  await expect(panel).toContainText("Last completed orphan pass");
});

test("unavailable and malformed orphan snapshots preserve prior data independently without revealing raw paths", async ({
  page,
}) => {
  await admin(page);
  let response: Record<string, unknown> = {
    ...orphanChecks,
    state: "pending",
    scan_pending: true,
    pending_candidates: 8,
  };
  let offline = false;
  let otherReads = 0;
  await page.route("**/api/v1/admin/counter-checks", (route) => {
    otherReads++;
    return route.fulfill({ json: counterChecks });
  });
  await page.route("**/api/v1/admin/orphan-checks", (route) => {
    expect(route.request().method()).toBe("GET");
    return route.fulfill(
      offline ? { status: 503, json: { error: "/private/DO_NOT_DISPLAY" } } : { json: response },
    );
  });
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Orphan file checks", exact: true });
  await expect(panel).toContainText("Orphan checks pending");
  for (const invalid of [
    { pending_directories: 65 },
    { pending_candidates: 257 },
    { failed_count: 321 },
    { busy_count: -1 },
    { unsupported_count: 257 },
    { pending_candidates: 0.5 },
    { state: "unsafe" },
    { saturated: "yes" },
    { unstable: null },
    { scan_pending: "yes" },
    { last_scan_completed_at: "/private/DO_NOT_DISPLAY" },
    { oldest_pending_at: "/private/DO_NOT_DISPLAY" },
    { scan_error_code: "/private/DO_NOT_DISPLAY" },
  ]) {
    response = { ...orphanChecks, ...invalid };
    await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
    await expect(panel.getByRole("alert")).toContainText(
      "previous snapshot remains below and may be stale",
    );
    await expect(panel.locator("dl > div").filter({ hasText: "Queued candidates" })).toContainText(
      "8",
    );
    await expect(panel).not.toContainText("DO_NOT_DISPLAY");
  }
  offline = true;
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "previous snapshot remains below and may be stale",
  );
  expect(otherReads).toBe(1);
  offline = false;
  response = orphanChecks;
  await panel.getByRole("button", { name: "Refresh orphan checks" }).click();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await expect(panel).toContainText("Last orphan pass completed");
});

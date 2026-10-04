import { test, expect, type Page } from "@playwright/test";
import { cleanupOverview, storageChecks, counterChecks } from "../admin-resource-fixture";

async function admin(page: Page) {
  await page.route("**/api/v1/admin/counter-checks", (route) =>
    route.fulfill({ json: counterChecks }),
  );
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
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [], next_cursor: null } }),
  );
}

test("initial file checks show pending coverage without claiming a clean disk", async ({
  page,
}) => {
  await admin(page);
  await page.route("**/api/v1/admin/storage-checks", (route) =>
    route.fulfill({
      json: {
        ...storageChecks,
        state: "pending",
        last_scan_completed_at: undefined,
        scan_pending: true,
      },
    }),
  );
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Stored file checks" });
  await expect(panel).toContainText("Initial checks pending");
  await expect(panel).toContainText("No completed database-file pass recorded yet");
  await expect(panel).toContainText("Files are still awaiting checks or retry");
  await expect(panel).toContainText("not an orphan-file scan");
  await expect(panel).not.toContainText("No recorded issues");
});

test("file checks distinguish published problems and repair failures, and preserve snapshots independently", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await admin(page);
  let mode = "ok",
    reads = 0;
  const snapshot = {
    ...storageChecks,
    state: "degraded",
    issue_count: 9,
    unavailable_count: 3,
    failed_count: 4,
    busy_count: 2,
    scan_pending: true,
    private_path: "/private/payload/DO_NOT_DISPLAY",
  };
  await page.route("**/api/v1/admin/storage-checks", (route) => {
    reads++;
    expect(route.request().method()).toBe("GET");
    return route.fulfill(
      mode === "offline"
        ? { status: 503, json: { error: "/private/payload/DO_NOT_DISPLAY" } }
        : {
            json:
              mode === "bad-count"
                ? { ...snapshot, busy_count: -1 }
                : mode === "bad-time"
                  ? { ...snapshot, last_scan_completed_at: "private/invalid-time" }
                  : snapshot,
          },
    );
  });
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Stored file checks" });
  await expect(panel).toContainText("Unresolved file checks");
  await expect(
    panel.locator("dl > div").filter({ hasText: "Published payloads unavailable" }),
  ).toContainText("3");
  await expect(
    panel.locator("dl > div").filter({ hasText: "Inspection or repair failures" }),
  ).toContainText("4");
  await expect(
    panel.locator("dl > div").filter({ hasText: "Waiting for active file operations" }),
  ).toContainText("2");
  await expect(panel).toContainText("Last completed database-file pass");
  await expect(panel).toContainText("Pending uploads can be repaired before publication");
  for (mode of ["offline", "bad-count", "bad-time"]) {
    await panel.getByRole("button", { name: "Refresh file checks", exact: true }).click();
    await expect(panel.getByRole("alert")).toContainText(
      "previous snapshot remains below and may be stale",
    );
    await expect(panel).toContainText("Unresolved file checks");
    await expect(panel).not.toContainText("DO_NOT_DISPLAY");
  }
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ status: 503, json: {} }),
  );
  await page.getByRole("button", { name: "Refresh resources", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "Resources could not be loaded" }),
  ).toBeVisible();
  mode = "ok";
  await panel.getByRole("button", { name: "Refresh file checks", exact: true }).click();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  expect(reads).toBe(5);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("storage-checks-mobile.png"), fullPage: true });
});

test("global scan failure remains visible with zero file issues and busy retries are not an initial scan", async ({
  page,
}) => {
  await admin(page);
  let response = {
    ...storageChecks,
    state: "degraded",
    scan_error_code: "scan_failed" as string | undefined,
  };
  await page.route("**/api/v1/admin/storage-checks", (route) => route.fulfill({ json: response }));
  await page.goto("/?view=resources");
  const panel = page.getByRole("region", { name: "Stored file checks" });
  await expect(panel.getByRole("alert")).toContainText(
    "The database-file scan could not complete. It will retry.",
  );
  await expect(panel).toContainText("File checks incomplete");
  await expect(panel).not.toContainText("No recorded issues");
  response = {
    ...storageChecks,
    state: "pending",
    issue_count: 2,
    busy_count: 2,
    scan_pending: true,
    scan_error_code: undefined,
  };
  await panel.getByRole("button", { name: "Refresh file checks", exact: true }).click();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await expect(panel).toContainText("File checks pending");
  await expect(panel).not.toContainText("Initial checks pending");
  await expect(panel).toContainText("Files are still awaiting checks or retry");
});

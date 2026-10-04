import { test, expect, type Page } from "@playwright/test";
import { adminResource, cleanupOverview, resourceID, receiveID } from "../admin-resource-fixture";

async function session(page: Page, role = "admin") {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: role, role, username: role } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25 * 1024 ** 2 } }),
  );
  await page.route("**/api/v1/admin/security", (route) =>
    route.fulfill({ json: { enabled: true, recovery_codes_remaining: 10, recent_until: null } }),
  );
  await page.route("**/api/v1/admin/cleanup", (route) => route.fulfill({ json: cleanupOverview }));
  await page.route("**/api/v1/admin/resources/*/*/events?**", (route) =>
    route.fulfill({
      json: {
        events: [
          {
            id: 10,
            occurred_at: "2026-10-04T12:00:00Z",
            kind: "transfer.revoked",
            origin: "administrator",
            actor_id: "administrator-id",
            target_type: "transfer",
            target_id: resourceID,
            outcome: "succeeded",
            count: 1,
          },
        ],
        next_before: null,
        retention_days: 90,
        max_events: 10000,
        degraded: false,
      },
    }),
  );
}

test("resource pages replace rows, preserve failed pages, and show owner and parent inbox details", async ({
  page,
}) => {
  await session(page);
  let fail = true;
  const transfer = adminResource({ parent_slot_id: receiveID });
  const slot = adminResource({
    id: receiveID,
    type: "slot",
    status: "waiting",
    child_transfer_count: 151,
    file_count: 154,
  });
  await page.route("**/api/v1/admin/resources?**", (route) => {
    const query = new URL(route.request().url()).searchParams;
    expect(query.get("limit")).toBe("50");
    if (query.has("after")) {
      expect(query.get("after")).toBe("page-2");
      return route.fulfill(
        fail
          ? { status: 503, json: { error: "Private database details" } }
          : { json: { resources: [slot], next_cursor: null } },
      );
    }
    return route.fulfill({ json: { resources: [transfer], next_cursor: "page-2" } });
  });
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) =>
    route.fulfill({ json: transfer }),
  );
  await page.route(`**/api/v1/admin/resources/slot/${receiveID}`, (route) =>
    route.fulfill({ json: slot }),
  );
  await page.goto("/?view=resources");
  const nav = page.getByRole("navigation", { name: "Account navigation" });
  await expect(nav.getByRole("link")).toHaveCount(5);
  await expect(page.locator(`[data-resource-id="${resourceID}"]`)).toContainText(
    "Received transfer",
  );
  await expect(page.getByRole("region", { name: "Cleanup overview" })).toContainText(
    "2 pending · 1 failed",
  );
  await page.getByRole("button", { name: "Older resources", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("previous resource page remains");
  await expect(page.getByRole("alert")).not.toContainText("Private database");
  await expect(page.locator(`[data-resource-id="${resourceID}"]`)).toBeVisible();
  fail = false;
  await page.getByRole("button", { name: "Older resources", exact: true }).click();
  await expect(page.locator("[data-resource-id]")).toHaveCount(1);
  await expect(page.locator(`[data-resource-id="${resourceID}"]`)).toHaveCount(0);
  await page.getByRole("button", { name: "Newer resources", exact: true }).click();
  await page.getByRole("button", { name: "Inspect", exact: true }).click();
  const detail = page.getByRole("region", { name: "Resource details", exact: true });
  await expect(detail).toContainText("Member");
  await expect(detail).toContainText("Reserved storage");
  await expect(detail.getByRole("heading", { name: "Related security activity" })).toBeVisible();
  await detail.getByRole("button", { name: receiveID, exact: true }).click();
  await expect(detail).toContainText("Receive link · Files received");
  await expect(detail).toContainText("151");
});

test("revocation stays visibly pending, cleanup failures preserve state, and confirmed removal keeps audit accessible", async ({
  page,
}) => {
  await session(page);
  let item = adminResource();
  let retryFails = true,
    removed = false;
  const writes: string[] = [];
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [item], next_cursor: null } }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) =>
    route.fulfill(removed ? { status: 404, json: {} } : { json: item }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}/revoke`, (route) => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postDataJSON()).toEqual({});
    writes.push("revoke");
    item = {
      ...item,
      status: "revoked",
      cleanup: {
        state: "failed",
        reason: "revoked",
        attempt_count: 2,
        failure_code: "storage_delete_failed",
        last_failure_at: "2026-10-04T12:00:00Z",
      },
    };
    return route.fulfill({
      status: 202,
      json: {
        state: "pending",
        type: "transfer",
        id: resourceID,
        cleanup: { state: "busy", attempt_count: 1 },
      },
    });
  });
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}/cleanup`, (route) => {
    expect(route.request().postDataJSON()).toEqual({});
    writes.push("cleanup");
    if (retryFails)
      return route.fulfill({ status: 503, json: { error: "/private/storage/unlink failed" } });
    removed = true;
    return route.fulfill({ json: { state: "removed", type: "transfer", id: resourceID } });
  });
  await page.goto("/?view=resources");
  await page.getByRole("button", { name: "Inspect", exact: true }).click();
  await page.getByRole("button", { name: "Revoke", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(resourceID);
  await expect(dialog).toContainText("Owner: Member");
  await dialog.getByRole("button", { name: "Revoke and delete" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByText("The link is revoked. Server cleanup is still pending.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Server cleanup is complete.", { exact: false })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Cleanup failed", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Retry cleanup", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "action could not be confirmed" }),
  ).not.toContainText("/private/storage");
  await expect(page.getByRole("heading", { name: "Cleanup failed", exact: true })).toBeVisible();
  retryFails = false;
  await page.getByRole("button", { name: "Retry cleanup", exact: true }).click();
  await expect(
    page.getByText("Resource removed. Server cleanup is complete.", { exact: false }),
  ).toBeVisible();
  await expect(page.locator("[data-resource-id]")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Related security activity" })).toBeVisible();
  await page.getByRole("button", { name: "Refresh details", exact: true }).click();
  await expect(page.getByText("Resource not found.", { exact: false })).toBeVisible();
  expect(writes).toEqual(["revoke", "cleanup", "cleanup"]);
});

test("resource lookup rejects complete links locally and distinguishes missing resources from unavailable details", async ({
  page,
}) => {
  await session(page);
  const requests: string[] = [];
  let mode = "ok";
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [], next_cursor: null } }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) => {
    requests.push(route.request().url());
    return route.fulfill(
      mode === "ok"
        ? { json: adminResource() }
        : { status: mode === "missing" ? 404 : 503, json: {} },
    );
  });
  await page.goto("/?view=resources");
  await page
    .getByLabel("Resource ID", { exact: true })
    .fill(`https://other.example/d/${resourceID}#DO_NOT_SEND_SECRET`);
  await page.getByRole("button", { name: "Find resource", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Enter the resource ID only");
  expect(requests).toEqual([]);
  await page.getByLabel("Resource ID", { exact: true }).fill(resourceID);
  await page.getByRole("button", { name: "Find resource", exact: true }).click();
  await expect(page.getByRole("region", { name: "Resource details", exact: true })).toContainText(
    "Member",
  );
  mode = "unavailable";
  await page.getByRole("button", { name: "Refresh details", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Previously loaded details may be stale");
  await expect(page.getByRole("region", { name: "Resource details", exact: true })).toContainText(
    "Member",
  );
  mode = "missing";
  await page.getByRole("button", { name: "Refresh details", exact: true }).click();
  await expect(page.getByText("Resource not found.", { exact: false })).toBeVisible();
  expect(requests.every((url) => !url.includes("DO_NOT_SEND_SECRET"))).toBe(true);
});

test("regular accounts cannot open resource administration", async ({ page }) => {
  await session(page, "user");
  let calls = 0;
  await page.route("**/api/v1/admin/resources**", (route) => {
    calls++;
    return route.fulfill({ json: {} });
  });
  await page.goto("/?view=resources");
  await expect(page.getByRole("heading", { name: "Send files", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Resources", exact: true })).toHaveCount(0);
  expect(calls).toBe(0);
});

test("resource details and safe cleanup failures fit a narrow viewport", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await session(page);
  const item = adminResource({
    status: "revoked",
    owner_username: "<img src=x onerror=alert(1)>",
    cleanup: { state: "failed", attempt_count: 3, failure_code: "private/path#error" },
  });
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [item], next_cursor: null } }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) =>
    route.fulfill({ json: item }),
  );
  await page.goto("/?view=resources");
  await page.getByRole("button", { name: "Inspect", exact: true }).click();
  const detail = page.getByRole("region", { name: "Resource details", exact: true });
  await expect(detail.getByRole("alert")).toContainText("last cleanup attempt failed");
  await expect(detail).not.toContainText("private/path");
  await expect(detail.locator("img")).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.evaluate(() => {
    (document.activeElement as HTMLElement)?.blur();
    window.scrollTo(0, 0);
  });
  await page.screenshot({ path: testInfo.outputPath("admin-resource-mobile.png"), fullPage: true });
});

test("retrying exhausted payload cleanup preserves transfer status without claiming revocation", async ({
  page,
}) => {
  await session(page);
  const item = adminResource({
    status: "complete",
    cleanup: {
      state: "failed",
      reason: "download_limit",
      attempt_count: 1,
      failure_code: "storage_delete_failed",
    },
  });
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [item], next_cursor: null } }),
  );
  let refreshing = false;
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) =>
    route.fulfill(refreshing ? { status: 503, json: {} } : { json: item }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}/cleanup`, (route) => {
    refreshing = true;
    return route.fulfill({
      status: 202,
      json: {
        state: "pending",
        type: "transfer",
        id: resourceID,
        cleanup: { state: "busy", reason: "download_limit", attempt_count: 2 },
      },
    });
  });
  await page.goto("/?view=resources");
  await page.getByRole("button", { name: "Inspect", exact: true }).click();
  await page.getByRole("button", { name: "Retry cleanup", exact: true }).click();
  await expect(
    page.getByText("Cleanup queued. Refresh details to confirm its progress.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Resource details", exact: true })).toContainText(
    "Ready to download",
  );
  await expect(page.getByRole("status").filter({ hasText: "revoked" })).toHaveCount(0);
  await expect(page.getByRole("alert")).not.toContainText("link is revoked");
});

test("completed payload cleanup keeps resource metadata and status without claiming removal", async ({
  page,
}) => {
  await session(page);
  let item = adminResource({
    status: "complete",
    cleanup: {
      state: "failed",
      reason: "download_limit",
      attempt_count: 1,
      failure_code: "storage_delete_failed",
    },
  });
  await page.route("**/api/v1/admin/resources?**", (route) =>
    route.fulfill({ json: { resources: [item], next_cursor: null } }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}`, (route) =>
    route.fulfill({ json: item }),
  );
  await page.route(`**/api/v1/admin/resources/transfer/${resourceID}/cleanup`, (route) => {
    item = {
      ...item,
      occupied_bytes_estimate: 128,
      reserved_bytes: 128,
      cleanup: { state: "none", attempt_count: 0 },
    };
    return route.fulfill({
      json: { state: "complete", type: "transfer", id: resourceID, cleanup: item.cleanup },
    });
  });
  await page.goto("/?view=resources");
  await page.getByRole("button", { name: "Inspect", exact: true }).click();
  await page.getByRole("button", { name: "Retry cleanup", exact: true }).click();
  await expect(
    page.getByText("Server cleanup is complete. Resource metadata remains available.", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.locator(`[data-resource-id="${resourceID}"]`)).toContainText(
    "128 B estimated occupied",
  );
  const detail = page.getByRole("region", { name: "Resource details", exact: true });
  await expect(detail).toContainText("Ready to download");
  await expect(detail.getByRole("heading", { name: "No cleanup queued" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry cleanup", exact: true })).toHaveCount(0);
  await expect(page.getByRole("status").filter({ hasText: /revoked|removed/ })).toHaveCount(0);
});

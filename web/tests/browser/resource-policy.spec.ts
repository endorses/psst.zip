import { historyTransfer, historySlot, historyPage, historyCursor } from "../history-page-fixture";
import {
  adminResource,
  cleanupOverview,
  storageChecks,
  counterChecks,
  orphanChecks,
} from "../admin-resource-fixture";
import { test, expect, type Page } from "@playwright/test";
import { resourcePolicy, resourceUsage } from "../resource-policy-fixture";

async function session(page: Page, role: "admin" | "user" = "user") {
  await page.route("**/api/v1/admin/abuse-contact", (route) =>
    route.fulfill({ json: { email: "" } }),
  );
  await page.route("**/api/v1/admin/incident-state", (route) =>
    route.fulfill({ json: { public_transfers_paused: false, updated_at: "2026-10-04T00:00:00Z" } }),
  );
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: `policy-${role}`, username: role, role } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({
      json: {
        max_file_size: 25 * 1024 ** 2,
        max_file_size_ceiling: 1024 ** 4,
        resource_policy: resourcePolicy,
      },
    }),
  );
  await page.route("**/api/v1/auth/usage", (route) =>
    route.fulfill({ json: { policy: resourcePolicy, usage: resourceUsage } }),
  );
}

test("administrator sees reservations and can lower quota without hiding existing usage", async ({
  page,
}) => {
  await session(page, "admin");
  let policy = { ...resourcePolicy };
  let fail = false;
  const writes: unknown[] = [];
  await page.route("**/api/v1/admin/resource-policy", (route) => {
    if (route.request().method() === "PATCH") {
      if (fail) return route.fulfill({ status: 503, json: { error: "Policy could not be saved" } });
      policy = route.request().postDataJSON();
      writes.push(policy);
    }
    return route.fulfill({ json: { policy, usage: resourceUsage } });
  });
  await page.goto("/?view=server");
  const usage = page.getByLabel("Server resource usage");
  await expect(usage).toContainText("1 GiB");
  await expect(usage).toContainText("1 MiB");
  await page.getByLabel("Server storage (MiB)", { exact: true }).fill("512");
  await page.getByRole("button", { name: "Save resource policy", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Lower quotas block new allocations");
  expect(writes).toHaveLength(1);
  expect(policy.server_storage_bytes).toBe(512 * 1024 ** 2);
  await expect(
    usage.getByText("Current allocations exceed a quota.", { exact: false }),
  ).toBeVisible();
  await expect(
    usage.locator("dl > div").filter({ hasText: "Available within storage quota" }),
  ).toContainText("0 B");
  await expect(usage.locator("dl > div").filter({ hasText: "Reserved storage" })).toContainText(
    "1 GiB",
  );
  fail = true;
  await page.getByLabel("Server storage (MiB)", { exact: true }).fill("768");
  await page.getByRole("button", { name: "Save resource policy", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Previous usage may be stale");
  await expect(page.getByLabel("Server storage (MiB)", { exact: true })).toHaveValue("768");
  await expect(page.getByRole("link", { name: "Overview", exact: true })).toBeVisible();
});

test("resource-policy failure leaves existing administrator upload settings usable", async ({
  page,
}) => {
  await session(page, "admin");
  await page.route("**/api/v1/admin/resource-policy", (route) =>
    route.fulfill({ status: 503, json: { error: "Unavailable" } }),
  );
  await page.route("**/api/v1/admin/settings", (route) =>
    route.fulfill({ json: { max_file_size: route.request().postDataJSON().max_file_size } }),
  );
  await page.goto("/?view=server");
  await expect(page.getByRole("alert")).toContainText(
    "Other server settings and recovery actions remain available",
  );
  await page.getByLabel("Maximum file size (MiB)").fill("16");
  await page.getByRole("button", { name: "Save file limit" }).click();
  await expect(page.getByRole("status")).toContainText("File limit saved");
  await expect(page.getByRole("button", { name: "Retry loading resource policy" })).toBeEnabled();
});

test("ordinary account sees its usage and public policy without administrator controls", async ({
  page,
}) => {
  await session(page);
  await page.goto("/");
  await expect(page.getByText(/Server policy: 2 GiB reserved storage per account/)).toBeVisible();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByText("Account storage and resource usage", { exact: true }).click();
  await expect(page.getByLabel("Account resource usage")).toContainText("1 GiB");
  await expect(page.getByLabel("Account resource usage")).toContainText("1000");
  await expect(page.getByLabel("Server storage (MiB)", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save resource policy" })).toHaveCount(0);
});

test("history navigation fetches one bounded page and retains it on a failed next-page request", async ({
  page,
}) => {
  await session(page);
  const first = "12345678-1234-1234-1234-123456789001",
    second = "12345678-1234-1234-1234-123456789002";
  const calls: string[] = [];
  let fail = true;
  await page.route("**/api/v1/auth/resources?*", (route) => {
    const query = new URL(route.request().url()).searchParams;
    expect(query.get("limit")).toBe("50");
    const after = query.get("after") || "";
    calls.push(after);
    if (after && fail)
      return route.fulfill({ status: 503, json: { error: "Next page unavailable" } });
    return route.fulfill({
      json: {
        paginated: true,
        transfers: [
          historyTransfer({
            id: after ? second : first,
            file_count: 1,
            status: "complete",
            expires_at: "2030-01-01T00:00:00Z",
            created_at: "2026-01-01T00:00:00Z",
          }),
        ],
        slots: [],
        next_cursor: after ? null : historyCursor(1),
      },
    });
  });
  await page.goto("/?view=history");
  await expect(page.locator(`[data-resource-id="${first}"]`)).toBeVisible();
  expect(calls.every((cursor) => cursor === "")).toBe(true);
  await page.getByRole("button", { name: "Older transfers" }).click();
  await expect(page.getByRole("alert")).toContainText("Next page unavailable");
  await expect(page.locator(`[data-resource-id="${first}"]`)).toBeVisible();
  fail = false;
  await page.getByRole("button", { name: "Older transfers" }).click();
  await expect(page.locator(`[data-resource-id="${second}"]`)).toBeVisible();
  await expect(page.locator(`[data-resource-id="${first}"]`)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Older transfers" })).toBeDisabled();
  await page.getByRole("button", { name: "Newer transfers" }).click();
  await expect(page.locator(`[data-resource-id="${first}"]`)).toBeVisible();
  expect(calls.filter((cursor) => cursor).every((cursor) => cursor === historyCursor(1))).toBe(
    true,
  );
});

test("quota rejection has a stable actionable message and never starts file upload", async ({
  page,
}) => {
  await session(page);
  let files = 0;
  await page.route("**/api/v1/transfers", (route) =>
    route.fulfill({
      status: 403,
      json: { code: "resource_limit", error: "opaque database detail" },
    }),
  );
  page.on("request", (request) => {
    if (request.url().endsWith("/files") && request.method() === "POST") files++;
  });
  await page.goto("/");
  await page
    .getByLabel("Choose files")
    .setInputFiles({ name: "tiny.txt", mimeType: "text/plain", buffer: Buffer.from("x") });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("storage or object limit");
  await expect(page.getByRole("alert")).not.toContainText("database");
  expect(files).toBe(0);
});

test("administrator account navigation is bounded and follows cursors only on demand", async ({
  page,
}) => {
  await session(page, "admin");
  const cursors: string[] = [];
  await page.route("**/api/v1/admin/users?*", (route) => {
    const query = new URL(route.request().url()).searchParams;
    expect(query.get("limit")).toBe("50");
    const after = query.get("after") || "";
    cursors.push(after);
    return route.fulfill({
      json: {
        users: [
          {
            id: after ? "second" : "first",
            username: after ? "Older member" : "First member",
            role: "user",
            disabled: false,
          },
        ],
        next_cursor: after ? null : "account-cursor",
      },
    });
  });
  await page.goto("/?view=users");
  await expect(page.getByText("First member", { exact: true })).toBeVisible();
  expect(cursors).toEqual([""]);
  await page.getByRole("button", { name: "More accounts" }).click();
  await expect(page.getByText("Older member", { exact: true })).toBeVisible();
  await expect(page.getByText("First member", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "More accounts" })).toBeDisabled();
  await page.getByRole("button", { name: "Previous accounts" }).click();
  await expect(page.getByText("First member", { exact: true })).toBeVisible();
});

for (const role of ["user", "admin"] as const) {
  test(`${role} resource list displays a 151-child inbox aggregate without expanding its children`, async ({
    page,
  }) => {
    await session(page, role);
    const id = "12345678-1234-1234-1234-123456789151";
    const unknown = "12345678-1234-1234-1234-123456789152";
    let detailedRequests = 0;
    await page.route("**/api/v1/slots/*", (route) => {
      detailedRequests++;
      return route.abort();
    });
    await page.route("**/api/v1/auth/resources?*", (route) =>
      route.fulfill({
        json: {
          paginated: true,
          transfers: [],
          next_cursor: null,
          slots: [
            historySlot({
              id,
              file_count: 154,
              completed_files: 151,
              reserved_files: 200,
              total_size: 1024 ** 2,
              transfers: [],
              receive_protocol: 2,
              expires_at: "2030-01-01T00:00:00Z",
            }),
            historySlot({
              id: unknown,
              file_count: null,
              completed_files: null,
              total_size: null,
              transfers: [],
              expires_at: "2030-01-01T00:00:00Z",
            }),
          ],
        },
      }),
    );
    if (role === "admin") {
      await page.route("**/api/v1/admin/orphan-checks", (route) =>
        route.fulfill({ json: orphanChecks }),
      );
      await page.route("**/api/v1/admin/counter-checks", (route) =>
        route.fulfill({ json: counterChecks }),
      );
      await page.route("**/api/v1/admin/storage-checks", (route) =>
        route.fulfill({ json: storageChecks }),
      );
      await page.route("**/api/v1/admin/overview", (route) =>
        route.fulfill({ status: 503, json: { error: "Metrics unavailable in fixture" } }),
      );
      await page.route("**/api/v1/admin/users", (route) => route.fulfill({ json: { users: [] } }));
      await page.route("**/api/v1/admin/resources?**", (route) =>
        route.fulfill({
          json: {
            resources: [
              adminResource({
                id,
                type: "slot",
                status: "waiting",
                file_count: 154,
                child_transfer_count: 151,
              }),
            ],
            next_cursor: null,
          },
        }),
      );
      await page.route("**/api/v1/admin/cleanup", (route) =>
        route.fulfill({ json: cleanupOverview }),
      );
      await page.goto("/?view=overview");
      await page.getByRole("link", { name: "View resources", exact: true }).click();
    } else await page.goto("/?view=history");
    const summary = page.locator(`[data-resource-id="${id}"]`);
    await expect(summary).toContainText(role === "admin" ? "154 files" : "151 files received");
    await expect(summary).toContainText("1.0 MiB");
    if (role === "admin") await expect(summary).toContainText("Files received");
    if (role === "user")
      await expect(page.locator(`[data-resource-id="${unknown}"]`)).toContainText(
        "File totals updating",
      );
    expect(detailedRequests).toBe(0);
  });
}

for (const state of ["ready", "blocked", "unknown"] as const) {
  test(`account capacity distinguishes ${state} disk state from quota headroom`, async ({
    page,
  }) => {
    await session(page);
    await page.route("**/api/v1/auth/usage", (route) =>
      route.fulfill({
        json: {
          policy: resourcePolicy,
          usage: resourceUsage,
          capacity: {
            checked_at: "2026-10-04T14:00:00Z",
            scope: "account",
            state,
            available_wire_bytes:
              state === "unknown" ? null : state === "ready" ? 8 * 1024 ** 2 : 0,
            available_files: 12,
            available_transfers: 3,
            available_slots: 2,
            ...(state === "blocked" ? { reason: "disk_capacity" } : {}),
            ...(state === "unknown" ? { reason: "capacity_unavailable" } : {}),
          },
        },
      }),
    );
    await page.goto("/?view=settings");
    await page.getByText("Account storage and resource usage", { exact: true }).click();
    const capacity = page.getByLabel("Current upload capacity");
    await expect(capacity).toContainText(
      state === "ready"
        ? "8 MiB currently available"
        : state === "blocked"
          ? "blocked by the disk safety reserve"
          : "Current disk capacity could not be checked",
    );
    await expect(capacity).toContainText("not a reservation");
    await expect(capacity).toContainText("Transfer pauses and per-link limits apply separately");
    const quota = page
      .getByLabel("Account resource usage")
      .locator("dl > div")
      .filter({ hasText: "Available within storage quota" });
    await expect(quota).toContainText("1 GiB");
    if (state !== "ready") await expect(capacity).not.toContainText("currently available for new");
  });
}

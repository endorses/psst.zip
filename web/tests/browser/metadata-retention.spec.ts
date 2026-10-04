import { test, expect, type Page } from "@playwright/test";
import type { TrafficReport, Totals } from "../../src/lib/admin";
import { trafficSnapshot } from "../traffic-policy-fixture";

async function signedIn(page: Page, role: "admin" | "user" = "admin") {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: role, role, username: role } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25 * 1024 ** 2 } }),
  );
  await page.route("**/api/v1/admin/security", (route) =>
    route.fulfill({ json: { enabled: true, recovery_codes_remaining: 10, recent_until: null } }),
  );
  await page.route("**/api/v1/admin/traffic-policy", (route) =>
    route.fulfill({ json: trafficSnapshot() }),
  );
}
const totals: Totals = {
  uploaded_bytes: 1024,
  downloaded_bytes: 2048,
  total_bytes: 3072,
  files_uploaded: 1,
  files_delivered: 2,
  standalone_files_uploaded: 1,
  received_files_uploaded: 0,
};
const report: TrafficReport = {
  recording_started_at: "2023-01-01T00:00:00Z",
  history_retained_from: "2025-08-31",
  history_retention_days: 400,
  updated_at: "2026-10-04T00:00:00Z",
  status: "ok",
  timezone: "UTC",
  settings: { allowance_bytes: null, cycle_start_day: 1, basis: "outbound" },
  today: totals,
  month: totals,
  lifetime: { ...totals, total_bytes: 1024 ** 3 },
  range: { from: "2026-10-01", to: "2026-10-04" },
  totals,
  days: [{ date: "2026-10-01", ...totals }],
  cycle: {
    ...totals,
    start: "2026-10-01",
    end: "2026-11-01",
    counted_bytes: 2048,
    remaining_bytes: null,
  },
};

test("retained chart dates are constrained and expired requests preserve the prior measurements", async ({
  page,
}) => {
  await signedIn(page);
  await page.route("**/api/v1/admin/traffic", (route) => route.fulfill({ json: report }));
  await page.route("**/api/v1/admin/traffic?**", (route) =>
    route.fulfill({
      status: 400,
      json: { code: "traffic_history_unavailable", error: "Internal storage diagnostic" },
    }),
  );
  await page.goto("/?view=traffic");
  const from = page.getByLabel("From (UTC)", { exact: true });
  const through = page.getByLabel("Through (UTC, inclusive)", { exact: true });
  await expect(from).toHaveAttribute("min", report.history_retained_from);
  await expect(through).toHaveAttribute("min", report.range.from);
  await expect(page.getByText(/Daily details are retained for 400 days/)).toContainText(
    "Measured lifetime totals preserve all recorded traffic, including older days",
  );
  await expect(
    page.locator(".metrics article").filter({ hasText: "Measured lifetime" }),
  ).toContainText("1.0 GiB");
  await from.fill("");
  await expect(through).toHaveAttribute("min", report.history_retained_from);
  await from.fill("2025-08-30");
  expect(await from.evaluate((input: HTMLInputElement) => input.validity.rangeUnderflow)).toBe(
    true,
  );
  // The cutoff may advance after loading; a rejected refresh must never fabricate zeros.
  await from.fill("2025-08-31");
  await through.fill("2025-09-01");
  await page.getByRole("button", { name: "Show traffic", exact: true }).click();
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("This period is outside the available daily history");
  await expect(alert).toContainText("The previous measurements remain below and may be stale");
  await expect(alert).not.toContainText("Internal storage diagnostic");
  await expect(page.getByText("2026-10-01 through 2026-10-04 (inclusive, UTC)")).toBeVisible();
  await expect(
    page.getByText("Uploaded 1.0 KiB · Downloaded 2.0 KiB · Combined 3.0 KiB"),
  ).toBeVisible();
  await page.getByText("Daily traffic data (exact bytes)", { exact: true }).click();
  await expect(page.getByRole("table")).toContainText("3,072");
  await expect(from).toHaveValue("2025-08-31");
});
for (const role of ["admin", "user"] as const) {
  test(`${role} sees session rotation and a truncated legacy session list warning`, async ({
    page,
  }) => {
    await signedIn(page, role);
    let reconciled = false;
    await page.route("**/api/v1/auth/sessions", (route) =>
      route.fulfill({
        json: {
          total_active_sessions: reconciled ? 1 : 33,
          total_active_sessions_exact: reconciled,
          sessions_limited: !reconciled,
          sessions: [
            {
              id: "current",
              device_name: "This laptop",
              current: true,
              expires_at: "2026-11-01T00:00:00Z",
            },
          ],
        },
      }),
    );
    await page.goto("/?view=devices");
    await expect(
      page.getByText("Up to 32 active sessions are allowed per account", { exact: false }),
    ).toBeVisible();
    await expect(page.getByRole("status")).toContainText(
      "Showing 1 of at least 33 active sessions",
    );
    await expect(page.getByRole("status")).toContainText("Older sessions are being removed");
    await expect(page.getByText("This laptop (this browser)", { exact: true })).toBeVisible();
    reconciled = true;
    await page.reload();
    await expect(page.getByText("This laptop (this browser)", { exact: true })).toBeVisible();
    await expect(page.getByText(/Showing 1 of at least 33 active sessions/)).toHaveCount(0);
    await expect(
      page.getByText("Up to 32 active sessions are allowed per account", { exact: false }),
    ).toBeVisible();
  });
}

import { test, expect, type Page } from "@playwright/test";

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
}
const event = (id: number) => ({
  id,
  occurred_at: "2026-10-04T12:00:00.000000000Z",
  kind: "account.created",
  origin: "administrator",
  actor_id: "admin-id",
  target_type: "user",
  target_id: `account-${id}`,
  outcome: "succeeded",
  count: 1,
});
const result = (events = [event(100)], next_before: number | null = null, degraded = false) => ({
  events,
  next_before,
  retention_days: 90,
  max_events: 10000,
  degraded,
});

test("administrator activity navigation pages without accumulating rows and preserves a failed page", async ({
  page,
}) => {
  await signedIn(page);
  const requests: string[] = [];
  let fail = true;
  const latest = result(
    Array.from({ length: 50 }, (_, i) => event(100 - i)),
    51,
  );
  await page.route("**/api/v1/admin/security-events?**", (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get("limit")).toBe("50");
    requests.push(url.search);
    if (!url.searchParams.has("before")) return route.fulfill({ json: latest });
    expect(url.searchParams.get("before")).toBe("51");
    if (fail) return route.fulfill({ status: 503, json: { error: "private storage diagnostic" } });
    return route.fulfill({ json: result([event(50)]) });
  });
  await page.goto("/?view=account");
  await page.getByRole("link", { name: "Security activity", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Security activity", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Security activity", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  );
  const rows = page.getByRole("list", { name: "Security events" }).locator("li");
  await expect(rows).toHaveCount(50);
  await expect(page.getByRole("button", { name: "Newer", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Older", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "The previous activity remains below and may be stale",
  );
  await expect(page.getByRole("alert")).not.toContainText("private storage diagnostic");
  await expect(rows).toHaveCount(50);
  await expect(page.getByRole("status")).toContainText("Page 1");
  fail = false;
  await page.getByRole("button", { name: "Older", exact: true }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("account-50");
  await expect(page.getByRole("status")).toContainText("Page 2");
  await expect(page.getByRole("button", { name: "Older", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Newer", exact: true }).click();
  await expect(rows).toHaveCount(50);
  await page.getByRole("button", { name: "Latest activity", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Page 1");
  expect(requests).toHaveLength(5);
});

test("activity shows degraded coverage, friendly fallbacks and escaped identifiers on narrow screens", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signedIn(page);
  const hostile = {
    ...event(100),
    kind: "constructor",
    origin: "<img src=x>",
    outcome: "<script>bad</script>",
    actor_id: "<img src=x onerror=alert(1)>",
    target_id: "<svg onload=alert(1)>",
    count: 8,
  };
  await page.route("**/api/v1/admin/security-events?**", (route) =>
    route.fulfill({
      json: result(
        [
          hostile,
          {
            ...event(99),
            kind: "authentication.login_rejected",
            origin: "system",
            outcome: "rejected",
            target_type: "authentication",
          },
        ],
        null,
        true,
      ),
    }),
  );
  await page.goto("/?view=security");
  await expect(page.getByRole("alert")).toContainText("Some events may be missing");
  const rows = page.getByRole("list", { name: "Security events" });
  await expect(rows).toContainText("Security event");
  await expect(rows).toContainText("Other source");
  await expect(rows).toContainText("Unknown outcome");
  await expect(rows).toContainText(hostile.actor_id);
  await expect(rows).toContainText(hostile.target_id);
  await expect(rows).toContainText("Sign-in rejected");
  await expect(rows).toContainText("UTC");
  await expect(rows.locator("img, svg, script")).toHaveCount(0);
  await expect(page.getByText(/Retained for up to 90 days and 10,000 entries/)).toBeVisible();
  await expect(page.getByText(/without passwords, login codes, link secrets/)).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  const positions = await page
    .getByRole("navigation", { name: "Account navigation" })
    .locator("a > svg")
    .evaluateAll((icons) => icons.map((icon) => icon.getBoundingClientRect().top));
  expect(positions).toHaveLength(5);
  expect(Math.max(...positions) - Math.min(...positions)).toBeLessThan(1);
  await page.screenshot({
    path: testInfo.outputPath("security-activity-mobile.png"),
    fullPage: true,
  });
});

test("regular accounts cannot navigate to administrator activity", async ({ page }) => {
  await signedIn(page, "user");
  let calls = 0;
  await page.route("**/api/v1/admin/security-events?**", (route) => {
    calls++;
    return route.fulfill({ json: result() });
  });
  await page.goto("/?view=security");
  await expect(page.getByRole("heading", { name: "Send files", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Security activity", exact: true })).toHaveCount(0);
  expect(calls).toBe(0);
});

test("oversized activity responses preserve the last bounded snapshot", async ({ page }) => {
  await signedIn(page);
  let oversized = false;
  await page.route("**/api/v1/admin/security-events?**", (route) =>
    route.fulfill({
      json: oversized ? result(Array.from({ length: 51 }, (_, i) => event(100 - i))) : result(),
    }),
  );
  await page.goto("/?view=security");
  const rows = page.getByRole("list", { name: "Security events" }).locator("li");
  await expect(rows).toHaveCount(1);
  oversized = true;
  await page.getByRole("button", { name: "Latest activity", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("The previous activity remains below");
  await expect(rows).toHaveCount(1);
});

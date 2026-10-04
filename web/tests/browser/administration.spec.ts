import { test, expect, authenticate, adminCredentials, signIn, retryAuth } from "./auth-fixture";

test("temporary password is mandatory, mismatch remains local, replacement invalidates the session", async ({
  page,
  adminRequest,
  request,
}) => {
  const username = `change-${Date.now()}`,
    password = "Temporary-test-pass-2026",
    replacement = "Replacement-test-pass-2026";
  expect(
    (
      await adminRequest.post("/api/v1/admin/users", { data: { username, password, role: "user" } })
    ).ok(),
  ).toBe(true);
  await page.goto("/?view=scan");
  await page.getByLabel("Username", { exact: true }).fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  const login = page.waitForResponse((r) => r.url().endsWith("/auth/login"));
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  expect((await login).ok()).toBe(true);
  await expect(page.getByRole("heading", { name: "Choose your own password" })).toBeVisible();
  await expect(page.getByRole("navigation")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Choose your own password" })).toBeVisible();
  await page.getByLabel("Current password", { exact: true }).fill(password);
  await page.getByLabel("New password", { exact: true }).fill(replacement);
  await page.getByLabel("Confirm password", { exact: true }).fill(replacement + "wrong");
  let submissions = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/auth/password")) submissions++;
  });
  await page.getByRole("button", { name: "Change password", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("do not match");
  expect(submissions).toBe(0);
  await page.getByLabel("Confirm password", { exact: true }).fill(replacement);
  await page.getByRole("button", { name: "Change password", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.getByRole("status")).toContainText("Password changed");
  await authenticate(page, { username, password: replacement });
  await expect(page.getByRole("heading", { name: "Scan QR code", exact: true })).toBeVisible();
  expect((await request.get("/api/v1/admin/overview")).status()).toBe(403);
});

test("admin shell redirects transfer routes and shows persistent traffic settings and operational resources", async ({
  page,
  adminRequest,
  request,
}, testInfo) => {
  const created = await request.post("/api/v1/slots");
  expect(created.ok()).toBe(true);
  const slot = await created.json();
  await page.goto("/?view=send");
  await authenticate(page, adminCredentials);
  await expect(page.getByRole("heading", { name: "Overview", exact: true })).toBeVisible();
  await expect(page.getByRole("navigation").getByRole("link")).toHaveCount(4);
  await expect(page.getByRole("link", { name: "Send", exact: true })).toHaveCount(0);
  await expect(page.locator("input[type=file]")).toHaveCount(0);
  await page.getByRole("button", { name: "View resources", exact: true }).click();
  await expect(page.getByRole("button", { name: "Refresh resources" })).toBeVisible();
  const row = page.locator(`[data-resource-id="${slot.id}"]`);
  await expect(row).toContainText("browser-member");
  await row.getByRole("button", { name: "Revoke", exact: true }).click();
  await page.getByRole("button", { name: "Revoke and delete" }).click();
  await expect(row).toHaveCount(0);
  expect((await request.get(`/api/v1/slots/${slot.id}`)).status()).toBe(404);
  await page.evaluate(() => {
    (document.activeElement as HTMLElement)?.blur();
    window.scrollTo(0, 0);
  });
  await page.screenshot({ path: testInfo.outputPath("admin-overview.png"), fullPage: true });
  await page.setViewportSize({ width: 320, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("admin-phone.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.getByRole("link", { name: "Traffic", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Traffic", exact: true })).toBeVisible();
  await page.getByLabel("Allowance (GiB, optional)", { exact: true }).fill("20");
  await page.getByLabel("Cycle starts on day (UTC)", { exact: true }).fill("31");
  await page.getByLabel("Count toward allowance").selectOption("combined");
  await page.getByRole("button", { name: "Save traffic settings" }).click();
  await expect(page.getByRole("status")).toContainText("Traffic allowance saved");
  await page.reload();
  await expect(page.getByLabel("Allowance (GiB, optional)")).toHaveValue("20");
  await expect(page.getByLabel("Cycle starts on day (UTC)")).toHaveValue("31");
  await page.getByText("Daily traffic data (exact bytes)", { exact: true }).click();
  await expect(page.getByRole("table")).toBeVisible();
  await page.evaluate(() => {
    (document.activeElement as HTMLElement)?.blur();
    window.scrollTo(0, 0);
  });
  await page.screenshot({ path: testInfo.outputPath("admin-traffic.png"), fullPage: true });
  await page.getByRole("link", { name: "Sessions", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Signed-in sessions" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Show login QR code" })).toHaveCount(0);
  expect((await adminRequest.post("/api/v1/auth/pairings")).status()).toBe(403);
});

test("phone navigation uses equal columns with icons above single-line labels in both themes", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await signIn(page);
  for (const mode of ["light", "dark"]) {
    await page.evaluate((mode) => (document.documentElement.dataset.theme = mode), mode);
    const layout = await page.getByRole("navigation").evaluate((nav) =>
      Array.from(nav.querySelectorAll("a")).map((a) => {
        const r = a.getBoundingClientRect(),
          icon = a.querySelector("svg")!.getBoundingClientRect(),
          label = a.querySelector("span")!.getBoundingClientRect();
        return {
          w: r.width,
          iconBottom: icon.bottom,
          labelTop: label.top,
          labelHeight: label.height,
          lineHeight: parseFloat(getComputedStyle(a).lineHeight),
          scroll: a.scrollWidth,
          width: a.clientWidth,
        };
      }),
    );
    expect(layout.length).toBe(5);
    for (const item of layout) {
      expect(Math.abs(item.w - layout[0].w)).toBeLessThan(1);
      expect(item.iconBottom).toBeLessThan(item.labelTop);
      expect(item.scroll).toBeLessThanOrEqual(item.width + 1);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.evaluate(() => (document.documentElement.style.fontSize = "125%"));
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.evaluate(() => (document.documentElement.style.fontSize = ""));
    await page.screenshot({ path: testInfo.outputPath(`navigation-${mode}.png`), fullPage: true });
  }
});

test("receive titles and renames stay local, clear restores fallback and reload retains edits", async ({
  page,
}) => {
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByLabel("Link name (optional)").fill("結婚式 photos");
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const url = await page.getByLabel("Full link", { exact: true }).inputValue();
  const id = new URL(url).pathname.split("/").pop();
  await page.getByRole("link", { name: "History", exact: true }).click();
  const row = page.locator(`[data-resource-id="${id}"]`);
  await expect(row.getByText("結婚式 photos", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: "Rename" }).click();
  await row.getByLabel("Name on this device").fill("Local renamed link");
  await row.getByRole("button", { name: "Save name" }).click();
  await page.reload();
  await expect(row.getByText("Local renamed link", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: "Rename" }).click();
  await row.getByLabel("Name on this device").fill("");
  await row.getByRole("button", { name: "Save name" }).click();
  await expect(row.getByText("Local renamed link", { exact: true })).toHaveCount(0);
  await expect(row.getByText("Receive link", { exact: true })).toBeVisible();
});

test("admin metrics and resources distinguish unavailable, degraded and stale data", async ({
  page,
  adminRequest,
}) => {
  const source = await (await adminRequest.get("/api/v1/admin/traffic")).json();
  source.status = "degraded";
  await page.route("**/api/v1/admin/traffic", (route) => route.fulfill({ json: source }));
  await page.goto("/?view=traffic");
  await authenticate(page, adminCredentials);
  await expect(page.getByRole("alert")).toContainText("accounting is degraded");
  await page.route("**/api/v1/admin/traffic?**", (route) => route.abort());
  await page.getByRole("button", { name: "Show traffic" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "previous measurements" })).toContainText(
    "may be stale",
  );
  await expect(page.getByText("Measured lifetime", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Overview", exact: true }).click();
  await page.route("**/api/v1/auth/resources?*all=true*", (route) =>
    route.fulfill({ status: 503, json: { error: "Resource service unavailable" } }),
  );
  await page.getByRole("button", { name: "View resources", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Resources could not be loaded");
  await expect(page.getByText("No resources found.", { exact: true })).toHaveCount(0);
  await page.unroute("**/api/v1/auth/resources?*all=true*");
  await page.getByRole("button", { name: "Refresh resources", exact: true }).click();
  await expect(page.getByText("Resources could not be loaded", { exact: false })).toHaveCount(0);
});

test("same-account selected files survive reset, temporary login and password replacement without auto-upload; another account clears them", async ({
  page,
  adminRequest,
}) => {
  test.setTimeout(90000);
  const username = `pending-${Date.now()}`,
    password = "Pending-selection-temporary-2026";
  const created = await adminRequest.post("/api/v1/admin/users", {
    data: { username, password, role: "user" },
  });
  expect(created.ok()).toBe(true);
  const { user } = await created.json();
  await page.goto("/");
  await authenticate(page, { username, password });
  const filename = "private-selection-before-reset.txt";
  await page.getByLabel("Choose files", { exact: true }).setInputFiles({
    name: filename,
    mimeType: "text/plain",
    buffer: Buffer.from("private pending bytes"),
  });
  let uploads = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/api/v1/transfers") && r.method() === "POST") uploads++;
  });
  const reset = "First-admin-reset-password-2026";
  expect(
    (
      await adminRequest.patch(`/api/v1/admin/users/${user.id}`, { data: { password: reset } })
    ).ok(),
  ).toBe(true);
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible({
    timeout: 15000,
  });
  // authenticate follows the real required-change form and signs in with the replacement.
  await authenticate(page, { username, password: reset });
  await expect(page.getByText(filename, { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Send files", exact: true })).toBeEnabled();
  expect(uploads).toBe(0);
  const resources = await (await adminRequest.get("/api/v1/auth/resources?all=true")).json();
  expect(
    resources.transfers.filter((t: { owner_id: string }) => t.owner_id === user.id),
  ).toHaveLength(0);
  expect(
    (
      await adminRequest.patch(`/api/v1/admin/users/${user.id}`, {
        data: { password: "Second-admin-reset-password-2026" },
      })
    ).ok(),
  ).toBe(true);
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible({
    timeout: 15000,
  });
  await authenticate(page);
  await expect(page.getByText(filename, { exact: true })).toHaveCount(0);
  expect(uploads).toBe(0);
});

test("a real 367-day traffic range fits phone and desktop while the numeric table remains usable", async ({
  page,
}) => {
  await page.goto("/?view=traffic");
  await authenticate(page, adminCredentials);
  await expect(page.getByRole("heading", { name: "Selected period", exact: true })).toBeVisible();
  await page.getByLabel("From (UTC)", { exact: true }).fill("2025-01-01");
  await page.getByLabel("Through (UTC, inclusive)", { exact: true }).fill("2026-01-02");
  const fetched = page.waitForResponse((r) => r.url().includes("/api/v1/admin/traffic?"));
  await page.getByRole("button", { name: "Show traffic", exact: true }).click();
  const result = await fetched;
  expect(result.ok()).toBe(true);
  expect((await result.json()).days).toHaveLength(367);
  await expect(page.locator(".chart .bar-pair")).toHaveCount(367);
  for (const width of [320, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    const measurement = await page.locator(".chart").evaluate((chart) => ({
      page: document.documentElement.scrollWidth,
      viewport: innerWidth,
      scroll: chart.scrollWidth,
      client: chart.clientWidth,
      children: chart.children.length,
    }));
    expect(measurement.page).toBeLessThanOrEqual(measurement.viewport);
    expect(measurement.scroll).toBeLessThanOrEqual(measurement.client + 1);
    expect(measurement.children).toBe(367);
  }
  await page.setViewportSize({ width: 320, height: 900 });
  await page.getByText("Daily traffic data (exact bytes)", { exact: true }).click();
  await expect(page.getByRole("table").getByRole("row")).toHaveCount(368);
  const table = page.getByRole("region", { name: "Daily traffic data", exact: true });
  await table.focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => table.evaluate((e) => e.scrollLeft)).toBeGreaterThan(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

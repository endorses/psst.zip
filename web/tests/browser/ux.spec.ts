import { test, expect, signIn, authenticate, retryAuth } from "./auth-fixture";

const file = {
  name: "recognizable-name.txt",
  mimeType: "text/plain",
  buffer: Buffer.from("private selected content"),
};

test("destinations survive refresh and browser history while selected files survive internal navigation", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(file);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await expect(page).toHaveURL(/view=receive/);
  await expect(page.getByRole("button", { name: "Return to your transfer" })).toBeVisible();
  await page.getByRole("link", { name: "History", exact: true }).click();
  await expect(page).toHaveURL(/view=history/);
  await expect(page.getByRole("heading", { name: "Your transfers", exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Receive files", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Return to your transfer" }).click();
  await expect(page.getByText(file.name, { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect(page).toHaveURL(/view=settings/);
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.reload();
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Connected devices", exact: true })).toBeVisible();
});

test("upload byte progress persists through navigation and cancellation removes the partial transfer", async ({
  page,
  request,
}) => {
  await signIn(page);
  let transferId = "";
  page.on("response", async (response) => {
    if (
      response.url().endsWith("/api/v1/transfers") &&
      response.request().method() === "POST" &&
      response.ok()
    )
      transferId = (await response.json()).id;
  });
  let release: () => void = () => {};
  const blocked = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/v1/transfers/*/files/*", async (route) => {
    if (route.request().method() === "PATCH") {
      await blocked;
      if (!route.request().failure()) await route.continue().catch(() => {});
    } else await route.continue();
  });
  await page
    .getByLabel("Choose files")
    .setInputFiles({ ...file, buffer: Buffer.alloc(4 * 1024 * 1024, 3) });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("progressbar")).toBeVisible();
  await expect.poll(() => transferId).not.toBe("");
  await page.getByRole("link", { name: "History", exact: true }).click();
  await expect(page.getByRole("button", { name: "Return to your transfer" })).toBeVisible();
  await page.getByRole("button", { name: "Return to your transfer" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Cancel upload" }).click();
  release();
  await expect(page.getByRole("alert")).toContainText("Partial server files removed");
  expect((await request.get(`/api/v1/transfers/${transferId}`)).status()).toBe(404);
});

test("logout clears selected private files and local labels never appear in another account", async ({
  page,
  adminRequest: request,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(file);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  const username = `ux-${Date.now()}`,
    password = "Test-member-password-2026";
  expect(
    (
      await request.post("/api/v1/admin/users", { data: { username, password, role: "user" } })
    ).ok(),
  ).toBe(true);
  await authenticate(page, { username, password });
  await expect(page.getByRole("navigation")).toBeVisible();
  await expect(page.getByText(file.name, { exact: true })).toHaveCount(0);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect(page.getByRole("link", { name: "Users", exact: true })).toHaveCount(0);
});

test("live history keeps known data when offline and recovers automatically", async ({ page }) => {
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  await page.getByRole("link", { name: "History", exact: true }).click();
  await expect(page.locator("article").first()).toBeVisible();
  await page.route("**/api/v1/auth/resources?*", (route) => route.abort());
  await expect(page.getByText(/Offline — last updated/)).toBeVisible({ timeout: 10000 });
  await expect(page.locator("article").first()).toBeVisible();
  await page.unroute("**/api/v1/auth/resources?*");
  await expect(page.getByText(/Offline — last updated/)).toHaveCount(0, { timeout: 15000 });
});

test("pairing replaces QR on redemption and cancel invalidates the unused code", async ({
  page,
  playwright,
  baseURL,
}) => {
  const deviceName = `UX phone ${Date.now()}`;
  await signIn(page);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByRole("link", { name: "Connected devices", exact: true }).click();
  async function issue() {
    const response = page.waitForResponse(
      (r) => r.url().endsWith("/auth/pairings") && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: /Show login QR code|Generate new code/ }).click();
    const result = await response;
    expect(result.ok(), await result.text()).toBe(true);
    return result.json();
  }
  const first = await issue();
  await page.getByRole("button", { name: "Cancel pairing" }).click();
  const anonymous = await playwright.request.newContext({ baseURL });
  expect(
    (
      await retryAuth(() =>
        anonymous.post("/api/v1/auth/pairings/redeem", {
          data: { code: first.code, device_name: "Canceled phone" },
        }),
      )
    ).ok(),
  ).toBe(false);
  const second = await issue();
  expect(
    (
      await retryAuth(() =>
        anonymous.post("/api/v1/auth/pairings/redeem", {
          data: { code: second.code, device_name: deviceName },
        }),
      )
    ).ok(),
  ).toBe(true);
  await expect(page.getByText(`Phone connected: ${deviceName}`, { exact: true })).toBeVisible({
    timeout: 10000,
  });
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toHaveCount(0);
  await expect(page.locator("article").filter({ hasText: deviceName })).toBeVisible();
  // A consumed grant cannot be replaced. Connecting another phone starts a fresh
  // grant in this same view while preserving the first phone's session.
  const third = await issue();
  expect(third.id).not.toBe(second.id);
  const anotherDeviceName = `Another phone ${Date.now()}`;
  const another = await retryAuth(() =>
    anonymous.post("/api/v1/auth/pairings/redeem", {
      data: { code: third.code, device_name: anotherDeviceName },
    }),
  );
  expect(another.ok(), await another.text()).toBe(true);
  await expect(
    page.getByText(`Phone connected: ${anotherDeviceName}`, { exact: true }),
  ).toBeVisible({ timeout: 10000 });
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toHaveCount(0);
  await expect(page.locator("article").filter({ hasText: deviceName })).toBeVisible();
  await expect(page.locator("article").filter({ hasText: anotherDeviceName })).toBeVisible();
  await anonymous.dispose();
});

test("theme follows system, keyboard login toggle works and phone QR actions fit viewport", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await page.goto("/");
  await expect(page.locator("body")).toHaveCSS("background-color", "rgb(11, 25, 23)");
  await page.getByLabel("Password", { exact: true }).fill("secret");
  await page.getByRole("button", { name: "Show password" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel("Password", { exact: true })).toHaveAttribute("type", "text");
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const qr = page.getByRole("img", { name: "QR code for shared link" });
  await expect(qr).toHaveCSS("background-color", "rgb(255, 255, 255)");
  await expect(qr).toBeInViewport();
  await expect(page.getByRole("button", { name: "Copy link", exact: true })).toBeInViewport();
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("body")).toHaveCSS("background-color", "rgb(246, 248, 247)");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("throttled upload advances by bytes before completion and weights unequal files by size", async ({
  page,
  context,
}) => {
  test.setTimeout(45000);
  await signIn(page);
  const cdp = await context.newCDPSession(page);
  await cdp.send("Network.enable");
  await cdp.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 30,
    downloadThroughput: 5 * 1024 * 1024,
    uploadThroughput: 256 * 1024,
  });
  await page.getByLabel("Choose files").setInputFiles([
    { ...file, buffer: Buffer.alloc(1024 * 1024, 5) },
    { name: "tiny.txt", mimeType: "text/plain", buffer: Buffer.alloc(64, 2) },
  ]);
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect
    .poll(
      async () =>
        page
          .getByRole("progressbar")
          .evaluate((el) => (el as HTMLProgressElement).value / (el as HTMLProgressElement).max)
          .catch(() => 0),
      { timeout: 15000 },
    )
    .toBeGreaterThan(0.1);
  const fraction = await page
    .getByRole("progressbar")
    .evaluate((el) => (el as HTMLProgressElement).value / (el as HTMLProgressElement).max);
  expect(fraction).toBeLessThan(1);
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible({
    timeout: 30000,
  });
  await cdp.detach();
});

test("session expiry preserves selected files only for reauthentication to the same account", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(file);
  let expire = true;
  await page.route("**/api/v1/auth/me", (route) =>
    expire ? route.fulfill({ status: 401, json: { error: "expired" } }) : route.continue(),
  );
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible({
    timeout: 10000,
  });
  expire = false;
  // The test helper reloads, so authenticate in place to exercise memory-only task continuation.
  const { credentials } = await import("./auth-fixture");
  await authenticate(page, credentials);
  await expect(page.getByText(file.name, { exact: true })).toBeVisible();
});

test("receive history reopens the original link and failed saving retries only the failed file", async ({
  page,
  context,
  request,
}) => {
  // Exercise the complete provider fallback in a real browser. This simulates
  // absent SubtleCrypto; the separate LAN deployment check covers insecure origins.
  await context.addInitScript(() =>
    Object.defineProperty(globalThis.crypto, "subtle", { value: undefined }),
  );
  await signIn(page);
  expect(await page.evaluate(() => typeof globalThis.crypto.subtle)).toBe("undefined");
  let slotCreations = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/api/v1/slots") && r.method() === "POST") slotCreations++;
  });
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const link = await page.getByLabel("Full link").inputValue(),
    slotId = new URL(link).pathname.split("/").pop()!;
  await page.getByRole("link", { name: "History", exact: true }).click();
  const sender = await context.newPage();
  await sender.goto(link);
  await sender.getByLabel("Choose files").setInputFiles([file, { ...file, name: "second.txt" }]);
  await sender.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(sender.getByRole("heading", { name: "Files sent" })).toBeVisible();
  await sender.close();
  await page
    .locator(`[data-resource-id="${slotId}"]`)
    .getByRole("button", { name: "View files" })
    .click();
  await expect(page).toHaveURL(new RegExp(`slot=${slotId}`));
  await expect(page.getByRole("link", { name: "2 files · Save files" })).toBeVisible();
  const info = await (await request.get(`/api/v1/slots/${slotId}`)).json();
  const transferId = info.transfers[0].transfer_id;
  await page.getByRole("link", { name: "2 files · Save files" }).click();
  await expect(page.getByRole("heading", { name: "Save files" })).toBeVisible();
  let requests = 0,
    failNext = false;
  await page.route(`**/api/v1/transfers/${transferId}/files/*`, (route) => {
    requests++;
    if (failNext) {
      failNext = false;
      return route.fulfill({ status: 503 });
    }
    return route.continue();
  });
  const rows = page.locator(".file-list li");
  let saved = page.waitForEvent("download");
  await rows.nth(0).getByRole("button").click();
  await saved;
  failNext = true;
  await rows.nth(1).getByRole("button").click();
  await expect(page.getByRole("alert")).toBeVisible();
  saved = page.waitForEvent("download");
  await rows.nth(1).getByRole("button").click();
  await saved;
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  expect(requests).toBe(3);
  expect(slotCreations).toBe(1);
});

test("receive cap rejects oversized batches and a device without its private key can inspect the inbox", async ({
  page,
  context,
  request,
}) => {
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByRole("checkbox", { name: "Limit files accepted" }).check();
  await page.getByRole("spinbutton", { name: "Limit files accepted" }).fill("1");
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const link = await page.getByLabel("Full link").inputValue();
  expect(link).toMatch(/#v2\.[A-Za-z0-9_-]{43}$/);
  const slot = new URL(link).pathname.split("/").pop()!;
  const sender = await context.newPage();
  await sender.goto(link);
  await sender.getByLabel("Choose files").setInputFiles([file, { ...file, name: "extra.txt" }]);
  await expect(sender.getByRole("alert")).toContainText("file");
  await expect(sender.getByRole("button", { name: "Send files", exact: true })).toHaveCount(0);
  expect((await (await request.get(`/api/v1/slots/${slot}`)).json()).transfers).toHaveLength(0);
  await sender.getByLabel("Choose files").setInputFiles(file);
  await sender.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(sender.getByRole("heading", { name: "Files sent" })).toBeVisible();
  await sender.close();
  const info = await (await request.get(`/api/v1/slots/${slot}`)).json();
  expect(info.remaining_files).toBe(0);
  await page.evaluate(() => {
    for (const key of Object.keys(localStorage))
      if (key.startsWith("psst.receive-key.v2.")) localStorage.removeItem(key);
  });
  await page.reload();
  await expect(
    page.getByText("This browser has no private key for this inbox.", { exact: false }),
  ).toBeVisible();
  await expect(page.getByText(/0 allocations remaining/)).toBeVisible();
  await expect(page.getByRole("link", { name: /Save files/ })).toHaveCount(0);
  await page.goto(`/d/${info.transfers[0].transfer_id}?inbox=${slot}`);
  await expect(page.getByRole("alert")).toContainText("This browser has no private key");
});

import { test, expect, signIn, openAdmin, retryAuth } from "./auth-fixture";

test("anonymous users cannot create transfers or receive links; public pages need no account", async ({
  playwright,
  baseURL,
  page,
}) => {
  const anonymous = await playwright.request.newContext({ baseURL });
  expect((await anonymous.post("/api/v1/transfers")).status()).toBe(401);
  expect((await anonymous.post("/api/v1/slots")).status()).toBe(401);
  expect((await anonymous.get("/api/v1/auth/resources")).status()).toBe(401);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await anonymous.dispose();
});

test("admin manages accounts while regular users cannot administer others", async ({
  page,
  adminRequest,
  playwright,
  baseURL,
}) => {
  await openAdmin(page);
  await expect(page.getByRole("heading", { name: "Overview", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Send", exact: true })).toHaveCount(0);
  await page.getByRole("link", { name: "Users", exact: true }).click();
  const username = `browser-${Date.now()}`,
    password = "User-test-password-2026";
  await page.getByLabel("New username", { exact: true }).fill(username);
  await page.getByLabel("Temporary password", { exact: true }).fill(password);
  await page.getByLabel("Confirm temporary password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  const row = page.locator("article").filter({ has: page.getByText(username, { exact: true }) });
  await expect(row).toBeVisible();
  const anonymous = await playwright.request.newContext({ baseURL });
  const login = await retryAuth(() =>
    anonymous.post("/api/v1/auth/login", { data: { username, password, session_type: "device" } }),
  );
  expect(login.status()).toBe(200);
  const { token, user } = await login.json();
  expect(user.must_change_password).toBe(true);
  const member = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  expect((await member.get("/api/v1/admin/users")).status()).toBe(403);
  const denied = await member.post("/api/v1/transfers");
  expect(denied.status()).toBe(403);
  expect((await denied.json()).code).toBe("password_change_required");
  expect((await adminRequest.post("/api/v1/transfers")).status()).toBe(403);
  expect((await adminRequest.get("/api/v1/auth/resources")).status()).toBe(403);
  expect((await adminRequest.get("/api/v1/auth/resources?all=true")).ok()).toBe(true);
  await row.getByRole("button", { name: "Disable sign-in", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Disable sign-in only" }).click();
  await expect(row.getByRole("button", { name: "Enable sign-in", exact: true })).toBeVisible();
  expect((await member.post("/api/v1/transfers")).status()).toBe(401);
  await member.dispose();
  await anonymous.dispose();
});

test("receive link survives logout; login QR is issued on demand; history revokes its link", async ({
  page,
  playwright,
  baseURL,
  request,
}) => {
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const link = await page.getByLabel("Full link", { exact: true }).inputValue();
  const id = new URL(link).pathname.split("/").pop()!;
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByRole("link", { name: "Connected devices", exact: true }).click();
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toHaveCount(0);
  const pairingResponse = page.waitForResponse(
    (r) => r.url().endsWith("/api/v1/auth/pairings") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Show login QR code" }).click();
  const pairing = await (await pairingResponse).json();
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toBeVisible();
  const anonymous = await playwright.request.newContext({ baseURL });
  const redeemed = await retryAuth(() =>
    anonymous.post("/api/v1/auth/pairings/redeem", {
      data: { code: pairing.code, device_name: "Paired test phone" },
    }),
  );
  expect(redeemed.status()).toBe(200);
  expect((await redeemed.json()).token).toBeTruthy();
  expect(
    (
      await retryAuth(() =>
        anonymous.post("/api/v1/auth/pairings/redeem", {
          data: { code: pairing.code, device_name: "Replay" },
        }),
      )
    ).ok(),
  ).toBe(false);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await page.goto(link);
  await expect(page.locator('input[type="file"]')).toBeEnabled();
  await page.locator('input[type="file"]').setInputFiles({
    name: "invited.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("anonymous invited upload"),
  });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Files sent" })).toBeVisible();
  await signIn(page);
  await page.getByRole("link", { name: "History", exact: true }).click();
  // Isolate our slot from other test-created history entries.
  const resources = await (await request.get("/api/v1/auth/resources")).json();
  expect(resources.slots.some((slot: { id: string }) => slot.id === id)).toBe(true);
  const rows = page
    .locator(`article[data-resource-id="${id}"]`)
    .filter({ has: page.getByText("Receive link", { exact: true }) });
  const row = rows
    .filter({ has: page.getByRole("button", { name: "Copy link", exact: true }) })
    .last();
  await row.getByRole("button", { name: "Revoke", exact: true }).click();
  await page.getByRole("button", { name: "Revoke and delete" }).click();
  await expect(page.getByRole("status")).toContainText("Link revoked");
  expect((await anonymous.get(`/api/v1/slots/${id}`)).status()).toBe(401);
  expect((await anonymous.get(`/api/v1/slots/${id}/availability`)).status()).toBe(404);
  await anonymous.dispose();
});

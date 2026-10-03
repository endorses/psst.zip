import { test, expect, signIn } from "./auth-fixture";

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
  await expect(page.getByRole("heading", { name: "Sign in to Psst" })).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await anonymous.dispose();
});

test("admin manages accounts; users cannot administer others and disabled sessions stop working", async ({
  page,
  request,
  playwright,
  baseURL,
}) => {
  await signIn(page);
  await page.getByRole("button", { name: "Users", exact: true }).click();
  const username = `browser-${Date.now()}`;
  const password = "User-test-password-2026";
  await page.getByLabel("New username", { exact: true }).fill(username);
  await page.getByLabel("Temporary password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  const row = page.locator("article").filter({ has: page.getByText(username, { exact: true }) });
  await expect(row).toBeVisible();
  const anonymous = await playwright.request.newContext({ baseURL });
  const login = await anonymous.post("/api/v1/auth/login", {
    data: { username, password, session_type: "device" },
  });
  expect(login.status()).toBe(200);
  const { token } = await login.json();
  const member = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  expect((await member.get("/api/v1/admin/users")).status()).toBe(403);
  const created = await member.post("/api/v1/transfers");
  expect(created.status()).toBe(201);
  const transfer = await created.json();
  const myResources = await (await member.get("/api/v1/auth/resources")).json();
  expect(myResources.transfers.some((item: { id: string }) => item.id === transfer.id)).toBe(true);
  const adminResources = await (await request.get("/api/v1/auth/resources")).json();
  expect(adminResources.transfers.some((item: { id: string }) => item.id === transfer.id)).toBe(
    false,
  );
  expect((await member.get("/api/v1/auth/resources?all=true")).status()).toBe(403);
  const allResources = await (await request.get("/api/v1/auth/resources?all=true")).json();
  expect(allResources.transfers.some((item: { id: string }) => item.id === transfer.id)).toBe(true);
  await row.getByRole("button", { name: "Disable", exact: true }).click();
  await expect(row.getByRole("button", { name: "Enable", exact: true })).toBeVisible();
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
  await page.getByRole("button", { name: "Receive", exact: true }).click();
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const link = await page.getByLabel("Receive link", { exact: true }).inputValue();
  const id = new URL(link).pathname.split("/").pop()!;
  await page.getByRole("button", { name: "Devices", exact: true }).click();
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toHaveCount(0);
  const pairingResponse = page.waitForResponse(
    (r) => r.url().endsWith("/api/v1/auth/pairings") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Show login QR code" }).click();
  const pairing = await (await pairingResponse).json();
  await expect(page.getByRole("img", { name: "Mobile app login QR code" })).toBeVisible();
  const anonymous = await playwright.request.newContext({ baseURL });
  const redeemed = await anonymous.post("/api/v1/auth/pairings/redeem", {
    data: { code: pairing.code, device_name: "Paired test phone" },
  });
  expect(redeemed.status()).toBe(200);
  expect((await redeemed.json()).token).toBeTruthy();
  expect(
    (
      await anonymous.post("/api/v1/auth/pairings/redeem", {
        data: { code: pairing.code, device_name: "Replay" },
      })
    ).ok(),
  ).toBe(false);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page.goto(link);
  await expect(page.locator('input[type="file"]')).toBeEnabled();
  await page.locator('input[type="file"]').setInputFiles({
    name: "invited.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("anonymous invited upload"),
  });
  await page.getByRole("button", { name: /Encrypt & Upload/ }).click();
  await expect(page.getByRole("heading", { name: /Upload Complete/i })).toBeVisible();
  await signIn(page);
  await page.getByRole("button", { name: "History", exact: true }).click();
  // Isolate our slot from other test-created history entries.
  const resources = await (await request.get("/api/v1/auth/resources")).json();
  expect(resources.slots.some((slot: { id: string }) => slot.id === id)).toBe(true);
  const rows = page
    .locator("article")
    .filter({ has: page.getByText("Receive link", { exact: true }) });
  const row = rows
    .filter({ has: page.getByRole("button", { name: "Copy link", exact: true }) })
    .last();
  await row.getByRole("button", { name: "Revoke", exact: true }).click();
  await page.getByRole("button", { name: "Revoke and delete" }).click();
  await expect(page.getByRole("status")).toContainText("Link revoked");
  expect((await anonymous.get(`/api/v1/slots/${id}`)).status()).toBe(404);
  await anonymous.dispose();
});

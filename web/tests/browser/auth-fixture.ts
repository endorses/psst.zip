import {
  test as base,
  expect,
  type Page,
  type APIRequestContext,
  type APIResponse,
} from "@playwright/test";
export const adminCredentials = {
  username: process.env.PSST_TEST_USERNAME ?? process.env.PSST_TEST_USERNAME ?? "admin",
  password:
    process.env.PSST_TEST_PASSWORD ?? process.env.PSST_TEST_PASSWORD ?? "Test-admin-password-2026",
};
export const credentials = { username: "browser-member", password: "Browser-member-final-2026" };
export const test = base.extend<
  { adminRequest: APIRequestContext },
  { apiToken: string; adminToken: string }
>({
  page: async ({ page }, use, testInfo) => {
    testInfo.setTimeout(Math.max(testInfo.timeout, 180000));
    // Tests share one loopback client. Let the real 20/s global bucket refill
    // after the preceding test and API setup before the page initializes.
    await page.waitForTimeout(1000);
    await use(page);
  },
  adminToken: [
    async ({ playwright }, use) => {
      const context = await playwright.request.newContext({
        baseURL: process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:4173",
      });
      const response = await retryAuth(() =>
        context.post("/api/v1/auth/login", {
          headers: { Origin: process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:4173" },
          data: {
            ...adminCredentials,
            session_type: "web",
            device_name: "Browser admin fixture",
          },
        }),
      );
      expect(response.ok(), await response.text()).toBe(true);
      const token = (await context.storageState()).cookies.find(
        (cookie) => cookie.name === "psst_session",
      )?.value;
      if (!token) throw new Error("Administrator web session cookie missing");
      await use(token);
      await context.dispose();
    },
    { scope: "worker", timeout: 180000 },
  ],
  apiToken: [
    async ({ playwright, adminToken }, use) => {
      const baseURL = process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:4173";
      const admin = await playwright.request.newContext({
        baseURL,
        extraHTTPHeaders: { Authorization: `Bearer ${adminToken}` },
      });
      const created = await admin.post("/api/v1/admin/users", {
        data: {
          username: credentials.username,
          password: "Browser-member-temporary-2026",
          role: "user",
        },
      });
      const context = await playwright.request.newContext({ baseURL });
      if (created.status() === 201) {
        const login = await retryAuth(() =>
          context.post("/api/v1/auth/login", {
            data: {
              username: credentials.username,
              password: "Browser-member-temporary-2026",
              session_type: "device",
            },
          }),
        );
        expect(login.ok(), await login.text()).toBe(true);
        const { token } = await login.json();
        const changed = await context.post("/api/v1/auth/password", {
          headers: { Authorization: `Bearer ${token}` },
          data: {
            current_password: "Browser-member-temporary-2026",
            password: credentials.password,
          },
        });
        expect(changed.ok(), await changed.text()).toBe(true);
      } else expect(created.status()).toBe(409);
      const login = await retryAuth(() =>
        context.post("/api/v1/auth/login", { data: { ...credentials, session_type: "device" } }),
      );
      expect(login.ok(), await login.text()).toBe(true);
      const { token } = await login.json();
      await use(token);
      await context.dispose();
      await admin.dispose();
    },
    { scope: "worker", auto: true, timeout: 180000 },
  ],
  request: async ({ playwright, baseURL, apiToken }, use) => {
    const context = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${apiToken}` },
    });
    await use(context);
    await context.dispose();
  },
  adminRequest: async ({ playwright, baseURL, adminToken }, use) => {
    const context = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${adminToken}` },
    });
    try {
      // The worker session outlives the five-minute proof. Refresh explicitly
      // before setup when needed; never replay a rejected administrative mutation.
      const status = await context.get("/api/v1/admin/security");
      expect(status.ok(), await status.text()).toBe(true);
      const { recent_until } = await status.json();
      const serverNow = Date.parse(status.headers().date);
      expect(Number.isFinite(serverNow)).toBe(true);
      if (!recent_until || Date.parse(recent_until) < serverNow + 90000) {
        const refreshed = await retryAuth(() =>
          context.post("/api/v1/admin/security/reauth", {
            data: { password: adminCredentials.password },
          }),
        );
        expect(refreshed.ok(), await refreshed.text()).toBe(true);
      }
      await use(context);
    } finally {
      await context.dispose();
    }
  },
});
// The IP login bucket refills every five seconds; the account bucket refills
// every thirty seconds. Keep both enabled and honor explicit 429 responses.
export async function authDelay(headers: Record<string, string>) {
  const value = headers["retry-after"];
  const seconds = value ? Number(value) : NaN;
  const delay = Number.isFinite(seconds)
    ? seconds * 1000
    : value
      ? Date.parse(value) - Date.now()
      : 5000;
  await new Promise((resolve) =>
    setTimeout(resolve, Math.max(1000, Math.min(60000, delay || 5000))),
  );
}
export async function retryAuth(operation: () => Promise<APIResponse>): Promise<APIResponse> {
  let response = await operation();
  for (let attempt = 0; response.status() === 429 && attempt < 3; attempt++) {
    await authDelay(response.headers());
    response = await operation();
  }
  return response;
}
export async function submitLogin(page: Page, loginCredentials = credentials) {
  test.setTimeout(Math.max(test.info().timeout, 180000));
  for (let attempt = 0; attempt < 4; attempt++) {
    await page.getByLabel("Username", { exact: true }).fill(loginCredentials.username);
    await page.getByLabel("Password", { exact: true }).fill(loginCredentials.password);
    const result = page.waitForResponse(
      (r) => r.url().endsWith("/api/v1/auth/login") && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    const response = await result;
    if (response.status() !== 429) {
      expect(response.ok(), await response.text()).toBe(true);
      return;
    }
    if (attempt === 3) {
      expect(response.ok(), `Sign-in remained throttled: ${await response.text()}`).toBe(true);
    }
    await authDelay(response.headers());
  }
}
export async function authenticate(page: Page, loginCredentials = credentials) {
  await submitLogin(page, loginCredentials);
  await expect(
    page
      .getByRole("navigation", { name: "Account navigation" })
      .or(page.getByRole("heading", { name: "Choose your own password" })),
  ).toBeVisible();
  if (await page.getByRole("heading", { name: "Choose your own password" }).isVisible()) {
    await page.getByLabel("Current password", { exact: true }).fill(loginCredentials.password);
    await page
      .getByLabel("New password", { exact: true })
      .fill(loginCredentials.password + "-changed");
    await page
      .getByLabel("Confirm password", { exact: true })
      .fill(loginCredentials.password + "-changed");
    await page.getByRole("button", { name: "Change password", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
    return authenticate(page, {
      ...loginCredentials,
      password: loginCredentials.password + "-changed",
    });
  }
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
}
export async function signIn(page: Page) {
  await page.goto("/");
  await authenticate(page);
}
export { expect };

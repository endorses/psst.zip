import { test as base, expect, type Page, type APIResponse } from "@playwright/test";
export const credentials = {
  username: process.env.PSST_TEST_USERNAME ?? "admin",
  password: process.env.PSST_TEST_PASSWORD ?? "Test-admin-password-2026",
};
export const test = base.extend<{}, { apiToken: string }>({
  apiToken: [
    async ({ playwright }, use) => {
      const baseURL = process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:4173";
      const context = await playwright.request.newContext({ baseURL });
      const response = await retryAuth(() =>
        context.post("/api/v1/auth/login", {
          data: { ...credentials, session_type: "device", device_name: "Browser test API" },
        }),
      );
      expect(response.ok(), await response.text()).toBe(true);
      const { token } = await response.json();
      await use(token);
      await context.dispose();
    },
    { scope: "worker" },
  ],
  request: async ({ playwright, baseURL, apiToken }, use) => {
    const context = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${apiToken}` },
    });
    await use(context);
    await context.dispose();
  },
});
// The production limiter permits one authentication attempt every five seconds after
// its initial burst. Keep it enabled and pace only explicit 429 responses.
async function authDelay(headers: Record<string, string>) {
  const value = headers["retry-after"];
  const seconds = value ? Number(value) : NaN;
  const delay = Number.isFinite(seconds)
    ? seconds * 1000
    : value
      ? Date.parse(value) - Date.now()
      : 5000;
  await new Promise((resolve) =>
    setTimeout(resolve, Math.max(1000, Math.min(10000, delay || 5000))),
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
export async function authenticate(page: Page, loginCredentials = credentials) {
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
      break;
    }
    await authDelay(response.headers());
  }
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
}
export async function signIn(page: Page) {
  await page.goto("/");
  await authenticate(page);
}
export { expect };

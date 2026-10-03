import { test as base, expect, type Page } from "@playwright/test";
export const credentials = {
  username: process.env.PSST_TEST_USERNAME ?? "admin",
  password: process.env.PSST_TEST_PASSWORD ?? "Test-admin-password-2026",
};
export const test = base.extend<{}, { apiToken: string }>({
  apiToken: [
    async ({ playwright }, use) => {
      const baseURL = process.env.PSST_TEST_BASE_URL ?? "http://127.0.0.1:4173";
      const context = await playwright.request.newContext({ baseURL });
      const response = await context.post("/api/v1/auth/login", {
        data: { ...credentials, session_type: "device", device_name: "Browser test API" },
      });
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
export async function signIn(page: Page) {
  await page.goto("/");
  await page.getByLabel("Username", { exact: true }).fill(credentials.username);
  await page.getByLabel("Password", { exact: true }).fill(credentials.password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
}
export { expect };

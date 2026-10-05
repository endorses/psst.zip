import { test, expect } from "@playwright/test";
import { resourcePolicy, resourceUsage } from "../resource-policy-fixture";

test("server sections retain unsaved fields across navigation and languages", async ({ page }) => {
  const reads: Record<string, number> = {};
  let writes = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    if (route.request().method() !== "GET") writes++;
    reads[path] = (reads[path] ?? 0) + 1;
    const responses: Record<string, unknown> = {
      "/auth/me": { user: { id: "organization-admin", role: "admin", username: "admin" } },
      "/config": { max_file_size: 25 * 1024 ** 2, max_file_size_ceiling: 1024 ** 4 },
      "/admin/resource-policy": { policy: resourcePolicy, usage: resourceUsage },
      "/admin/abuse-contact": { email: "reports@example.org" },
      "/admin/incident-state": {
        public_transfers_paused: false,
        updated_at: "2026-10-04T00:00:00Z",
      },
    };
    await route.fulfill(
      path in responses
        ? { json: responses[path] }
        : { status: 503, json: { error: "Unavailable fixture" } },
    );
  });
  await page.goto("/?view=server");
  const nav = page.getByRole("navigation", { name: "Page sections" });
  const limit = page.getByLabel("Maximum file size (MiB)", { exact: true });
  await expect(limit).toBeEnabled();
  await limit.fill("48");
  expect(reads["/admin/resource-policy"] ?? 0).toBe(0);
  expect(reads["/admin/abuse-contact"] ?? 0).toBe(0);
  await nav.getByRole("link", { name: "Storage", exact: true }).click();
  const storage = page.getByLabel("Server storage (MiB)", { exact: true });
  await storage.fill("768");
  await expect(limit).not.toBeVisible();
  await nav.getByRole("link", { name: "Abuse reports", exact: true }).click();
  const email = page.getByLabel("Public contact email (optional)", { exact: true });
  await email.fill("draft@example.org");
  await page.goBack();
  await expect(storage).toHaveValue("768");
  await page.goForward();
  await expect(email).toHaveValue("draft@example.org");
  await page.locator(".language-picker select").selectOption("de");
  await expect(page.getByRole("navigation", { name: "Seitenbereiche" })).toBeVisible();
  await page.locator(".language-picker select").selectOption("en");
  await nav.getByRole("link", { name: "Uploads", exact: true }).click();
  await expect(limit).toHaveValue("48");
  await page.getByRole("link", { name: "Traffic", exact: true }).click();
  await page.getByRole("link", { name: "Server settings", exact: true }).click();
  await expect(limit).toHaveValue("48");
  await nav.getByRole("link", { name: "Storage", exact: true }).click();
  await expect(storage).toHaveValue("768");
  await nav.getByRole("link", { name: "Abuse reports", exact: true }).click();
  await expect(email).toHaveValue("draft@example.org");
  expect(reads["/admin/resource-policy"]).toBe(1);
  expect(reads["/admin/abuse-contact"]).toBe(1);
  expect(writes).toBe(0);
  // Invalid sections fall back to a useful page without exposing hidden fields.
  await page.goto("/?view=server&section=unknown");
  await expect(limit).toBeVisible();
  await expect(page.getByLabel("Server storage (MiB)", { exact: true })).toHaveCount(0);
  for (const language of ["en", "de"]) {
    await page.locator(".language-picker select").selectOption(language);
    for (const width of [320, 390, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
  }
  await page.screenshot({
    path: test.info().outputPath("server-settings-german.png"),
    fullPage: true,
  });
});

test("header selectors have matching hover and focus styles in both themes", async ({ page }) => {
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({ status: 401, json: {} }));
  await page.goto("/");
  const appearance = page.locator(".theme-picker select");
  const language = page.locator(".language-picker select");
  for (const theme of ["light", "dark"]) {
    await appearance.selectOption(theme);
    const styles = [];
    for (const picker of [appearance, language]) {
      await picker.hover();
      styles.push(
        await picker.evaluate((element) => {
          const style = getComputedStyle(element);
          return {
            background: style.backgroundColor,
            border: style.borderColor,
            padding: style.padding,
            height: style.height,
          };
        }),
      );
    }
    expect(styles[0]).toEqual(styles[1]);
    for (const picker of [appearance, language]) {
      await picker.focus();
      await expect(picker).toBeFocused();
    }
  }
});

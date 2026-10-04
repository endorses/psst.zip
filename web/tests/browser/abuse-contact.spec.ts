import { test, expect } from "@playwright/test";

const id = "59b91455-7313-42a4-b8ce-58294ceb42ba";
const secret = "private-key-must-never-be-reported";
test("public error page offers a safe report reference, copy fallback and bounded modal", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 740 });
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({
      json: { max_file_size: 26214400, abuse_contact_email: "abuse+reports@example.com" },
    }),
  );
  const requests: string[] = [];
  page.on("request", (request) => requests.push(request.url()));
  await page.goto(`/d/${id}?not-for-report=query-secret#${secret}`);
  await page.getByRole("button", { name: "Report abuse", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Report abuse" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Report reference")).toHaveValue(
    `Instance: http://127.0.0.1:4173\nResource type: transfer\nResource ID: ${id}`,
  );
  const href = await dialog.getByRole("link", { name: "Open email draft" }).getAttribute("href");
  expect(href).not.toContain(secret);
  expect(href).not.toContain("query-secret");
  expect(requests.every((url) => !url.includes(secret))).toBe(true);
  await page.evaluate(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async () => {
          throw Error("unavailable");
        },
      },
    }),
  );
  await dialog.getByRole("button", { name: "Copy report reference" }).click();
  await expect(dialog.getByRole("status")).toContainText("Select and copy");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: test.info().outputPath("abuse-report-phone.png") });
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Report abuse", exact: true })).toBeFocused();
});

test("unconfigured and hostile contacts never expose a mail link", async ({ page }) => {
  let email = "";
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 26214400, abuse_contact_email: email } }),
  );
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ status: 401, json: { error: "Sign in" } }),
  );
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Report abuse", exact: true })).toHaveCount(0);
  email = "x@example.com?bcc=other@example.com";
  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.locator('a[href^="mailto:"]')).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Report abuse", exact: true })).toHaveCount(0);
});

test("admin publishes and clears contact, while failed writes retain draft", async ({ page }) => {
  let saved = "",
    fail = false;
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "admin", username: "admin", role: "admin" } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 26214400, abuse_contact_email: saved } }),
  );
  await page.route("**/api/v1/admin/security", (route) =>
    route.fulfill({ json: { enabled: true, recovery_codes_remaining: 10, recent_until: null } }),
  );
  await page.route("**/api/v1/admin/abuse-contact", async (route) => {
    if (route.request().method() === "PATCH") {
      if (fail) return route.fulfill({ status: 503, json: { error: "private storage detail" } });
      saved = route.request().postDataJSON().email;
    }
    return route.fulfill({ json: { email: saved } });
  });
  await page.goto("/?view=server");
  const field = page.getByLabel("Public contact email (optional)");
  await field.fill("abuse@example.com");
  await page.getByRole("button", { name: "Save abuse contact", exact: true }).click();
  await expect(page.getByText("Abuse contact published.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Report abuse", exact: true })).toBeVisible();
  fail = true;
  await field.fill("new@example.com");
  await page.getByRole("button", { name: "Save abuse contact", exact: true }).click();
  await expect(page.getByText(/Could not save the abuse contact/)).toBeVisible();
  await expect(field).toHaveValue("new@example.com");
  await expect(page.getByText("private storage detail")).toHaveCount(0);
  fail = false;
  await field.fill("");
  await page.getByRole("button", { name: "Save abuse contact", exact: true }).click();
  await expect(page.getByText("Abuse contact disabled.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Report abuse", exact: true })).toHaveCount(0);
});

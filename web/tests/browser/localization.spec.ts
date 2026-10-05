import { test, expect, type Page } from "@playwright/test";
import { historyPage, historySlot, historyTransfer } from "../history-page-fixture";
import { guestAvailability, guestSlotID, guestPublicKey } from "../guest-capacity-fixture";

async function language(page: Page, value: "en" | "de" | "system") {
  await page.locator(".language-picker select").selectOption(value);
  await expect(page.locator("html")).toHaveAttribute("lang", value === "system" ? "en" : value);
}
async function config(page: Page) {
  await page.route("**/api/v1/config", (r) => r.fulfill({ json: { max_file_size: 1048576 } }));
}

test("login language is global, persists, syncs and preserves credentials and typed failures", async ({
  page,
}) => {
  await config(page);
  await page.route("**/api/v1/auth/me", (r) =>
    r.fulfill({ status: 401, json: { code: "authentication_required" } }),
  );
  let requests = 0;
  await page.route("**/api/v1/auth/login", (r) => {
    requests++;
    return r.fulfill({
      status: 401,
      json: { code: "invalid_credentials", error: "DO_NOT_SHOW https://secret/#key" },
    });
  });
  await page.goto("/");
  await page.getByLabel("Username", { exact: true }).fill("my-username");
  await page.getByLabel("Password", { exact: true }).fill("preserved-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Incorrect username");
  await page.getByLabel("Password", { exact: true }).fill("preserved-password");
  await language(page, "de");
  await expect(page.getByLabel("Benutzername", { exact: true })).toHaveValue("my-username");
  await expect(page.getByLabel("Passwort", { exact: true })).toHaveValue("preserved-password");
  await expect(page.getByRole("alert")).toContainText("Benutzername");
  await expect(page.getByRole("alert")).not.toContainText("DO_NOT_SHOW");
  expect(requests).toBe(1);
  await page.reload();
  await expect(page.locator(".language-picker select")).toBeEnabled();
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
  await page.evaluate(() =>
    window.dispatchEvent(new StorageEvent("storage", { key: "psst.language", newValue: "en" })),
  );
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible();
});

test("guest selection, title and failed upload survive language changes without payload retry", async ({
  page,
}, info) => {
  await config(page);
  const availability = guestAvailability();
  await page.route(`**/api/v1/slots/${guestSlotID}/availability`, (r) =>
    r.fulfill({ json: availability }),
  );
  let allocations = 0;
  await page.route(`**/api/v1/slots/${guestSlotID}/transfers`, (r) => {
    allocations++;
    return r.fulfill({
      status: 503,
      json: { code: "service_unavailable", error: "secret internal path" },
    });
  });
  await page.setViewportSize({ width: 320, height: 740 });
  await page.goto(`/u/${guestSlotID}#v2.${guestPublicKey}`);
  await page.getByLabel("Choose files").setInputFiles({
    name: "User supplied ß-file.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("hello"),
  });
  await expect(page.locator(".file-list")).toContainText("User supplied ß-file.txt");
  await language(page, "de");
  await expect(page.locator(".selection-summary")).toContainText("1 Datei");
  await expect(page.locator(".file-list")).toContainText("User supplied ß-file.txt");
  expect(allocations).toBe(0);
  await page.getByRole("button", { name: "Dateien senden", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  expect(allocations).toBe(1);
  await language(page, "en");
  await expect(page.locator(".file-list")).toContainText("User supplied ß-file.txt");
  await expect(page.getByRole("alert")).not.toContainText("secret internal path");
  expect(allocations).toBe(1);
  await language(page, "de");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: info.outputPath("guest-german-320.png"), fullPage: true });
});

test("German admin pages and normal-user settings remain usable at narrow width and zoom", async ({
  page,
}, info) => {
  await config(page);
  let role = "admin";
  await page.route("**/api/v1/auth/me", (r) =>
    r.fulfill({
      json: {
        user: {
          id: "11111111-1111-4111-8111-111111111111",
          username: "Untranslated User",
          role,
          disabled: false,
        },
      },
    }),
  );
  await page.route("**/api/v1/admin/**", (r) =>
    r.fulfill({ status: 503, json: { code: "service_unavailable", error: "DO_NOT_SHOW" } }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/?view=server");
  await expect(page.getByRole("heading", { name: "Server settings", exact: true })).toBeVisible();
  await language(page, "de");
  await page.evaluate(() => (document.documentElement.style.fontSize = "125%"));
  await expect(
    page.getByRole("heading", { name: "Servereinstellungen", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Kontonavigation" })).toContainText(
    "Sicherheit",
  );
  await expect(page.locator("body")).not.toContainText("DO_NOT_SHOW");
  await expect(page.locator("body")).not.toContainText("[object Object]");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: info.outputPath("admin-german-mobile.png"), fullPage: true });
  role = "user";
  await page.goto("/?view=settings");
  await expect(page.getByRole("heading", { name: "Einstellungen", exact: true })).toBeVisible();
  await expect(page.locator("body")).toContainText("Untranslated User");
  await expect(page.locator("body")).not.toContainText("Manage your password");
});

test("system regional preference and denied local storage still allow in-memory switching", async ({
  browser,
}) => {
  const context = await browser.newContext({ locale: "de-AT" });
  const page = await context.newPage();
  await page.addInitScript(() => {
    Object.defineProperty(Storage.prototype, "getItem", {
      value() {
        throw new DOMException("blocked", "SecurityError");
      },
    });
    Object.defineProperty(Storage.prototype, "setItem", {
      value() {
        throw new DOMException("blocked", "SecurityError");
      },
    });
  });
  await config(page);
  await page.route("**/api/v1/auth/me", (r) =>
    r.fulfill({ status: 401, json: { code: "authentication_required" } }),
  );
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
  await expect(page.getByLabel("Benutzername", { exact: true })).toBeVisible();
  await language(page, "en");
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible();
  await context.close();
});

for (const appearance of ["light", "dark"] as const)
  test(`German history, receive draft and scanner in ${appearance}`, async ({ page }, info) => {
    await config(page);
    await page.emulateMedia({ colorScheme: appearance });
    await page.route("**/api/v1/auth/me", (r) =>
      r.fulfill({
        json: {
          user: {
            id: "11111111-1111-4111-8111-111111111111",
            username: "My User",
            role: "user",
            disabled: false,
          },
        },
      }),
    );
    await page.route("**/api/v1/auth/resources?*", (r) =>
      r.fulfill({
        json: historyPage({
          transfers: [historyTransfer({ title: "User title with English words" })],
          slots: [historySlot()],
        }),
      }),
    );
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/?view=history");
    await expect(page.getByRole("heading", { name: "Your transfers", exact: true })).toBeVisible();
    await language(page, "de");
    await expect(page.locator("body")).toContainText("User title with English words");
    await expect(page.locator("body")).toContainText("1 Datei empfangen");
    await page.getByRole("link", { name: "Empfangen", exact: true }).click();
    const title = page.getByLabel("Linktitel (optional)");
    await title.fill("Keep my title unchanged");
    await language(page, "en");
    await expect(page.getByLabel("Link title (optional)")).toHaveValue("Keep my title unchanged");
    await language(page, "de");
    await expect(title).toHaveValue("Keep my title unchanged");
    await page.getByRole("link", { name: "QR-Code scannen", exact: true }).click();
    await expect(page.getByRole("heading", { name: "QR-Code scannen", exact: true })).toBeVisible();
    await expect(page.getByLabel("Link einfügen", { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: info.outputPath(`scanner-german-${appearance}.png`),
      fullPage: true,
    });
  });

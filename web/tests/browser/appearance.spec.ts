import { test, expect, credentials } from "./auth-fixture";

const dark = "rgb(11, 25, 23)";
const light = "rgb(246, 248, 247)";

test("appearance overrides system, persists on public pages, and synchronizes across tabs", async ({
  page,
  context,
}) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  const picker = page.getByRole("combobox", { name: "Appearance" });
  await expect(picker).toHaveValue("system");
  await expect(page.locator("body")).toHaveCSS("background-color", dark);
  await picker.selectOption("light");
  await expect(page.locator("body")).toHaveCSS("background-color", light);
  await page.reload();
  await expect(picker).toHaveValue("light");
  await expect(page.locator("body")).toHaveCSS("background-color", light);
  await page.goto("/u/00000000-0000-4000-8000-000000000000");
  await expect(picker).toHaveValue("light");
  await expect(page.locator("body")).toHaveCSS("background-color", light);
  await picker.selectOption("dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("body")).toHaveCSS("background-color", dark);
  const other = await context.newPage();
  await other.goto("/");
  await expect(other.getByRole("combobox", { name: "Appearance" })).toHaveValue("dark");
  await other.getByRole("combobox", { name: "Appearance" }).selectOption("system");
  await expect(picker).toHaveValue("system");
  await expect(page.locator("body")).toHaveCSS("background-color", light);
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("body")).toHaveCSS("background-color", dark);
});

test("appearance remains usable when preference storage is blocked", async ({ page }) => {
  await page.addInitScript(() => {
    Storage.prototype.getItem = () => {
      throw new DOMException("Blocked", "SecurityError");
    };
    Storage.prototype.setItem = () => {
      throw new DOMException("Blocked", "SecurityError");
    };
  });
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  const picker = page.getByRole("combobox", { name: "Appearance" });
  await picker.selectOption("dark");
  await expect(page.locator("body")).toHaveCSS("background-color", dark);
  await picker.selectOption("system");
  await expect(page.locator("body")).toHaveCSS("background-color", light);
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
});

test("password eye stays in the input, toggles by keyboard, and preserves Enter sign-in", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByLabel("Username", { exact: true }).fill(credentials.username);
  const password = page.getByLabel("Password", { exact: true });
  await password.fill(credentials.password);
  const eye = page.getByRole("button", { name: "Show password", exact: true });
  const fieldBounds = (await password.boundingBox())!;
  const eyeBounds = (await eye.boundingBox())!;
  expect(eyeBounds.width).toBeGreaterThanOrEqual(44);
  expect(eyeBounds.x).toBeGreaterThanOrEqual(fieldBounds.x + fieldBounds.width - 60);
  expect(eyeBounds.x + eyeBounds.width).toBeLessThanOrEqual(fieldBounds.x + fieldBounds.width);
  expect(eyeBounds.y).toBeGreaterThanOrEqual(fieldBounds.y);
  expect(eyeBounds.y + eyeBounds.height).toBeLessThanOrEqual(fieldBounds.y + fieldBounds.height);
  await eye.focus();
  await page.keyboard.press("Enter");
  await expect(password).toHaveAttribute("type", "text");
  await expect(password).toHaveValue(credentials.password);
  await expect(page.getByRole("button", { name: "Hide password" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.keyboard.press("Space");
  await expect(password).toHaveAttribute("type", "password");
  await password.press("Enter");
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
});

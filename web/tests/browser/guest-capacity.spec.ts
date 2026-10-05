import { test, expect, type Page } from "@playwright/test";
import { guestAvailability, guestSlotID, guestPublicKey } from "../guest-capacity-fixture";

const file = (name: string, size: number) => ({
  name,
  mimeType: "application/octet-stream",
  buffer: Buffer.alloc(size),
});
async function setup(page: Page) {
  await page
    .context()
    .addCookies([{ name: "saved-login", value: "must-not-forward", url: "http://127.0.0.1:4173" }]);
  await page.route("**/api/v1/config", (route) => {
    expect(route.request().headers().cookie).toBeUndefined();
    return route.fulfill({ json: { max_file_size: 1024 ** 2 } });
  });
  await page.route("**/api/v1/slots/*/transfers", (route) =>
    route.fulfill({ status: 403, json: { code: "resource_limit" } }),
  );
}
const open = (page: Page) => page.goto(`/u/${guestSlotID}#v2.${guestPublicKey}`);

test("accumulated picker additions include empty-file overhead and retain prior files when the next batch exceeds capacity", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setup(page);
  const v = guestAvailability();
  v.upload_capacity.available_files = 2;
  v.upload_capacity.available_wire_bytes = 121;
  let reads = 0;
  await page.route(`**/api/v1/slots/${guestSlotID}/availability`, (route) => {
    reads++;
    expect(route.request().headers().cookie).toBeUndefined();
    expect(route.request().url()).not.toContain(guestPublicKey);
    return route.fulfill({ json: v });
  });
  await open(page);
  await expect(page.getByLabel("Choose files")).toBeVisible();
  await expect(page.getByLabel("Upload availability")).toHaveCount(0);
  await page.getByLabel("Choose files").setInputFiles(file("first.bin", 1));
  await page.getByLabel("Choose files").setInputFiles(file("empty.bin", 0));
  await expect(page.locator(".file-list li")).toHaveCount(2);
  await expect(page.getByRole("button", { name: "Send files", exact: true })).toBeEnabled();
  await page.getByLabel("Choose files").setInputFiles(file("extra.bin", 0));
  await expect(page.getByRole("alert")).toContainText("Too many files");
  await expect(page.locator(".file-list li")).toHaveCount(2);
  await expect(page.locator(".file-list")).not.toContainText("extra.bin");
  await page.getByRole("button", { name: "Remove first.bin", exact: true }).click();
  await expect(page.locator(".file-list li")).toHaveCount(1);
  await page.getByLabel("Choose files").setInputFiles(file("larger.bin", 2));
  await expect(page.getByRole("alert")).toContainText("exceed the space currently available");
  await expect(page.locator(".file-list li")).toHaveCount(1);
  expect(reads).toBeGreaterThanOrEqual(7);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole("button", { name: "Remove empty.bin", exact: true }).focus();
  await page.screenshot({ path: testInfo.outputPath("guest-capacity-mobile.png"), fullPage: true });
});

test("unknown, unavailable and malformed refreshed capacity keep the selection and require a fresh usable check", async ({
  page,
}) => {
  await setup(page);
  const v = guestAvailability();
  let response: unknown = {
    ...v,
    upload_capacity: {
      ...v.upload_capacity,
      state: "unknown",
      reason: "capacity_unavailable",
      available_files: null,
      available_wire_bytes: null,
    },
  };
  let offline = false;
  await page.route(`**/api/v1/slots/${guestSlotID}/availability`, (route) =>
    route.fulfill(
      offline ? { status: 503, json: { error: "/private/DO_NOT_DISPLAY" } } : { json: response },
    ),
  );
  await open(page);
  const panel = page.getByLabel("Upload availability", { exact: true });
  await expect(panel).toContainText("Could not check availability");
  response = v;
  await panel.getByRole("button", { name: "Refresh" }).click();
  await page.getByLabel("Choose files").setInputFiles(file("keep.bin", 20));
  const send = page.getByRole("button", { name: "Send files", exact: true });
  await expect(send).toBeEnabled();
  offline = true;
  // A new selection performs a fresh check, preserving the prior files on failure.
  await page.getByLabel("Choose files").setInputFiles(file("offline.bin", 0));
  await expect(panel).toContainText("Could not check availability");
  await expect(send).toBeDisabled();
  await expect(page.getByRole("alert")).not.toContainText("DO_NOT_DISPLAY");
  offline = false;
  for (response of [
    {
      ...v,
      upload_capacity: {
        ...v.upload_capacity,
        checked_at: new Date(Date.now() - 180000).toISOString(),
      },
    },
    {
      ...v,
      upload_capacity: {
        ...v.upload_capacity,
        checked_at: new Date(Date.now() + 180000).toISOString(),
      },
    },
    { ...v, upload_capacity: undefined },
    { ...v, upload_capacity: { ...v.upload_capacity, available_files: -1 } },
    { ...v, recipient_public_key: "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI" },
  ]) {
    await panel.getByRole("button", { name: "Refresh" }).click();
    await expect(send).toBeDisabled();
    await expect(page.locator(".file-list li")).toHaveCount(1);
    await expect(panel).toContainText("Could not check availability");
  }
  response = v;
  await panel.getByRole("button", { name: "Refresh" }).click();
  await expect(send).toBeEnabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a final fresh preflight blocks allocation, while an authoritative allocation race keeps selected files", async ({
  page,
}) => {
  await setup(page);
  const v = guestAvailability();
  let response = v;
  let posts = 0;
  let configLimit = 1024 ** 2;
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: configLimit } }),
  );
  await page.route(`**/api/v1/slots/${guestSlotID}/availability`, (route) =>
    route.fulfill({ json: response }),
  );
  await page.route(`**/api/v1/slots/${guestSlotID}/transfers`, (route) => {
    posts++;
    expect(route.request().headers().cookie).toBeUndefined();
    return route.fulfill({ status: 403, json: { code: "resource_limit" } });
  });
  await open(page);
  await page.getByLabel("Choose files").setInputFiles(file("keep.bin", 20));
  await expect(page.getByRole("button", { name: "Send files", exact: true })).toBeEnabled();
  response = {
    ...v,
    upload_capacity: { ...v.upload_capacity, available_files: 1, available_wire_bytes: 60 },
  };
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("exceed the space currently available");
  expect(posts).toBe(0);
  async function refreshSelection() {
    response = v;
    await expect(page.locator(".file-list li")).toHaveCount(1);
    await page.getByRole("button", { name: "Remove keep.bin", exact: true }).click();
    await page.getByLabel("Choose files").setInputFiles(file("keep.bin", 20));
    await expect(page.getByRole("button", { name: "Retry upload", exact: true })).toBeEnabled();
  }
  await refreshSelection();
  response = { ...v, upload_capacity: { ...v.upload_capacity, manifest_reserve_bytes: 128 } };
  await page.getByRole("button", { name: "Retry upload", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("file details exceed");
  expect(posts).toBe(0);
  await refreshSelection();
  configLimit = 10;
  await page.getByRole("button", { name: "Retry upload", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Files must be no larger");
  expect(posts).toBe(0);
  configLimit = 1024 ** 2;
  await refreshSelection();
  await page.getByRole("button", { name: "Retry upload", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("storage or object limit");
  expect(posts).toBe(1);
  await expect(page.locator(".file-list li")).toHaveCount(1);
  await page.getByRole("button", { name: "Remove keep.bin", exact: true }).click();
  await expect(page.locator(".file-list li")).toHaveCount(0);
});

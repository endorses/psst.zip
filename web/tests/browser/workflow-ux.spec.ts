import { test as browserTest, expect, type Page } from "@playwright/test";
import { test, signIn } from "./auth-fixture";
import {
  historyID,
  historyPage,
  historyTransfer,
  historySlot,
  historyCursor,
} from "../history-page-fixture";
import { inboxID, inboxOwner, inboxKey, inboxPage } from "../inbox-page-fixture";
import { readFile } from "node:fs/promises";

for (const mode of ["light", "dark"] as const) {
  browserTest(
    `receive creation remains focused at phone/desktop sizes in ${mode} mode`,
    async ({ page }, testInfo) => {
      await mockOwner(page);
      await page.emulateMedia({ colorScheme: mode });
      for (const viewport of [
        { width: 320, height: 740 },
        { width: 1440, height: 900 },
      ]) {
        await page.setViewportSize(viewport);
        await page.goto("/?view=receive");
        await page.evaluate(() => (document.documentElement.style.fontSize = "125%"));
        const title = page.getByLabel("Link title (optional)");
        await expect(title).toBeVisible();
        expect((await title.boundingBox())!.y).toBeLessThan(viewport.height);
        await title.focus();
        await expect(title).toBeFocused();
        await page.keyboard.press("Tab");
        const summary = page.locator("summary").filter({ hasText: "Link limits" });
        await expect(summary).toBeFocused();
        await page.keyboard.press("Enter");
        const check = page.getByRole("checkbox", { name: "Limit files accepted" });
        await page.keyboard.press("Tab");
        await expect(check).toBeFocused();
        await page.keyboard.press("Space");
        await expect(check).toBeChecked();
        const checkBox = (await check.boundingBox())!,
          labelBox = (await check.locator("..").boundingBox())!;
        expect(checkBox.x - labelBox.x).toBeLessThan(8);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await page.screenshot({
          path: testInfo.outputPath(`receive-${viewport.width}-${mode}.png`),
          fullPage: true,
        });
      }
    },
  );
}

async function mockOwner(page: Page, owner = inboxOwner) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: { user: { id: owner, username: "owner", role: "user", disabled: false } },
    }),
  );
}
browserTest(
  "received files precede collapsed sharing and single-page controls disappear",
  async ({ page }) => {
    await mockOwner(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(
      ({ owner, id, key }) => {
        localStorage.setItem(
          `psst.receive-key.v2.${owner}.${id}`,
          JSON.stringify({ publicKey: key, privateKey: key }),
        );
        localStorage.setItem(
          `psst.links.${owner}`,
          JSON.stringify({ [id]: `${location.origin}/u/${id}#v2.${key}` }),
        );
      },
      { owner: inboxOwner, id: inboxID, key: inboxKey },
    );
    await page.route(`**/api/v1/slots/${inboxID}/inbox?*`, (route) =>
      route.fulfill({ json: inboxPage({ title: "Wedding photos" }) }),
    );
    await page.goto(`/?view=receive&slot=${inboxID}`);
    await expect(page.getByRole("heading", { name: "Wedding photos", exact: true })).toBeVisible();
    const files = page.getByRole("region", { name: "Received files", exact: true });
    await expect(
      files.getByRole("link", { name: "1 file · View files", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Received files pages" })).toHaveCount(0);
    await expect(page.getByLabel("Link title (optional)")).toHaveCount(0);
    const share = page.locator("summary").filter({ hasText: "Show QR / Share link" });
    await expect(share).toBeVisible();
    await expect(page.locator(".receive-share .qr")).toBeHidden();
    const fileBox = (await files.boundingBox())!,
      shareBox = (await share.boundingBox())!;
    expect(fileBox.y).toBeLessThan(shareBox.y);
    expect(fileBox.y).toBeLessThan(844);
    await share.click();
    await expect(page.getByRole("button", { name: "Copy link", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Create another link", exact: true }).click();
    await expect(page.getByLabel("Link title (optional)")).toBeVisible();
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(files).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  },
);
browserTest(
  "History filter requests matching records beyond the all-page and uses shared names",
  async ({ page }) => {
    await mockOwner(page, historyID(900));
    await page.route("**/api/v1/config", async (route) => {
      const response = await route.fetch();
      const config = await response.json();
      // The mocked pages below use the bounded legacy history protocol.
      delete config.history_sync_version;
      await route.fulfill({ response, json: config });
    });
    const kinds: string[] = [];
    await page.route("**/api/v1/auth/resources?*", (route) => {
      const kind = new URL(route.request().url()).searchParams.get("kind") ?? "";
      kinds.push(kind);
      return route.fulfill({
        json:
          kind === "slot"
            ? historyPage({ slots: [historySlot({ title: "Older wedding inbox" })] })
            : historyPage({
                transfers: Array.from({ length: 50 }, (_, n) =>
                  historyTransfer({ id: historyID(n + 10) }),
                ),
                next_cursor: historyCursor(1),
              }),
      });
    });
    await page.goto("/?view=history");
    await expect(page.locator(".resource")).toHaveCount(50);
    await page.getByRole("combobox", { name: "History filter" }).selectOption("slots");
    await expect(page.locator(".resource")).toHaveCount(1);
    await expect(page.locator(".resource")).toContainText("Older wedding inbox");
    expect(kinds).toContain("slot");
    await expect(page.getByRole("navigation", { name: "History pages" })).toHaveCount(0);
  },
);
test("a named receive link saves in the signed-in workspace and returns to its inbox", async ({
  page,
  browser,
  baseURL,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await page.getByRole("link", { name: "Receive", exact: true }).click();
  const field = page.getByLabel("Link title (optional)");
  await field.fill("Wedding photos");
  expect((await field.boundingBox())!.y).toBeLessThan(844);
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
  const check = page.getByRole("checkbox", { name: "Limit files accepted" });
  await check.check();
  const label = check.locator("..");
  const checkBox = (await check.boundingBox())!,
    labelBox = (await label.boundingBox())!;
  expect(checkBox.x - labelBox.x).toBeLessThan(8);
  await page.getByRole("button", { name: "Create receive link", exact: true }).click();
  const url = await page.getByLabel("Full link").inputValue();
  const guestContext = await browser.newContext({ baseURL });
  try {
    const guest = await guestContext.newPage();
    await guest.goto(url);
    await expect(guest.getByText("Wedding photos", { exact: true })).toBeVisible();
    await guest.getByLabel("Choose files").setInputFiles({
      name: "photo.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("private photo"),
    });
    await guest.getByRole("button", { name: "Send files", exact: true }).click();
    await expect(guest.getByRole("heading", { name: "Files sent" })).toBeVisible();
    const files = page.getByRole("region", { name: "Received files", exact: true });
    const item = files.getByRole("link", { name: "1 file · View files", exact: true });
    await expect(item).toBeVisible({ timeout: 10000 });
    await item.click();
    await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Signed-in account" })).toContainText(
      "browser-member",
    );
    await expect(page.getByRole("heading", { name: "Wedding photos", exact: true })).toBeVisible();
    await expect(page.getByText(/cannot check free space/)).toHaveCount(0);
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    expect(await readFile((await (await download).path())!)).toEqual(Buffer.from("private photo"));
    await page.getByRole("link", { name: "Back to received files" }).click();
    await expect(page.getByRole("heading", { name: "Wedding photos", exact: true })).toBeVisible();
    await expect(
      files.getByRole("link", { name: "1 file · View files", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Received files pages" })).toHaveCount(0);
  } finally {
    await guestContext.close();
  }
});
test("shared send title survives another browser and the whole link closes only after both files", async ({
  page,
  browser,
  baseURL,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles([
    { name: "first.txt", mimeType: "text/plain", buffer: Buffer.from("first") },
    { name: "second.txt", mimeType: "text/plain", buffer: Buffer.from("second") },
  ]);
  await page.locator("summary").filter({ hasText: "Link settings" }).click();
  await page.getByLabel("Link title", { exact: true }).fill("Trip documents");
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  const url = await page.getByLabel("Full link").inputValue();
  const recipient = await browser.newContext({ baseURL });
  try {
    const downloadPage = await recipient.newPage();
    await downloadPage.goto(url);
    await expect(
      downloadPage.getByRole("heading", { name: "Trip documents", exact: true }),
    ).toBeVisible();
    for (const expected of ["first", "second"]) {
      const pending = downloadPage.waitForEvent("download");
      await downloadPage
        .getByRole("button", { name: "Save file", exact: true })
        .filter({ visible: true })
        .first()
        .click();
      expect(await readFile((await (await pending).path())!)).toEqual(Buffer.from(expected));
      if (expected === "first") {
        await expect(
          downloadPage.getByRole("button", { name: "Save file", exact: true }).last(),
        ).toBeEnabled();
      }
    }
    await expect(downloadPage.getByText("Sender notified.", { exact: true })).toBeVisible();
    await downloadPage.reload();
    await expect(downloadPage.getByRole("alert")).toContainText("reached its download limit");
    await expect(downloadPage.getByRole("button", { name: "Save file", exact: true })).toHaveCount(
      0,
    );
    await page.getByRole("link", { name: "History", exact: true }).click();
    const row = page.locator(".resource").filter({ hasText: "Trip documents" });
    await expect(row).toContainText("Download limit reached");
    await expect(row).not.toContainText("Expires");
    await expect(row.getByRole("link", { name: "Open", exact: true })).toHaveCount(0);
    await expect(row.getByRole("button", { name: "Copy link", exact: true })).toHaveCount(0);
    await expect(row.getByRole("link", { name: "New send link" })).toBeVisible();
  } finally {
    await recipient.close();
  }
});

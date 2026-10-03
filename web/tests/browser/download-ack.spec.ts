import { test, expect, type Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { encrypt, encryptManifest, exportKey, generateKey } from "../../src/lib/crypto";

async function prepareDownload(
  page: Page,
  options: {
    count?: number;
    wrongKey?: boolean;
    corruptFile?: number;
    wrongSize?: number;
    failedFile?: number;
    failAcknowledgment?: boolean;
  } = {},
) {
  const id = randomUUID();
  const key = await generateKey();
  const files = Array.from({ length: options.count ?? 2 }, (_, index) => ({
    name: `file-${index}.txt`,
    size: 6,
    mime_type: "text/plain",
    blob_id: randomUUID(),
  }));
  if (options.wrongSize !== undefined) files[options.wrongSize].size++;
  let acknowledgments = 0;
  let blobRequests = 0;
  let downloads = 0;
  page.on("download", () => downloads++);

  const api = `**/api/v1/transfers/${id}`;
  await page.route(api, (route) =>
    route.fulfill({
      json: { id, file_count: files.length, total_size: files.length * 34, downloaded_at: null },
    }),
  );
  await page.route(`${api}/manifest`, async (route) =>
    route.fulfill({ body: Buffer.from(await encryptManifest(key, { files })) }),
  );
  for (const [index, file] of files.entries()) {
    const encrypted = new Uint8Array(
      await encrypt(key, new TextEncoder().encode(`file ${index}`).buffer),
    );
    if (index === options.corruptFile) encrypted[encrypted.length - 1] ^= 1;
    await page.route(`${api}/files/${file.blob_id}`, (route) => {
      blobRequests++;
      return route.fulfill({
        status: index === options.failedFile ? 503 : 200,
        body: Buffer.from(encrypted),
      });
    });
  }
  await page.route(`${api}/downloaded`, (route) => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postData()).toBeNull();
    acknowledgments++;
    return route.fulfill({
      status: options.failAcknowledgment && acknowledgments === 1 ? 503 : 204,
    });
  });

  const encodedKey = await exportKey(options.wrongKey ? await generateKey() : key);
  await page.goto(`/d/${id}#${encodedKey}`);
  return {
    acknowledgments: () => acknowledgments,
    blobRequests: () => blobRequests,
    downloads: () => downloads,
  };
}

async function downloadIndividual(page: Page, index: number) {
  const button = page.locator(".file-list li").nth(index).getByRole("button");
  await expect(button).toHaveText(/Save files|Save again/);
  const download = page.waitForEvent("download");
  await button.click();
  await download;
}

test("manifest viewing and repeated partial downloads do not acknowledge the transfer", async ({
  page,
}) => {
  const transfer = await prepareDownload(page);
  await expect(page.getByRole("heading", { name: "Save files" })).toBeVisible();
  expect(transfer.acknowledgments()).toBe(0);
  expect(transfer.blobRequests()).toBe(0);
  await downloadIndividual(page, 0);
  await downloadIndividual(page, 0);
  expect(transfer.acknowledgments()).toBe(0);
  await downloadIndividual(page, 1);
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  expect(transfer.acknowledgments()).toBe(1);
  await downloadIndividual(page, 0);
  expect(transfer.acknowledgments()).toBe(1);
  await expect(
    page.getByText("All files handed to your browser. Check its Downloads list for saved files."),
  ).toBeVisible();
});

test("ZIP acknowledgment follows decryption and browser handoff of every file", async ({
  page,
}) => {
  const transfer = await prepareDownload(page);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save all as ZIP" }).click();
  expect((await download).suggestedFilename()).toBe("files.zip");
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  expect(transfer.blobRequests()).toBe(2);
  expect(transfer.acknowledgments()).toBe(1);
});

test("wrong decryption keys never acknowledge even when metadata is accessible", async ({
  page,
}) => {
  const transfer = await prepareDownload(page, { wrongKey: true });
  await expect(page.getByRole("heading", { name: "Cannot open files" })).toBeVisible();
  expect(transfer.blobRequests()).toBe(0);
  expect(transfer.downloads()).toBe(0);
  expect(transfer.acknowledgments()).toBe(0);
});

for (const failure of ["corruptFile", "wrongSize", "failedFile"] as const) {
  test(`${failure} prevents single-file and ZIP acknowledgment`, async ({ page }) => {
    const transfer = await prepareDownload(page, { [failure]: 1 });
    await downloadIndividual(page, 0);
    await page.locator(".file-list li").nth(1).getByRole("button").click();
    await expect(page.getByRole("alert")).toBeVisible();
    expect(transfer.acknowledgments()).toBe(0);
    expect(transfer.downloads()).toBe(1);

    await page.getByRole("button", { name: "Save all as ZIP" }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    expect(transfer.acknowledgments()).toBe(0);
    expect(transfer.downloads()).toBe(1);
  });
}

test("failed acknowledgments retry without downloading files again", async ({ page }) => {
  const transfer = await prepareDownload(page, { count: 1, failAcknowledgment: true });
  await downloadIndividual(page, 0);
  const retry = page.getByRole("button", { name: "Retry confirmation" });
  await expect(retry).toBeVisible();
  await expect(
    page.getByText("Files downloaded, but the sender could not be notified."),
  ).toBeVisible();
  expect(transfer.acknowledgments()).toBe(1);
  expect(transfer.downloads()).toBe(1);
  await retry.click();
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  expect(transfer.acknowledgments()).toBe(2);
  expect(transfer.blobRequests()).toBe(1);
  expect(transfer.downloads()).toBe(1);
});

test("failed browser handoff never acknowledges decrypted files", async ({ page }) => {
  const transfer = await prepareDownload(page, { count: 1 });
  await page.evaluate(() => {
    HTMLAnchorElement.prototype.click = () => {
      throw new Error("Browser handoff failed");
    };
  });
  await page.getByRole("button", { name: "Save files", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Could not save files");
  expect(transfer.acknowledgments()).toBe(0);
  expect(transfer.downloads()).toBe(0);
});

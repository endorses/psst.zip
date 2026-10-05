import { test, expect, type Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { encryptFileFrame, wireSize } from "../../src/lib/chunked-files";
import { encryptManifest, exportKey, generateKey } from "../../src/lib/crypto";

async function prepareDownload(
  page: Page,
  options: {
    count?: number;
    wrongKey?: boolean;
    corruptFile?: number;
    wrongSize?: number;
    failedFile?: number;
    failAcknowledgment?: boolean;
    metadata?: Record<string, unknown>;
    size?: number;
    stallRefresh?: boolean;
  } = {},
) {
  const id = randomUUID();
  const key = await generateKey();
  const files = Array.from({ length: options.count ?? 2 }, (_, index) => ({
    name: `file-${index}.txt`,
    size: options.size ?? 6,
    mime_type: "text/plain",
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: index.toString(16).padStart(32, "0"),
    blob_id: randomUUID(),
  }));
  if (options.wrongSize !== undefined) files[options.wrongSize].size++;
  let acknowledgments = 0;
  let blobRequests = 0;
  let downloads = 0;
  let metadataRequests = 0;
  page.on("download", () => downloads++);

  const api = `**/api/v1/transfers/${id}`;
  await page.route(api, (route) => {
    metadataRequests++;
    if (options.stallRefresh && metadataRequests > 1) return;
    return route.fulfill({
      json: {
        id,
        status: "complete",
        file_count: files.length,
        total_size: files.length * wireSize(options.size ?? 6),
        downloaded_at: null,
        max_downloads: 0,
        files: files.map((file) => ({
          id: file.blob_id,
          size: wireSize(options.size ?? 6),
          download_count: 0,
          remaining_downloads: null,
        })),
        ...options.metadata,
      },
    });
  });
  await page.route(`${api}/manifest`, async (route) =>
    route.fulfill({ body: Buffer.from(await encryptManifest(key, { files })) }),
  );
  for (const [index, file] of files.entries()) {
    const encrypted = new Uint8Array(
      await encryptFileFrame(
        key,
        file.encryption_id,
        0,
        6,
        new TextEncoder().encode(`file ${index}`).buffer,
      ),
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
    metadataRequests: () => metadataRequests,
  };
}

test("insufficient OPFS quota prevents a large file request and explains destination-space limits", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.addInitScript(() => {
    Object.defineProperty(window, "showSaveFilePicker", { configurable: true, value: undefined });
    Object.defineProperty(navigator, "storage", {
      configurable: true,
      value: {
        estimate: async () => ({ quota: 256 * 1024 * 1024, usage: 0 }),
        getDirectory: async () => {
          throw new Error("Preflight must run first");
        },
      },
    });
  });
  const transfer = await prepareDownload(page, { count: 1, size: 26 * 1024 * 1024 });
  await expect(page.getByText(/cannot check free space in your download folder/)).toHaveCount(0);
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Not enough browser storage");
  await page.screenshot({ path: testInfo.outputPath("recipient-storage.png"), fullPage: true });
  expect(transfer.blobRequests()).toBe(0);
  expect(transfer.downloads()).toBe(0);
});

test("a valid encrypted manifest above the aggregate ceiling never fetches file bodies", async ({
  page,
}) => {
  const transfer = await prepareDownload(page, { count: 2, size: 1024 ** 4 });
  await expect(page.getByRole("heading", { name: "Cannot open files" })).toBeVisible();
  expect(transfer.blobRequests()).toBe(0);
  expect(transfer.downloads()).toBe(0);
  expect(transfer.acknowledgments()).toBe(0);
});

test("completed saving does not wait for a stalled metadata refresh or offer cancellation", async ({
  page,
}) => {
  const transfer = await prepareDownload(page, { count: 1, stallRefresh: true });
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  await expect.poll(transfer.metadataRequests).toBe(2);
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel saving" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save again", exact: true })).toBeEnabled();
  expect(transfer.downloads()).toBe(1);
  expect(transfer.acknowledgments()).toBe(1);
});

async function downloadIndividual(page: Page, index: number) {
  const button = page.locator(".file-list li").nth(index).getByRole("button");
  await expect(button).toHaveAccessibleName(/^(Save file|Save again)$/);
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

for (const failure of ["corruptFile", "failedFile"] as const) {
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

test("manifest size mismatch is rejected before any file retrieval", async ({ page }) => {
  const transfer = await prepareDownload(page, { wrongSize: 1 });
  await expect(page.getByRole("heading", { name: "Cannot open files" })).toBeVisible();
  expect(transfer.blobRequests()).toBe(0);
  expect(transfer.acknowledgments()).toBe(0);
  expect(transfer.downloads()).toBe(0);
});

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
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Could not save files");
  expect(transfer.acknowledgments()).toBe(0);
  expect(transfer.downloads()).toBe(0);
});

for (const metadata of [
  { id: "00000000-0000-0000-0000-000000000000" },
  { status: "pending" },
  { files: [] },
  {
    files: [
      {
        id: "00000000-0000-0000-0000-000000000000",
        size: wireSize(6),
        download_count: 0,
        remaining_downloads: null,
      },
    ],
  },
  { max_downloads: -1 },
]) {
  test(`hostile metadata ${JSON.stringify(metadata)} never fetches a file`, async ({ page }) => {
    const transfer = await prepareDownload(page, { count: 1, metadata });
    await expect(page.getByRole("heading", { name: "Cannot open files" })).toBeVisible();
    expect(transfer.blobRequests()).toBe(0);
    expect(transfer.downloads()).toBe(0);
    expect(transfer.acknowledgments()).toBe(0);
  });
}

import { test, expect, signIn } from "./auth-fixture";
import { readFile } from "node:fs/promises";
import { unzipSync, strFromU8 } from "fflate";
import { randomBytes, webcrypto } from "node:crypto";

const files = [
  { name: 'first "quoted".txt', mimeType: "text/plain", buffer: Buffer.from("secret first file") },
  { name: "empty.txt", mimeType: "text/plain", buffer: Buffer.alloc(0) },
];

test("LAN deployment runs without a secure context or Web Crypto", async ({ page }) => {
  test.skip(
    process.env.PSST_EXPECT_INSECURE_CONTEXT !== "1",
    "Only required for LAN HTTP deployment checks",
  );
  await page.goto("/");
  expect(
    await page.evaluate(() => ({
      secureContext: window.isSecureContext,
      subtle: typeof globalThis.crypto?.subtle,
      getRandomValues: typeof globalThis.crypto?.getRandomValues,
    })),
  ).toEqual({ secureContext: false, subtle: "undefined", getRandomValues: "function" });
});

test("share page uploads files; download page decrypts individual files and ZIP", async ({
  page,
}) => {
  await signIn(page);
  await expect(page.locator('input[type="file"]')).toBeEnabled();
  await page.locator('input[type="file"]').setInputFiles(files);
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  const link = await page.getByLabel("Full link").inputValue();
  await page.getByRole("button", { name: "Copy link", exact: true }).click();
  await expect(page.getByText("Link copied.", { exact: true })).toBeVisible();
  expect(link).toMatch(/\/d\/[0-9a-f-]+#[A-Za-z0-9_-]+$/);
  await page.goto(link);
  await expect(page.getByRole("heading", { name: "Save files" })).toBeVisible();
  const single = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save file", exact: true }).first().click();
  const download = await single;
  // Chromium sanitizes quotation marks in the suggested filesystem name.
  expect(download.suggestedFilename()).toBe("first _quoted_.txt");
  expect(await readFile((await download.path())!)).toEqual(files[0].buffer);
  const zipped = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save all as ZIP" }).click();
  const zip = unzipSync(await readFile((await (await zipped).path())!));
  expect(Object.values(zip).map((bytes) => strFromU8(bytes))).toEqual(["secret first file", ""]);
});

test("slot upload page creates a complete transfer with decryptable manifest", async ({
  page,
  request,
}) => {
  const response = await request.post("/api/v1/slots");
  const slot = await response.json();
  const rawKey = randomBytes(32);
  await page.goto(`/u/${slot.id}#${rawKey.toString("base64url")}`);
  await expect(page.locator('input[type="file"]')).toBeEnabled();
  await page.locator('input[type="file"]').setInputFiles(files);
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Files sent" })).toBeVisible();
  const info = await (await request.get(`/api/v1/slots/${slot.id}`)).json();
  expect(info.transfers).toHaveLength(1);
  expect(info.transfers[0].status).toBe("complete");
  const encrypted = await (
    await request.get(`/api/v1/transfers/${info.transfers[0].transfer_id}/manifest`)
  ).body();
  const key = await webcrypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["decrypt"]);
  const plaintext = await webcrypto.subtle.decrypt(
    { name: "AES-GCM", iv: new Uint8Array(encrypted.subarray(0, 12)) },
    key,
    new Uint8Array(encrypted.subarray(12)),
  );
  const manifest = JSON.parse(Buffer.from(plaintext).toString());
  expect(manifest.files[0]).toMatchObject({ name: files[0].name, mime_type: "text/plain" });
  expect(manifest.files[0].blob_id).toMatch(/^[0-9a-f-]{36}$/);
});

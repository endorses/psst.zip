import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { unzipSync, strFromU8 } from "fflate";
import { randomBytes, webcrypto } from "node:crypto";

const files = [
  { name: 'first "quoted".txt', mimeType: "text/plain", buffer: Buffer.from("secret first file") },
  { name: "empty.txt", mimeType: "text/plain", buffer: Buffer.alloc(0) },
];

test("share page uploads files; download page decrypts individual files and ZIP", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.locator('input[type="file"]')).toBeEnabled();
  await page.locator('input[type="file"]').setInputFiles(files);
  await page.getByRole("button", { name: /Encrypt & Upload/ }).click();
  await expect(page.getByRole("heading", { name: "Ready to Share" })).toBeVisible();
  const link = await page.locator(".link-input").inputValue();
  expect(link).toMatch(/\/d\/[0-9a-f-]+#[A-Za-z0-9_-]+$/);
  await page.goto(link);
  await expect(page.getByRole("heading", { name: "Your Files" })).toBeVisible();
  const single = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download", exact: true }).first().click();
  const download = await single;
  // Chromium sanitizes quotation marks in the suggested filesystem name.
  expect(download.suggestedFilename()).toBe("first _quoted_.txt");
  expect(await readFile((await download.path())!)).toEqual(files[0].buffer);
  const zipped = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download All as ZIP" }).click();
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
  await page.getByRole("button", { name: /Encrypt & Upload/ }).click();
  await expect(page.getByRole("heading", { name: /Upload Complete/i })).toBeVisible();
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

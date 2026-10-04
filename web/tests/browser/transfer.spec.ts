import { test, expect, signIn } from "./auth-fixture";
import { readFile } from "node:fs/promises";
import { unzipSync, strFromU8 } from "fflate";
import { randomBytes, webcrypto } from "node:crypto";
import {
  generateReceiveKeyPair,
  openSubmissionKey,
  decodeReceiveEnvelope,
} from "../../src/lib/receive-crypto.ts";

const files = [
  { name: 'first "quoted".txt', mimeType: "text/plain", buffer: Buffer.from("secret first file") },
  { name: "empty.txt", mimeType: "text/plain", buffer: Buffer.alloc(0) },
];

test("LAN deployment runs without a secure context or Web Crypto", async ({ page }) => {
  test.skip(
    (process.env.PSST_EXPECT_INSECURE_CONTEXT ?? process.env.PSST_EXPECT_INSECURE_CONTEXT) !== "1",
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
  const pair = await generateReceiveKeyPair();
  const publicKey = Buffer.from(pair.publicKey).toString("base64url");
  const response = await request.post("/api/v1/slots", {
    data: { receive_protocol: 2, recipient_public_key: publicKey },
  });
  const slot = await response.json();
  await page.goto(`/u/${slot.id}#v2.${publicKey}`);
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
  const envelope = decodeReceiveEnvelope(new Uint8Array(encrypted));
  const rawKey = await openSubmissionKey(
    pair.privateKey,
    pair.publicKey,
    slot.id,
    info.transfers[0].transfer_id,
    envelope.wrappedKey,
  );
  const key = await webcrypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["decrypt"]);
  const plaintext = await webcrypto.subtle.decrypt(
    { name: "AES-GCM", iv: envelope.encryptedManifest.slice(0, 12) },
    key,
    envelope.encryptedManifest.slice(12),
  );
  const manifest = JSON.parse(Buffer.from(plaintext).toString());
  expect(manifest.files[0]).toMatchObject({ name: files[0].name, mime_type: "text/plain" });
  expect(manifest.files[0].blob_id).toMatch(/^[0-9a-f-]{36}$/);
});

test("per-file download limit is fixed at creation and exhausted controls reflect server counts", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(files);
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  await page.getByRole("spinbutton", { name: "Limit downloads per file" }).fill("1");
  const created = page.waitForResponse(
    (r) => r.url().endsWith("/api/v1/transfers") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  expect((await created).request().postDataJSON().max_downloads).toBe(1);
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  await page.goto(await page.getByLabel("Full link").inputValue());
  await expect(page.getByText("· 1 attempts remaining", { exact: true }).first()).toBeVisible();
  const saved = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save file", exact: true }).first().click();
  await saved;
  await expect(page.getByRole("button", { name: "Save file", exact: true }).first()).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save all as ZIP" })).toBeDisabled();
  await expect(page.getByText("· 0 attempts remaining", { exact: true })).toBeVisible();
});

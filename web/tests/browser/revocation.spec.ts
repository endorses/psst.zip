import { test, expect, type APIRequestContext } from "@playwright/test";
import { randomBytes, webcrypto } from "node:crypto";

async function createEncryptedTransfer(request: APIRequestContext) {
  const created = await request.post("/api/v1/transfers");
  expect(created.status()).toBe(201);
  const transfer = (await created.json()) as { id: string; delete_token: string };
  expect(transfer.delete_token).toEqual(expect.any(String));
  expect(transfer.delete_token.length).toBeGreaterThan(0);

  const rawKey = randomBytes(32);
  const key = await webcrypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["encrypt"]);
  async function encrypted(data: string): Promise<Buffer> {
    const nonce = randomBytes(12);
    const ciphertext = await webcrypto.subtle.encrypt(
      { name: "AES-GCM", iv: nonce },
      key,
      new TextEncoder().encode(data),
    );
    return Buffer.concat([nonce, Buffer.from(ciphertext)]);
  }

  const plaintext = "This file must become unavailable after revocation.";
  const blob = await encrypted(plaintext);
  const upload = await request.post(`/api/v1/transfers/${transfer.id}/files`, {
    headers: { "Tus-Resumable": "1.0.0", "Upload-Length": String(blob.length) },
  });
  expect(upload.status()).toBe(201);
  const location = upload.headers().location;
  expect(location).toBeTruthy();
  const uploadURL = new URL(location, upload.url());
  const uploaded = await request.patch(uploadURL.toString(), {
    headers: {
      "Tus-Resumable": "1.0.0",
      "Upload-Offset": "0",
      "Content-Type": "application/offset+octet-stream",
    },
    data: blob,
  });
  expect(uploaded.status()).toBe(204);

  const manifest = await encrypted(
    JSON.stringify({
      files: [
        {
          name: "revocable.txt",
          size: Buffer.byteLength(plaintext),
          mime_type: "text/plain",
          blob_id: uploadURL.pathname.split("/").pop(),
        },
      ],
    }),
  );
  const manifestResponse = await request.post(`/api/v1/transfers/${transfer.id}/manifest`, {
    headers: { "Content-Type": "application/octet-stream" },
    data: manifest,
  });
  expect(manifestResponse.ok()).toBe(true);
  expect((await request.post(`/api/v1/transfers/${transfer.id}/complete`)).ok()).toBe(true);
  return {
    ...transfer,
    blobPath: uploadURL.pathname,
    link: `/d/${transfer.id}#${rawKey.toString("base64url")}`,
  };
}

test("revoking a transfer blocks downloads from an already loaded page and a fresh shared link", async ({
  page,
  context,
  request,
}) => {
  const transfer = await createEncryptedTransfer(request);
  let browserDownloads = 0;
  page.on("download", () => browserDownloads++);
  await page.goto(transfer.link);
  const download = page.getByRole("button", { name: "Download", exact: true });
  await expect(download).toBeEnabled();

  const revoked = await request.delete(`/api/v1/transfers/${transfer.id}`, {
    headers: { Authorization: `Bearer ${transfer.delete_token}` },
  });
  expect(revoked.status()).toBe(204);

  const blobResponse = page.waitForResponse(
    (response) => new URL(response.url()).pathname === transfer.blobPath,
  );
  await download.click();
  expect((await blobResponse).status()).toBe(404);
  await expect(page.getByRole("alert")).toContainText("404");
  expect(browserDownloads).toBe(0);

  const freshPage = await context.newPage();
  await freshPage.goto(transfer.link);
  await expect(freshPage.getByText("This transfer has expired or does not exist.")).toBeVisible();
  await expect(freshPage.getByRole("button", { name: "Download", exact: true })).toHaveCount(0);
});

test("revoking a slot blocks uploads from an already loaded page and a fresh upload link", async ({
  page,
  context,
  request,
}) => {
  const created = await request.post("/api/v1/slots");
  expect(created.status()).toBe(201);
  const slot = (await created.json()) as { id: string; delete_token: string };
  expect(slot.delete_token).toEqual(expect.any(String));
  expect(slot.delete_token.length).toBeGreaterThan(0);
  const link = `/u/${slot.id}#${randomBytes(32).toString("base64url")}`;
  await page.goto(link);
  const picker = page.locator('input[type="file"]');
  await expect(picker).toBeEnabled();
  await picker.setInputFiles({
    name: "blocked.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("This upload must fail after slot revocation."),
  });

  const revoked = await request.delete(`/api/v1/slots/${slot.id}`, {
    headers: { Authorization: `Bearer ${slot.delete_token}` },
  });
  expect(revoked.status()).toBe(204);

  const createResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === `/api/v1/slots/${slot.id}/transfers` &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: /Encrypt & Upload/ }).click();
  expect((await createResponse).status()).toBe(404);
  await expect(page.getByText(/API 404:/)).toBeVisible();

  const freshPage = await context.newPage();
  await freshPage.goto(link);
  await expect(
    freshPage.getByText("This upload slot has expired or does not exist."),
  ).toBeVisible();
  await expect(freshPage.locator('input[type="file"]')).toHaveCount(0);
});

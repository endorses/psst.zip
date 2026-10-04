import { type APIRequestContext } from "@playwright/test";
import { test, expect } from "./auth-fixture";
import { encryptFileFrame, newEncryptionId } from "../../src/lib/chunked-files";
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
  const encryption_id = newEncryptionId();
  const blob = Buffer.from(
    await encryptFileFrame(
      new Uint8Array(rawKey),
      encryption_id,
      0,
      Buffer.byteLength(plaintext),
      new TextEncoder().encode(plaintext).buffer,
    ),
  );
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
          encoding: "chunked-v1",
          chunk_size: 4194304,
          encryption_id,
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
  const download = page.getByRole("button", { name: "Save file", exact: true });
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
  await expect(page.getByRole("alert")).toContainText("Could not save files");
  expect(browserDownloads).toBe(0);

  const freshPage = await context.newPage();
  await freshPage.goto(transfer.link);
  await expect(
    freshPage.getByText("This transfer has expired or was revoked. Ask the sender for a new link."),
  ).toBeVisible();
  await expect(freshPage.getByRole("button", { name: "Save file", exact: true })).toHaveCount(0);
});

test("revoking a slot blocks uploads from an already loaded page and a fresh upload link", async ({
  page,
  context,
  request,
}) => {
  const publicKey = randomBytes(32).toString("base64url");
  const created = await request.post("/api/v1/slots", {
    data: { receive_protocol: 2, recipient_public_key: publicKey },
  });
  expect(created.status()).toBe(201);
  const slot = (await created.json()) as { id: string; delete_token: string };
  expect(slot.delete_token).toEqual(expect.any(String));
  expect(slot.delete_token.length).toBeGreaterThan(0);
  const link = `/u/${slot.id}#v2.${publicKey}`;
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
      new URL(response.url()).pathname === `/api/v1/slots/${slot.id}/availability` &&
      response.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  expect((await createResponse).status()).toBe(404);
  await expect(page.getByRole("alert")).toBeVisible();

  const freshPage = await context.newPage();
  await freshPage.goto(link);
  await expect(
    freshPage.getByText("This receive link has expired or was revoked. Ask for a new link."),
  ).toBeVisible();
  await expect(freshPage.locator('input[type="file"]')).toHaveCount(0);
});

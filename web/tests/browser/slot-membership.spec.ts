import { test, expect, type Page } from "@playwright/test";
import {
  generateReceiveKeyPair,
  sealSubmissionKey,
  encodeReceiveEnvelope,
} from "../../src/lib/receive-crypto";
import { generateKey, exportKey, encryptManifest } from "../../src/lib/crypto";
const slot = "11111111-1111-4111-8111-111111111111";
const transfer = "22222222-2222-4222-8222-222222222222";
const owner = "33333333-3333-4333-8333-333333333333";
async function setup(page: Page) {
  const pair = await generateReceiveKeyPair();
  const publicKey = await exportKey(pair.publicKey),
    privateKey = await exportKey(pair.privateKey);
  const key = await generateKey();
  const file = {
    name: "older-submission.txt",
    size: 0,
    mime_type: "text/plain",
    blob_id: "44444444-4444-4444-8444-444444444444",
    encoding: "chunked-v1" as const,
    chunk_size: 4194304 as const,
    encryption_id: "a".repeat(32),
  };
  const encrypted = await encryptManifest(key, { files: [file] });
  const envelope = encodeReceiveEnvelope(
    await sealSubmissionKey(pair.publicKey, slot, transfer, key),
    new Uint8Array(encrypted),
  );
  await page.addInitScript(({ name, value }) => localStorage.setItem(name, value), {
    name: `psst.receive-key.v2.${owner}.${slot}`,
    value: JSON.stringify({ publicKey, privateKey }),
  });
  const requests: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    requests.push(path);
    if (path === "/api/v1/auth/me")
      return route.fulfill({
        json: { user: { id: owner, role: "user", username: "owner", disabled: false } },
      });
    if (path === `/api/v1/transfers/${transfer}`)
      return route.fulfill({
        json: {
          id: transfer,
          file_count: 1,
          total_size: 60,
          expires_at: "2099-01-01T00:00:00Z",
          downloaded_at: null,
        },
      });
    if (path === `/api/v1/transfers/${transfer}/manifest`)
      return route.fulfill({
        body: Buffer.from(envelope),
        contentType: "application/octet-stream",
      });
    return route.fulfill({ status: 404, body: "Unexpected endpoint" });
  });
  return {
    requests,
    membership: {
      slot_id: slot,
      transfer_id: transfer,
      receive_protocol: 2,
      recipient_public_key: publicKey,
    },
    key,
    encrypted,
  };
}
test("exact owner membership precedes manifest download without inbox enumeration", async ({
  page,
}) => {
  const data = await setup(page);
  const path = `/api/v1/slots/${slot}/transfers/${transfer}/membership`;
  await page.route(`**${path}`, (route) => {
    data.requests.push(path);
    return route.fulfill({ json: data.membership });
  });
  await page.goto(`/d/${transfer}?inbox=${slot}`);
  await expect(page.getByText("older-submission.txt", { exact: true })).toBeVisible();
  expect(data.requests).not.toContain(`/api/v1/slots/${slot}`);
  expect(data.requests.indexOf(path)).toBeLessThan(
    data.requests.indexOf(`/api/v1/transfers/${transfer}/manifest`),
  );
});
test("invalid membership and denied access never fetch the manifest", async ({ page }) => {
  const data = await setup(page);
  let result: unknown = data.membership,
    status = 200;
  await page.route(`**/slots/${slot}/transfers/${transfer}/membership`, (route) =>
    route.fulfill({ status, json: result }),
  );
  for (const override of [
    { slot_id: transfer },
    { transfer_id: slot },
    { receive_protocol: 1 },
    { recipient_public_key: "A".repeat(43) },
  ]) {
    result = { ...data.membership, ...override };
    await page.goto(`/d/${transfer}?inbox=${slot}`);
    await expect(page.getByRole("alert")).toContainText("Could not open these files");
  }
  result = { error: "private error" };
  for (status of [401, 403, 404, 410]) {
    await page.goto(`/d/${transfer}?inbox=${slot}`);
    await expect(page.getByRole("alert")).toContainText(
      status < 404 ? "Sign in as the inbox owner" : "expired or was revoked",
    );
  }
  expect(data.requests).not.toContain(`/api/v1/transfers/${transfer}/manifest`);
});
test("standalone symmetric-key download needs no inbox membership", async ({ page }) => {
  const data = await setup(page);
  await page.route(`**/transfers/${transfer}/manifest`, (route) =>
    route.fulfill({ body: Buffer.from(data.encrypted), contentType: "application/octet-stream" }),
  );
  await page.goto(`/d/${transfer}#${await exportKey(data.key)}`);
  await expect(page.getByText("older-submission.txt", { exact: true })).toBeVisible();
  expect(data.requests.some((path) => path.includes("/slots/"))).toBe(false);
});

import { test as authenticatedTest, signIn } from "./auth-fixture";
import { readFile } from "node:fs/promises";
authenticatedTest(
  "real receive upload decrypts and saves through the owner membership endpoint",
  async ({ page, request }) => {
    await signIn(page);
    await page.getByRole("link", { name: "Receive", exact: true }).click();
    await page.getByRole("button", { name: "Create receive link", exact: true }).click();
    const link = await page.getByLabel("Full link").inputValue();
    const slotID = new URL(link).pathname.split("/").pop()!;
    await page.goto(link);
    const contents = Buffer.from("owner-only membership roundtrip");
    await page
      .getByLabel("Choose files")
      .setInputFiles({ name: "received.txt", mimeType: "text/plain", buffer: contents });
    const created = page.waitForResponse(
      (r) => r.url().endsWith(`/slots/${slotID}/transfers`) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    const submission = await (await created).json();
    await expect(page.getByRole("heading", { name: "Files sent" })).toBeVisible();
    const membership = await request.get(
      `/api/v1/slots/${slotID}/transfers/${submission.id}/membership`,
    );
    expect(membership.status()).toBe(200);
    expect(await membership.json()).toMatchObject({
      slot_id: slotID,
      transfer_id: submission.id,
      receive_protocol: 2,
    });
    let listed = false;
    await page.route(`**/api/v1/slots/${slotID}`, (route) => {
      listed = true;
      return route.abort();
    });
    await page.goto(`/d/${submission.id}?inbox=${slotID}`);
    await expect(page.getByText("received.txt", { exact: true })).toBeVisible();
    const downloaded = page.waitForEvent("download");
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    expect(await readFile((await (await downloaded).path())!)).toEqual(contents);
    expect(listed).toBe(false);
  },
);

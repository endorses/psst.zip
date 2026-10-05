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
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
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
  await expect(page.getByText("Download limit reached", { exact: true })).toBeVisible();
});

test("an allocated send keeps its fixed download policy through retry and resets it for a new send", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(files);
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  await page.getByRole("spinbutton", { name: "Limit downloads per file" }).fill("3");
  const selectedPolicies: number[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/api/v1/transfers") && request.method() === "POST") {
      selectedPolicies.push(request.postDataJSON().max_downloads);
    }
  });
  let interrupted = false;
  await page.route("**/api/v1/transfers/*/manifest", (route) => {
    if (route.request().method() !== "POST" || interrupted) return route.continue();
    interrupted = true;
    return route.fulfill({ status: 503, json: { error: "Temporary manifest outage" } });
  });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("button", { name: "Retry upload", exact: true })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Limit downloads per file" })).toBeDisabled();
  await expect(page.getByRole("spinbutton", { name: "Limit downloads per file" })).toHaveValue("3");
  await expect(page.getByRole("spinbutton", { name: "Limit downloads per file" })).toBeDisabled();
  await page.getByRole("button", { name: "Retry upload", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  expect(selectedPolicies).toEqual([3, 3]);
  await expect(
    page.getByText("3 download attempts per file, including interrupted downloads."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Send more files", exact: true }).click();
  await page.getByLabel("Choose files").setInputFiles(files);
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
  await expect(page.getByRole("checkbox", { name: "Limit downloads per file" })).not.toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Limit downloads per file" })).toBeEnabled();
});

test("invalid enabled link limit is announced inline without allocating a transfer", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(files);
  const limits = page.locator("details.optional-limit");
  await expect(limits).not.toHaveAttribute("open", "");
  await limits.locator("summary").click();
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  const input = page.getByRole("spinbutton", { name: "Limit downloads per file" });
  let allocations = 0;
  page.on("request", (request) => {
    if (request.url().endsWith("/api/v1/transfers") && request.method() === "POST") allocations++;
  });
  for (const invalid of ["", "1.5", "2147483648"]) {
    await input.fill(invalid);
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await expect(limits.getByRole("alert")).toContainText("Enter a whole number");
    await page.getByRole("button", { name: /^(Send files|Retry upload)$/ }).click();
    await expect(page.getByRole("button", { name: "Retry upload", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Ready to share" })).toHaveCount(0);
  }
  expect(allocations).toBe(0);
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).uncheck();
  await expect(limits.getByRole("alert")).toHaveCount(0);
});

test("cancelling a started download refreshes exhaustion without retrying the payload", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Choose files").setInputFiles(files.slice(0, 1));
  await page.locator("summary").filter({ hasText: "Link limits" }).click();
  await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  await page.goto(await page.getByLabel("Full link").inputValue());
  let payloadRequests = 0;
  await page.route("**/api/v1/transfers/*/files/*", async (route) => {
    payloadRequests++;
    // The real server reserves the attempt; stop before delivering encrypted
    // bytes to the browser. The refresh must use its own control signal.
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    await page.getByRole("button", { name: "Cancel saving", exact: true }).click();
    await route.fulfill({ response }).catch(() => {});
  });
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  await expect(page.getByText("Download limit reached", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save file", exact: true })).toBeDisabled();
  expect(payloadRequests).toBe(1);
});

for (const exhausted of ["files", "bytes", "batches"] as const) {
  test(`receive opening distinguishes exhausted ${exhausted} allowance`, async ({
    page,
    context,
  }) => {
    await signIn(page);
    await page.getByRole("link", { name: "Receive", exact: true }).click();
    await page.getByRole("button", { name: "Create receive link", exact: true }).click();
    const link = await page.getByLabel("Full link").inputValue();
    const sender = await context.newPage();
    await sender.route("**/api/v1/slots/*/availability", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.available = false;
      if (exhausted === "files") {
        body.max_files = 1;
        body.remaining_files = 0;
      }
      if (exhausted === "bytes") body.remaining_bytes = 0;
      if (exhausted === "batches") body.remaining_transfers = 0;
      await route.fulfill({ response, json: body });
    });
    await sender.goto(link);
    await expect(sender.getByRole("alert")).toContainText(
      exhausted === "files"
        ? "file allowance"
        : exhausted === "bytes"
          ? "byte allowance"
          : "upload batch allowance",
    );
    await expect(sender.getByLabel("Choose files")).toHaveCount(0);
    await sender.close();
  });
}

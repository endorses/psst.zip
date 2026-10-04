import { test, expect, signIn } from "./auth-fixture";
import { createHash } from "node:crypto";
import { writeFile, rm } from "node:fs/promises";
import { createReadStream } from "node:fs";

test("a file over 100 MiB uploads in bounded frames and saves through OPFS with exact bytes", async ({
  page,
  adminRequest: request,
}, testInfo) => {
  test.setTimeout(120000);
  const size = 101 * 1024 * 1024 + 17,
    plaintext = Buffer.alloc(size, 0x5a);
  const response = await request.patch("/api/v1/admin/settings", {
    data: { max_file_size: 128 * 1024 * 1024 },
  });
  expect(response.ok()).toBe(true);
  await page.addInitScript(() => {
    Object.defineProperty(window, "showSaveFilePicker", { value: undefined });
    const original = Blob.prototype.arrayBuffer;
    (window as any).largestPlaintextRead = 0;
    Blob.prototype.arrayBuffer = function () {
      (window as any).largestPlaintextRead = Math.max(
        (window as any).largestPlaintextRead,
        this.size,
      );
      return original.call(this);
    };
  });
  await signIn(page);
  const uploadPath = testInfo.outputPath("large.bin");
  await writeFile(uploadPath, plaintext);
  await page.getByLabel("Choose files", { exact: true }).setInputFiles(uploadPath);
  await expect(page.getByText("Up to 128 MiB per file.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible({
    timeout: 90000,
  });
  expect(await page.evaluate(() => (window as any).largestPlaintextRead)).toBeLessThanOrEqual(
    4194304,
  );
  const link = await page.getByLabel("Full link").inputValue();
  expect(
    (await request.patch("/api/v1/admin/settings", { data: { max_file_size: 1024 * 1024 } })).ok(),
  ).toBe(true);
  await page.goto(link);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  const file = await download,
    path = (await file.path())!;
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  expect(hash.digest("hex")).toBe(createHash("sha256").update(plaintext).digest("hex"));
  await expect(page.getByText("Sender notified.", { exact: true })).toBeVisible();
  await request.patch("/api/v1/admin/settings", { data: { max_file_size: 25 * 1024 * 1024 } });
});

test("large HTTP save fallback explains capability instead of fetching a large blob", async ({
  page,
}) => {
  await page.addInitScript(() =>
    Object.defineProperty(window, "isSecureContext", { value: false }),
  );
  await page.goto("/");
  const result = await page.evaluate(async () => {
    const path = "/src/lib/file-save.ts";
    const { createSaveSink } = await import(path);
    try {
      await createSaveSink({
        name: "large",
        size: 101 * 1024 * 1024,
        mime_type: "application/octet-stream",
      });
      return "unexpected";
    } catch (error) {
      return (error as Error).message;
    }
  });
  expect(result).toContain("Large files need HTTPS");
});

test.afterEach(async ({}, testInfo) => {
  await rm(testInfo.outputPath("large.bin"), { force: true });
});

test("lost frame response resumes from confirmed tus offset without duplicate ciphertext", async ({
  page,
}) => {
  await signIn(page);
  let dropped = false,
    heads = 0;
  await page.route("**/api/v1/transfers/*/files/*", async (route) => {
    if (route.request().method() === "HEAD") heads++;
    if (route.request().method() === "PATCH" && !dropped) {
      dropped = true;
      const response = await route.fetch();
      expect(response.status()).toBe(204);
      await route.abort("connectionfailed");
    } else await route.continue();
  });
  const plaintext = Buffer.alloc(5 * 1024 * 1024 + 3, 0x31);
  await page
    .getByLabel("Choose files", { exact: true })
    .setInputFiles({ name: "resume.bin", mimeType: "application/octet-stream", buffer: plaintext });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
  expect(dropped).toBe(true);
  expect(heads).toBeGreaterThan(0);
  await page.goto(await page.getByLabel("Full link").inputValue());
  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  const hash = createHash("sha256");
  for await (const chunk of createReadStream((await (await downloaded).path())!))
    hash.update(chunk);
  expect(hash.digest("hex")).toBe(createHash("sha256").update(plaintext).digest("hex"));
});

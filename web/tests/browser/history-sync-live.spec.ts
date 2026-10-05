import { readFileSync } from "node:fs";
import { test, expect, signIn, credentials, retryAuth } from "./auth-fixture";
import { generateReceiveKeyPair } from "../../src/lib/receive-crypto.ts";

test("two clients reconcile real send, rename, exhaustion, receive and revoke without loading payloads in History", async ({
  page,
  browser,
  request,
}) => {
  page.setDefaultTimeout(10000);
  const observerContext = await browser.newContext({ baseURL: "http://127.0.0.1:4173" });
  try {
    const login = await retryAuth(() =>
      observerContext.request.post("/api/v1/auth/login", {
        headers: { Origin: "http://127.0.0.1:4173" },
        data: { ...credentials, session_type: "web", device_name: "History observer" },
      }),
    );
    expect(login.ok(), await login.text()).toBe(true);
    const observer = await observerContext.newPage();
    let snapshots = 0,
      feeds = 0,
      payloads = 0;
    observer.on("request", (r) => {
      if (r.url().includes("/api/v1/auth/resources?")) snapshots++;
      if (r.url().includes("/api/v1/auth/history/changes?")) feeds++;
      if (/\/api\/v1\/transfers\/[^/]+\/(manifest|files)/.test(r.url())) payloads++;
    });
    await observer.goto("/?view=history");
    await expect(observer.getByRole("heading", { name: "Your transfers" })).toBeVisible();
    await expect.poll(() => feeds).toBeGreaterThan(0);
    const title = `Sync send ${Date.now()}`;
    await signIn(page);
    await page.getByLabel("Choose files").setInputFiles({
      name: "sync.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("quiet sync"),
    });
    await page.locator("summary").filter({ hasText: "Link settings" }).click();
    await page.getByLabel("Link title").fill(title);
    await page.locator("summary").filter({ hasText: "Link limits" }).click();
    await page.getByRole("checkbox", { name: "Limit downloads per file" }).check();
    await page.getByRole("spinbutton", { name: "Limit downloads per file" }).fill("1");
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Ready to share" })).toBeVisible();
    const link = await page.getByLabel("Full link").inputValue();
    const transferId = new URL(link).pathname.split("/").at(-1)!;
    await expect(observer.locator(".resource").filter({ hasText: title })).toBeVisible({
      timeout: 15000,
    });
    const renamed = `${title} renamed`;
    expect(
      (
        await request.patch(`/api/v1/transfers/${transferId}/title`, { data: { title: renamed } })
      ).ok(),
    ).toBe(true);
    await expect(observer.locator(".resource").filter({ hasText: renamed })).toBeVisible({
      timeout: 15000,
    });
    await page.goto(link);
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    expect((await download).suggestedFilename()).toBe("sync.txt");
    await expect(observer.locator(".resource").filter({ hasText: renamed })).toContainText(
      "Download limit reached",
      { timeout: 15000 },
    );

    const pair = await generateReceiveKeyPair();
    const publicKey = Buffer.from(pair.publicKey).toString("base64url");
    const inboxTitle = `Sync inbox ${Date.now()}`;
    const created = await request.post("/api/v1/slots", {
      data: { receive_protocol: 2, recipient_public_key: publicKey, title: inboxTitle },
    });
    expect(created.ok(), await created.text()).toBe(true);
    const slot = await created.json();
    await page.goto(`/u/${slot.id}#v2.${publicKey}`);
    await page.getByLabel("Choose files").setInputFiles({
      name: "received.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("private received file"),
    });
    await page.getByRole("button", { name: "Send files", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Files sent" })).toBeVisible();
    await expect(observer.locator(".resource").filter({ hasText: inboxTitle })).toContainText(
      "1 file received",
      { timeout: 15000 },
    );
    expect((await request.delete(`/api/v1/slots/${slot.id}`)).ok()).toBe(true);
    await expect(observer.locator(".resource").filter({ hasText: inboxTitle })).toHaveCount(0, {
      timeout: 15000,
    });
    expect(payloads).toBe(0);
    expect(snapshots).toBe(1);

    if (process.env.PSST_TEST_STATE_FILE) {
      const state = JSON.parse(readFileSync(process.env.PSST_TEST_STATE_FILE, "utf8"));
      const before = await request.get("/api/v1/auth/resources?limit=1");
      const generation = (await before.json()).generation;
      process.kill(state.runnerPID, "SIGUSR2");
      await expect
        .poll(async () => {
          try {
            const reply = await request.get("/api/v1/auth/resources?limit=1");
            return reply.ok() ? (await reply.json()).generation : generation;
          } catch {
            return generation;
          }
        })
        .not.toBe(generation);
      await observer.getByRole("button", { name: "Refresh", exact: true }).click();
      await expect.poll(() => snapshots).toBe(2);
      await expect(observer.locator(".resource").filter({ hasText: renamed })).toBeVisible();
      expect(payloads).toBe(0);
    }

    await observer.route("**/api/v1/auth/history/changes?*", (route) => route.abort());
    await observer.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(observer.locator(".resource").filter({ hasText: renamed })).toBeVisible();
    await expect(observer.getByRole("button", { name: "Refresh", exact: true })).toBeEnabled();
    await observer.unroute("**/api/v1/auth/history/changes?*");
    expect((await request.delete(`/api/v1/transfers/${transferId}`)).ok()).toBe(true);
    await observer.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(observer.locator(".resource").filter({ hasText: renamed })).toHaveCount(0);
    expect(snapshots).toBe(process.env.PSST_TEST_STATE_FILE ? 2 : 1);
  } finally {
    await observerContext.close();
  }
});

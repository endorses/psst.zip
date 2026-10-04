import { test, expect, type Page } from "@playwright/test";
import { trafficPolicy, trafficSnapshot } from "../traffic-policy-fixture";
import { randomBytes, randomUUID } from "node:crypto";
import { encryptManifest } from "../../src/lib/crypto";
import { encryptFile, wireSize } from "../../src/lib/chunked-files";

async function admin(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "admin", role: "admin", username: "admin" } } }),
  );
  const totals = { uploaded_bytes: 0, downloaded_bytes: 0, total_bytes: 0 };
  await page.route("**/api/v1/admin/traffic", (route) =>
    route.fulfill({
      json: {
        recording_started_at: "2026-10-01T00:00:00Z",
        history_retained_from: "2025-08-31",
        history_retention_days: 400,
        updated_at: "2026-10-04T00:00:00Z",
        status: "ok",
        timezone: "UTC",
        settings: { allowance_bytes: 20 * 1024 ** 3, cycle_start_day: 1, basis: "outbound" },
        today: totals,
        month: totals,
        lifetime: totals,
        range: { from: "2026-10-01", to: "2026-10-04" },
        totals,
        days: [],
        cycle: {
          ...totals,
          start: "2026-10-01",
          end: "2026-11-01",
          counted_bytes: 0,
          remaining_bytes: 20 * 1024 ** 3,
        },
      },
    }),
  );
}

test("enforcement is separate from monitoring and persists accepted settings", async ({ page }) => {
  await admin(page);
  let policy = { ...trafficPolicy },
    writes = 0,
    monitorWrites = 0;
  await page.route("**/api/v1/admin/traffic/settings", (route) => {
    monitorWrites++;
    return route.fulfill({ json: {} });
  });
  await page.route("**/api/v1/admin/traffic-policy", (route) => {
    if (route.request().method() === "PATCH") {
      policy = route.request().postDataJSON();
      writes++;
    }
    return route.fulfill({ json: trafficSnapshot(policy) });
  });
  await page.goto("/?view=traffic");
  const section = page.getByRole("region", { name: "Transfer traffic enforcement" });
  await expect(
    section.getByLabel("Enforce transfer traffic budget", { exact: true }),
  ).not.toBeChecked();
  await expect(page.getByLabel("Allowance (GiB, optional)")).toHaveValue("20");
  await section.getByLabel("Enforce transfer traffic budget", { exact: true }).check();
  await section.getByLabel("Server traffic budget (GiB)", { exact: true }).fill("50");
  await section.getByLabel("Default account traffic budget (GiB)", { exact: true }).fill("3");
  await section.getByLabel("Count toward enforced budget").selectOption("combined");
  await section.getByLabel("Enforced cycle starts on day (UTC)").fill("31");
  await section.getByLabel("Upload bandwidth (MiB/s)").fill("2");
  await section.getByLabel("Active streams per account", { exact: true }).fill("2");
  await section.getByRole("button", { name: "Save transfer traffic policy", exact: true }).click();
  await expect(section.getByRole("status")).toContainText("Transfer traffic policy saved");
  expect(policy).toMatchObject({
    enforcement_enabled: true,
    server_budget_bytes: 50 * 1024 ** 3,
    default_account_budget_bytes: 3 * 1024 ** 3,
    basis: "combined",
    cycle_start_day: 31,
    upload_bytes_per_second: 2 * 1024 ** 2,
    max_streams_per_account: 2,
  });
  expect([writes, monitorWrites]).toEqual([1, 0]);
  await page.reload();
  await expect(
    section.getByLabel("Enforce transfer traffic budget", { exact: true }),
  ).toBeChecked();
  await expect(page.getByLabel("Allowance (GiB, optional)")).toHaveValue("20");
  await expect(section).toContainText("cannot cap or predict the provider's bill");
  await page.setViewportSize({ width: 320, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({
    path: test.info().outputPath("traffic-budget-phone.png"),
    fullPage: true,
  });
});

test("exhaustion keeps recovery and failed-save drafts usable", async ({ page }) => {
  await admin(page);
  const snapshot = trafficSnapshot({ ...trafficPolicy, enforcement_enabled: true });
  snapshot.state = "exhausted";
  snapshot.usage.remaining_bytes = 0;
  snapshot.usage.charged_bytes = snapshot.usage.budget_bytes;
  await page.route("**/api/v1/admin/traffic-policy", (route) =>
    route.request().method() === "PATCH"
      ? route.fulfill({ status: 503, json: { error: "Policy store unavailable" } })
      : route.fulfill({ json: snapshot }),
  );
  await page.goto("/?view=traffic");
  const section = page.getByRole("region", { name: "Transfer traffic enforcement" });
  await expect(section).toContainText("traffic budget is exhausted");
  await section.getByLabel("Server traffic budget (GiB)", { exact: true }).fill("150");
  await section.getByRole("button", { name: "Save transfer traffic policy", exact: true }).click();
  await expect(section.getByRole("alert")).toContainText("Unsaved values remain");
  await expect(section.getByLabel("Server traffic budget (GiB)", { exact: true })).toHaveValue(
    "150",
  );
  await expect(page.getByRole("link", { name: "Users", exact: true })).toBeEnabled();
  await expect(page.getByRole("link", { name: "Server settings", exact: true })).toBeEnabled();
});

test("account overrides load lazily and null restores inheritance", async ({ page }) => {
  await admin(page);
  await page.route("**/api/v1/admin/users?*", (route) =>
    route.fulfill({
      json: {
        users: [
          { id: "member", username: "Member", role: "user", disabled: false },
          { id: "other", username: "Other", role: "user", disabled: false },
        ],
        next_cursor: null,
      },
    }),
  );
  let reads = 0,
    override: number | null = null;
  const writes: (number | null)[] = [];
  await page.route("**/api/v1/admin/users/*/traffic-policy", (route) => {
    expect(route.request().url()).toContain("/users/member/");
    if (route.request().method() === "PATCH") {
      override = route.request().postDataJSON().account_budget_bytes;
      writes.push(override);
    } else reads++;
    return route.fulfill({
      json: {
        ...trafficSnapshot(),
        account_budget_bytes: override,
        effective_budget_bytes: override ?? trafficPolicy.default_account_budget_bytes,
      },
    });
  });
  await page.goto("/?view=users");
  await expect(page.getByText("Traffic budget for Member", { exact: true })).toBeVisible();
  expect(reads).toBe(0);
  await page.getByText("Traffic budget for Member", { exact: true }).click();
  const details = page
    .locator("details")
    .filter({ has: page.getByText("Traffic budget for Member", { exact: true }) });
  await expect(
    details.getByLabel("Use the server's default account budget", { exact: false }),
  ).toBeChecked();
  await details.getByLabel("Use the server's default account budget", { exact: false }).uncheck();
  await details.getByLabel("Account traffic budget (GiB)", { exact: true }).fill("5");
  await details.getByRole("button", { name: "Save account traffic budget", exact: true }).click();
  await expect(details.getByRole("status")).toContainText("Account traffic budget saved");
  await details.getByLabel("Use the server's default account budget", { exact: false }).check();
  await details.getByRole("button", { name: "Save account traffic budget", exact: true }).click();
  await expect.poll(() => writes).toEqual([5 * 1024 ** 3, null]);
  expect(reads).toBe(1);
});

test("own usage is one lazy scoped request without administrator policy", async ({ page }) => {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: "member", username: "Member", role: "user" } } }),
  );
  let reads = 0,
    adminReads = 0;
  await page.route("**/api/v1/admin/**", (route) => {
    adminReads++;
    return route.fulfill({ status: 403 });
  });
  await page.route("**/api/v1/auth/usage", (route) => route.fulfill({ status: 503 }));
  await page.route("**/api/v1/auth/traffic-usage", (route) => {
    reads++;
    return route.fulfill({
      json: trafficSnapshot({ ...trafficPolicy, enforcement_enabled: true }),
    });
  });
  await page.goto("/?view=settings");
  await page.getByText("Account traffic budget and usage", { exact: true }).click();
  await expect(page.getByText("Budget enforcement is on.", { exact: false })).toBeVisible();
  expect([reads, adminReads]).toEqual([1, 0]);
});

for (const code of [
  "traffic_budget_exhausted",
  "traffic_accounting_unavailable",
  "traffic_policy_changed",
]) {
  test(`${code} public save preserves earlier saved files and never retries bytes`, async ({
    page,
  }) => {
    const id = randomUUID(),
      key = new Uint8Array(randomBytes(32)),
      blobs = [randomUUID(), randomUUID()],
      encryptionIds = ["12".repeat(16), "34".repeat(16)];
    const manifest = new Uint8Array(
      await encryptManifest(key, {
        files: blobs.map((blob_id, i) => ({
          name: `file-${i + 1}.txt`,
          size: 3,
          mime_type: "text/plain",
          blob_id,
          encoding: "chunked-v1" as const,
          chunk_size: 4194304 as const,
          encryption_id: encryptionIds[i],
        })),
      }),
    );
    const chunks: Uint8Array[] = [];
    for await (const frame of encryptFile(
      key,
      new Blob(["one"]),
      encryptionIds[0],
      new AbortController().signal,
    ))
      chunks.push(new Uint8Array(frame));
    const bytes = Buffer.concat(chunks);
    let payloads = 0,
      auth = 0;
    await page.route("**/api/v1/auth/me", (route) => {
      auth++;
      return route.fulfill({ status: 401 });
    });
    await page.route(`**/api/v1/transfers/${id}`, (route) =>
      route.fulfill({
        json: {
          id,
          status: "complete",
          file_count: 2,
          total_size: 2 * wireSize(3),
          expires_at: "2099-01-01T00:00:00Z",
          downloaded_at: null,
        },
      }),
    );
    await page.route(`**/api/v1/transfers/${id}/manifest`, (route) =>
      route.fulfill({ body: Buffer.from(manifest), contentType: "application/octet-stream" }),
    );
    await page.route(`**/api/v1/transfers/${id}/files/*`, (route) => {
      payloads++;
      return route.request().url().endsWith(blobs[0])
        ? route.fulfill({ body: bytes, contentType: "application/octet-stream" })
        : route.fulfill({ status: 429, json: { code, retry_at: "2026-11-01T00:00:00Z" } });
    });
    await page.goto(`/d/${id}#${Buffer.from(key).toString("base64url")}`);
    const saveButtons = page.locator(".file-list li button");
    await expect(saveButtons).toHaveCount(2);
    const download = page.waitForEvent("download");
    await saveButtons.nth(0).click();
    await download;
    await saveButtons.nth(1).click();
    await expect(page.getByRole("alert")).toContainText(
      code === "traffic_policy_changed"
        ? "Server transfer limits changed"
        : code === "traffic_budget_exhausted"
          ? "traffic budget is exhausted"
          : "Traffic accounting is unavailable",
    );
    await expect(page.getByRole("button", { name: "Save again", exact: true })).toBeVisible();
    expect([payloads, auth]).toEqual([2, 0]);
  });
}

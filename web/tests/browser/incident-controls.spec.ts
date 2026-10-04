import { test, expect, type Page } from "@playwright/test";
import { randomBytes, randomUUID } from "node:crypto";
import { encryptManifest } from "../../src/lib/crypto";
import { wireSize } from "../../src/lib/chunked-files";

async function session(page: Page, role: "admin" | "user" = "admin") {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: { id: `incident-${role}`, username: role, role } } }),
  );
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({
      json: {
        max_file_size: 25 * 1024 ** 2,
        max_file_size_ceiling: 1024 ** 4,
        public_transfers_paused: false,
      },
    }),
  );
  await page.route("**/api/v1/admin/resource-policy", (route) =>
    route.fulfill({ status: 503, json: { error: "Resource policy unavailable in fixture" } }),
  );
}

test("pause and resume require explicit scope confirmation and reload persistent state", async ({
  page,
}) => {
  await session(page);
  let paused = false;
  const changes: boolean[] = [];
  await page.route("**/api/v1/admin/incident-state", (route) => {
    if (route.request().method() === "PATCH") {
      paused = route.request().postDataJSON().public_transfers_paused;
      changes.push(paused);
    }
    return route.fulfill({
      json: { public_transfers_paused: paused, updated_at: "2026-10-04T00:00:00Z" },
    });
  });
  await page.goto("/?view=server");
  const controls = page.getByRole("region", { name: "Public transfer control" });
  await controls.getByRole("button", { name: "Pause public transfers", exact: true }).click();
  const confirmation = page.getByRole("dialog");
  await expect(confirmation).toContainText("including active streams");
  await expect(confirmation).toContainText("Downloaded copies cannot be recalled");
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(changes).toEqual([]);
  await controls.getByRole("button", { name: "Pause public transfers", exact: true }).click();
  await confirmation.getByRole("button", { name: "Pause public transfers", exact: true }).click();
  await expect(controls).toContainText("Public transfers are paused");
  await page.reload();
  await expect(controls).toContainText("Public transfers are paused");
  await expect(page.getByLabel("Maximum file size (MiB)")).toBeEnabled();
  await controls.getByRole("button", { name: "Resume public transfers", exact: true }).click();
  await expect(confirmation).toContainText("Revoked links stay revoked");
  await confirmation.getByRole("button", { name: "Resume public transfers", exact: true }).click();
  await expect(controls).toContainText("Public transfers are enabled");
  expect(changes).toEqual([true, false]);
});

test("ordinary login disable and account incident shutdown show and send distinct scopes", async ({
  page,
}) => {
  await session(page);
  let member = { id: "member-id", username: "Incident member", role: "user", disabled: false };
  let disables = 0,
    shutdowns = 0;
  await page.route("**/api/v1/admin/users?*", (route) =>
    route.fulfill({ json: { users: [member], next_cursor: null } }),
  );
  await page.route("**/api/v1/admin/users/member-id", (route) => {
    expect(route.request().method()).toBe("PATCH");
    expect(route.request().postDataJSON()).toEqual({ disabled: true });
    disables++;
    member = { ...member, disabled: true };
    return route.fulfill({ json: { user: member } });
  });
  await page.route("**/api/v1/admin/users/member-id/shutdown", (route) => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postDataJSON()).toEqual({});
    shutdowns++;
    return route.fulfill({
      json: {
        user: member,
        revoked_sessions: 2,
        revoked_pairings: 1,
        revoked_transfers: 4,
        revoked_slots: 3,
        cleanup_pending: true,
      },
    });
  });
  await page.goto("/?view=users");
  const memberRow = page.locator("article").filter({ hasText: "Incident member" });
  await memberRow.getByRole("button", { name: "Disable sign-in", exact: true }).click();
  const confirmation = page.getByRole("dialog");
  await expect(confirmation).toContainText(
    "Existing public receive and download links will keep working",
  );
  await confirmation.getByRole("button", { name: "Disable sign-in only", exact: true }).click();
  await expect(
    memberRow.getByRole("button", { name: "Enable sign-in", exact: true }),
  ).toBeVisible();
  expect([disables, shutdowns]).toEqual([1, 0]);
  await memberRow.getByRole("button", { name: "Incident shutdown", exact: true }).click();
  await expect(confirmation).toContainText("revoke every existing send and receive link");
  await expect(confirmation).toContainText("Already downloaded copies remain");
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(shutdowns).toBe(0);
  await memberRow.getByRole("button", { name: "Incident shutdown", exact: true }).click();
  await confirmation
    .getByRole("button", { name: "Shut down account and revoke all links", exact: true })
    .click();
  await expect(page.getByRole("status")).toContainText("4 transfers and 3 receive links revoked");
  await expect(page.getByRole("status")).toContainText("cleanup is pending");
  expect([disables, shutdowns]).toEqual([1, 1]);
});

test("a paused public download stays loginless and makes only the user-requested payload attempt", async ({
  page,
}) => {
  const id = randomUUID(),
    file = randomUUID(),
    key = new Uint8Array(randomBytes(32));
  const manifest = new Uint8Array(
    await encryptManifest(key, {
      files: [
        {
          name: "tiny.txt",
          size: 3,
          mime_type: "text/plain",
          blob_id: file,
          encoding: "chunked-v1",
          chunk_size: 4194304,
          encryption_id: "12".repeat(16),
        },
      ],
    }),
  );
  let payloads = 0,
    identityReads = 0;
  await page.route("**/api/v1/auth/me", (route) => {
    identityReads++;
    return route.fulfill({ status: 401, json: {} });
  });
  await page.route(`**/api/v1/transfers/${id}`, (route) =>
    route.fulfill({
      json: {
        id,
        file_count: 1,
        total_size: wireSize(3),
        expires_at: "2030-01-01T00:00:00Z",
        files: [{ id: file, download_count: 0, remaining_downloads: 2 }],
        max_downloads: 2,
      },
    }),
  );
  await page.route(`**/api/v1/transfers/${id}/manifest`, (route) =>
    route.fulfill({ body: Buffer.from(manifest), contentType: "application/octet-stream" }),
  );
  await page.route(`**/api/v1/transfers/${id}/files/${file}`, (route) => {
    payloads++;
    return route.fulfill({ status: 503, json: { code: "public_transfers_paused" } });
  });
  await page.goto(`/d/${id}#${Buffer.from(key).toString("base64url")}`);
  await expect(page.getByRole("heading", { name: "Save files", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Save file", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Public transfers are paused");
  await expect(page.getByRole("alert")).toContainText("retry manually");
  expect(payloads).toBe(1);
  expect(identityReads).toBe(0);
});

test("creation rejected during pause never starts a file upload or automatically retries", async ({
  page,
}) => {
  await session(page, "user");
  let creations = 0,
    files = 0;
  await page.route("**/api/v1/transfers", (route) => {
    creations++;
    return route.fulfill({ status: 503, json: { code: "public_transfers_paused" } });
  });
  page.on("request", (request) => {
    if (request.url().endsWith("/files") && request.method() === "POST") files++;
  });
  await page.goto("/");
  await page
    .getByLabel("Choose files")
    .setInputFiles({ name: "tiny.txt", mimeType: "text/plain", buffer: Buffer.from("x") });
  await page.getByRole("button", { name: "Send files", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Public transfers are paused");
  expect([creations, files]).toEqual([1, 0]);
});

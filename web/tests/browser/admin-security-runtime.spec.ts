import { test, expect, authenticate, retryAuth, authDelay } from "./auth-fixture";
import { createHmac } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { execFileSync } from "node:child_process";
import type { Page } from "@playwright/test";

// Declare the opt-in before fixture setup, so the default suite performs no
// extra authentication or administrative mutations for this lifecycle test.
test.skip(
  !process.env.PSST_TEST_STATE_FILE,
  "Opt in with a private temporary state marker for disposable database timestamp aging.",
);

function authenticatorCode(secret: string): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let buffer = 0,
    bits = 0;
  const bytes: number[] = [];
  for (const letter of secret) {
    const value = alphabet.indexOf(letter);
    if (value < 0) throw new Error("Unexpected test enrollment alphabet");
    buffer = (buffer << 5) | value;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >> bits) & 255);
    }
  }
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const hash = createHmac("sha1", Buffer.from(bytes)).update(counter).digest(),
    offset = hash[19] & 15;
  return String((hash.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, "0");
}
function ageDisposableSession(session: string) {
  const statePath = process.env.PSST_TEST_STATE_FILE;
  if (!statePath)
    throw new Error("PSST_TEST_STATE_FILE is required for this explicit disposable test");
  const state = JSON.parse(readFileSync(statePath, "utf8"));
  const directory = realpathSync(state.directory),
    db = realpathSync(state.db);
  if (
    dirname(directory) !== realpathSync(tmpdir()) ||
    !basename(directory).startsWith("psst-browser-") ||
    db !== join(directory, "psst.db")
  )
    throw new Error("Refusing to modify anything except the harness-owned temporary database");
  execFileSync("python3", [
    "-c",
    "import sqlite3,sys\nwith sqlite3.connect(sys.argv[1]) as db:\n result=db.execute('UPDATE sessions SET recent_until=0 WHERE id=?',(sys.argv[2],))\n assert result.rowcount==1",
    db,
    session,
  ]);
}
async function factorSignIn(page: Page, username: string, password: string, recovery: string) {
  let submissions = 0;
  while (submissions < 8) {
    await page.getByLabel("Username", { exact: true }).fill(username);
    await page.getByLabel("Password", { exact: true }).fill(password);
    const required = page.waitForResponse((r) => r.url().endsWith("/auth/login"));
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    submissions++;
    const challenge = await required;
    if (challenge.status() === 429) {
      await authDelay(challenge.headers());
      continue;
    }
    expect(challenge.status()).toBe(401);
    await page.getByRole("button", { name: "Use a recovery code" }).click();
    while (submissions < 8) {
      // Refill after each rejection: the UI deliberately clears submitted proof.
      await page.getByLabel("Recovery code", { exact: true }).fill(recovery);
      // Pace explicit test clicks; production never retries authentication itself.
      await authDelay({ "retry-after": "5" });
      const verified = page.waitForResponse((r) => r.url().endsWith("/auth/login"));
      await page.getByRole("button", { name: "Verify and sign in" }).click();
      submissions++;
      const response = await verified;
      if (response.status() === 429) {
        await authDelay(response.headers());
        // Persistent administrator cooldown preserves the factor prompt. The
        // general IP limiter can instead return the UI to password sign-in.
        if (await page.getByLabel("Recovery code", { exact: true }).isVisible()) continue;
        break;
      }
      expect(response.ok()).toBe(true);
      await expect(page.getByRole("navigation", { name: "Account navigation" })).toBeVisible();
      return;
    }
  }
  throw new Error("Disposable authentication rate limit did not recover");
}
async function acknowledgeCodes(page: Page) {
  await expect(page.getByRole("button", { name: "Continue to sign in" })).toBeDisabled();
  await page.getByLabel("I have saved these recovery codes securely.").check();
  await page.getByRole("button", { name: "Continue to sign in" }).click();
}

test("real disposable enrollment, factor login, expired recent proof, recovery rotation and disabling revoke sessions", async ({
  page,
  adminRequest,
  playwright,
  baseURL,
}) => {
  test.setTimeout(90000);
  const username = `factor-${Date.now()}`,
    password = "Disposable-factor-password-2026";
  expect(
    (
      await adminRequest.post("/api/v1/admin/users", {
        data: { username, password, role: "admin" },
      })
    ).status(),
  ).toBe(201);
  const previous = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Origin: baseURL! },
  });
  const priorLogin = await retryAuth(() =>
    previous.post("/api/v1/auth/login", {
      data: {
        username,
        password,
        session_type: "web",
        device_name: "Previous disposable session",
      },
    }),
  );
  expect(priorLogin.ok()).toBe(true);
  const priorToken = (await previous.storageState()).cookies[0]?.value;
  expect(priorToken).toBeTruthy();
  await page.goto("/?view=account");
  await authenticate(page, { username, password });
  await expect(
    page.getByText("Your administrator account has no second factor.", { exact: true }),
  ).toBeVisible();
  const setupResponse = page.waitForResponse((r) => r.url().endsWith("/admin/security/enrollment"));
  await page.getByRole("button", { name: "Set up authenticator", exact: true }).click();
  expect((await setupResponse).status()).toBe(201);
  const key = await page.getByLabel("Manual setup key", { exact: true }).inputValue();
  await page.getByLabel("Authenticator setup code").fill(authenticatorCode(key));
  const confirmation = page.waitForResponse((r) =>
    r.url().endsWith("/admin/security/enrollment/confirm"),
  );
  await page.getByRole("button", { name: "Confirm authenticator", exact: true }).click();
  const confirmed = await confirmation;
  expect(confirmed.ok()).toBe(true);
  const codes: string[] = (await confirmed.json()).recovery_codes;
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  expect(
    (
      await previous.get("/api/v1/auth/me", { headers: { Authorization: `Bearer ${priorToken}` } })
    ).status(),
  ).toBe(401);
  expect((await page.request.get("/api/v1/auth/me")).status()).toBe(401);
  await page.waitForTimeout(3300);
  await expect(page.locator(".codes li")).toHaveCount(10);
  await acknowledgeCodes(page);
  await factorSignIn(page, username, password, codes[0]);
  const identity = await (await page.request.get("/api/v1/auth/me")).json();
  ageDisposableSession(identity.session_id);
  await page.getByRole("link", { name: "Server settings", exact: true }).click();
  await page.getByLabel("Maximum file size (MiB)").fill("30");
  let mutations = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/admin/settings") && r.method() === "PATCH") mutations++;
  });
  const blocked = page.waitForResponse((r) => r.url().endsWith("/admin/settings"));
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  expect((await blocked).status()).toBe(403);
  const dialog = page.getByRole("dialog", { name: "Confirm administrator identity" });
  await dialog.getByLabel("Administrator password").fill(password);
  await dialog.getByRole("button", { name: "Use a recovery code" }).click();
  await dialog.getByLabel("Recovery code", { exact: true }).fill(codes[1]);
  const proof = page.waitForResponse((r) => r.url().endsWith("/admin/security/reauth"));
  await dialog.getByRole("button", { name: "Confirm identity", exact: true }).click();
  expect((await proof).ok()).toBe(true);
  await expect(dialog).toHaveCount(0);
  expect(mutations).toBe(1);
  await expect(page.getByLabel("Maximum file size (MiB)")).toHaveValue("30");
  const saved = page.waitForResponse((r) => r.url().endsWith("/admin/settings"));
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  expect((await saved).ok()).toBe(true);
  expect(mutations).toBe(2);
  await page.getByRole("link", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Regenerate recovery codes", exact: true }).click();
  const regenerated = page.waitForResponse((r) =>
    r.url().endsWith("/admin/security/recovery-codes"),
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Replace codes and sign out" })
    .click();
  const response = await regenerated;
  expect(response.ok()).toBe(true);
  const replacement: string[] = (await response.json()).recovery_codes;
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  expect((await page.request.get("/api/v1/auth/me")).status()).toBe(401);
  const oldCode = await retryAuth(() =>
    previous.post("/api/v1/auth/login", {
      data: { username, password, recovery_code: codes[9], session_type: "web" },
    }),
  );
  expect(oldCode.status()).toBe(401);
  expect((await oldCode.json()).code).toBe("administrator_factor_invalid");
  await acknowledgeCodes(page);
  await factorSignIn(page, username, password, replacement[0]);
  await page.getByRole("button", { name: "Disable authenticator", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Disable authenticator and sign out" })
    .click();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await authenticate(page, { username, password });
  await expect(
    page.getByText("Your administrator account has no second factor.", { exact: true }),
  ).toBeVisible();
  await previous.dispose();
});

test.describe.serial("worker administrator proof refresh", () => {
  test("an expired worker proof rejects account creation", async ({ adminRequest }) => {
    const identityResponse = await adminRequest.get("/api/v1/auth/me");
    expect(identityResponse.ok()).toBe(true);
    ageDisposableSession((await identityResponse.json()).session_id);
    const rejected = await adminRequest.post("/api/v1/admin/users", {
      data: {
        username: `expired-proof-${Date.now()}`,
        password: "Disposable-member-password-2026",
        role: "user",
      },
    });
    expect(rejected.status()).toBe(403);
    expect((await rejected.json()).code).toBe("recent_authentication_required");
  });

  test("the next setup explicitly refreshes the same worker proof", async ({ adminRequest }) => {
    const status = await adminRequest.get("/api/v1/admin/security");
    expect(status.ok()).toBe(true);
    expect(Date.parse((await status.json()).recent_until)).toBeGreaterThan(
      Date.parse(status.headers().date),
    );
    const created = await adminRequest.post("/api/v1/admin/users", {
      data: {
        username: `fresh-proof-${Date.now()}`,
        password: "Disposable-member-password-2026",
        role: "user",
      },
    });
    expect(created.status()).toBe(201);
  });
});

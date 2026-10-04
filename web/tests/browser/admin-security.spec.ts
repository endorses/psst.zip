import { test, expect, type Page, type Route } from "@playwright/test";
const administrator = {
  id: "security-admin",
  username: "Security admin",
  role: "admin",
  disabled: false,
};
const secret = "JBSWY3DPEHPK3PXP";
const codes = Array.from({ length: 10 }, (_, i) => `disposable_recovery_${i}`);
const status = (enabled = false) => ({
  enabled,
  recovery_codes_remaining: enabled ? 10 : 0,
  recent_until: "2030-01-01T00:00:00Z",
});
const enrollment = () => ({
  secret,
  otpauth_url: `otpauth://totp/psst.zip:test?secret=${secret}&issuer=psst.zip`,
  expires_at: new Date(Date.now() + 300000).toISOString(),
});
async function config(page: Page) {
  await page.route("**/api/v1/config", (route) =>
    route.fulfill({ json: { max_file_size: 25 * 1024 ** 2, max_file_size_ceiling: 1024 ** 4 } }),
  );
  await page.route("**/api/v1/admin/incident-state", (route) =>
    route.fulfill({ json: { public_transfers_paused: false, updated_at: "2026-10-04T00:00:00Z" } }),
  );
  await page.route("**/api/v1/admin/resource-policy", (route) =>
    route.fulfill({ status: 503, json: { error: "Fixture resource policy unavailable" } }),
  );
}
async function signedIn(page: Page, enabled = false) {
  await config(page);
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { user: administrator, session_id: "session" } }),
  );
  await page.route("**/api/v1/admin/security", (route) => route.fulfill({ json: status(enabled) }));
}
async function begin(page: Page) {
  await page.goto("/?view=account");
  await page.getByRole("button", { name: "Set up authenticator", exact: true }).click();
  await expect(page.getByLabel("Manual setup key", { exact: true })).toHaveValue(secret);
}

test("administrator sign-in requires a factor; wrong codes keep the prompt and recovery completes it", async ({
  page,
}) => {
  await config(page);
  let authenticated = false,
    attempts = 0;
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill(authenticated ? { json: { user: administrator } } : { status: 401, json: {} }),
  );
  await page.route("**/api/v1/auth/status", (route) =>
    route.fulfill({ json: { setup_required: false } }),
  );
  await page.route("**/api/v1/admin/security", (route) => route.fulfill({ json: status(true) }));
  await page.route("**/api/v1/auth/login", (route) => {
    attempts++;
    const body = route.request().postDataJSON();
    expect(body.password === "Disposable-password-2026").toBe(true);
    if (body.recovery_code === codes[0]) {
      authenticated = true;
      return route.fulfill({ json: { user: administrator } });
    }
    return route.fulfill({
      status: 401,
      json: { code: body.code ? "administrator_factor_invalid" : "administrator_factor_required" },
    });
  });
  await page.goto("/?view=account");
  await page.getByLabel("Username", { exact: true }).fill("Security admin");
  await page.getByLabel("Password", { exact: true }).fill("Disposable-password-2026");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Administrator verification" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Account navigation" })).toHaveCount(0);
  await page.getByLabel("Authenticator code", { exact: true }).fill("000000");
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByRole("alert")).toContainText("not accepted");
  await page.getByRole("button", { name: "Use a recovery code" }).click();
  await page.getByLabel("Recovery code", { exact: true }).fill(codes[0]);
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByRole("heading", { name: "Administrator account security" })).toBeVisible();
  expect(attempts).toBe(3);
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(stored.includes(codes[0]) || stored.includes("Disposable-password-2026")).toBe(false);
});

test("enrollment uses local QR and one-time recovery codes survive revocation until acknowledged", async ({
  page,
}) => {
  await signedIn(page);
  let revoked = false;
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill(revoked ? { status: 401, json: {} } : { json: { user: administrator } }),
  );
  await page.route("**/api/v1/admin/security/enrollment", (route) =>
    route.fulfill({ status: 201, json: enrollment() }),
  );
  await page.route("**/api/v1/admin/security/enrollment/confirm", (route) => {
    revoked = true;
    return route.fulfill({ json: { recovery_codes: codes, reauthentication_required: true } });
  });
  await begin(page);
  await expect(page.getByRole("img", { name: "Authenticator enrollment QR code" })).toHaveAttribute(
    "src",
    /^data:image\/png;base64,/,
  );
  const decoded = await page
    .getByRole("img", { name: "Authenticator enrollment QR code" })
    .evaluate(async (element: HTMLImageElement) => {
      const decoderPath = "/node_modules/qr-scanner/qr-scanner.min.js";
      const { default: Scanner } = await import(decoderPath);
      delete (window as any).BarcodeDetector;
      return (await Scanner.scanImage(element.src, { returnDetailedScanResult: true })).data;
    });
  expect(decoded).toBe(enrollment().otpauth_url);
  await page.getByLabel("Authenticator setup code").fill("123456");
  await page.getByRole("button", { name: "Confirm authenticator", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  await page.waitForTimeout(3300);
  await expect(page.locator(".codes li")).toHaveCount(10);
  await expect(page.getByRole("button", { name: "Continue to sign in" })).toBeDisabled();
  expect(
    (await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).includes(
      codes[0],
    ),
  ).toBe(false);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Save recovery codes as text" }).click();
  await download;
  await page.getByLabel("I have saved these recovery codes securely.").check();
  await page.getByRole("button", { name: "Continue to sign in" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await expect(page.locator(".codes li")).toHaveCount(0);
});

test("an in-flight401 identity poll cannot discard a delayed successful enrollment response", async ({
  page,
}) => {
  await signedIn(page);
  let identityReads = 0;
  let pollRoute: Route | undefined;
  let heldConfirm: Route | undefined;
  await page.route("**/api/v1/auth/me", (route) => {
    identityReads++;
    if (identityReads === 1) return route.fulfill({ json: { user: administrator } });
    pollRoute = route;
  });
  await page.route("**/api/v1/admin/security/enrollment", (route) =>
    route.fulfill({ status: 201, json: enrollment() }),
  );
  await page.route("**/api/v1/admin/security/enrollment/confirm", (route) => {
    heldConfirm = route;
  });
  await begin(page);
  await expect.poll(() => !!pollRoute).toBe(true);
  await page.getByLabel("Authenticator setup code").fill("123456");
  await page.getByRole("button", { name: "Confirm authenticator", exact: true }).click();
  await expect.poll(() => !!heldConfirm).toBe(true);
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toBeDisabled();
  await page.getByRole("link", { name: "Server settings", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Administrator account security" })).toBeVisible();
  await pollRoute!.fulfill({ status: 401, json: {} });
  await expect(page.getByRole("heading", { name: "Administrator account security" })).toBeVisible();
  await heldConfirm!.fulfill({ json: { recovery_codes: codes, reauthentication_required: true } });
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toBeVisible();
  await expect(page.locator(".codes li")).toHaveCount(10);
});

test("expired enrollment cannot confirm and cancellation discards its setup secret", async ({
  page,
}) => {
  await signedIn(page);
  let canceled = 0,
    confirms = 0;
  await page.route("**/api/v1/admin/security/enrollment", (route) => {
    if (route.request().method() === "DELETE") {
      canceled++;
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({
      status: 201,
      json: { ...enrollment(), expires_at: new Date(Date.now() - 1000).toISOString() },
    });
  });
  await page.route("**/api/v1/admin/security/enrollment/confirm", (route) => {
    confirms++;
    return route.fulfill({ status: 410, json: { code: "enrollment_expired" } });
  });
  await begin(page);
  await expect(
    page.getByRole("button", { name: "Confirm authenticator", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Cancel authenticator setup" }).click();
  await expect(page.getByLabel("Manual setup key")).toHaveCount(0);
  expect(canceled).toBe(1);
  expect(confirms).toBe(0);
});

test("recent authentication preserves draft, wrong factor preserves session, and proof never replays mutation", async ({
  page,
}) => {
  await signedIn(page, true);
  let writes = 0,
    verified = false,
    proofs = 0;
  await page.route("**/api/v1/admin/settings", (route) => {
    writes++;
    return route.fulfill(
      verified
        ? { json: { max_file_size: 64 * 1024 ** 2 } }
        : { status: 403, json: { code: "recent_authentication_required" } },
    );
  });
  await page.route("**/api/v1/admin/security/reauth", (route) => {
    proofs++;
    if (proofs === 1)
      return route.fulfill({ status: 401, json: { code: "administrator_factor_invalid" } });
    verified = true;
    return route.fulfill({ json: { recent_until: "2030-01-01T00:00:00Z" } });
  });
  await page.goto("/?view=server");
  await page.getByLabel("Maximum file size (MiB)").fill("64");
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Confirm administrator identity" });
  await dialog.getByLabel("Administrator password").fill("Disposable-password-2026");
  await dialog.getByLabel("Authenticator code", { exact: true }).fill("000000");
  await dialog.getByRole("button", { name: "Confirm identity", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("not accepted");
  await expect(page.getByRole("heading", { name: "Server settings", exact: true })).toBeVisible();
  await dialog.getByLabel("Administrator password").fill("Disposable-password-2026");
  await dialog.getByRole("button", { name: "Use a recovery code" }).click();
  await dialog.getByLabel("Recovery code", { exact: true }).fill(codes[1]);
  await dialog.getByRole("button", { name: "Confirm identity", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(writes).toBe(1);
  await expect(page.getByLabel("Maximum file size (MiB)")).toHaveValue("64");
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  await expect.poll(() => writes).toBe(2);
});

test("canceling recent authentication clears proof and keeps the unsaved action", async ({
  page,
}) => {
  await signedIn(page);
  let proofs = 0,
    writes = 0;
  await page.route("**/api/v1/admin/settings", (route) => {
    writes++;
    return route.fulfill({ status: 403, json: { code: "recent_authentication_required" } });
  });
  await page.route("**/api/v1/admin/security/reauth", (route) => {
    proofs++;
    return route.fulfill({ status: 204 });
  });
  await page.goto("/?view=server");
  await page.getByLabel("Maximum file size (MiB)").fill("75");
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  await page.getByRole("dialog").getByLabel("Administrator password").fill("Discard-this-proof");
  await page.getByRole("button", { name: "Cancel verification" }).click();
  await expect(page.getByLabel("Maximum file size (MiB)")).toHaveValue("75");
  await page.getByRole("button", { name: "Save file limit", exact: true }).click();
  await expect(page.getByRole("dialog").getByLabel("Administrator password")).toHaveValue("");
  expect([writes, proofs]).toEqual([2, 0]);
});

test("an identity change discards enrollment proof and ignores its late recovery response", async ({
  page,
}) => {
  await signedIn(page);
  let held: Route | undefined;
  await page.route("**/api/v1/admin/security/enrollment", (route) =>
    route.fulfill({ status: 201, json: enrollment() }),
  );
  await page.route("**/api/v1/admin/security/enrollment/confirm", (route) => {
    held = route;
  });
  await begin(page);
  await page.getByLabel("Authenticator setup code").fill("123456");
  await page.getByRole("button", { name: "Confirm authenticator", exact: true }).click();
  await expect.poll(() => !!held).toBe(true);
  await page.evaluate(() =>
    window.dispatchEvent(
      new StorageEvent("storage", { key: "psst.auth-change", newValue: "changed" }),
    ),
  );
  await expect(page.getByRole("heading", { name: "Sign in to psst.zip" })).toBeVisible();
  await held!.fulfill({ json: { recovery_codes: codes, reauthentication_required: true } });
  await expect(page.getByRole("heading", { name: "Save your recovery codes" })).toHaveCount(0);
  await expect(page.getByLabel("Manual setup key", { exact: true })).toHaveCount(0);
  expect(
    (await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).includes(
      secret,
    ),
  ).toBe(false);
});

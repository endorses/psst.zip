import assert from "node:assert/strict";
import { test } from "node:test";
import {
  validateAdministratorSecurity,
  validateRecentAuthentication,
  validateEnrollment,
  validateRecoveryCodes,
  securityIdentityChanged,
  onRecentAuthenticationRequired,
} from "../src/lib/admin-security.ts";
import { accountRequest, AccountError } from "../src/lib/account.ts";

const secret = "JBSWY3DPEHPK3PXP";
const enrollment = {
  secret,
  otpauth_url: `otpauth://totp/psst.zip:test?secret=${secret}&issuer=psst.zip`,
  expires_at: "2030-01-01T00:00:00Z",
};
test("authenticator setup validates manual/QR agreement and bounded secrets", () => {
  assert.equal(validateEnrollment(enrollment).secret, secret);
  assert.throws(() =>
    validateEnrollment({ ...enrollment, otpauth_url: "https://example.test/qr?secret=" + secret }),
  );
  assert.throws(() =>
    validateEnrollment({
      ...enrollment,
      otpauth_url: enrollment.otpauth_url.replace(secret, "AAAAAAAAAAAAAAAA"),
    }),
  );
  assert.throws(() => validateEnrollment({ ...enrollment, secret: "x".repeat(10000) }));
  assert.throws(() => validateEnrollment({ ...enrollment, expires_at: "unknown" }));
});
test("security status and one-time recovery response fail closed on malformed state", () => {
  assert.equal(
    validateAdministratorSecurity({
      enabled: false,
      recovery_codes_remaining: 0,
      recent_until: null,
    }).enabled,
    false,
  );
  assert.throws(() =>
    validateAdministratorSecurity({
      enabled: "false",
      recovery_codes_remaining: 0,
      recent_until: null,
    }),
  );
  assert.throws(() => validateRecentAuthentication({ recent_until: "invalid" }));
  assert.equal(
    validateRecentAuthentication({ recent_until: "2030-01-01T00:00:00Z" }).recent_until,
    "2030-01-01T00:00:00Z",
  );
  const codes = Array.from({ length: 10 }, (_, i) => `test-code-${i}`);
  assert.deepEqual(
    validateRecoveryCodes({ recovery_codes: codes, reauthentication_required: true }),
    codes,
  );
  assert.throws(() =>
    validateRecoveryCodes({ recovery_codes: codes, reauthentication_required: false }),
  );
  assert.throws(() =>
    validateRecoveryCodes({
      recovery_codes: Array(10).fill("duplicate-code"),
      reauthentication_required: true,
    }),
  );
});
test("recent authentication rejection notifies once without replay or retaining an action", async () => {
  const previous = globalThis.fetch,
    previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", { configurable: true, value: new EventTarget() });
  let requests = 0,
    notifications = 0;
  const stop = onRecentAuthenticationRequired(() => notifications++);
  try {
    globalThis.fetch = async () => {
      requests++;
      return Response.json({ code: "recent_authentication_required" }, { status: 403 });
    };
    await assert.rejects(
      accountRequest("/admin/settings", "PATCH", { max_file_size: 123 }),
      (error) => error instanceof AccountError && error.code === "recent_authentication_required",
    );
    assert.equal(requests, 1);
    assert.equal(notifications, 1);
    globalThis.fetch = async () => {
      requests++;
      securityIdentityChanged();
      return Response.json({ code: "recent_authentication_required" }, { status: 403 });
    };
    await assert.rejects(accountRequest("/admin/settings", "PATCH", { max_file_size: 123 }));
    assert.equal(
      notifications,
      1,
      "a response from the previous identity must not open verification",
    );
    globalThis.fetch = async () =>
      Response.json({ code: "administrator_factor_invalid" }, { status: 401 });
    await assert.rejects(
      accountRequest("/admin/security/reauth", "POST", { password: "test", code: "000000" }),
      (error) => error instanceof AccountError && error.code === "administrator_factor_invalid",
    );
    assert.equal(notifications, 1);
  } finally {
    stop();
    globalThis.fetch = previous;
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});

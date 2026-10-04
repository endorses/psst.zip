export interface AdministratorSecurity {
  enabled: boolean;
  recovery_codes_remaining: number;
  recent_until: string | null;
}
export interface Enrollment {
  secret: string;
  otpauth_url: string;
  expires_at: string;
}
const utc = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 64 &&
  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(value) &&
  Number.isFinite(Date.parse(value));
export function validateAdministratorSecurity(value: unknown): AdministratorSecurity {
  const status = value as AdministratorSecurity;
  if (
    !status ||
    typeof status.enabled !== "boolean" ||
    !Number.isSafeInteger(status.recovery_codes_remaining) ||
    status.recovery_codes_remaining < 0 ||
    status.recovery_codes_remaining > 10 ||
    (status.recent_until !== null && !utc(status.recent_until))
  )
    throw new Error("The server returned unsupported administrator security settings.");
  return status;
}
export function validateRecentAuthentication(value: unknown): { recent_until: string } {
  const result = value as { recent_until: unknown };
  if (!result || !utc(result.recent_until))
    throw new Error(
      "The server did not confirm recent authentication. Review your session and try again.",
    );
  return { recent_until: result.recent_until };
}
export function validateEnrollment(value: unknown): Enrollment {
  const enrollment = value as Enrollment;
  if (
    !enrollment ||
    typeof enrollment.secret !== "string" ||
    !/^[A-Z2-7]{16,128}$/.test(enrollment.secret) ||
    typeof enrollment.otpauth_url !== "string" ||
    enrollment.otpauth_url.length > 4096 ||
    !utc(enrollment.expires_at)
  )
    throw new Error("The server returned unsupported authenticator enrollment details.");
  let url: URL;
  try {
    url = new URL(enrollment.otpauth_url);
  } catch {
    throw new Error("The server returned an invalid authenticator URI.");
  }
  if (
    url.protocol !== "otpauth:" ||
    url.hostname !== "totp" ||
    url.searchParams.get("secret") !== enrollment.secret
  )
    throw new Error("The authenticator QR code does not match the manual secret.");
  return enrollment;
}
export function validateRecoveryCodes(value: unknown): string[] {
  const result = value as { recovery_codes: unknown; reauthentication_required: unknown };
  if (
    !result ||
    result.reauthentication_required !== true ||
    !Array.isArray(result.recovery_codes) ||
    result.recovery_codes.length !== 10 ||
    result.recovery_codes.some(
      (code) => typeof code !== "string" || !/^[A-Za-z0-9_-]{8,128}$/.test(code),
    ) ||
    new Set(result.recovery_codes).size !== 10
  )
    throw new Error(
      "Recovery codes were not returned in a supported format. Sign in again and regenerate them before relying on recovery.",
    );
  return [...result.recovery_codes];
}
export function administratorSecurityMessage(code: unknown): string | null {
  switch (code) {
    case "recent_authentication_required":
      return "Confirm your administrator identity, then submit this action again. Your unsaved values remain here.";
    case "administrator_factor_required":
      return "Enter an authenticator code or a recovery code to finish administrator sign-in.";
    case "administrator_factor_invalid":
      return "That authenticator or recovery code was not accepted. Check the code and try again.";
    case "administrator_authentication_invalid":
      return "The administrator password was not accepted. Check it and try again.";
    case "enrollment_expired":
      return "This enrollment expired. Cancel it and start again.";
    case "enrollment_pending":
      return "An enrollment is already pending. Cancel it in the browser where it began, or wait for it to expire.";
    case "administrator_authentication_locked":
      return "Too many administrator verification attempts. Wait before trying again.";
    default:
      return null;
  }
}
const recentEvent = "psst:recent-administrator-authentication";
let identityGeneration = 0;
export function securityIdentityChanged() {
  identityGeneration++;
}
export function securityIdentityGeneration() {
  return identityGeneration;
}
export function recentAuthenticationRequired(generation: number) {
  if (generation === identityGeneration && typeof window !== "undefined")
    window.dispatchEvent(new Event(recentEvent));
}
export function onRecentAuthenticationRequired(listener: () => void): () => void {
  window.addEventListener(recentEvent, listener);
  return () => window.removeEventListener(recentEvent, listener);
}

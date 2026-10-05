import { message as m, LocalizedError, type DisplayText } from "./i18n/index.ts";
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
    throw new LocalizedError(m("theServerReturnedUnsupportedAdministratorSecuritySettings"));
  return status;
}
export function validateRecentAuthentication(value: unknown): { recent_until: string } {
  const result = value as { recent_until: unknown };
  if (!result || !utc(result.recent_until))
    throw new LocalizedError(m("theServerDidNotConfirmRecentAuthenticationReviewYour"));
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
    throw new LocalizedError(m("theServerReturnedUnsupportedAuthenticatorEnrollmentDetails"));
  let url: URL;
  try {
    url = new URL(enrollment.otpauth_url);
  } catch {
    throw new LocalizedError(m("theServerReturnedAnInvalidAuthenticatorURI"));
  }
  if (
    url.protocol !== "otpauth:" ||
    url.hostname !== "totp" ||
    url.searchParams.get("secret") !== enrollment.secret
  )
    throw new LocalizedError(m("theAuthenticatorQRCodeDoesNotMatchTheManual"));
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
    throw new LocalizedError(m("recoveryCodesWereNotReturnedInASupportedFormat"));
  return [...result.recovery_codes];
}
export function administratorSecurityMessage(code: unknown): DisplayText | null {
  switch (code) {
    case "recent_authentication_required":
      return m("confirmYourAdministratorIdentityThenSubmitThisActionAgain");
    case "administrator_factor_required":
      return m("enterAnAuthenticatorCodeOrARecoveryCodeTo");
    case "administrator_factor_invalid":
      return m("thatAuthenticatorOrRecoveryCodeWasNotAcceptedCheck");
    case "administrator_authentication_invalid":
      return m("theAdministratorPasswordWasNotAcceptedCheckItAnd");
    case "enrollment_expired":
      return m("thisEnrollmentExpiredCancelItAndStartAgain");
    case "enrollment_pending":
      return m("anEnrollmentIsAlreadyPendingCancelItInThe");
    case "administrator_authentication_locked":
      return m("tooManyAdministratorVerificationAttemptsWaitBeforeTryingAgain");
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

import { LocalizedError, message as m, type DisplayText } from "./i18n/messages.ts";
export class ApiError extends LocalizedError {
  readonly status: number;
  readonly code: string | undefined;
  constructor(status: number, code?: string) {
    super(apiErrorMessage(status, code));
    this.status = status;
    this.code = code;
  }
}
export function hasStatus(error: unknown, ...statuses: number[]): boolean {
  return (
    error instanceof Error &&
    "status" in error &&
    typeof error.status === "number" &&
    statuses.includes(error.status)
  );
}
export function hasCode(error: unknown, code: string): boolean {
  return error instanceof Error && "code" in error && error.code === code;
}
export function apiErrorMessage(status: number, code?: string): DisplayText {
  switch (code) {
    case "invalid_credentials":
      return m("apiInvalidCredentials");
    case "https_required":
      return m("apiHttpsRequired");
    case "same_origin_required":
      return m("apiSameOriginRequired");
    case "admin_required":
      return m("apiAdminRequired");
    case "account_changed":
      return m("apiAccountChanged");
    case "pairing_invalid":
      return m("apiPairingInvalid");
    case "pairing_already_connected":
      return m("apiPairingConnected");
    case "invalid_username_or_role":
      return m("apiInvalidUsername");
    case "username_conflict":
      return m("apiUsernameConflict");
    case "incorrect_password":
      return m("apiIncorrectPassword");
    case "password_reused":
      return m("apiPasswordReused");
    case "invalid_password":
      return m("apiInvalidPassword");
    case "last_admin_required":
      return m("apiLastAdminRequired");
    case "download_limit":
      return m("thisLinkHasReachedItsDownloadLimitAskThe");
    case "receive_file_limit":
      return m("thisLinkCannotAcceptMoreFilesItsFileAllowance");
  }
  if (status === 401) return m("apiAuthenticationRequired");
  if (status === 403) return m("apiPermissionDenied");
  if (status === 404 || status === 410) return m("apiResourceUnavailable");
  if (status === 409) return m("apiConflict");
  if (status === 413) return m("apiPayloadTooLarge");
  if (status === 429) return m("apiRateLimited");
  if (status === 408 || status === 504) return m("apiTimeout");
  if (status >= 500) return m("apiUnavailable");
  return m("apiInvalidRequest");
}

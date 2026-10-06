import Foundation

#if canImport(Shared)
    import Shared
#endif

/// Only classified native/shared failures are presentable. Unknown platform/server prose stays private.
extension L10n {
    static func failure(_ error: Error, fallback: String) -> String {
        if error is CancellationError {
            return fallback
        }
        if let incident = TransferIncident.from(error) {
            return incident.errorDescription ?? fallback
        }
        switch error {
        case let value as LocalUploadFailure:
            switch value {
            case .invalidSelection:
                return
                    "Choose up to 100 readable files within this server’s per-file limit. Your previous selection is unchanged."
            case .fileTooLarge(let limitBytes):
                guard limitBytes > 0, limitBytes <= LocalUploadFailure.maximumFileBytes else { return fallback }
                return typedFormat("Each file must be no larger than %@.", .byteCount(limitBytes, binary: true))
            }
        case let value as AccountError: return value.errorDescription ?? fallback
        case let value as SharedLinkTitle.Failure: return value.errorDescription ?? fallback
        case let value as LinkLimitError: return value.errorDescription ?? fallback
        case let value as ShareSelectionError: return value.message
        case let value as GuestUploadSelectionError: return value.errorDescription ?? fallback
        case let value as ReceiveSafetyError: return value.errorDescription ?? fallback
        default: break
        }
        #if canImport(Shared)
            if let exception = (error as NSError).kotlinException as? KotlinThrowable,
                let description = FailureDescriptions.shared.describe(error: exception)
            {
                return failureCode(description.code, arguments: description.arguments)
            }
        #endif
        return fallback
    }

    static func failureCode(_ code: String, arguments: [String: String] = [:]) -> String {
        if code == "traffic_budget_exhausted" {
            return TransferIncident.budget(retryAt: TransferIncident.retryDate(arguments["retry_at"])).errorDescription
                ?? "A transfer traffic budget has been reached."
        }
        switch code {
        case "authentication_required": return "Sign in again to continue."
        case "password_change_required": return "Replace your temporary password before continuing."
        case "admin_transfer_forbidden":
            return
                "Administrator accounts manage the server on the website. Sign in with a regular account to transfer files. You can still scan public links without signing in."
        case "resource_revoked", "download_limit_reached", "download_limit", "link_expired":
            return
                "This link is no longer available. Ask the sender for a new link. Files already saved on this device are still available."
        case "invalid_credentials", "incorrect_password": return "Check your username and password, then try again."
        case "invalid_password", "password_reused":
            return "Check your current password. The new password must differ and contain 12–72 UTF-8 bytes."
        case "pairing_invalid":
            return "This login code is invalid, expired, or already used. Generate a new code on the website."
        case "authentication_rate_limited": return "Too many sign-in attempts. Wait a moment and try again."
        case "pairing_server_invalid", "invalid_server_url":
            return "Enter a server origin such as https://files.example.com, without a path."
        case "server_api_unsupported", "server_web_missing", "authentication_unsupported":
            return "This server does not support this operation. Ask its administrator to update it."
        case "public_transfers_paused": return TransferIncident.paused.errorDescription!
        case "traffic_accounting_unavailable": return TransferIncident.accountingUnavailable.errorDescription!
        case "traffic_policy_changed": return TransferIncident.policyChanged.errorDescription!
        case "receive_capacity_unavailable", "receive_capacity_stale":
            return "Receive capacity could not be checked. Refresh and try again; your files are still selected."
        case "receive_capacity_exhausted", "receive_file_limit", "receive_batch_limit", "resource_limit",
            "disk_capacity", "retention_limit":
            return "This receive link cannot accept these files right now. Remove files or refresh and try again."
        case "receive_selection_file_limit", "selection_file_limit":
            return "Too many files for the remaining receive capacity. Remove files or refresh and try again."
        case "receive_selection_byte_limit", "selection_too_large", "transfer_file_limit_exceeded":
            return "The selected files exceed the remaining receive capacity. Remove files or refresh and try again."
        case "invalid_upload_selection":
            return
                "Choose up to 100 readable files within this server’s per-file limit. Your previous selection is unchanged."
        case "invalid_link_policy", "unsupported_manifest", "legacy_receive_disabled":
            return
                "This server does not support the requested private receive protocol or link limit. Ask its administrator to update it before creating this link."
        case "invalid_link_title": return "Use at most 200 characters without control characters for the shared title."
        case "deletion_denied", "deletion_unsupported", "deletion_failed", "deletion_timeout":
            return
                "Could not revoke this link. Its history entry has been kept. Reconnect or sign in again, then retry."
        case "password_change_failed": return "Could not change your password. Reconnect and retry."
        case "signout_failed": return "Could not revoke your session. Reconnect and retry signing out."
        default: return "Could not connect. Check your network and server address, then retry."
        }
    }
}

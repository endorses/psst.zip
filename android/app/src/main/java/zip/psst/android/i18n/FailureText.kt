package zip.psst.android.i18n

import zip.psst.android.R
import zip.psst.shared.api.FailureDescriptions

/** Present only documented semantic failure codes; never arbitrary remote error bodies. */
fun failureText(error: Throwable): UiText {
    if (error is UiFailureException) return error.display
    val description = FailureDescriptions.describe(error)
    if (description?.code == "traffic_budget_exhausted") {
        val retry =
            description.arguments["retry_at"]?.let {
                runCatching { java.time.Instant.parse(it).toEpochMilli() }.getOrNull()
            }
        if (retry != null) return message(R.string.failure_budget_retry_at, UiRetryTime(retry))
    }
    return message(
        when (description?.code) {
            "authentication_required" -> R.string.failure_authentication_required
            "password_change_required" -> R.string.failure_password_change_required
            "admin_transfer_forbidden" -> R.string.failure_admin_transfer_forbidden
            "public_transfers_paused" -> R.string.failure_public_transfers_paused
            "resource_revoked" -> R.string.failure_resource_revoked
            "traffic_budget_exhausted" -> R.string.failure_traffic_budget_exhausted
            "traffic_accounting_unavailable" -> R.string.failure_traffic_accounting_unavailable
            "traffic_policy_changed" -> R.string.failure_traffic_policy_changed
            "download_interrupted" -> R.string.failure_download_interrupted
            "deletion_unsupported" -> R.string.failure_deletion_unsupported
            "deletion_denied" -> R.string.failure_deletion_denied
            "deletion_failed" -> R.string.failure_deletion_failed
            "deletion_timeout" -> R.string.failure_deletion_timeout
            "invalid_credentials" -> R.string.failure_invalid_credentials
            "pairing_invalid" -> R.string.failure_pairing_invalid
            "authentication_rate_limited" -> R.string.failure_authentication_rate_limited
            "authentication_unsupported" -> R.string.failure_authentication_unsupported
            "authentication_unavailable" -> R.string.failure_authentication_unavailable
            "incorrect_password" -> R.string.failure_incorrect_password
            "invalid_password" -> R.string.failure_invalid_password
            "password_reused" -> R.string.failure_password_reused
            "password_change_failed" -> R.string.failure_password_change_failed
            "signout_failed" -> R.string.failure_signout_failed
            "pairing_server_invalid" -> R.string.failure_pairing_server_invalid
            "invalid_server_url" -> R.string.failure_invalid_server_url
            "server_api_unsupported" -> R.string.failure_server_api_unsupported
            "server_web_missing" -> R.string.failure_server_web_missing
            "server_validation_timeout" -> R.string.failure_server_validation_timeout
            "server_validation_failed" -> R.string.failure_server_validation_failed
            "invalid_link_title" -> R.string.failure_invalid_link_title
            "upload_request_failed" -> R.string.failure_upload_request_failed
            "invalid_session" -> R.string.failure_invalid_session
            "server_endpoint_failed" -> R.string.failure_server_endpoint_failed
            "server_redirect" -> R.string.failure_server_redirect
            "server_content_type" -> R.string.failure_server_content_type
            "server_response_too_large" -> R.string.failure_server_response_too_large
            "receive_capacity_unavailable" -> R.string.failure_receive_capacity_unavailable
            "receive_capacity_stale" -> R.string.failure_receive_capacity_stale
            "receive_capacity_exhausted" -> R.string.failure_receive_capacity_exhausted
            "receive_selection_file_limit" -> R.string.failure_receive_selection_file_limit
            "receive_selection_byte_limit" -> R.string.failure_receive_selection_byte_limit
            "invalid_upload_selection" -> R.string.failure_invalid_upload_selection
            "selection_file_limit" -> R.string.failure_selection_file_limit
            "selection_too_large" -> R.string.failure_selection_too_large
            "download_limit" -> R.string.failure_download_limit
            "link_expired" -> R.string.failure_link_expired
            "receive_file_limit" -> R.string.failure_receive_file_limit
            "receive_batch_limit" -> R.string.failure_receive_batch_limit
            "resource_limit" -> R.string.failure_resource_limit
            "disk_capacity" -> R.string.failure_disk_capacity
            "retention_limit" -> R.string.failure_retention_limit
            "legacy_receive_disabled" -> R.string.failure_legacy_receive_disabled
            "invalid_link_policy" -> R.string.failure_invalid_link_policy
            "unsupported_manifest" -> R.string.failure_unsupported_manifest
            "transfer_file_limit_exceeded" -> R.string.failure_transfer_file_limit_exceeded
            "https_required" -> R.string.failure_https_required
            "same_origin_required" -> R.string.failure_same_origin_required
            "pairing_already_connected" -> R.string.failure_pairing_already_connected
            else -> R.string.failure_unknown
        }
    )
}

fun combinedMessages(values: List<UiText>): UiText =
    values.reduce { left, right -> message(R.string.message_pair, left, right) }

package zip.psst.shared.api

import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsChannel
import io.ktor.utils.io.readAvailable
import kotlin.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

class AuthenticationRequiredException :
    IllegalArgumentException(
        "Your session has expired or no longer has permission. Sign in again under Server Configuration."
    )

class PasswordChangeRequiredException :
    IllegalArgumentException(
        "Change your temporary password under Server Configuration before continuing."
    )

class AdminTransferForbiddenException :
    IllegalArgumentException(
        "Administrator accounts manage the server. Sign in with a regular account to transfer files."
    )

/** Operator actions must not be mistaken for an expired login or retried automatically. */
open class TransferPolicyException(message: String, val title: String = "Transfer unavailable") :
    IllegalArgumentException(message)

class PublicTransfersPausedException :
    TransferPolicyException(
        "The server administrator has paused file transfers. Your saved files are safe. Retry after the administrator resumes transfers.",
        "Transfers paused",
    )

class ResourceRevokedException :
    TransferPolicyException(
        "This link is no longer available. Ask the sender for a new link. Files already saved on this device are still available.",
        "Link unavailable",
    )

/** A validated UTC timestamp is safe to show; untrusted prose is never reflected. */
internal fun normalizedTrafficRetryAt(value: String?): String? {
    if (value == null || value.length !in 20..40 || !value.endsWith("Z")) return null
    return try {
        Instant.parse(value).toString()
    } catch (_: IllegalArgumentException) {
        null
    }
}

class TrafficBudgetExhaustedException(retryAt: String? = null) :
    TransferPolicyException(
        "The server or account transfer traffic budget has been reached. Saved files are kept. " +
            (normalizedTrafficRetryAt(retryAt)?.let {
                "Retry after $it (UTC), when the billing cycle resets, or contact the administrator."
            } ?: "Retry after the next billing cycle or contact the administrator."),
        "Traffic budget reached",
    ) {
    val retryAt: String? = normalizedTrafficRetryAt(retryAt)
}

class TrafficAccountingUnavailableException :
    TransferPolicyException(
        "The server cannot safely account for transfer traffic right now. Saved files are kept. Retry later or contact the administrator.",
        "Transfers temporarily unavailable",
    )

class TrafficPolicyChangedException :
    TransferPolicyException(
        "The server's traffic limits changed during this transfer. Saved files are kept. Retry manually to continue under the new limits.",
        "Traffic limits changed",
    )

internal suspend fun HttpResponse.checkAccountRestriction() {
    if (status.value !in listOf(403, 409, 410, 429, 503)) return
    val headerCode = headers["X-Psst-Error-Code"]?.takeIf { it.length <= 64 }
    val headerRetryAt = normalizedTrafficRetryAt(headers["X-Psst-Retry-At"])
    val errorBody =
        if (
            headerCode == null ||
                (headerCode == "traffic_budget_exhausted" && headerRetryAt == null)
        ) {
            readPolicyErrorBody()
        } else null
    val code =
        headerCode
            ?: try {
                errorBody?.get("code")?.jsonPrimitive?.content
            } catch (_: IllegalArgumentException) {
                null
            }
    when (code) {
        "traffic_policy_changed" -> if (status.value == 409) throw TrafficPolicyChangedException()
        "password_change_required" ->
            if (status.value == 403) throw PasswordChangeRequiredException()
        "admin_transfer_forbidden" ->
            if (status.value == 403) throw AdminTransferForbiddenException()
        "public_transfers_paused" -> if (status.value == 503) throw PublicTransfersPausedException()
        "resource_revoked" -> if (status.value == 410) throw ResourceRevokedException()
        "traffic_budget_exhausted" ->
            if (status.value == 429) {
                val bodyRetryAt =
                    try {
                        errorBody?.get("retry_at")?.jsonPrimitive?.content
                    } catch (_: IllegalArgumentException) {
                        null
                    }
                throw TrafficBudgetExhaustedException(
                    headerRetryAt ?: normalizedTrafficRetryAt(bodyRetryAt)
                )
            }
        "traffic_accounting_unavailable" ->
            if (status.value == 503) throw TrafficAccountingUnavailableException()
    }
}

private suspend fun HttpResponse.readPolicyErrorBody(): JsonObject? =
    try {
        val channel = bodyAsChannel()
        val bytes = ByteArray(4097)
        var total = 0
        while (total < bytes.size) {
            val count = channel.readAvailable(bytes, total, bytes.size - total)
            if (count == -1) break
            total += count
        }
        if (total > 4096) null
        else Json.parseToJsonElement(bytes.copyOf(total).decodeToString()).jsonObject
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (_: Exception) {
        null
    }

/**
 * Safe messages for revoked/expired sessions; never include request credentials or response bodies.
 */
internal suspend fun HttpResponse.checkAuthenticatedWrite() {
    checkAccountRestriction()
    if (status.value in listOf(401, 403)) throw AuthenticationRequiredException()
    require(status.value in 200..299) {
        "The server rejected the upload request (HTTP ${status.value})."
    }
}

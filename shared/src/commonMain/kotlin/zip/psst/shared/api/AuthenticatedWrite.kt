package zip.psst.shared.api

import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsChannel
import io.ktor.utils.io.readAvailable
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
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

internal suspend fun HttpResponse.checkAccountRestriction() {
    if (status.value != 403) return
    val code =
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
            else
                Json.parseToJsonElement(bytes.copyOf(total).decodeToString())
                    .jsonObject["code"]
                    ?.jsonPrimitive
                    ?.content
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            null
        }
    when (code) {
        "password_change_required" -> throw PasswordChangeRequiredException()
        "admin_transfer_forbidden" -> throw AdminTransferForbiddenException()
    }
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

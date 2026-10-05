package zip.psst.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.prepareDelete
import kotlinx.coroutines.withTimeoutOrNull

/** Safe, actionable failure text; never includes the owner token or server response body. */
class LinkDeletionException(
    message: String,
    val statusCode: Int? = null,
    code: String =
        when (statusCode) {
            405 -> "deletion_unsupported"
            401,
            403 -> "deletion_denied"
            else -> "deletion_failed"
        },
) : Exception(message), ClientFailure {
    override val failureCode: String = code
    override val failureArguments: Map<String, String> =
        statusCode?.let { mapOf("status" to it.toString()) } ?: emptyMap()
}

internal suspend fun deleteLink(httpClient: HttpClient, url: String, deleteToken: String?) {
    val finished =
        withTimeoutOrNull(10_000L) {
            httpClient
                .prepareDelete(url) {
                    expectSuccess = false
                    if (deleteToken != null) bearerAuth(deleteToken)
                }
                .execute { response ->
                    when (response.status.value) {
                        204,
                        404 -> Unit
                        405 ->
                            throw LinkDeletionException(
                                "This server does not support revoking links. Update the server and retry.",
                                405,
                            )
                        401,
                        403 ->
                            throw LinkDeletionException(
                                "The server refused to revoke this link. Check deletion permissions with the server administrator and retry.",
                                response.status.value,
                            )
                        else ->
                            throw LinkDeletionException(
                                "The server could not revoke this link (${response.status.value}). Please retry.",
                                response.status.value,
                            )
                    }
                }
            true
        }
    if (finished == null)
        throw LinkDeletionException(
            "Revoking the link timed out. Check the connection and retry.",
            code = "deletion_timeout",
        )
}

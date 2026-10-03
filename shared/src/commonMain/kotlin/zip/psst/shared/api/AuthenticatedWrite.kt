package zip.psst.shared.api

import io.ktor.client.statement.HttpResponse

class AuthenticationRequiredException :
    IllegalArgumentException(
        "Your session has expired or no longer has permission. Sign in again under Server Configuration."
    )

/**
 * Safe messages for revoked/expired sessions; never include request credentials or response bodies.
 */
internal fun HttpResponse.checkAuthenticatedWrite() {
    if (status.value in listOf(401, 403)) throw AuthenticationRequiredException()
    require(status.value in 200..299) {
        "The server rejected the upload request (HTTP ${status.value})."
    }
}

package zip.psst.shared.api

import zip.psst.shared.model.AbuseContact
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.headersOf
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext

class AbuseContactConfigTest {
    @Test
    fun optionalContactUsesExactPublicOriginAndNeverAccountCredentials() = runTest {
        withContext(Dispatchers.Default) {
            for ((field, expected) in
                listOf(
                    "" to "",
                    ",\"abuse_contact_email\":\"operator@example.com\"" to "operator@example.com",
                    ",\"abuse_contact_email\":\"a@example.com?bcc=bad@example.com\"" to
                        "a@example.com?bcc=bad@example.com",
                )) {
                val http =
                    HttpClient(
                        MockEngine { request ->
                            assertEquals(
                                "https://other.example:8443/api/v1/config",
                                request.url.toString(),
                            )
                            assertNull(request.headers[HttpHeaders.Authorization])
                            assertNull(request.headers[HttpHeaders.Cookie])
                            respond(
                                """{"max_file_size":26214400,"max_file_size_ceiling":5368709120$field}""",
                                headers = headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        }
                    ) {
                        applyClientPolicy()
                    }
                // Even an account-bearing API never authorizes this public config operation.
                val api =
                    ApiClient(ServerConfig("https://other.example:8443"), http, "account-secret")
                try {
                    val contact = api.limits.get().abuseContactEmail
                    assertEquals(expected, contact)
                    if (contact.isEmpty() || '?' in contact)
                        assertNull(AbuseContact.normalize(contact))
                } finally {
                    api.close()
                }
            }
        }
    }
}

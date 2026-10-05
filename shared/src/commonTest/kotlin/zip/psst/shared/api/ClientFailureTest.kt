package zip.psst.shared.api

import kotlin.test.*
import kotlinx.coroutines.CancellationException
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.client.request.get
import kotlinx.coroutines.test.runTest

class ClientFailureTest {
    @Test
    fun limitErrorsRemainPolicyFailuresRegardlessOfDiagnosticLanguage() = runTest {
        for ((status, code) in listOf(403 to "receive_file_limit", 410 to "download_limit", 507 to "disk_capacity")) {
            val client = HttpClient(MockEngine {
                respond("untrusted private diagnostic", HttpStatusCode.fromValue(status), headersOf("X-Psst-Error-Code", code))
            })
            try {
                val error = assertFailsWith<TransferPolicyException> {
                    client.get("https://server.test").checkAuthenticatedWrite()
                }
                assertEquals(code, FailureDescriptions.describe(error)?.code)
                assertFalse(error.message.orEmpty().contains("private"))
                assertFalse(TrafficFailureClassifier.shouldProbe(error))
            } finally { client.close() }
        }
    }

    @Test
    fun diagnosticLanguageDoesNotDeterminePresentation() {
        for (diagnostic in listOf("invalid credentials", "Anmeldedaten ungültig", "private token")) {
            val failure = ClientStateFailureException(diagnostic, "invalid_credentials")
            assertEquals("invalid_credentials", FailureDescriptions.describe(failure)?.code)
            assertEquals(emptyMap(), FailureDescriptions.describe(failure)?.arguments)
        }
        assertNull(FailureDescriptions.describe(IllegalArgumentException("invalid credentials")))
    }

    @Test
    fun boundedArgumentsAreCopiedAndValidated() {
        val arguments = mutableMapOf("count" to "100")
        val descriptor = FailureDescription("selection_file_limit", arguments)
        arguments["count"] = "999"
        assertEquals("100", descriptor.arguments["count"])
        for (invalid in listOf("", "invalid code", "x".repeat(65))) {
            assertFailsWith<IllegalArgumentException> { FailureDescription(invalid) }
        }
        for (invalid in listOf(mapOf("token\n" to "x"), mapOf("count" to "x".repeat(257)), mapOf("count" to "1\n2"), (0..8).associate { "arg$it" to "x" })) {
            assertFailsWith<IllegalArgumentException> { FailureDescription("invalid_request", invalid) }
        }
    }

    @Test
    fun cancellationAndUnknownCausesStayUnpresented() {
        assertNull(FailureDescriptions.describe(CancellationException("cancelled")))
        assertNull(FailureDescriptions.describe(Exception("wrapper", CancellationException("cancelled"))))
        val failure = ClientFailureException("private diagnostic", "invalid_request")
        assertEquals("invalid_request", FailureDescriptions.describe(Exception("wrapper", failure))?.code)
        var nested: Throwable = failure
        repeat(4) { nested = Exception("wrapper", nested) }
        assertNull(FailureDescriptions.describe(nested))
    }

    @Test
    fun policyMetadataKeepsOnlySafeRetryTime() {
        val retry = FailureDescriptions.describe(TrafficBudgetExhaustedException("2026-11-01T00:00:00Z"))!!
        assertEquals("traffic_budget_exhausted", retry.code)
        assertEquals("2026-11-01T00:00:00Z", retry.arguments["retry_at"])
        assertTrue(FailureDescriptions.describe(TrafficBudgetExhaustedException("private\nsecret"))!!.arguments.isEmpty())
    }
}

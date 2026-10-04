package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.*

class InboxPageTest {
    private val slot = "11111111-1111-1111-1111-111111111111"
    private val child = "22222222-2222-2222-2222-222222222222"

    private fun body(
        children: String = "",
        next: String = "null",
        summary: String =
            """{"state":"ready","completed_files":200,"file_count":300,"total_size":4000}""",
    ) =
        """{"id":"$slot","receive_protocol":2,"recipient_public_key":"CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","max_files":0,"reserved_files":0,"remaining_files":null,"paginated":true,"next_cursor":$next,"summary":$summary,"transfers":[$children]}"""

    private val item
        get() = """{"transfer_id":"$child","status":"complete","file_count":2}"""

    private fun decode(raw: String, after: String? = null) =
        decodeInboxPage(Json.parseToJsonElement(raw).jsonObject, slot, after, 50)

    @Test
    fun partialAndEmptyPagesKeepIndependentTotalsAndContinuation() {
        val page = decode(body(item, "\"next\""))
        assertEquals(2, page.fileCount)
        assertEquals(200L, page.summary?.completedFiles)
        assertEquals("next", page.nextCursor)
        assertEquals("next", decode(body(next = "\"next\"")).nextCursor)
        val updating =
            decode(
                body(
                    summary =
                        """{"state":"updating","completed_files":null,"file_count":null,"total_size":null}"""
                )
            )
        assertFalse(updating.summary!!.ready)
        assertNull(updating.summary!!.completedFiles)
    }

    @Test
    fun malformedPagesNeverBecomeEmptySuccessOrUnlimitedTotals() {
        val valid = body(item)
        for (raw in
            listOf(
                valid.replace("\"paginated\":true,", ""),
                valid.replace("\"next_cursor\":null,", ""),
                valid.replace("\"next_cursor\":null", "\"next_cursor\":\"\""),
                valid.replace("\"completed_files\":200", "\"completed_files\":\"200\""),
                valid.replace("\"completed_files\":200", "\"completed_files\":null"),
                valid.replace("\"file_count\":300", "\"file_count\":9223372036854775808"),
                valid.replace("\"file_count\":2", "\"file_count\":2.0"),
                valid.replace("\"file_count\":2", "\"file_count\":101"),
                valid.replace("\"file_count\":2", "\"file_count\":-1"),
                valid.replace("\"state\":\"ready\"", "\"state\":\"updating\""),
                body("$item,$item"),
                body(List(51) { item }.joinToString(",")),
                valid.replace(child, "wrong"),
                valid.replace(slot, child),
            )) assertFails(raw.take(100)) { decode(raw) }
        assertFails { decode(body(next = "\"same\""), "same") }
    }

    @Test
    fun pageRequestIsAuthenticatedSingleBoundedFetchWithNoFallback() = runTest {
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Default) {
            var calls = 0
            val http =
                HttpClient(
                    MockEngine { request ->
                        calls++
                        assertEquals("/api/v1/slots/$slot/inbox", request.url.encodedPath)
                        assertEquals("50", request.url.parameters["limit"])
                        assertEquals("cursor", request.url.parameters["after"])
                        assertEquals("Bearer token", request.headers[HttpHeaders.Authorization])
                        respond(
                            body(next = "\"next\""),
                            headers = headersOf(HttpHeaders.ContentType, "application/json"),
                        )
                    }
                )
            try {
                val api = SlotApi(http, ServerConfig("https://owner.test"), "token")
                assertEquals("next", api.getPage(slot, "cursor", 50).nextCursor)
                assertEquals(1, calls)
                assertFails { api.getPage(slot, "", 50) }
                assertFails { api.getPage(slot, null, 101) }
                assertEquals(1, calls)
            } finally {
                http.close()
            }
        }
    }
}

package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import kotlin.test.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.*

class ResourcePaginationTest {
    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    private val config = ServerConfig("https://owner.test")
    private val id = "11111111-1111-1111-1111-111111111111"
    private val row
        get() =
            """{"id":"$id","status":"has_uploads","file_count":350,"completed_files":250,"total_size":4096,"transfers":[],"summary":{"state":"ready","file_count":350,"completed_files":250,"total_size":4096}}"""

    private fun page(rows: String = row, cursor: String = "null") =
        """{"paginated":true,"transfers":[],"slots":[$rows],"next_cursor":$cursor}"""

    private fun decode(raw: String, after: String? = null, limit: Int = 50) =
        decodeResourcePage(Json.parseToJsonElement(raw).jsonObject, after, limit)

    @Test
    fun compactSummariesAndUpdatingRemainDistinct() {
        val slot = decode(page()).slots.single()
        assertEquals(350L, slot.fileCount)
        assertEquals(250L, slot.completedFiles)
        assertTrue(slot.transfers.isEmpty())
        val updating =
            page()
                .replace("350", "null")
                .replace("250", "null")
                .replace("4096", "null")
                .replace("ready", "updating")
        assertNull(decode(updating).slots.single().fileCount)
        assertFalse(decode(updating).slots.single().summary!!.ready)
        assertEquals("later", decode(page("", "\"later\"")).nextCursor)
    }

    @Test
    fun strictPagesRejectMissingConflictingDuplicateOrOverflowingMetadata() {
        for (bad in
            listOf(
                page().replace("\"paginated\":true,", ""),
                page().replace("\"next_cursor\":null", "\"next_cursor\":\"\""),
                page().replace("\"next_cursor\":null", "\"next_cursor\":\"${"x".repeat(513)}\""),
                page().replace("\"transfers\":[],", ""),
                page().replace("\"file_count\":350", "\"file_count\":\"350\""),
                page().replace("\"file_count\":350", "\"file_count\":350.0"),
                page().replaceFirst("\"file_count\":350", "\"file_count\":351"),
                page().replace("4096", "9223372036854775808"),
                page().replace("4096", "-1"),
                page().replace("ready", "updating"),
                page().replace(id, "bad-id"),
                page("$row,$row"),
                page(List(51) { row }.joinToString(",")),
            )) assertFails(bad.take(100)) { decode(bad) }
        assertFails { decode(page("", "\"same\""), "same") }
    }

    @Test
    fun pagesAreExplicitAndFailedNavigationDoesNotConsumeVisibleSnapshot() = transportTest {
        var calls = 0
        val http =
            HttpClient(
                MockEngine { request ->
                    calls++
                    assertEquals("owner.test", request.url.host)
                    assertEquals("50", request.url.parameters["limit"])
                    assertEquals("Bearer owner-token", request.headers[HttpHeaders.Authorization])
                    assertNull(request.url.parameters["all"])
                    if (request.url.parameters["after"] == null) respond(page(cursor = "\"next\""))
                    else respond("unavailable", HttpStatusCode.ServiceUnavailable)
                }
            )
        try {
            val api = AuthApi(http, config, "owner-token")
            val shown = api.resourcesPage(null, 50)
            assertEquals(1, calls)
            assertEquals("next", shown.nextCursor)
            assertFails { api.resourcesPage(shown.nextCursor, 50) }
            assertEquals(id, shown.slots.single().id)
            assertEquals(2, calls)
        } finally {
            http.close()
        }
    }

    @Test
    fun oversizedPageAndInvalidRequestsFailWithoutFallbackOrFollowingPages() = transportTest {
        var calls = 0
        val http =
            HttpClient(
                MockEngine {
                    calls++
                    respond(" ".repeat(1024 * 1024 + 1))
                }
            )
        try {
            val api = AuthApi(http, config, "token")
            assertFails { api.resourcesPage(null, 50) }
            assertEquals(1, calls)
            assertFails { api.resourcesPage("", 50) }
            assertFails { api.resourcesPage(null, 101) }
            assertEquals(1, calls)
        } finally {
            http.close()
        }
    }
}

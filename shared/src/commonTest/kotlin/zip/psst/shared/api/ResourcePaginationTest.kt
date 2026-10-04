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

class ResourcePaginationTest {
    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    private val config = ServerConfig("https://owner.test")

    @Test
    fun compactSlotCountsRemainAvailableWithoutNestedChildren() = transportTest {
        val http =
            HttpClient(
                MockEngine {
                    respond(
                        """{"transfers":[],"slots":[{"id":"inbox","status":"has_uploads","file_count":350,"completed_files":250,"total_size":4096,"transfers":[]}],"next_cursor":null}"""
                    )
                }
            )
        try {
            val slot = AuthApi(http, config, "owner-token").resources().slots.single()
            assertEquals(350, slot.fileCount)
            assertEquals(250L, slot.completedFiles)
            assertEquals(4096L, slot.totalSize)
            assertTrue(slot.transfers.isEmpty())
        } finally {
            http.close()
        }
    }

    @Test
    fun completeSnapshotIncludesEveryPageAndOpaqueCursorStaysOnConfiguredOrigin() = transportTest {
        var requests = 0
        val http =
            HttpClient(
                MockEngine { request ->
                    requests++
                    assertEquals("owner.test", request.url.host)
                    assertEquals("100", request.url.parameters["limit"])
                    assertEquals("Bearer owner-token", request.headers[HttpHeaders.Authorization])
                    when (request.url.parameters["after"]) {
                        null ->
                            respond(
                                """{"transfers":[{"id":"first","status":"complete"}],"slots":[],"next_cursor":"opaque_cursor-1"}"""
                            )
                        "opaque_cursor-1" ->
                            respond(
                                """{"transfers":[],"slots":[{"id":"second","status":"waiting"}],"next_cursor":null}"""
                            )
                        else -> error("Unexpected cursor")
                    }
                }
            )
        try {
            val snapshot = AuthApi(http, config, "owner-token").resources()
            assertEquals(listOf("first"), snapshot.transfers.map { it.id })
            assertEquals(listOf("second"), snapshot.slots.map { it.id })
            assertNull(snapshot.nextCursor)
            assertEquals(2, requests)
        } finally {
            http.close()
        }
    }

    @Test
    fun failedSecondPageNeverReturnsPartialSnapshot() = transportTest {
        var requests = 0
        val http =
            HttpClient(
                MockEngine {
                    requests++
                    if (requests == 1)
                        respond(
                            """{"transfers":[{"id":"first","status":"complete"}],"next_cursor":"next"}"""
                        )
                    else respond("unavailable", HttpStatusCode.ServiceUnavailable)
                }
            )
        try {
            assertFails { AuthApi(http, config, "token").resources() }
            assertEquals(2, requests)
        } finally {
            http.close()
        }
    }

    @Test
    fun repeatingOrUnboundedCursorsFailInsteadOfLoopingOrReturningIncompleteHistory() =
        transportTest {
            for (repeating in listOf(true, false)) {
                var requests = 0
                val http =
                    HttpClient(
                        MockEngine {
                            requests++
                            respond(
                                """{"transfers":[],"slots":[],"next_cursor":"${if (repeating) "repeat" else "page$requests"}"}"""
                            )
                        }
                    )
                try {
                    assertFails { AuthApi(http, config, "token").resources() }
                    assertEquals(if (repeating) 2 else 100, requests)
                } finally {
                    http.close()
                }
            }
        }

    @Test
    fun oversizedHistoryPageIsRejectedBeforeAggregation() = transportTest {
        val http = HttpClient(MockEngine { respond(" ".repeat(1024 * 1024 + 1)) })
        try {
            assertFails { AuthApi(http, config, "token").resources() }
        } finally {
            http.close()
        }
    }
}

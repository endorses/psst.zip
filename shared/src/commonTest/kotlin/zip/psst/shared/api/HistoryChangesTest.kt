package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.*
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.*

class HistoryChangesTest {
    @Test
    fun boundedSnapshotsAndCapabilityReadsPreserveRetryAfter() = runTest {
        withContext(Dispatchers.Default) {
            var requests = 0
            val http =
                HttpClient(
                    MockEngine {
                        requests++
                        val seconds =
                            when (it.url.encodedPath) {
                                "/api/v1/auth/resources" -> {
                                    assertEquals(
                                        "Bearer token",
                                        it.headers[HttpHeaders.Authorization],
                                    )
                                    "120"
                                }
                                "/api/v1/config" -> "180"
                                else -> error("Unexpected history request")
                            }
                        respond(
                            "",
                            HttpStatusCode.TooManyRequests,
                            headersOf(HttpHeaders.RetryAfter, seconds),
                        )
                    }
                )
            val config = ServerConfig("https://owner.test")
            try {
                assertEquals(
                    120L,
                    assertFailsWith<HistorySyncRateLimitedException> {
                            AuthApi(http, config, "token").resourcesPage(null, 50)
                        }
                        .retryAfterSeconds,
                )
                assertEquals(
                    180L,
                    assertFailsWith<HistorySyncRateLimitedException> {
                            LimitsApi(http, config).get()
                        }
                        .retryAfterSeconds,
                )
                assertEquals(2, requests)
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun retryAfterHonorsNumericAndHttpDateDelaysWithoutOverflow() {
        assertEquals(90L, historyRetryAfterSeconds("90", 0))
        assertEquals(90L, historyRetryAfterSeconds("Thu, 01 Jan 1970 00:01:30 GMT", 0))
        assertEquals(0L, historyRetryAfterSeconds("Thu, 01 Jan 1970 00:01:30 GMT", 100_000))
        assertEquals(Long.MAX_VALUE / 1000, historyRetryAfterSeconds(Long.MAX_VALUE.toString(), 0))
        assertEquals(10L, historyRetryAfterSeconds("-1", 0))
        assertEquals(10L, historyRetryAfterSeconds("invalid", 0))
    }

    private val id = "11111111-1111-1111-1111-111111111111"
    private val generation = "22222222-2222-2222-2222-222222222222"
    private val fact
        get() =
            """{"id":"$id","revision":2,"created_at":"2026-10-05T00:00:00Z","status":"complete","file_count":1,"total_size":1,"summary":{"state":"ready","file_count":1,"completed_files":1,"total_size":1}}"""

    private fun feed(changes: String = "", next: String = "next", more: Boolean = false) =
        """{"version":1,"generation":"$generation","changes":[$changes],"next_cursor":"$next","has_more":$more}"""

    private fun decode(raw: String) =
        decodeHistoryChanges(Json.parseToJsonElement(raw).jsonObject, "current", 50)

    @Test
    fun emptyAndIdentityOnlyRemovalsAreValid() {
        assertTrue(decode(feed()).changes.isEmpty())
        val removed =
            decode(feed("""{"kind":"transfer","id":"$id","revision":2,"action":"remove"}"""))
        assertEquals("remove", removed.changes.single().action)
        assertNull(removed.changes.single().resource)
    }

    @Test
    fun upsertsReuseStrictSnapshotsAndRejectUnsafeOrConflictingRevisions() {
        val change =
            """{"kind":"transfer","id":"$id","revision":2,"action":"upsert","resource":$fact}"""
        assertEquals(
            2,
            decode(feed(change)).changes.single().resource!!.transfers.single().revision,
        )
        for (bad in
            listOf(
                feed(change.replaceFirst("\"revision\":2", "\"revision\":3")),
                feed(change.replace("\"revision\":2", "\"revision\":9007199254740992")),
                feed(change.replace("\"revision\":2", "\"revision\":\"2\"")),
                feed(change.replace("complete", "bogus")),
                feed("$change,$change"),
                feed(next = "current", more = true),
                feed().replace("\"version\":1", "\"version\":2"),
                feed().replace(generation, "bad"),
                feed().replace("\"has_more\":false", "\"has_more\":\"false\""),
            )) assertFails { decode(bad) }
    }

    @Test
    fun typedResetAndRateLimitAreNotLegacyFallbacks() = runTest {
        withContext(Dispatchers.Default) {
            var count = 0
            val http =
                HttpClient(
                    MockEngine {
                        count++
                        assertEquals("/api/v1/auth/history/changes", it.url.encodedPath)
                        assertEquals("Bearer token", it.headers[HttpHeaders.Authorization])
                        assertEquals("current", it.url.parameters["cursor"])
                        if (count == 1)
                            respond(
                                "",
                                HttpStatusCode.Conflict,
                                headersOf("X-Psst-Error-Code", "history_sync_reset_required"),
                            )
                        else
                            respond(
                                "",
                                HttpStatusCode.TooManyRequests,
                                headersOf(HttpHeaders.RetryAfter, "80"),
                            )
                    }
                )
            try {
                val api = AuthApi(http, ServerConfig("https://owner.test"), "token")
                assertFailsWith<HistorySyncResetRequiredException> { api.historyChanges("current") }
                assertEquals(
                    80,
                    assertFailsWith<HistorySyncRateLimitedException> {
                            api.historyChanges("current")
                        }
                        .retryAfterSeconds,
                )
                assertEquals(2, count)
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun snapshotWatermarkRequiresMatchingGenerationAndRevisions() {
        val raw =
            """{"paginated":true,"transfers":[$fact],"slots":[],"next_cursor":null,"sync_cursor":"current","generation":"$generation"}"""
        assertEquals(
            "current",
            decodeResourcePage(Json.parseToJsonElement(raw).jsonObject, null, 50).syncCursor,
        )
        assertFails {
            decodeResourcePage(
                Json.parseToJsonElement(raw.replace(",\"generation\":\"$generation\"", ""))
                    .jsonObject,
                null,
                50,
            )
        }
        assertFails {
            decodeResourcePage(
                Json.parseToJsonElement(raw.replace("\"revision\":2,", "")).jsonObject,
                null,
                50,
            )
        }
    }

    @Test
    fun persistedMetadataIsStrictlyRevalidatedWithoutRequiringDefaultFields() {
        val cached =
            AuthResources(
                transfers =
                    listOf(
                        AuthResourceTransfer(
                            id,
                            "complete",
                            revision = 2,
                            fileCount = 1,
                            totalSize = 1,
                            summary = zip.psst.shared.model.InboxSummary("ready", 1, 1, 1),
                            createdAt = "2026-10-05T00:00:00Z",
                        )
                    )
            )
        val encoded = Json.encodeToString(AuthResources.serializer(), cached)
        assertEquals(id, decodeCachedHistoryResources(encoded).transfers.single().id)
        val updating =
            AuthResources(
                slots =
                    listOf(
                        AuthResourceSlot(
                            id,
                            "waiting",
                            revision = 2,
                            summary =
                                zip.psst.shared.model.InboxSummary("updating", null, null, null),
                            createdAt = "2026-10-05T00:00:00Z",
                        )
                    )
            )
        assertNull(
            decodeCachedHistoryResources(Json.encodeToString(AuthResources.serializer(), updating))
                .slots
                .single()
                .fileCount
        )
        for (bad in
            listOf(
                "{",
                encoded.replace("complete", "bogus"),
                encoded.replace(id, "bad-id"),
                encoded.replace("\"revision\":2", "\"revision\":-1"),
            )) assertFails { decodeCachedHistoryResources(bad) }
    }
}

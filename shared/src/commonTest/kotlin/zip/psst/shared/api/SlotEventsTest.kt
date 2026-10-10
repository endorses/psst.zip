package zip.psst.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteChannel
import io.ktor.utils.io.ByteReadChannel
import io.ktor.utils.io.cancel
import io.ktor.utils.io.writeStringUtf8
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.io.EOFException
import zip.psst.shared.model.ServerConfig

class SlotEventsTest {
    private val slot = "11111111-1111-1111-1111-111111111111"

    private fun client(channel: ByteReadChannel): HttpClient =
        HttpClient(
            MockEngine { request ->
                assertEquals("/api/v1/slots/$slot/events", request.url.encodedPath)
                assertEquals("Bearer owner-token", request.headers[HttpHeaders.Authorization])
                respond(channel, headers = headersOf(HttpHeaders.ContentType, "text/event-stream"))
            },
        )

    private fun api(http: HttpClient) =
        SlotApi(http, ServerConfig("https://owner.test"), "owner-token")

    private fun rejectsWithoutWaitingForEof(payload: String) = runTest {
        withContext(Dispatchers.Default) {
            supervisorScope {
                val channel = ByteChannel(autoFlush = true)
                val http = client(channel)
                val reader = async { api(http).events(slot).collect() }
                try {
                    channel.writeStringUtf8(payload)
                    // No delimiter or EOF: crossing the bound must terminate the open response.
                    val failure = assertFails { withTimeout(5_000) { reader.await() } }
                    assertFalse(failure is TimeoutCancellationException)
                    assertTrue(channel.isClosedForWrite)
                } finally {
                    reader.cancelAndJoin()
                    channel.cancel()
                    http.close()
                }
            }
        }
    }

    @Test
    fun rejectsOversizedUnterminatedLine() =
        rejectsWithoutWaitingForEof("data: " + "x".repeat(8192))

    @Test
    fun lineLimitCountsUtf8BytesInsteadOfDecodedCharacters() =
        rejectsWithoutWaitingForEof("data: " + "é".repeat(2046))

    @Test
    fun rejectsOversizedUnterminatedMultilineEvent() =
        rejectsWithoutWaitingForEof(("data: " + "x".repeat(2000) + "\n").repeat(10))

    @Test
    fun ignoredFieldsCannotBypassEventBudget() =
        rejectsWithoutWaitingForEof((": " + "x".repeat(2000) + "\n").repeat(10))

    @Test
    fun validEventsResetBudgetAndRetainNotificationSemantics() = runTest {
        withContext(Dispatchers.Default) {
            val channel = ByteChannel(autoFlush = true)
            val http = client(channel)
            val reader = async { api(http).events(slot).toList() }
            try {
                channel.writeStringUtf8("event: connected\r\ndata: $slot\r\n\r\n")
                channel.writeStringUtf8("data: first\ndata: second\n\n")
                val data = "x".repeat(3000)
                repeat(10) { channel.writeStringUtf8("data: $data\n\n") }
                channel.flushAndClose()
                val events = withTimeout(5_000) { reader.await() }
                assertEquals(SlotEvent("connected", slot), events[0])
                assertEquals(SlotEvent("message", "first\nsecond"), events[1])
                assertEquals(List(10) { SlotEvent("message", data) }, events.drop(2))
            } finally {
                reader.cancelAndJoin()
                channel.cancel()
                http.close()
            }
        }
    }

    @Test
    fun bareCrDelimitersAndPartialFinalLineRetainSemantics() = runTest {
        withContext(Dispatchers.Default) {
            val channel = ByteChannel(autoFlush = true)
            val http = client(channel)
            val reader = async { api(http).events(slot).toList() }
            try {
                channel.writeStringUtf8(
                    "event: connected\rdata: $slot\r\rdata: next\r\rignored: partial",
                )
                channel.flushAndClose()
                assertEquals(
                    listOf(SlotEvent("connected", slot), SlotEvent("message", "next")),
                    withTimeout(5_000) { reader.await() },
                )
            } finally {
                reader.cancelAndJoin()
                channel.cancel()
                http.close()
            }
        }
    }

    private fun readFailureIsPropagated(cause: Throwable) = runTest {
        withContext(Dispatchers.Default) {
            supervisorScope {
                val channel = ByteChannel(autoFlush = true)
                val reading = CompletableDeferred<Unit>()
                val readFailure = CompletableDeferred<Throwable>()
                val observed =
                    object : ByteReadChannel by channel {
                        override suspend fun awaitContent(min: Int): Boolean {
                            reading.complete(Unit)
                            throw readFailure.await()
                        }
                    }
                val http = client(observed)
                val reader = async { api(http).events(slot).collect() }
                try {
                    withTimeout(5_000) { reading.await() }
                    readFailure.complete(cause)
                    val failure = assertFails { withTimeout(5_000) { reader.await() } }
                    assertFalse(failure is TimeoutCancellationException)
                    assertEquals(cause.message, failure.message)
                    assertTrue(channel.isClosedForWrite)
                } finally {
                    reader.cancelAndJoin()
                    channel.cancel()
                    http.close()
                }
            }
        }
    }

    @Test
    fun unrelatedEofExceptionIsPropagated() =
        readFailureIsPropagated(EOFException("Injected channel failure"))

    @Test
    fun channelErrorIsPropagated() =
        readFailureIsPropagated(IllegalStateException("Injected channel failure"))

    @Test
    fun cancellingBlockedReadClosesResponse() = runTest {
        withContext(Dispatchers.Default) {
            val channel = ByteChannel(autoFlush = true)
            val http = client(channel)
            val connected = CompletableDeferred<Unit>()
            val reader = async { api(http).events(slot).collect { connected.complete(Unit) } }
            try {
                channel.writeStringUtf8("event: connected\ndata: $slot\n\n")
                withTimeout(5_000) { connected.await() }
                reader.cancelAndJoin()
                assertTrue(channel.isClosedForWrite)
            } finally {
                reader.cancelAndJoin()
                channel.cancel()
                http.close()
            }
        }
    }

    @Test
    fun stoppingCollectionAfterOneEventClosesResponse() = runTest {
        withContext(Dispatchers.Default) {
            val channel = ByteChannel(autoFlush = true)
            val http = client(channel)
            val reader = async { api(http).events(slot).take(1).toList() }
            try {
                channel.writeStringUtf8("event: connected\ndata: $slot\n\n")
                assertEquals(
                    listOf(SlotEvent("connected", slot)),
                    withTimeout(5_000) { reader.await() },
                )
                assertTrue(channel.isClosedForWrite)
            } finally {
                reader.cancelAndJoin()
                channel.cancel()
                http.close()
            }
        }
    }
}

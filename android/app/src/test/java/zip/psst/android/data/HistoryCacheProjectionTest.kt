package zip.psst.android.data

import zip.psst.shared.api.*
import zip.psst.shared.model.InboxSummary
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.*
import io.ktor.http.*
import kotlinx.coroutines.*
import org.junit.Assert.*
import org.junit.Test

class HistoryCacheProjectionTest {
    private val access = HistoryAccess("https://owner.test", "owner")
    private val id = "11111111-1111-1111-1111-111111111111"

    private fun row(account: String = "owner") =
        TransferHistoryEntity(
            id,
            "sent",
            1,
            1,
            access.serverUrl,
            "key-$account",
            "complete",
            accountId = account,
        )

    private val fact =
        AuthResourceTransfer(
            id,
            "complete",
            revision = 2,
            fileCount = 2,
            totalSize = 10,
            summary = InboxSummary("ready", 2, 2, 10),
            createdAt = "2026-10-05T00:00:00Z",
            title = "Files",
        )

    @Test
    fun serverOnlyFactsDoNotAccumulateInPrivateDeviceHistory() = runBlocking {
        val dao = HistoryTest.MemoryDao(row())
        val other = fact.copy(id = "22222222-2222-2222-2222-222222222222")
        val page = AuthResources(transfers = listOf(fact, other))
        syncAccountHistory(dao, page, access, retainNew = false) { access }
        assertEquals("key-owner", dao.getById(id)!!.encryptionKey)
        assertEquals(2, dao.getById(id)!!.fileCount)
        assertNull(dao.getById(other.id))
        val incoming = accountHistoryMetadata(page, access)
        assertEquals(2, incoming.size)
        assertTrue(incoming.all { it.encryptionKey.isBlank() })
    }

    @Test
    fun metadataRevocationDoesNotDeleteForeignPrivateIdentity() = runBlocking {
        withContext(Dispatchers.Default) {
            val foreign = row("foreign")
            val dao = HistoryTest.MemoryDao(foreign)
            var requests = 0
            val http =
                HttpClient(
                    MockEngine {
                        requests++
                        assertEquals(HttpMethod.Delete, it.method)
                        assertEquals("Bearer owner-token", it.headers[HttpHeaders.Authorization])
                        respond("", HttpStatusCode.NoContent)
                    }
                )
            try {
                revokeHistoryEntry(
                    dao,
                    id,
                    { access },
                    metadata = transferHistoryResource(fact, access),
                ) {
                    ApiClient(it, http, "owner-token")
                }
                assertEquals(1, requests)
                assertEquals(foreign, dao.getById(id))
            } finally {
                http.close()
            }
        }
    }
}

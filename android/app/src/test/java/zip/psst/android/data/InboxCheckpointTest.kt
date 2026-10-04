package zip.psst.android.data

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class InboxCheckpointTest {
    private fun parent() =
        TransferHistoryEntity(
            "slot",
            "received",
            0,
            0,
            "https://owner.test",
            "private-key",
            "has_uploads",
            accountId = "owner",
        )

    @Test
    fun migrationResumesFromBoundedOffsetsWithoutRewritingOriginals() = runTest {
        val children = (0 until 507).associate { "child-$it" to ReceivedChild(2, it.toLong()) }
        val original =
            parent()
                .copy(
                    checkpointState = "pending",
                    receivedTransfersJson = Json.encodeToString(children),
                    savedTransferIdsJson = Json.encodeToString(children.keys),
                    savedFileIdsJson = Json.encodeToString(children.keys.map { "$it/blob" }),
                )
        val dao = HistoryTest.MemoryDao(original)
        var row = original
        var passes = 0
        while (row.checkpointState == "pending") {
            val before = row.checkpointKnownFiles
            row =
                requireNotNull(
                    dao.mergeReceived(row.id, ReceivedSnapshot(emptyMap(), partial = true))
                )
            assertTrue(row.checkpointKnownFiles - before <= 128)
            assertEquals("{}", row.receivedTransfersJson)
            assertEquals(
                original.receivedTransfersJson,
                dao.archives[original.checkpointScope() to original.id]?.receivedTransfersJson,
            )
            assertEquals(
                original.savedFileIdsJson,
                dao.archives[original.checkpointScope() to original.id]?.savedFileIdsJson,
            )
            assertEquals(
                original.savedTransferIdsJson,
                dao.archives[original.checkpointScope() to original.id]?.savedTransferIdsJson,
            )
            passes++
            assertTrue(passes < 100)
        }
        assertTrue(passes >= 24)
        assertEquals("ready", row.checkpointState)
        assertEquals(1014L, row.checkpointKnownFiles)
        assertEquals(children.values.sumOf { it.plaintextSize!! }, row.checkpointKnownBytes)
        assertEquals(1014L, row.checkpointSavedFiles)
        assertEquals(setOf("child-500"), dao.savedChildren(row, listOf("child-500", "absent")))
        assertEquals(setOf("child-500/blob"), dao.savedFiles(row, listOf("child-500")))
        assertEquals("private-key", row.encryptionKey)
    }

    @Test
    fun duplicateSavesDoNotDoubleCountAndScopeDoesNotCrossAccounts() = runTest {
        val original = parent()
        val dao = HistoryTest.MemoryDao(original)
        dao.mergeReceived(
            original.id,
            ReceivedSnapshot(mapOf("child" to ReceivedChild(3, 99)), partial = true),
        )
        repeat(3) {
            dao.recordSavedFile(
                original.id,
                "child/a",
                original.checkpointScope(),
                "content://downloads/a",
            )
        }
        var row = requireNotNull(dao.getById(original.id))
        assertEquals(1L, row.checkpointSavedFiles)
        assertEquals(
            "content://downloads/a",
            dao.checkpointFile(original.checkpointScope(), original.id, "child", "a")?.uri,
        )
        dao.mergeReceived(
            original.id,
            ReceivedSnapshot(mapOf("child" to ReceivedChild(3, 99)), partial = true),
            saved = true,
        )
        dao.recordSavedFile(original.id, "child/b")
        row = requireNotNull(dao.getById(original.id))
        assertEquals(3L, row.checkpointSavedFiles)
        assertEquals(99L, row.checkpointKnownBytes)
        assertTrue(dao.savedChildren(row.copy(accountId = "other"), listOf("child")).isEmpty())
        assertTrue(
            dao.savedFiles(row.copy(originScope = "https://other.test"), listOf("child")).isEmpty()
        )
        try {
            dao.recordSavedFile(row.id, "child/c", row.copy(accountId = "other").checkpointScope())
            fail("Wrong account wrote checkpoint")
        } catch (_: IllegalArgumentException) {}
    }

    @Test
    fun pageObservationsDoNotOverwriteSavedCheckpointsOrClaimWholeInboxSaved() = runTest {
        val original = parent()
        val dao = HistoryTest.MemoryDao(original)
        val child = mapOf("child" to ReceivedChild(2, 55))
        dao.mergeReceived(original.id, ReceivedSnapshot(child, partial = true), saved = true)
        val row =
            requireNotNull(
                dao.mergeReceived(
                    original.id,
                    ReceivedSnapshot(
                        mapOf("child" to ReceivedChild(2)),
                        completedFiles = 900,
                        partial = true,
                        summaryObserved = true,
                    ),
                )
            )
        assertEquals(900, row.fileCount)
        assertEquals(2L, row.checkpointSavedFiles)
        assertEquals(55L, row.totalSize)
        assertEquals("has_uploads", row.status)
        assertEquals(setOf("child"), dao.savedChildren(row, listOf("child")))
        assertEquals("{}", row.receivedTransfersJson)
        assertEquals("[]", row.savedTransferIdsJson)
    }

    @Test
    fun malformedSourcesRemainRecoverableAndCannotBeTreatedAsUnsaved() = runTest {
        val original =
            parent().copy(checkpointState = "pending", savedFileIdsJson = "[\"child/a\",BROKEN]")
        val dao = HistoryTest.MemoryDao(original)
        val row =
            requireNotNull(
                dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
            )
        assertEquals("recovery", row.checkpointState)
        assertEquals(
            original.savedFileIdsJson,
            dao.archives[original.checkpointScope() to original.id]?.savedFileIdsJson,
        )
        assertEquals(1L, row.checkpointSavedFiles)
        try {
            dao.savedFiles(row, listOf("child"))
            fail("Unknown checkpoints became empty saved set")
        } catch (_: IllegalArgumentException) {}
        try {
            dao.recordSavedFile(row.id, "child/b")
            fail("Save started before recovery")
        } catch (_: IllegalArgumentException) {}
    }

    @Test
    fun parserRestartsAtUtf8ByteOffsetsAndBoundsEveryRead() = runTest {
        val source = "[\"a\",\"escaped-\\u0062\",\"c\"]".toByteArray()
        var calls = 0
        suspend fun read(offset: Long, maximum: Int): ByteArray {
            assertTrue(maximum <= 16384)
            calls++
            return source.copyOfRange(offset.toInt(), minOf(source.size, offset.toInt() + 3))
        }
        var reader = LegacyCheckpointReader(0, 1, ::read)
        assertEquals("a", reader.next()!!.first)
        reader = LegacyCheckpointReader(reader.position, 1, ::read)
        assertEquals("escaped-b", reader.next()!!.first)
        reader = LegacyCheckpointReader(reader.position, 1, ::read)
        assertEquals("c", reader.next()!!.first)
        assertNull(reader.next())
        assertTrue(calls > 1)
    }

    @Test
    fun oversizedSourceIsRetainedWithoutReadingItsPayload() = runTest {
        val original =
            parent()
                .copy(
                    checkpointState = "pending",
                    receivedTransfersJson = "{\"child\":{\"fileCount\":1}}",
                )
        val dao =
            object : HistoryTest.MemoryDao(original) {
                override suspend fun checkpointLegacyLength(
                    id: String,
                    scope: String,
                    source: Int,
                ) = LEGACY_CHECKPOINT_SOURCE_LIMIT + 1

                override suspend fun checkpointLegacyChunk(
                    id: String,
                    scope: String,
                    source: Int,
                    offset: Long,
                    maximum: Int,
                ): ByteArray = error("Oversized source was read")
            }
        val row =
            requireNotNull(
                dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
            )
        assertEquals("recovery", row.checkpointState)
        assertEquals(
            original.receivedTransfersJson,
            dao.archives[original.checkpointScope() to original.id]?.receivedTransfersJson,
        )
        assertEquals("{}", row.receivedTransfersJson)
    }

    @Test
    fun importCannotReplaceNewerKnownBytesOrDoubleCountAnObservedChild() = runTest {
        val original =
            parent()
                .copy(
                    checkpointState = "pending",
                    receivedTransfersJson = "{\"child\":{\"fileCount\":2,\"plaintextSize\":50}}",
                )
        val dao = HistoryTest.MemoryDao(original)
        val observed =
            dao.mergeCheckpointChild(
                original,
                InboxChildCheckpoint(
                    original.checkpointScope(),
                    original.id,
                    "child",
                    2,
                    99,
                    saved = true,
                ),
            )
        dao.update(observed)
        val row =
            requireNotNull(
                dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
            )
        assertEquals("ready", row.checkpointState)
        assertEquals(2L, row.checkpointKnownFiles)
        assertEquals(99L, row.checkpointKnownBytes)
        assertEquals(2L, row.checkpointSavedFiles)
        assertEquals(setOf("child"), dao.savedChildren(row, listOf("child")))
    }
}

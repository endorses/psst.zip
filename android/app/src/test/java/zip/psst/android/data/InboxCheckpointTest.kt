package zip.psst.android.data

import java.io.RandomAccessFile
import java.nio.file.Files
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
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
    fun largeAggregateSourceContinuesWithBoundedReadsAndPersistedOffsets() = runTest {
        val original = parent().copy(checkpointState = "pending")
        // An exact virtual byte source avoids allocating a giant String in the
        // test DAO. Its valid repeated prefix is >64 MiB, followed by one new
        // saved file. Repetition has the same idempotent checkpoint state.
        val repeated = "\"child/blob\",".toByteArray()
        val repeats = (65L * 1024 * 1024 / repeated.size) + 1
        val tail = "\"last/blob\"]".toByteArray()
        val tailOffset = 1 + repeated.size * repeats
        val totalBytes = tailOffset + tail.size
        var maximumRead = 0
        var readCalls = 0
        val dao =
            object : HistoryTest.MemoryDao(original) {
                override suspend fun checkpointLegacyChunk(
                    id: String,
                    scope: String,
                    source: Int,
                    offset: Long,
                    maximum: Int,
                ): ByteArray {
                    if (source != 1)
                        return super.checkpointLegacyChunk(id, scope, source, offset, maximum)
                    assertEquals(original.checkpointScope(), scope)
                    maximumRead = maxOf(maximumRead, maximum)
                    readCalls++
                    return ByteArray(
                        minOf(maximum.toLong(), maxOf(0L, totalBytes - offset)).toInt()
                    ) { index ->
                        val position = offset + index
                        when {
                            position == 0L -> '['.code.toByte()
                            position < tailOffset ->
                                repeated[((position - 1) % repeated.size).toInt()]
                            else -> tail[(position - tailOffset).toInt()]
                        }
                    }
                }
            }
        var row =
            requireNotNull(
                dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
            )
        assertEquals("pending", row.checkpointState)
        assertEquals(1, row.checkpointColumn)
        assertEquals(1 + repeated.size * 64L, row.checkpointOffset)
        assertEquals(1L, row.checkpointSavedFiles)
        // Simulate a persisted offset after the identical idempotent prefix.
        // All rows/counters are exactly those of the already imported first
        // entry. The production parser resumes using Long UTF-8 offsets.
        dao.update(row.copy(checkpointOffset = tailOffset))
        row =
            requireNotNull(
                dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
            )
        assertEquals("ready", row.checkpointState)
        assertEquals(2L, row.checkpointSavedFiles)
        assertEquals(setOf("child/blob", "last/blob"), dao.savedFiles(row, listOf("child", "last")))
        assertEquals("private-key", row.encryptionKey)
        assertTrue(readCalls > 1)
        assertTrue(maximumRead <= 16384)
        assertTrue(totalBytes > 64L * 1024 * 1024)
        assertEquals(original, dao.archives[original.checkpointScope() to original.id])
    }

    @Test
    fun realLargeSourceParsesWithoutHoldingItsWholeArrayAndResumesAtEntryBoundary() = runTest {
        val path = Files.createTempFile("psst-checkpoint-large-", ".json")
        try {
            val id = "c".repeat(128) + "/" + "b".repeat(128)
            val member = ("\"" + id + "\",").toByteArray()
            val count = (65L * 1024 * 1024 / member.size + 1).toInt()
            Files.newOutputStream(path).buffered(65536).use { output ->
                output.write('['.code)
                repeat(count - 1) { output.write(member) }
                output.write(("\"" + id + "\"]").toByteArray())
            }
            assertTrue(Files.size(path) > 64L * 1024 * 1024)
            RandomAccessFile(path.toFile(), "r").use { source ->
                var reads = 0
                suspend fun read(offset: Long, maximum: Int): ByteArray {
                    assertTrue(maximum <= 16384)
                    reads++
                    source.seek(offset)
                    val buffer = ByteArray(maximum)
                    val amount = source.read(buffer)
                    return if (amount < 0) byteArrayOf() else buffer.copyOf(amount)
                }
                var reader = LegacyCheckpointReader(0, 1, ::read)
                repeat(count) { index ->
                    assertEquals(id, reader.next()!!.first)
                    if (index == 63) reader = LegacyCheckpointReader(reader.position, 1, ::read)
                }
                assertNull(reader.next())
                assertEquals(Files.size(path), reader.position)
                assertTrue(reads > 4096)
            }
        } finally {
            Files.deleteIfExists(path)
        }
    }

    @Test
    fun cancellationStopsBeforeAnotherSourceSliceIsRead() = runTest {
        var reads = 0
        val probe = Job()
        try {
            withContext(probe) {
                val reader =
                    LegacyCheckpointReader(0, 1) { _, maximum ->
                        reads++
                        currentCoroutineContext().cancel()
                        ByteArray(maximum) { index ->
                            when (index) {
                                0 -> '['.code.toByte()
                                1 -> '"'.code.toByte()
                                else -> 'a'.code.toByte()
                            }
                        }
                    }
                reader.next()
                fail("Canceled parser kept reading")
            }
        } catch (_: CancellationException) {
            assertEquals(1, reads)
        } finally {
            probe.cancel()
        }
    }

    @Test
    fun invalidProgressRetainsArchiveAndDoesNotClaimCheckpointsReady() = runTest {
        for ((column, offset) in listOf(-1 to 0L, 4 to 0L, 1 to -1L, 3 to 1L)) {
            val original =
                parent()
                    .copy(
                        checkpointState = "pending",
                        checkpointColumn = column,
                        checkpointOffset = offset,
                    )
            val dao = HistoryTest.MemoryDao(original)
            val row =
                requireNotNull(
                    dao.mergeReceived(original.id, ReceivedSnapshot(emptyMap(), partial = true))
                )
            assertEquals("recovery", row.checkpointState)
            assertEquals(original, dao.archives[original.checkpointScope() to original.id])
        }
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

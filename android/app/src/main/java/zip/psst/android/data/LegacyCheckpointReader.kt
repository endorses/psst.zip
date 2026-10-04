package zip.psst.android.data

import java.io.ByteArrayOutputStream
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.serialization.json.Json

private const val LEGACY_ENTRY_LIMIT = 64 * 1024

/** Byte offsets survive process restarts. No parser buffer or whole JSON tree is persisted. */
internal class LegacyCheckpointReader(
    start: Long,
    private val source: Int,
    private val readChunk: suspend (Long, Int) -> ByteArray,
) {
    var position: Long = start
        private set

    private var buffer = byteArrayOf()
    private var bufferStart = start

    init {
        require(start >= 0 && source in 0..2) { "Invalid saved checkpoint position" }
    }

    private suspend fun peek(): Int {
        if (position - bufferStart >= buffer.size) {
            currentCoroutineContext().ensureActive()
            bufferStart = position
            buffer = readChunk(position, 16 * 1024)
        }
        return buffer.getOrNull((position - bufferStart).toInt())?.toInt()?.and(255) ?: -1
    }

    private suspend fun take(): Int = peek().also { if (it >= 0) position++ }

    private suspend fun whitespace() {
        val start = position
        while (peek() in listOf(9, 10, 13, 32)) {
            take()
            require(position - start <= LEGACY_ENTRY_LIMIT)
        }
    }

    private suspend fun quoted(): String {
        val out = ByteArrayOutputStream()
        require(take() == 34)
        out.write(34)
        var escaped = false
        while (true) {
            val byte = take()
            require(byte >= 0) { "Incomplete saved checkpoint" }
            out.write(byte)
            require(out.size() <= LEGACY_ENTRY_LIMIT) { "Saved checkpoint entry is too large" }
            if (!escaped && byte == 34) break
            escaped = !escaped && byte == 92
        }
        return Json.decodeFromString(out.toString(Charsets.UTF_8.name()))
    }

    private suspend fun objectValue(): String {
        require(peek() == 123)
        val out = ByteArrayOutputStream()
        var depth = 0
        var quoted = false
        var escaped = false
        do {
            val byte = take()
            require(byte >= 0)
            out.write(byte)
            require(out.size() <= LEGACY_ENTRY_LIMIT) { "Saved checkpoint entry is too large" }
            if (quoted) {
                if (!escaped && byte == 34) quoted = false
                escaped = !escaped && byte == 92
            } else
                when (byte) {
                    34 -> quoted = true
                    123,
                    91 -> {
                        depth++
                        require(depth <= 8)
                    }
                    125,
                    93 -> depth--
                }
        } while (depth > 0 || quoted)
        return out.toString(Charsets.UTF_8.name())
    }

    /** Null denotes a complete source, otherwise one object member or array string. */
    suspend fun next(): Pair<String, String?>? {
        if (position == 0L) {
            whitespace()
            require(take() == if (source == 0) 123 else 91)
        }
        whitespace()
        val closing = if (source == 0) 125 else 93
        if (peek() == closing) {
            take()
            whitespace()
            require(peek() == -1)
            return null
        }
        val key = quoted()
        whitespace()
        val value =
            if (source == 0) {
                require(take() == 58)
                whitespace()
                objectValue()
            } else null
        whitespace()
        when (peek()) {
            44 -> {
                take()
                whitespace()
                require(peek() != closing)
            }
            closing -> Unit
            else -> error("Invalid saved checkpoint separator")
        }
        return key to value
    }
}

/**
 * There is no aggregate JSON-size ceiling: each entry/read and batch is bounded, so a legitimately
 * large inbox can continue from its committed byte offset. SQLite may internally materialize legacy
 * TEXT for archive/substr; this bounds app parser allocations, not that migration engine
 * allocation.
 */
internal suspend fun TransferHistoryDao.advanceCheckpointMigration(
    initial: TransferHistoryEntity
): TransferHistoryEntity {
    if (initial.checkpointState != "pending") return initial
    archiveCheckpoints(initial)
    var row =
        initial.copy(
            receivedTransfersJson = "{}",
            savedFileIdsJson = "[]",
            savedTransferIdsJson = "[]",
        )
    try {
        require(row.checkpointColumn in 0..3 && row.checkpointOffset >= 0) {
            "Invalid saved checkpoint progress"
        }
        require(row.checkpointColumn != 3 || row.checkpointOffset == 0L) {
            "Invalid completed checkpoint progress"
        }
        var consumed = 0
        while (row.checkpointColumn < 3 && consumed < 64) {
            val source = row.checkpointColumn
            val reader =
                LegacyCheckpointReader(row.checkpointOffset, source) { offset, maximum ->
                    checkpointLegacyChunk(row.id, row.checkpointScope(), source, offset, maximum)
                        ?: byteArrayOf()
                }
            val batchStart = reader.position
            while (consumed < 64 && reader.position - batchStart < 256 * 1024) {
                val entry = reader.next()
                if (entry == null) {
                    row = row.copy(checkpointColumn = source + 1, checkpointOffset = 0)
                    break
                }
                when (source) {
                    0 -> {
                        require(checkpointComponent(entry.first))
                        val child =
                            Json.decodeFromString<ReceivedChild>(requireNotNull(entry.second))
                        row =
                            mergeCheckpointChild(
                                row,
                                InboxChildCheckpoint(
                                    row.checkpointScope(),
                                    row.id,
                                    entry.first,
                                    child.fileCount,
                                    child.plaintextSize,
                                ),
                                importing = true,
                            )
                    }
                    1 -> {
                        val parts = entry.first.split('/')
                        require(parts.size == 2 && parts.all(::checkpointComponent))
                        row = insertSavedCheckpoint(row, parts[0], parts[1])
                    }
                    else -> {
                        require(checkpointComponent(entry.first))
                        row =
                            mergeCheckpointChild(
                                row,
                                InboxChildCheckpoint(
                                    row.checkpointScope(),
                                    row.id,
                                    entry.first,
                                    saved = true,
                                ),
                            )
                    }
                }
                row = row.copy(checkpointOffset = reader.position)
                consumed++
            }
            // Yield at the byte bound as well as the entry bound, without moving to a new parent.
            if (row.checkpointColumn == source) break
        }
        if (row.checkpointColumn == 3) row = row.copy(checkpointState = "ready")
    } catch (error: kotlinx.coroutines.CancellationException) {
        throw error
    } catch (error: IllegalArgumentException) {
        row = row.copy(checkpointState = "recovery")
    } catch (error: IllegalStateException) {
        row = row.copy(checkpointState = "recovery")
    }
    update(row)
    return row
}

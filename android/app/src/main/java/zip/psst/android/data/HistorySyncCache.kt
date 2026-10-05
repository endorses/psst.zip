package zip.psst.android.data

import androidx.room.*
import zip.psst.shared.api.*
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

/** Disposable server metadata. Private keys and checkpoints stay in transfer_history. */
@Entity(
    tableName = "history_server_facts",
    primaryKeys = ["scope", "kind", "id"],
    indices =
        [
            Index(value = ["scope", "kind", "createdAt", "id"]),
            Index(value = ["scope", "createdAt", "id"]),
        ],
)
data class HistoryServerFact(
    val scope: String,
    val kind: String,
    val id: String,
    val generation: String,
    val revision: Long,
    val createdAt: Long,
    val body: String,
    val removed: Boolean = false,
)

@Entity(tableName = "history_sync_state")
data class HistorySyncState(
    @PrimaryKey val scope: String,
    val generation: String,
    val cursor: String,
)

@Entity(tableName = "history_cached_windows", primaryKeys = ["scope", "kind", "cursor"])
data class HistoryCachedWindow(
    val scope: String,
    val kind: String,
    val cursor: String,
    val generation: String,
    val floorTime: Long,
    val floorId: String,
    val beforeTime: Long,
    val beforeId: String,
    val nextCursor: String?,
    val legacyPage: String? = null,
    val serverAfter: String? = null,
)

internal fun HistoryAccess.syncScope(): String =
    "${localHistoryScope(serverUrl)}|${requireNotNull(accountId)}"

class HistoryCacheCorruptException(cause: Throwable) :
    IllegalStateException("Invalid disposable history cache", cause)

private fun decodedHistory(raw: String): AuthResources =
    try {
        decodeCachedHistoryResources(raw)
    } catch (error: Exception) {
        throw HistoryCacheCorruptException(error)
    }

private val historyJson = Json { ignoreUnknownKeys = true }

@Dao
abstract class HistorySyncDao {
    @Query("SELECT * FROM history_sync_state WHERE scope=:scope")
    abstract suspend fun state(scope: String): HistorySyncState?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    abstract suspend fun writeState(state: HistorySyncState)

    @Query("SELECT * FROM history_server_facts WHERE scope=:scope AND kind=:kind AND id=:id")
    abstract suspend fun fact(scope: String, kind: String, id: String): HistoryServerFact?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    abstract suspend fun writeFact(fact: HistoryServerFact)

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    abstract suspend fun writeWindow(window: HistoryCachedWindow)

    @Query(
        "SELECT * FROM history_cached_windows WHERE scope=:scope AND kind=:kind AND cursor=:cursor"
    )
    abstract suspend fun window(scope: String, kind: String, cursor: String): HistoryCachedWindow?

    @Query("DELETE FROM history_server_facts WHERE scope=:scope")
    abstract suspend fun clearFacts(scope: String)

    @Query("DELETE FROM history_cached_windows WHERE scope=:scope")
    abstract suspend fun clearWindows(scope: String)

    @Query("DELETE FROM history_sync_state WHERE scope=:scope")
    abstract suspend fun clearState(scope: String)

    @Query(
        "SELECT * FROM history_server_facts WHERE scope=:scope AND removed=0 AND (:kind='' OR kind=:kind) AND (createdAt>:floorTime OR (createdAt=:floorTime AND id>=:floorId)) AND (createdAt<:beforeTime OR (createdAt=:beforeTime AND id<:beforeId)) ORDER BY createdAt DESC,id DESC LIMIT 51"
    )
    abstract suspend fun rows(
        scope: String,
        kind: String,
        floorTime: Long,
        floorId: String,
        beforeTime: Long,
        beforeId: String,
    ): List<HistoryServerFact>

    @Query(
        "DELETE FROM history_server_facts WHERE rowid IN (SELECT rowid FROM history_server_facts WHERE scope=:scope ORDER BY removed ASC,createdAt DESC,id DESC LIMIT -1 OFFSET 2000)"
    )
    abstract suspend fun trimScope(scope: String): Int

    @Query(
        "DELETE FROM history_server_facts WHERE rowid IN (SELECT rowid FROM history_server_facts ORDER BY removed ASC,createdAt DESC,id DESC LIMIT -1 OFFSET 10000)"
    )
    abstract suspend fun trimGlobal(): Int

    @Query(
        "DELETE FROM history_cached_windows WHERE rowid IN (SELECT rowid FROM history_cached_windows ORDER BY rowid DESC LIMIT -1 OFFSET 1000)"
    )
    abstract suspend fun trimWindows()

    @Query(
        "DELETE FROM history_cached_windows WHERE legacyPage IS NOT NULL AND rowid IN (SELECT rowid FROM history_cached_windows WHERE legacyPage IS NOT NULL ORDER BY rowid DESC LIMIT -1 OFFSET 20)"
    )
    abstract suspend fun trimLegacyWindows()

    @Query("DELETE FROM history_cached_windows") abstract suspend fun clearAllWindows()

    @Query("DELETE FROM history_sync_state") abstract suspend fun clearAllStates()

    @Query(
        "DELETE FROM history_sync_state WHERE scope NOT IN (SELECT DISTINCT scope FROM history_cached_windows)"
    )
    abstract suspend fun trimStates()

    @Query("DELETE FROM history_cached_windows WHERE scope=:scope AND cursor!=''")
    abstract suspend fun invalidateOlder(scope: String)

    private suspend fun merge(resources: AuthResources, access: HistoryAccess, generation: String) {
        val scope = access.syncScope()
        for ((kind, page) in
            resources.transfers.map { "transfer" to AuthResources(transfers = listOf(it)) } +
                resources.slots.map { "slot" to AuthResources(slots = listOf(it)) }) {
            val row = page.transfers.firstOrNull()
            val slot = page.slots.firstOrNull()
            val id = row?.id ?: requireNotNull(slot).id
            val revision = row?.revision ?: requireNotNull(slot).revision
            val old = fact(scope, kind, id)
            if (
                old != null &&
                    generation != "legacy" &&
                    old.generation == generation &&
                    old.revision >= revision
            )
                continue
            val created =
                parseHistoryExpiry(row?.createdAt ?: slot?.createdAt)
                    ?: error("Missing creation time")
            val encoded = historyJson.encodeToString(page)
            require(encoded.toByteArray().size <= 8192) { "History metadata is too large" }
            if (
                old != null &&
                    old.generation == generation &&
                    !old.removed &&
                    old.body == encoded &&
                    old.revision == revision
            )
                continue
            writeFact(HistoryServerFact(scope, kind, id, generation, revision, created, encoded))
        }
    }

    @Transaction
    open suspend fun invalidate(scope: String) {
        clearFacts(scope)
        clearWindows(scope)
        clearState(scope)
    }

    /** Reset only disposable coverage/facts. transfer_history and saved downloads are untouched. */
    @Transaction
    open suspend fun snapshot(
        resources: AuthResources,
        access: HistoryAccess,
        kind: String,
        cursor: String?,
        reset: Boolean = false,
        serverAfter: String? = cursor,
    ) {
        val scope = access.syncScope()
        val generation = resources.generation ?: "legacy"
        val previous = state(scope)
        if (reset || (previous != null && previous.generation != generation)) {
            clearFacts(scope)
            clearWindows(scope)
            clearState(scope)
        }
        merge(resources, access, generation)
        val facts =
            (resources.transfers.map { "transfer" to it.id } +
                    resources.slots.map { "slot" to it.id })
                .mapNotNull { (type, id) -> fact(scope, type, id) }
                .sortedWith(
                    compareByDescending<HistoryServerFact> { it.createdAt }
                        .thenByDescending { it.id }
                )
        val last = facts.lastOrNull()
        val first = facts.firstOrNull()
        writeWindow(
            HistoryCachedWindow(
                scope,
                kind,
                cursor.orEmpty(),
                generation,
                last?.createdAt ?: Long.MIN_VALUE,
                last?.id ?: "",
                if (cursor == null) Long.MAX_VALUE else first?.createdAt ?: Long.MIN_VALUE,
                if (cursor == null) "\uffff" else (first?.id.orEmpty() + "\uffff"),
                resources.nextCursor,
                if (generation == "legacy") historyJson.encodeToString(resources) else null,
                serverAfter,
            )
        )
        // Older/filter snapshots must never move the account watermark forward.
        if (
            resources.syncCursor != null &&
                (previous == null || reset || previous.generation != generation)
        )
            writeState(HistorySyncState(scope, generation, requireNotNull(resources.syncCursor)))
        evict(scope)
    }

    @Transaction
    open suspend fun batch(
        changes: HistoryChanges,
        access: HistoryAccess,
        expected: HistorySyncState,
    ) {
        val scope = access.syncScope()
        check(state(scope) == expected && changes.generation == expected.generation) {
            "History scope changed"
        }
        for (change in changes.changes) {
            val old = fact(scope, change.kind, change.id)
            if (old != null && old.revision >= change.revision) continue
            if (change.action == "remove")
                writeFact(
                    HistoryServerFact(
                        scope,
                        change.kind,
                        change.id,
                        changes.generation,
                        change.revision,
                        old?.createdAt ?: 0,
                        "",
                        true,
                    )
                )
            else merge(requireNotNull(change.resource), access, changes.generation)
        }
        if (expected.cursor != changes.nextCursor)
            writeState(HistorySyncState(scope, changes.generation, changes.nextCursor))
        evict(scope)
    }

    private suspend fun evict(scope: String) {
        if (trimScope(scope) > 0) {
            // Eviction invalidates coverage, including removal revisions. Reset before accepting
            // another snapshot so an evicted tombstone cannot be resurrected by a late page.
            clearWindows(scope)
            clearState(scope)
        }
        if (trimGlobal() > 0) {
            clearAllWindows()
            clearAllStates()
        }
        trimLegacyWindows()
        trimWindows()
        trimStates()
    }

    @Transaction
    open suspend fun rename(access: HistoryAccess, kind: String, id: String, title: String?) {
        val old = fact(access.syncScope(), kind, id) ?: return
        if (old.removed) return
        val page = decodedHistory(old.body)
        val updated =
            page.copy(
                transfers = page.transfers.map { it.copy(title = title) },
                slots = page.slots.map { it.copy(title = title) },
            )
        writeFact(old.copy(body = historyJson.encodeToString(updated)))
    }

    @Transaction
    open suspend fun cachedPage(
        access: HistoryAccess,
        kind: String,
        cursor: String?,
    ): AuthResources? {
        val scope = access.syncScope()
        val window = window(scope, kind, cursor.orEmpty()) ?: return null
        if (window.legacyPage != null) return decodedHistory(window.legacyPage)
        val rows =
            rows(scope, kind, window.floorTime, window.floorId, window.beforeTime, window.beforeId)
        val shown = rows.take(50)
        val next =
            if (rows.size > 50) {
                val last = shown.last()
                // A durable boundary, not an offset: new arrivals never shift an older page.
                val key =
                    "local_" +
                        java.util.UUID.nameUUIDFromBytes(
                                "$scope|$kind|${window.generation}|${last.createdAt}|${last.id}|${window.floorTime}|${window.floorId}"
                                    .toByteArray()
                            )
                            .toString()
                            .replace("-", "")
                val boundary = decodedHistory(last.body)
                val after =
                    if (kind.isEmpty())
                        boundary.transfers.firstOrNull()?.historyAfter
                            ?: boundary.slots.firstOrNull()?.historyAfter
                    else
                        boundary.transfers.firstOrNull()?.historyAfterKind
                            ?: boundary.slots.firstOrNull()?.historyAfterKind
                val continuation =
                    window.copy(
                        cursor = key,
                        beforeTime = last.createdAt,
                        beforeId = last.id,
                        serverAfter = after,
                    )
                if (window(scope, kind, key) != continuation) writeWindow(continuation)
                key
            } else window.nextCursor
        val pages = shown.map { decodedHistory(it.body) }
        return AuthResources(
            pages.flatMap { it.transfers },
            pages.flatMap { it.slots },
            next,
            true,
            state(scope)?.cursor,
            window.generation.takeIf { it != "legacy" },
        )
    }
}

package zip.psst.android.data

import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Entity(
    tableName = "inbox_child_checkpoint",
    primaryKeys = ["scope", "slotId", "childId"],
    indices = [Index("slotId")],
    foreignKeys =
        [
            ForeignKey(
                entity = TransferHistoryEntity::class,
                parentColumns = ["id"],
                childColumns = ["slotId"],
                onDelete = ForeignKey.CASCADE,
            )
        ],
)
data class InboxChildCheckpoint(
    val scope: String,
    val slotId: String,
    val childId: String,
    val fileCount: Int = 0,
    val plaintextSize: Long? = null,
    val saved: Boolean = false,
    val savedFiles: Int = 0,
) {
    @get:androidx.room.Ignore
    val savedCount: Long
        get() = maxOf(savedFiles, if (saved) fileCount else 0).toLong()
}

@Entity(
    tableName = "inbox_file_checkpoint",
    primaryKeys = ["scope", "slotId", "childId", "blobId"],
    indices = [Index("slotId")],
    foreignKeys =
        [
            ForeignKey(
                entity = TransferHistoryEntity::class,
                parentColumns = ["id"],
                childColumns = ["slotId"],
                onDelete = ForeignKey.CASCADE,
            )
        ],
)
data class InboxFileCheckpoint(
    val scope: String,
    val slotId: String,
    val childId: String,
    val blobId: String,
    val uri: String? = null,
)

@Entity(
    tableName = "inbox_legacy_checkpoint",
    primaryKeys = ["scope", "slotId"],
    indices = [Index("slotId")],
    foreignKeys =
        [
            ForeignKey(
                entity = TransferHistoryEntity::class,
                parentColumns = ["id"],
                childColumns = ["slotId"],
                onDelete = ForeignKey.CASCADE,
            )
        ],
)
data class InboxLegacyCheckpoint(
    val scope: String,
    val slotId: String,
    val receivedTransfersJson: String,
    val savedFileIdsJson: String,
    val savedTransferIdsJson: String,
)

internal fun TransferHistoryEntity.checkpointScope(): String =
    Json.encodeToString(listOf(originScope, accountId.orEmpty()))

interface InboxCheckpointQueries {
    @Query(
        "SELECT * FROM inbox_child_checkpoint WHERE scope=:scope AND slotId=:slotId AND childId=:childId"
    )
    suspend fun checkpointChild(
        scope: String,
        slotId: String,
        childId: String,
    ): InboxChildCheckpoint?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putCheckpointChild(row: InboxChildCheckpoint)

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertCheckpointFile(row: InboxFileCheckpoint): Long

    @Query(
        "SELECT * FROM inbox_file_checkpoint WHERE scope=:scope AND slotId=:slotId AND childId=:childId AND blobId=:blobId"
    )
    suspend fun checkpointFile(
        scope: String,
        slotId: String,
        childId: String,
        blobId: String,
    ): InboxFileCheckpoint?

    @Query(
        "SELECT childId FROM inbox_child_checkpoint WHERE scope=:scope AND slotId=:slotId AND childId IN (:ids) AND saved=1 LIMIT 100"
    )
    suspend fun checkpointSavedChildren(
        scope: String,
        slotId: String,
        ids: List<String>,
    ): List<String>

    @Query(
        "SELECT childId || '/' || blobId FROM inbox_file_checkpoint WHERE scope=:scope AND slotId=:slotId AND childId IN (:ids) LIMIT 10000"
    )
    suspend fun checkpointSavedFiles(scope: String, slotId: String, ids: List<String>): List<String>

    @Query(
        "INSERT OR IGNORE INTO inbox_legacy_checkpoint(scope,slotId,receivedTransfersJson,savedFileIdsJson,savedTransferIdsJson) SELECT :scope,id,receivedTransfersJson,savedFileIdsJson,savedTransferIdsJson FROM transfer_history WHERE id=:id AND accountId IS :accountId AND originScope=:originScope"
    )
    suspend fun archiveCheckpointSource(
        id: String,
        scope: String,
        accountId: String?,
        originScope: String,
    )

    @Query(
        "UPDATE transfer_history SET receivedTransfersJson='{}',savedFileIdsJson='[]',savedTransferIdsJson='[]' WHERE id=:id AND EXISTS(SELECT 1 FROM inbox_legacy_checkpoint WHERE slotId=:id AND scope=:scope)"
    )
    suspend fun clearArchivedCheckpointSource(id: String, scope: String)

    @Query(
        "SELECT CASE :source WHEN 0 THEN substr(CAST(receivedTransfersJson AS BLOB), :offset + 1, :maximum) WHEN 1 THEN substr(CAST(savedFileIdsJson AS BLOB), :offset + 1, :maximum) ELSE substr(CAST(savedTransferIdsJson AS BLOB), :offset + 1, :maximum) END FROM inbox_legacy_checkpoint WHERE slotId=:id AND scope=:scope"
    )
    suspend fun checkpointLegacyChunk(
        id: String,
        scope: String,
        source: Int,
        offset: Long,
        maximum: Int,
    ): ByteArray?
}

/** Caller owns the Room transaction. Changes to counters are exact old/new child deltas. */
internal suspend fun TransferHistoryDao.mergeCheckpointChild(
    parent: TransferHistoryEntity,
    incoming: InboxChildCheckpoint,
    importing: Boolean = false,
): TransferHistoryEntity {
    val old = checkpointChild(incoming.scope, incoming.slotId, incoming.childId)
    val next =
        incoming.copy(
            fileCount = old?.fileCount?.takeIf { it > 0 } ?: incoming.fileCount,
            plaintextSize =
                if (importing) old?.plaintextSize ?: incoming.plaintextSize
                else incoming.plaintextSize ?: old?.plaintextSize,
            saved = incoming.saved || old?.saved == true,
            savedFiles = maxOf(incoming.savedFiles, old?.savedFiles ?: 0),
        )
    require(next.fileCount in 0..100 && next.savedFiles in 0..100 && (next.plaintextSize ?: 0) >= 0)
    putCheckpointChild(next)
    return parent.copy(
        checkpointKnownFiles =
            Math.addExact(
                parent.checkpointKnownFiles,
                next.fileCount.toLong() - (old?.fileCount ?: 0),
            ),
        checkpointKnownBytes =
            Math.addExact(
                parent.checkpointKnownBytes,
                (next.plaintextSize ?: 0) - (old?.plaintextSize ?: 0),
            ),
        checkpointSavedFiles =
            Math.addExact(parent.checkpointSavedFiles, next.savedCount - (old?.savedCount ?: 0)),
    )
}

internal suspend fun TransferHistoryDao.insertSavedCheckpoint(
    parent: TransferHistoryEntity,
    child: String,
    blob: String,
    uri: String? = null,
): TransferHistoryEntity {
    val scope = parent.checkpointScope()
    if (checkpointFile(scope, parent.id, child, blob) != null) return parent
    val old =
        checkpointChild(scope, parent.id, child) ?: InboxChildCheckpoint(scope, parent.id, child)
    require(old.savedFiles < 100)
    if (insertCheckpointFile(InboxFileCheckpoint(scope, parent.id, child, blob, uri)) == -1L)
        return parent
    return mergeCheckpointChild(parent, old.copy(savedFiles = Math.addExact(old.savedFiles, 1)))
}

internal fun checkpointComponent(id: String) = id.matches(Regex("[A-Za-z0-9_-]{1,128}"))

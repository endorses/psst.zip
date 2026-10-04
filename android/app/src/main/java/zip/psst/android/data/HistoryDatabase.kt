package zip.psst.android.data

import android.content.Context
import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Index
import androidx.room.Insert
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.room.Transaction
import androidx.room.Update
import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase
import zip.psst.shared.model.Transfer
import kotlinx.coroutines.flow.Flow

@Entity(
    tableName = "transfer_history",
    indices = [Index(value = ["accountId", "originScope", "createdAt", "id"])],
)
data class TransferHistoryEntity(
    @PrimaryKey val id: String,
    val type: String, // "sent" or "received"
    val fileCount: Int,
    val totalSize: Long,
    val serverUrl: String,
    val encryptionKey: String, // base64url-encoded key
    val status: String, // "pending", "complete", "expired"
    val createdAt: Long = System.currentTimeMillis(),
    val expiresAt: Long? = null,
    val deletionToken: String? = null,
    val accountId: String? = null,
    val title: String? = null,
    val automaticTitle: String? = null,
    @ColumnInfo(defaultValue = "''") val originScope: String = localHistoryScope(serverUrl),
    @ColumnInfo(defaultValue = "0") val summaryUpdating: Boolean = false,
    @ColumnInfo(defaultValue = "'pending'") val checkpointState: String = "ready",
    @ColumnInfo(defaultValue = "0") val checkpointColumn: Int = 0,
    @ColumnInfo(defaultValue = "0") val checkpointOffset: Long = 0,
    @ColumnInfo(defaultValue = "0") val checkpointKnownFiles: Long = 0,
    @ColumnInfo(defaultValue = "0") val checkpointKnownBytes: Long = 0,
    @ColumnInfo(defaultValue = "0") val checkpointSavedFiles: Long = 0,
    @ColumnInfo(defaultValue = "'[]'") val savedFileIdsJson: String = "[]",
    @ColumnInfo(defaultValue = "'{}'") val receivedTransfersJson: String = "{}",
    @ColumnInfo(defaultValue = "'[]'") val savedTransferIdsJson: String = "[]",
)

@Dao
interface TransferHistoryDao : InboxCheckpointQueries {
    @Query(ACCOUNT_LOCAL_PAGE_SQL)
    fun observeLocalPage(
        accountId: String,
        originScope: String,
        beforeTime: Long,
        beforeId: String,
    ): Flow<List<TransferHistoryEntity>>

    @Query("SELECT EXISTS(SELECT 1 FROM transfer_history WHERE accountId IS NULL LIMIT 1)")
    fun hasLegacy(): Flow<Boolean>

    @Query(
        "SELECT $HISTORY_METADATA_PROJECTION FROM transfer_history WHERE id IN (:ids) AND accountId = :accountId AND lower(rtrim(serverUrl, '/')) = lower(rtrim(:serverUrl, '/')) ORDER BY createdAt DESC, id DESC"
    )
    fun observePage(
        ids: List<String>,
        serverUrl: String,
        accountId: String,
    ): Flow<List<TransferHistoryEntity>>

    @Insert suspend fun insert(entity: TransferHistoryEntity)

    @Query("DELETE FROM transfer_history WHERE id = :id") suspend fun delete(id: String)

    @Transaction
    suspend fun updateStatus(id: String, status: String) {
        getById(id)?.let { archiveCheckpoints(it) }
        writeStatus(id, status)
    }

    @Query("UPDATE transfer_history SET status = :status WHERE id = :id")
    suspend fun writeStatus(id: String, status: String)

    @Transaction
    suspend fun update(entity: TransferHistoryEntity) {
        archiveCheckpoints(entity)
        updateMetadata(entity.metadata())
    }

    suspend fun archiveCheckpoints(entity: TransferHistoryEntity) {
        if (entity.checkpointState != "ready") {
            archiveCheckpointSource(
                entity.id,
                entity.checkpointScope(),
                entity.accountId,
                entity.originScope,
            )
            clearArchivedCheckpointSource(entity.id, entity.checkpointScope())
        }
    }

    @Update(entity = TransferHistoryEntity::class)
    suspend fun updateMetadata(entity: TransferHistoryMetadata)

    @Query(
        "UPDATE transfer_history SET automaticTitle = :title WHERE id = :id AND automaticTitle IS NULL"
    )
    suspend fun writeTitleIfEmpty(id: String, title: String)

    @Query(
        "UPDATE transfer_history SET title = :title WHERE id = :id AND serverUrl = :serverUrl AND accountId = :accountId AND type = :type"
    )
    suspend fun writeRename(
        id: String,
        serverUrl: String,
        accountId: String,
        type: String,
        title: String?,
    )

    @Transaction
    suspend fun setTitleIfEmpty(id: String, title: String) {
        getById(id)?.let { archiveCheckpoints(it) }
        writeTitleIfEmpty(id, title)
    }

    @Transaction
    suspend fun rename(
        id: String,
        serverUrl: String,
        accountId: String,
        type: String,
        title: String?,
    ) {
        val row = getById(id) ?: return
        if (row.serverUrl != serverUrl || row.accountId != accountId || row.type != type) return
        archiveCheckpoints(row)
        writeRename(id, serverUrl, accountId, type, title)
    }

    @Transaction
    suspend fun retryCheckpointMigration(id: String, scope: String) {
        val row = getById(id) ?: return
        require(row.checkpointScope() == scope)
        if (row.checkpointState == "recovery") update(row.copy(checkpointState = "pending"))
    }

    @Transaction
    suspend fun mergeAccountResource(incoming: TransferHistoryEntity, access: HistoryAccess) {
        val current = getById(incoming.id)
        val merged = zip.psst.android.data.mergeAccountResource(current, incoming, access) ?: return
        if (current == null) insert(merged) else if (merged != current) update(merged)
    }

    @Transaction
    suspend fun recordSavedFile(
        id: String,
        fileId: String,
        expectedScope: String? = null,
        uri: String? = null,
    ) {
        val row = getById(id) ?: return
        require(expectedScope == null || expectedScope == row.checkpointScope()) {
            "Inbox ownership changed"
        }
        require(row.checkpointState == "ready") { "Saved checkpoints are still being imported" }
        val parts = fileId.split('/')
        require(parts.size == 2 && parts.all(::checkpointComponent))
        update(insertSavedCheckpoint(row, parts[0], parts[1], uri))
    }

    @Transaction
    suspend fun mergeReceived(
        id: String,
        snapshot: ReceivedSnapshot,
        saved: Boolean = false,
        expectedScope: String? = null,
    ): TransferHistoryEntity? {
        require(snapshot.children.size <= 100)
        var row = getById(id) ?: return null
        require(expectedScope == null || expectedScope == row.checkpointScope()) {
            "Inbox ownership changed"
        }
        row = advanceCheckpointMigration(row)
        if (saved)
            require(row.checkpointState == "ready") { "Saved checkpoints are still being imported" }
        for ((childId, child) in snapshot.children) {
            require(checkpointComponent(childId))
            row =
                mergeCheckpointChild(
                    row,
                    InboxChildCheckpoint(
                        row.checkpointScope(),
                        id,
                        childId,
                        child.fileCount,
                        child.plaintextSize,
                        saved,
                    ),
                )
        }
        val count =
            if (snapshot.partial)
                snapshot.completedFiles?.coerceAtMost(Int.MAX_VALUE.toLong())?.toInt()
                    ?: row.fileCount
            else
                maxOf(
                    row.fileCount,
                    row.checkpointKnownFiles.coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
                )
        row =
            row.copy(
                fileCount = count,
                summaryUpdating =
                    if (snapshot.summaryObserved) snapshot.completedFiles == null
                    else row.summaryUpdating,
                totalSize = maxOf(row.totalSize, row.checkpointKnownBytes),
                expiresAt = snapshot.expiresAt ?: row.expiresAt,
                status =
                    when {
                        count > 0 || snapshot.children.isNotEmpty() -> "has_uploads"
                        row.status == "complete" -> "waiting"
                        else -> row.status
                    },
            )
        update(row)
        return row
    }

    suspend fun savedChildren(row: TransferHistoryEntity, ids: List<String>): Set<String> {
        require(ids.size <= 100)
        if (row.checkpointState != "ready") return emptySet()
        return checkpointSavedChildren(row.checkpointScope(), row.id, ids).toSet()
    }

    suspend fun savedFiles(row: TransferHistoryEntity, ids: List<String>): Set<String> {
        require(ids.size <= 100)
        require(row.checkpointState == "ready") { "Saved checkpoints are still being imported" }
        return checkpointSavedFiles(row.checkpointScope(), row.id, ids).toSet()
    }

    @Transaction
    suspend fun mergeSent(id: String, transfer: Transfer): TransferHistoryEntity? {
        val current = getById(id) ?: return null
        val updated = mergeSentHistory(current, transfer)
        if (updated != current) update(updated)
        return updated
    }

    @Query("SELECT $HISTORY_METADATA_PROJECTION FROM transfer_history WHERE id = :id")
    suspend fun getById(id: String): TransferHistoryEntity?
}

@Database(
    entities =
        [
            TransferHistoryEntity::class,
            InboxChildCheckpoint::class,
            InboxFileCheckpoint::class,
            InboxLegacyCheckpoint::class,
        ],
    version = 9,
    exportSchema = false,
)
abstract class AppDatabase : RoomDatabase() {
    abstract fun transferHistoryDao(): TransferHistoryDao

    companion object {
        val MIGRATION_8_9 =
            object : Migration(8, 9) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    INBOX_CHECKPOINT_SCHEMA.forEach(db::execSQL)
                }
            }

        val MIGRATION_7_8 =
            object : Migration(7, 8) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL(ACCOUNT_SCOPE_COLUMN_SQL)
                    db.execSQL(ACCOUNT_SCOPE_BACKFILL_SQL)
                    db.execSQL(ACCOUNT_PAGE_INDEX_SQL)
                }
            }

        val MIGRATION_6_7 =
            object : Migration(6, 7) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL(
                        "ALTER TABLE transfer_history ADD COLUMN summaryUpdating INTEGER NOT NULL DEFAULT 0"
                    )
                }
            }

        val MIGRATION_1_2 =
            object : Migration(1, 2) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL(
                        "ALTER TABLE transfer_history ADD COLUMN receivedTransfersJson TEXT NOT NULL DEFAULT '{}'"
                    )
                    db.execSQL(
                        "ALTER TABLE transfer_history ADD COLUMN savedTransferIdsJson TEXT NOT NULL DEFAULT '[]'"
                    )
                }
            }

        val MIGRATION_2_3 =
            object : Migration(2, 3) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL("ALTER TABLE transfer_history ADD COLUMN deletionToken TEXT")
                }
            }

        val MIGRATION_3_4 =
            object : Migration(3, 4) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL("ALTER TABLE transfer_history ADD COLUMN accountId TEXT")
                }
            }

        val MIGRATION_4_5 =
            object : Migration(4, 5) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL("ALTER TABLE transfer_history ADD COLUMN title TEXT")
                    db.execSQL(
                        "ALTER TABLE transfer_history ADD COLUMN savedFileIdsJson TEXT NOT NULL DEFAULT '[]'"
                    )
                }
            }

        val MIGRATION_5_6 =
            object : Migration(5, 6) {
                override fun migrate(db: SupportSQLiteDatabase) {
                    db.execSQL("ALTER TABLE transfer_history ADD COLUMN automaticTitle TEXT")
                    db.execSQL("UPDATE transfer_history SET automaticTitle = title")
                }
            }

        fun create(context: Context): AppDatabase {
            return Room.databaseBuilder(
                    context.applicationContext,
                    AppDatabase::class.java,
                    "psst-history.db",
                )
                .addMigrations(
                    MIGRATION_1_2,
                    MIGRATION_2_3,
                    MIGRATION_3_4,
                    MIGRATION_4_5,
                    MIGRATION_5_6,
                    MIGRATION_6_7,
                    MIGRATION_7_8,
                    MIGRATION_8_9,
                )
                .build()
        }
    }
}

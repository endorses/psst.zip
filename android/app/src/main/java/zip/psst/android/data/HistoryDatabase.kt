package zip.psst.android.data

import android.content.Context
import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
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

@Entity(tableName = "transfer_history")
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
    @ColumnInfo(defaultValue = "0") val summaryUpdating: Boolean = false,
    @ColumnInfo(defaultValue = "'[]'") val savedFileIdsJson: String = "[]",
    @ColumnInfo(defaultValue = "'{}'") val receivedTransfersJson: String = "{}",
    @ColumnInfo(defaultValue = "'[]'") val savedTransferIdsJson: String = "[]",
)

@Dao
interface TransferHistoryDao {
    @Query("SELECT * FROM transfer_history ORDER BY createdAt DESC")
    fun getAll(): Flow<List<TransferHistoryEntity>>

    @Query(
        "SELECT * FROM transfer_history WHERE id IN (:ids) AND accountId = :accountId AND lower(rtrim(serverUrl, '/')) = lower(rtrim(:serverUrl, '/')) ORDER BY createdAt DESC, id DESC"
    )
    fun observePage(
        ids: List<String>,
        serverUrl: String,
        accountId: String,
    ): Flow<List<TransferHistoryEntity>>

    @Insert suspend fun insert(entity: TransferHistoryEntity)

    @Query("DELETE FROM transfer_history WHERE id = :id") suspend fun delete(id: String)

    @Query("UPDATE transfer_history SET status = :status WHERE id = :id")
    suspend fun updateStatus(id: String, status: String)

    @Update suspend fun update(entity: TransferHistoryEntity)

    @Query(
        "UPDATE transfer_history SET automaticTitle = :title WHERE id = :id AND automaticTitle IS NULL"
    )
    suspend fun setTitleIfEmpty(id: String, title: String)

    @Query(
        "UPDATE transfer_history SET title = :title WHERE id = :id AND serverUrl = :serverUrl AND accountId = :accountId AND type = :type"
    )
    suspend fun rename(
        id: String,
        serverUrl: String,
        accountId: String,
        type: String,
        title: String?,
    )

    @Transaction
    suspend fun mergeAccountResource(
        incoming: TransferHistoryEntity,
        access: HistoryAccess,
        snapshot: ReceivedSnapshot? = null,
    ) {
        val current = getById(incoming.id)
        val merged =
            zip.psst.android.data.mergeAccountResource(current, incoming, access, snapshot) ?: return
        if (current == null) insert(merged) else if (merged != current) update(merged)
    }

    @Transaction
    suspend fun recordSavedFile(id: String, fileId: String) {
        val current = getById(id) ?: return
        val saved =
            kotlinx.serialization.json.Json.decodeFromString<Set<String>>(current.savedFileIdsJson)
        update(
            current.copy(
                savedFileIdsJson =
                    kotlinx.serialization.json.Json.encodeToString(
                        kotlinx.serialization.builtins.SetSerializer(
                            kotlinx.serialization.serializer<String>()
                        ),
                        saved + fileId,
                    )
            )
        )
    }

    @Transaction
    suspend fun mergeReceived(
        id: String,
        snapshot: ReceivedSnapshot,
        saved: Boolean = false,
    ): TransferHistoryEntity? {
        val current = getById(id) ?: return null
        val updated = mergeReceivedHistory(current, snapshot, saved)
        if (updated != current) update(updated)
        return updated
    }

    @Transaction
    suspend fun mergeSent(id: String, transfer: Transfer): TransferHistoryEntity? {
        val current = getById(id) ?: return null
        val updated = mergeSentHistory(current, transfer)
        if (updated != current) update(updated)
        return updated
    }

    @Query("SELECT * FROM transfer_history WHERE id = :id")
    suspend fun getById(id: String): TransferHistoryEntity?
}

@Database(entities = [TransferHistoryEntity::class], version = 7, exportSchema = false)
abstract class AppDatabase : RoomDatabase() {
    abstract fun transferHistoryDao(): TransferHistoryDao

    companion object {
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
                )
                .build()
        }
    }
}

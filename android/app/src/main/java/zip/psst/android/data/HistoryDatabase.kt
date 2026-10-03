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
    @ColumnInfo(defaultValue = "'{}'") val receivedTransfersJson: String = "{}",
    @ColumnInfo(defaultValue = "'[]'") val savedTransferIdsJson: String = "[]",
)

@Dao
interface TransferHistoryDao {
    @Query("SELECT * FROM transfer_history ORDER BY createdAt DESC")
    fun getAll(): Flow<List<TransferHistoryEntity>>

    @Insert suspend fun insert(entity: TransferHistoryEntity)

    @Query("DELETE FROM transfer_history WHERE id = :id") suspend fun delete(id: String)

    @Query("UPDATE transfer_history SET status = :status WHERE id = :id")
    suspend fun updateStatus(id: String, status: String)

    @Update suspend fun update(entity: TransferHistoryEntity)

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

@Database(entities = [TransferHistoryEntity::class], version = 2, exportSchema = false)
abstract class AppDatabase : RoomDatabase() {
    abstract fun transferHistoryDao(): TransferHistoryDao

    companion object {
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

        fun create(context: Context): AppDatabase {
            return Room.databaseBuilder(
                    context.applicationContext,
                    AppDatabase::class.java,
                    "psst-history.db",
                )
                .addMigrations(MIGRATION_1_2)
                .build()
        }
    }
}

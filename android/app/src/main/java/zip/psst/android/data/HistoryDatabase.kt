package zip.psst.android.data

import android.content.Context
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
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
)

@Dao
interface TransferHistoryDao {
    @Query("SELECT * FROM transfer_history ORDER BY createdAt DESC")
    fun getAll(): Flow<List<TransferHistoryEntity>>

    @Insert
    suspend fun insert(entity: TransferHistoryEntity)

    @Query("DELETE FROM transfer_history WHERE id = :id")
    suspend fun delete(id: String)

    @Query("UPDATE transfer_history SET status = :status WHERE id = :id")
    suspend fun updateStatus(id: String, status: String)

    @Query("SELECT * FROM transfer_history WHERE id = :id")
    suspend fun getById(id: String): TransferHistoryEntity?
}

@Database(entities = [TransferHistoryEntity::class], version = 1, exportSchema = false)
abstract class AppDatabase : RoomDatabase() {
    abstract fun transferHistoryDao(): TransferHistoryDao

    companion object {
        fun create(context: Context): AppDatabase {
            return Room.databaseBuilder(
                context.applicationContext,
                AppDatabase::class.java,
                "psst-history.db",
            ).build()
        }
    }
}

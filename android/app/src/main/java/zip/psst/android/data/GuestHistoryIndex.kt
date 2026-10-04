package zip.psst.android.data

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import java.io.InputStream

internal const val GUEST_RECORD_LIMIT = 512 * 1024

/** A corrupt legacy record must not allocate an unbounded buffer or prevent later imports. */
internal fun InputStream.readGuestRecord(): String {
    val output = java.io.ByteArrayOutputStream()
    val buffer = ByteArray(8192)
    while (true) {
        val count = read(buffer, 0, minOf(buffer.size, GUEST_RECORD_LIMIT + 1 - output.size()))
        if (count < 0) return output.toString(Charsets.UTF_8.name())
        output.write(buffer, 0, count)
        require(output.size() <= GUEST_RECORD_LIMIT) {
            "Local transfer metadata exceeds the supported record size; its original file is retained"
        }
    }
}

internal data class GuestIndexRow(val identity: String, val createdAt: Long, val payload: String?)

/** SQLite serializes writes across store instances; import never overwrites a newer checkpoint. */
internal class GuestHistoryIndex(context: Context) :
    SQLiteOpenHelper(context, "guest-history.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        GUEST_HISTORY_SCHEMA.forEach(db::execSQL)
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) =
        error("Unsupported guest history schema")

    fun contains(kind: String, id: String): Boolean =
        readableDatabase
            .rawQuery("SELECT 1 FROM records WHERE kind=? AND identity=?", arrayOf(kind, id))
            .use { it.moveToFirst() }

    fun isDeleted(kind: String, id: String): Boolean =
        readableDatabase
            .rawQuery("SELECT deleted FROM records WHERE kind=? AND identity=?", arrayOf(kind, id))
            .use { it.moveToFirst() && it.getInt(0) != 0 }

    fun importComplete(): Boolean =
        readableDatabase.rawQuery("SELECT complete FROM migration WHERE id=1", emptyArray()).use {
            it.moveToFirst() && it.getInt(0) != 0
        }

    fun finishImport() {
        writableDatabase.execSQL("UPDATE migration SET complete=1 WHERE id=1")
    }

    fun read(kind: String, id: String): String? =
        readableDatabase
            .rawQuery(
                "SELECT payload FROM records WHERE kind=? AND identity=? AND deleted=0",
                arrayOf(kind, id),
            )
            .use { if (it.moveToFirst() && !it.isNull(0)) it.getString(0) else null }

    fun put(
        kind: String,
        id: String,
        payload: String?,
        createdAt: Long = 0,
        pending: Boolean = false,
        importing: Boolean = false,
    ) {
        require(payload == null || payload.toByteArray(Charsets.UTF_8).size <= GUEST_RECORD_LIMIT) {
            "Local transfer metadata is too large"
        }
        writableDatabase.execSQL(
            if (importing) GUEST_IMPORT_SQL else GUEST_SAVE_SQL,
            arrayOf<Any?>(
                kind,
                id,
                createdAt,
                if (pending) 1 else 0,
                if (payload == null) 1 else 0,
                payload,
            ),
        )
    }

    fun tombstone(kind: String, id: String) {
        writableDatabase.execSQL(GUEST_DELETE_SQL, arrayOf(kind, id))
    }

    fun page(after: LocalHistoryCursor?): List<GuestIndexRow> {
        val args =
            if (after == null) emptyArray() else arrayOf(after.createdAt.toString(), after.id)
        return readableDatabase
            .rawQuery(if (after == null) GUEST_FIRST_PAGE_SQL else GUEST_NEXT_PAGE_SQL, args)
            .use { cursor ->
                buildList {
                    while (cursor.moveToNext()) add(
                        GuestIndexRow(
                            cursor.getString(0),
                            cursor.getLong(1),
                            if (cursor.isNull(2)) null else cursor.getString(2),
                        )
                    )
                }
            }
    }

    fun queue(kind: String, after: String?, pendingOnly: Boolean = false): List<GuestIndexRow> {
        val pending = if (pendingOnly) " AND pending=1" else " AND pending=0"
        return readableDatabase
            .rawQuery(
                "SELECT identity, created_at, payload FROM records WHERE kind=? AND deleted=0 AND failed=0$pending AND identity>? ORDER BY identity LIMIT 20",
                arrayOf(kind, after.orEmpty()),
            )
            .use { cursor ->
                buildList {
                    while (cursor.moveToNext()) add(
                        GuestIndexRow(
                            cursor.getString(0),
                            cursor.getLong(1),
                            if (cursor.isNull(2)) null else cursor.getString(2),
                        )
                    )
                }
            }
    }

    fun hasPending(kind: String, pendingOnly: Boolean = false): Boolean {
        val pending = if (pendingOnly) " AND pending=1" else " AND pending=0"
        return readableDatabase
            .rawQuery(
                "SELECT 1 FROM records WHERE kind=? AND deleted=0 AND failed=0$pending AND payload IS NOT NULL LIMIT 1",
                arrayOf(kind),
            )
            .use { it.moveToFirst() }
    }

    fun hasImportErrors(): Boolean =
        readableDatabase
            .rawQuery("SELECT 1 FROM records WHERE failed=1 AND deleted=0 LIMIT 1", emptyArray())
            .use { it.moveToFirst() }

    fun transaction(action: () -> Unit) {
        val db = writableDatabase
        db.beginTransaction()
        try {
            action()
            db.setTransactionSuccessful()
        } finally {
            db.endTransaction()
        }
    }
}

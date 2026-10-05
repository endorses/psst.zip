package zip.psst.android.data

import android.content.ContentValues
import android.database.sqlite.SQLiteDatabase
import android.os.Build
import android.os.Process
import android.util.AtomicFile
import androidx.room.Room
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.shared.model.FileMetadata
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Run each prepare/resume pair in separate `am instrument` invocations with a force-stop between.
 */
@RunWith(AndroidJUnit4::class)
class StorageMigrationDeviceTest {
    private val context
        get() = InstrumentationRegistry.getInstrumentation().targetContext

    private val prefs
        get() = context.getSharedPreferences("migration-device-test", 0)

    private val slot = "11111111-1111-4111-8111-111111111111"
    private val origin = "https://migration.invalid"
    private val account = "owner-a"
    private val key = ByteArray(32) { (it + 1).toByte() }
    private val roomName = "instrumented-history-migration.db"
    private val marker =
        "v2." +
            android.util.Base64.encodeToString(
                ByteArray(32) { 9 },
                android.util.Base64.URL_SAFE or
                    android.util.Base64.NO_PADDING or
                    android.util.Base64.NO_WRAP,
            )
    private val json = Json { encodeDefaults = true }

    @Before
    fun requireDisposableEmulator() {
        check(InstrumentationRegistry.getArguments().getString("disposableStorage") == "true") {
            "Explicit disposableStorage=true is required"
        }
        check(Build.HARDWARE in setOf("ranchu", "goldfish")) {
            "Storage fixtures may run only on a disposable emulator"
        }
    }

    private fun room() =
        Room.databaseBuilder(context, AppDatabase::class.java, roomName)
            .addMigrations(
                AppDatabase.MIGRATION_7_8,
                AppDatabase.MIGRATION_8_9,
                AppDatabase.MIGRATION_9_10,
            )
            .build()

    private suspend fun withRoom(block: suspend (AppDatabase) -> Unit) {
        val db = room()
        try {
            block(db)
        } finally {
            db.close()
        }
    }

    @Test
    fun prepareRoomMigration() = runBlocking {
        context.deleteDatabase(roomName)
        val children = (0 until 130).associate { "child-$it" to ReceivedChild(2, it.toLong() + 1) }
        val childJson = json.encodeToString(children)
        val fileJson = json.encodeToString(children.keys.map { "$it/blob" })
        val savedJson = json.encodeToString(children.keys)
        val path = context.getDatabasePath(roomName).apply { parentFile!!.mkdirs() }
        SQLiteDatabase.openOrCreateDatabase(path, null).use { db ->
            db.execSQL(
                "CREATE TABLE transfer_history(id TEXT NOT NULL PRIMARY KEY,type TEXT NOT NULL,fileCount INTEGER NOT NULL,totalSize INTEGER NOT NULL,serverUrl TEXT NOT NULL,encryptionKey TEXT NOT NULL,status TEXT NOT NULL,createdAt INTEGER NOT NULL,expiresAt INTEGER,deletionToken TEXT,accountId TEXT,title TEXT,automaticTitle TEXT,savedFileIdsJson TEXT NOT NULL DEFAULT '[]',receivedTransfersJson TEXT NOT NULL DEFAULT '{}',savedTransferIdsJson TEXT NOT NULL DEFAULT '[]',summaryUpdating INTEGER NOT NULL DEFAULT 0)"
            )
            fun insert(id: String, owner: String, server: String) {
                val values =
                    ContentValues().apply {
                        put("id", id)
                        put("type", "received")
                        put("fileCount", 260)
                        put("totalSize", 0)
                        put("serverUrl", server)
                        put("encryptionKey", marker)
                        put("status", "complete")
                        put("createdAt", 1000)
                        put("accountId", owner)
                        put("title", "Retained local title")
                        put("deletionToken", "retained-owner-capability")
                        put("receivedTransfersJson", childJson)
                        put("savedFileIdsJson", fileJson)
                        put("savedTransferIdsJson", savedJson)
                    }
                db.insertOrThrow("transfer_history", null, values)
            }
            insert(slot, account, origin)
            insert("22222222-2222-4222-8222-222222222222", "owner-b", origin)
            insert("33333333-3333-4333-8333-333333333333", account, "https://other.invalid")
            db.version = 7
        }
        InboxKeyStore(context).save(origin, account, slot, key)
        withRoom { db ->
            val dao = db.transferHistoryDao()
            val upgraded =
                requireNotNull(dao.getById(slot)) // Room validates the actual 7→8→9 schema.
            assertEquals(10, db.openHelper.writableDatabase.version)
            assertEquals("pending", upgraded.checkpointState)
            assertEquals("{}", upgraded.receivedTransfersJson)
            assertEquals(marker, upgraded.encryptionKey)
            assertArrayEquals(key, InboxKeyStore(context).read(upgraded))
            val partial =
                requireNotNull(
                    dao.mergeReceived(
                        slot,
                        ReceivedSnapshot(emptyMap(), partial = true),
                        expectedScope = upgraded.checkpointScope(),
                    )
                )
            assertEquals("pending", partial.checkpointState)
            assertEquals(128L, partial.checkpointKnownFiles)
            assertTrue(partial.checkpointOffset > 0)
            db.openHelper.readableDatabase
                .query(
                    "SELECT receivedTransfersJson FROM inbox_legacy_checkpoint WHERE slotId=?",
                    arrayOf(slot),
                )
                .use {
                    assertTrue(it.moveToFirst())
                    assertEquals(childJson, it.getString(0))
                }
            db.openHelper.readableDatabase
                .query(
                    "SELECT receivedTransfersJson FROM transfer_history WHERE id=?",
                    arrayOf(slot),
                )
                .use {
                    assertTrue(it.moveToFirst())
                    assertEquals("{}", it.getString(0))
                }
            assertTrue(
                prefs
                    .edit()
                    .putInt("roomPid", Process.myPid())
                    .putLong("roomOffset", partial.checkpointOffset)
                    .commit()
            )
        }
    }

    @Test
    fun resumeRoomMigration() = runBlocking {
        assertTrue("Run prepareRoomMigration first", prefs.contains("roomPid"))
        assertNotEquals(
            "A real app process restart is required",
            prefs.getInt("roomPid", 0),
            Process.myPid(),
        )
        withRoom { db ->
            val dao = db.transferHistoryDao()
            var row = requireNotNull(dao.getById(slot))
            assertEquals(prefs.getLong("roomOffset", -1), row.checkpointOffset)
            repeat(10) {
                if (row.checkpointState == "pending")
                    row =
                        requireNotNull(
                            dao.mergeReceived(
                                slot,
                                ReceivedSnapshot(emptyMap(), partial = true),
                                expectedScope = row.checkpointScope(),
                            )
                        )
            }
            assertEquals("ready", row.checkpointState)
            assertEquals(260L, row.checkpointKnownFiles)
            assertEquals(8515L, row.checkpointKnownBytes)
            assertEquals(260L, row.checkpointSavedFiles)
            assertEquals("Retained local title", row.title)
            assertEquals("retained-owner-capability", row.deletionToken)
            assertEquals(setOf("child-129"), dao.savedChildren(row, listOf("child-129")))
            assertEquals(setOf("child-129/blob"), dao.savedFiles(row, listOf("child-129")))
            assertTrue(
                dao.savedChildren(row.copy(accountId = "owner-b"), listOf("child-129")).isEmpty()
            )
            assertTrue(
                dao.savedFiles(row.copy(originScope = "https://other.invalid"), listOf("child-129"))
                    .isEmpty()
            )
            assertEquals(
                listOf(slot),
                dao.observeLocalPage(account, origin, Long.MAX_VALUE, "\uffff").first().map {
                    it.id
                },
            )
            assertArrayEquals(key, InboxKeyStore(context).read(row))
            assertNull(InboxKeyStore(context).read(row.copy(accountId = "owner-b")))
            assertNull(InboxKeyStore(context).read(row.copy(serverUrl = "https://other.invalid")))
            val output =
                File(context.filesDir, "instrumented-owner-saved.txt").apply {
                    writeText("owner saved bytes")
                }
            val uri = android.net.Uri.fromFile(output).toString()
            repeat(2) { dao.recordSavedFile(slot, "fresh/blob", row.checkpointScope(), uri) }
            dao.mergeReceived(
                slot,
                ReceivedSnapshot(
                    mapOf("fresh" to ReceivedChild(1, output.length())),
                    partial = true,
                ),
                saved = true,
                expectedScope = row.checkpointScope(),
            )
            row = requireNotNull(dao.getById(slot))
            assertEquals(261L, row.checkpointSavedFiles)
            assertEquals(uri, dao.checkpointFile(row.checkpointScope(), slot, "fresh", "blob")?.uri)
            assertEquals("owner saved bytes", output.readText())
            checkArchiveRollback(db)
        }
        withRoom { db ->
            val row = requireNotNull(db.transferHistoryDao().getById(slot))
            assertEquals(261L, row.checkpointSavedFiles)
            assertArrayEquals(key, InboxKeyStore(context).read(row))
        }
    }

    private suspend fun checkArchiveRollback(db: AppDatabase) {
        val id = "44444444-4444-4444-8444-444444444444"
        val original = "{\"child\":{\"fileCount\":1}}"
        val row =
            TransferHistoryEntity(
                id,
                "received",
                1,
                0,
                origin,
                marker,
                "has_uploads",
                accountId = account,
                checkpointState = "pending",
                receivedTransfersJson = original,
            )
        val dao = db.transferHistoryDao()
        dao.insert(row)
        db.openHelper.writableDatabase.execSQL(
            "CREATE TRIGGER fail_checkpoint_clear BEFORE UPDATE OF receivedTransfersJson ON transfer_history WHEN OLD.id='$id' BEGIN SELECT RAISE(ABORT,'injected archive clear failure'); END"
        )
        var failed = false
        try {
            dao.mergeReceived(id, ReceivedSnapshot(emptyMap(), partial = true))
        } catch (_: android.database.SQLException) {
            failed = true
        }
        assertTrue("Injected clear failure must roll back archive insertion", failed)
        db.openHelper.readableDatabase
            .query("SELECT receivedTransfersJson FROM transfer_history WHERE id=?", arrayOf(id))
            .use {
                assertTrue(it.moveToFirst())
                assertEquals(original, it.getString(0))
            }
        db.openHelper.readableDatabase
            .query("SELECT slotId FROM inbox_legacy_checkpoint WHERE slotId=?", arrayOf(id))
            .use { assertFalse(it.moveToFirst()) }
        db.openHelper.writableDatabase.execSQL("DROP TRIGGER fail_checkpoint_clear")
        assertEquals(
            "ready",
            dao.mergeReceived(id, ReceivedSnapshot(emptyMap(), partial = true))?.checkpointState,
        )
    }

    private fun guestId(number: Int) = GuestDownloadStore.identity(origin, "guest-$number")

    private fun atomic(name: String, value: String) {
        val directory = File(context.filesDir, "guest-downloads").apply { mkdirs() }
        val file = AtomicFile(File(directory, name))
        val stream = file.startWrite()
        try {
            stream.write(value.toByteArray())
            stream.fd.sync()
            file.finishWrite(stream)
        } catch (e: Exception) {
            file.failWrite(stream)
            throw e
        }
    }

    private fun guestRecord(number: Int): GuestDownload {
        val output = File(context.filesDir, "instrumented-guest-saved.txt")
        val saved =
            SavedGuestFile(
                "blob",
                android.net.Uri.fromFile(output).toString(),
                output.name,
                output.length(),
                "text/plain",
                MessageDigest.getInstance("SHA-256").digest(output.readBytes()).joinToString("") {
                    "%02x".format(it)
                },
            )
        return GuestDownload(
            guestId(number),
            origin,
            "guest-$number",
            createdAt = 10000L - number,
            files =
                if (number == 0) listOf(FileMetadata(output.name, output.length(), blobId = "blob"))
                else emptyList(),
            saved = if (number == 0) listOf(saved) else emptyList(),
            complete = number == 0,
            receiptPending = number == 0,
        )
    }

    @Test
    fun prepareGuestMigration() {
        File(context.filesDir, "guest-downloads").deleteRecursively()
        context.deleteDatabase("guest-history.db")
        File(context.filesDir, "instrumented-guest-saved.txt").writeText("guest saved bytes")
        var store = GuestDownloadStore(context)
        store.open(origin, "guest-0", key)
        val upload = store.queueUpload(origin, "interrupted-upload", "encrypted-upload-capability")
        store.close()
        context.deleteDatabase(
            "guest-history.db"
        ) // Recreate exactly the pre-index metadata layout, preserving encrypted key files.
        repeat(140) { atomic("${guestId(it)}.json", json.encodeToString(guestRecord(it))) }
        val directory = File(context.filesDir, "guest-downloads")
        val main = File(directory, "${guestId(0)}.json")
        check(main.renameTo(File(main.path + ".bak"))) // AtomicFile crash-recovery layout.
        atomic(
            "${guestId(0)}.receipt",
            json.encodeToString(GuestReceipt(guestId(0), origin, "guest-0")),
        )
        atomic(
            "${guestId(141)}.receipt",
            json.encodeToString(GuestReceipt(guestId(141), origin, "guest-141")),
        )
        atomic("${upload.identity}.upload", json.encodeToString(upload))
        File(directory, "${guestId(142)}.json.new").writeText("incomplete temporary record")
        store = GuestDownloadStore(context)
        try {
            assertTrue(store.importLegacyBatch())
            assertTrue(store.page(null).importing)
            val mainRow = store.read(guestId(0))
            assertEquals(
                "guest saved bytes",
                File(android.net.Uri.parse(mainRow.saved.single().uri).path!!).readText(),
            )
            assertArrayEquals(key, store.readKey(guestId(0)))
            store.markReceiptSent(guestId(0))
            store.finishReceipt(guestId(0))
            atomic(
                "${guestId(0)}.receipt",
                json.encodeToString(GuestReceipt(guestId(0), origin, "guest-0")),
            ) // A stale legacy source must not undo a tombstone.
            store.read(guestId(1))
            store.remove(guestId(1))
            atomic("${guestId(1)}.json", json.encodeToString(guestRecord(1)))
            assertTrue(
                prefs
                    .edit()
                    .putInt("guestPid", Process.myPid())
                    .putString("uploadId", upload.identity)
                    .commit()
            )
        } finally {
            store.close()
        }
    }

    @Test
    fun resumeGuestMigration() {
        assertTrue("Run prepareGuestMigration first", prefs.contains("guestPid"))
        assertNotEquals(
            "A real app process restart is required",
            prefs.getInt("guestPid", 0),
            Process.myPid(),
        )
        GuestDownloadStore(context).let { store ->
            try {
                var more = true
                var batches = 0
                while (more) {
                    more = store.importLegacyBatch()
                    batches++
                    check(batches <= 10)
                }
                val main = store.read(guestId(0))
                assertFalse(main.receiptPending)
                assertTrue(main.complete)
                assertArrayEquals(key, store.readKey(guestId(0)))
                assertEquals(
                    "guest saved bytes",
                    File(android.net.Uri.parse(main.saved.single().uri).path!!).readText(),
                )
                assertEquals(setOf(guestId(141)), store.receipts().map { it.identity }.toSet())
                val upload = store.uploads().single()
                assertEquals(prefs.getString("uploadId", null), upload.identity)
                assertEquals(
                    "encrypted-upload-capability",
                    store.readKey(upload.identity).decodeToString(),
                )
                var cursor: LocalHistoryCursor? = null
                val seen = mutableSetOf<String>()
                var pages = 0
                do {
                    val page = store.page(cursor)
                    assertTrue(page.records.size <= 50)
                    assertFalse(page.importing)
                    page.records.forEach { assertTrue(seen.add(it.identity)) }
                    cursor = page.next
                    pages++
                    check(pages <= 5)
                } while (cursor != null)
                assertEquals(139, seen.size)
                assertFalse(guestId(1) in seen)
                assertTrue(File(context.filesDir, "guest-downloads/${guestId(139)}.json").exists())
                store.finishUpload(upload)
                assertTrue(store.uploads().isEmpty())
            } finally {
                store.close()
            }
        }
    }
}

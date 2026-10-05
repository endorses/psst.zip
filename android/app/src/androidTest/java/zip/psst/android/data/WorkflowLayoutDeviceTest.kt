package zip.psst.android.data

import android.os.Build
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.android.PsstApplication
import zip.psst.shared.model.FileMetadata
import java.io.File
import kotlinx.coroutines.runBlocking
import org.junit.Test
import org.junit.runner.RunWith

/** Seeds only a disposable emulator for manual UI checks; this does not test the wire protocol. */
@RunWith(AndroidJUnit4::class)
class WorkflowLayoutDeviceTest {
    @Test
    fun seedDisposableLayoutFixture() = runBlocking {
        val args = InstrumentationRegistry.getArguments()
        check(args.getString("disposableStorage") == "true")
        check(Build.HARDWARE in setOf("ranchu", "goldfish"))
        val origin = requireNotNull(args.getString("layoutFixtureOrigin"))
        check(origin.startsWith("http://10.0.2.2:"))
        val app =
            InstrumentationRegistry.getInstrumentation().targetContext.applicationContext
                as PsstApplication
        app.database.clearAllTables()
        app.deleteDatabase("guest-history.db")
        File(app.filesDir, "guest-downloads").deleteRecursively()
        app.prefs.saveSession(
            origin,
            "layout-owner",
            "layout-fixture-token",
            "layout-owner",
            "user",
        )
        app.prefs.setAppearance(Appearance.SYSTEM)
        // Instrumentation exits without an Activity lifecycle callback to flush apply().
        check(
            app.getSharedPreferences("psst_prefs", 0)
                .edit()
                .putString("server_url", origin)
                .putString("appearance", "system")
                .commit()
        )
        if (args.getString("emptyHistory") == "true") return@runBlocking
        val publicKey = ByteArray(32) { 9 }
        val marker =
            "v2." +
                android.util.Base64.encodeToString(
                    publicKey,
                    android.util.Base64.URL_SAFE or
                        android.util.Base64.NO_PADDING or
                        android.util.Base64.NO_WRAP,
                )
        val now = System.currentTimeMillis()
        val dao = app.database.transferHistoryDao()
        val slot = "11111111-1111-4111-8111-111111111111"
        InboxKeyStore(app).save(origin, "layout-owner", slot, ByteArray(32) { 7 })
        dao.insert(
            TransferHistoryEntity(
                slot,
                "received",
                12,
                1234,
                origin,
                marker,
                "has_uploads",
                createdAt = now - 1000,
                accountId = "layout-owner",
                sharedTitle = "Family photos inbox",
                checkpointKnownFiles = 12,
                checkpointKnownBytes = 1234,
            )
        )
        dao.insert(
            TransferHistoryEntity(
                "22222222-2222-4222-8222-222222222222",
                "sent",
                2,
                4200,
                origin,
                "layout-key",
                "complete",
                createdAt = now - 3000,
                accountId = "layout-owner",
                sharedTitle = "Trip documents",
            )
        )
        dao.insert(
            TransferHistoryEntity(
                "33333333-3333-4333-8333-333333333333",
                "sent",
                1,
                24,
                origin,
                "layout-key",
                "exhausted",
                createdAt = now - 5000,
                accountId = "layout-owner",
                sharedTitle = "Download limit reached example",
            )
        )
        GuestDownloadStore(app).let { store ->
            try {
                val row =
                    store.open(origin, "44444444-4444-4444-8444-444444444444", ByteArray(32) { 3 })
                store.save(
                    row.copy(
                        createdAt = now - 2000,
                        files = listOf(FileMetadata("photo.jpg", 2048, blobId = "blob")),
                        sharedTitle = "Downloaded shared title",
                    )
                )
            } finally {
                store.close()
            }
        }
    }
}

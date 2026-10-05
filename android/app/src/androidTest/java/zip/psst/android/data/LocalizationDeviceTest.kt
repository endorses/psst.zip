package zip.psst.android.data

import android.content.ContentValues
import android.content.Context
import android.content.res.Configuration
import android.os.LocaleList
import android.provider.MediaStore
import androidx.activity.compose.setContent
import androidx.appcompat.app.AppCompatDelegate
import androidx.core.os.LocaleListCompat
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.android.MainActivity
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.android.ui.screens.SendScreen
import zip.psst.android.ui.screens.SettingsScreen
import zip.psst.android.ui.theme.PsstTheme
import zip.psst.android.viewmodel.*
import java.io.File
import java.util.Locale
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

/** Runs on a disposable emulator; no network payload or allowance is consumed. */
@RunWith(AndroidJUnit4::class)
class LocalizationDeviceTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val app
        get() = instrumentation.targetContext.applicationContext as PsstApplication

    private fun context(language: String): Context =
        app.createConfigurationContext(
            Configuration(app.resources.configuration).apply {
                setLocales(LocaleList(Locale.forLanguageTag(language)))
            }
        )

    @Test
    fun nativeResourcesPluralCountsAndSafeErrorsAreBilingual() {
        val english = context("en-GB")
        val german = context("de-DE")
        assertEquals(
            "0 files selected",
            english.resources.getQuantityString(R.plurals.files_selected_count, 0, 0),
        )
        assertEquals(
            "0 Dateien ausgewählt",
            german.resources.getQuantityString(R.plurals.files_selected_count, 0, 0),
        )
        assertEquals(
            "1 file saved",
            pluralMessage(R.plurals.files_saved_short, 1, 1).resolve(english),
        )
        assertEquals(
            "1 Datei gespeichert",
            pluralMessage(R.plurals.files_saved_short, 1, 1).resolve(german),
        )
        assertEquals(
            "2 Dateien gespeichert",
            pluralMessage(R.plurals.files_saved_short, 2, 2).resolve(german),
        )
        val failure =
            failureText(
                zip.psst.shared.api.ClientFailureException(
                    "<html>SECRET</html>",
                    "invalid_credentials",
                )
            )
        assertNotEquals(failure.resolve(english), failure.resolve(german))
        assertFalse(failure.resolve(german).contains("SECRET"))
        assertEquals(
            "Meine Fotos 🐈.jpg + 2 Dateien",
            automaticHistoryTitle("Meine Fotos 🐈.jpg", 3)!!.resolve(german),
        )
        assertEquals(
            "1.234 von 2.345 Dateiplätzen belegt · unvollständige Uploads zählen mit",
            pluralMessage(R.plurals.policy_receive_usage, 2345, 1234, 2345).resolve(german),
        )
        assertEquals(
            "1,5 MiB",
            message(R.string.l_total_1_s_n_e4c487, UiByteCount(1536L * 1024))
                .resolve(german)
                .substringAfter(": ")
                .trim(),
        )
    }

    @Test
    fun nativeLocaleSwitchRetainsActivityJobsDraftsFailuresSessionAndCheckpoints() {
        val scenario = ActivityScenario.launch(MainActivity::class.java)
        val content = app.contentResolver
        val uri =
            content.insert(
                MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                ContentValues().apply {
                    put(MediaStore.MediaColumns.DISPLAY_NAME, "Meine Fotos 🐈.bin")
                    put(MediaStore.MediaColumns.MIME_TYPE, "application/octet-stream")
                    put(MediaStore.MediaColumns.RELATIVE_PATH, "Download/psst.zip")
                },
            )!!
        content.openOutputStream(uri)!!.use { it.write(byteArrayOf(7, 8, 9)) }
        var activity: MainActivity? = null
        lateinit var send: SendViewModel
        lateinit var receive: ReceiveViewModel
        lateinit var scan: ScanViewModel
        lateinit var login: ServerConfigViewModel
        lateinit var probe: LocaleJobProbe
        val preferences = app.getSharedPreferences("psst_prefs", 0)
        scenario.onActivity {
            activity = it
            val provider = ViewModelProvider(it)
            send = provider[SendViewModel::class.java]
            receive = provider[ReceiveViewModel::class.java]
            scan = provider[ScanViewModel::class.java]
            login = provider[ServerConfigViewModel::class.java]
            probe = provider[LocaleJobProbe::class.java]
            app.prefs.saveSession(
                "http://127.0.0.1:1",
                "locale-user",
                "locale-test-token",
                "locale-account",
                "user",
            )
        }
        instrumentation.waitForIdleSync()
        Thread.sleep(100)
        val preferencesBefore = preferences.all.toMap()
        val sessionBefore = app.getSharedPreferences("psst_device_session", 0).all.toMap()
        val accessBefore = app.prefs.historyAccess.value
        val checkpoint =
            TransferHistoryEntity(
                "localization-checkpoint",
                "sent",
                1,
                3,
                "http://127.0.0.1:1",
                "unchanged-key",
                "incomplete",
                accountId = "locale-account",
                savedFileIdsJson = "[\"saved\"]",
                sharedTitle = "Meine Fotos unchanged",
            )
        kotlinx.coroutines.runBlocking { app.database.transferHistoryDao().insert(checkpoint) }
        val persistedCheckpointBefore =
            kotlinx.coroutines.runBlocking {
                app.database.transferHistoryDao().getById(checkpoint.id)
            }
        try {
            scenario.onActivity {
                send.setSharedTitle("Meine Fotos 🐈 — unchanged")
                send.setDownloadLimitEnabled(true)
                send.setMaxDownloads("1234")
                send.addFiles(listOf(uri))
                receive.renameLocal("Empfangstitel unchanged")
                receive.setFileLimitEnabled(true)
                receive.setMaxFiles("456")
                login.onUsernameChange("unchanged-user")
                login.onPasswordChange("temporary-secret")
                login.onNewPasswordChange("replacement-secret")
                login.onConfirmPasswordChange("replacement-secret")
                scan.error(message(R.string.failure_invalid_credentials))
                scan.setInputDraft("https://unchanged.invalid/d/id#SECRET")
                login.setPairingDraft("unchanged-pairing-draft")
                AppCompatDelegate.setApplicationLocales(LocaleListCompat.forLanguageTags("en"))
            }
            instrumentation.waitForIdleSync()
            val sendBefore = send.uiState.value
            val receiveBefore = receive.uiState.value
            val loginBefore = login.uiState.value
            val scanBefore = scan.state.value
            val progressBefore = probe.ticks.get()
            scenario.onActivity {
                AppCompatDelegate.setApplicationLocales(LocaleListCompat.forLanguageTags("de"))
            }
            instrumentation.waitForIdleSync()
            Thread.sleep(200)
            scenario.onActivity {
                // Platform per-app locales may recreate the Activity; retained work must survive.
                assertSame(send, ViewModelProvider(it)[SendViewModel::class.java])
                assertSame(probe, ViewModelProvider(it)[LocaleJobProbe::class.java])
                assertEquals("de", it.resources.configuration.locales[0].language)
                assertEquals("de", AppCompatDelegate.getApplicationLocales()[0]!!.language)
                assertTrue(probe.ticks.get() > progressBefore)
                assertFalse(probe.cancelled)
                assertEquals(sendBefore, send.uiState.value)
                assertEquals(receiveBefore, receive.uiState.value)
                assertEquals(loginBefore, login.uiState.value)
                assertEquals(scanBefore, scan.state.value)
                assertEquals(
                    context("de").getString(R.string.failure_invalid_credentials),
                    scan.state.value.error!!.text(),
                )
                assertEquals(accessBefore, app.prefs.historyAccess.value)
                assertEquals(preferencesBefore, preferences.all.toMap())
                assertEquals(
                    sessionBefore,
                    app.getSharedPreferences("psst_device_session", 0).all.toMap(),
                )
                assertEquals("locale-test-token", app.prefs.getSessionToken())
            }
            kotlinx.coroutines.runBlocking {
                assertEquals(
                    persistedCheckpointBefore,
                    app.database.transferHistoryDao().getById(checkpoint.id),
                )
            }
        } finally {
            scenario.onActivity {
                AppCompatDelegate.setApplicationLocales(LocaleListCompat.getEmptyLocaleList())
            }
            scenario.close()
            content.delete(uri, null, null)
            kotlinx.coroutines.runBlocking {
                app.database.transferHistoryDao().delete(checkpoint.id)
            }
            app.prefs.clearSession()
            app.prefs.setServerUrl("")
            preferences.edit().clear().commit()
        }
    }

    @Test
    fun germanNarrowEnlargedSettingsAndSelectedFilesUseBothAppearances() {
        // Settle the OS locale before launching fixtures; each Activity hosts exactly one screen.
        ActivityScenario.launch(MainActivity::class.java).use { initial ->
            initial.onActivity {
                AppCompatDelegate.setApplicationLocales(LocaleListCompat.forLanguageTags("de"))
            }
            instrumentation.waitForIdleSync()
            Thread.sleep(750)
        }
        try {
            for (dark in listOf(false, true)) {
                for (settings in listOf(true, false)) {
                    ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                        scenario.onActivity { activity ->
                            activity.setContent {
                                PsstTheme(darkTheme = dark) {
                                    if (settings) SettingsScreen({}, {}, {}, {})
                                    else
                                        SendScreen(
                                            sharedUris = emptyList(),
                                            onBack = {},
                                            onSignIn = {},
                                            onTransferCreated = { _, _, _ -> },
                                        )
                                }
                            }
                        }
                        instrumentation.waitForIdleSync()
                        Thread.sleep(1000)
                        instrumentation.waitForIdleSync()
                        screenshot(
                            "${if(settings) "settings" else "send"}-de-${if(dark) "dark" else "light"}"
                        )
                        val labels =
                            accessibilityLabels(instrumentation.uiAutomation.rootInActiveWindow)
                        if (settings) {
                            assertTrue(labels.toString(), labels.any { it.contains("Sprache") })
                            assertTrue(labels.toString(), labels.any { it.contains("Deutsch") })
                        } else assertTrue(labels.toString(), labels.any { it.contains("Dateien") })
                    }
                }
            }
        } finally {
            instrumentation.runOnMainSync {
                AppCompatDelegate.setApplicationLocales(LocaleListCompat.getEmptyLocaleList())
            }
        }
    }

    private fun screenshot(name: String) {
        val image = instrumentation.uiAutomation.takeScreenshot() ?: error("No screenshot")
        val directory = File(app.filesDir, "localization-checks").apply { mkdirs() }
        File(directory, "$name.png").outputStream().use {
            image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
        }
        image.recycle()
    }

    private fun accessibilityLabels(
        node: android.view.accessibility.AccessibilityNodeInfo?
    ): List<String> {
        if (node == null) return emptyList()
        return listOfNotNull(node.text?.toString(), node.contentDescription?.toString()) +
            (0 until node.childCount).flatMap { accessibilityLabels(node.getChild(it)) }
    }
}

/** A live viewModelScope job verifies the locale change does not clear retained work. */
class LocaleJobProbe : ViewModel() {
    val ticks = AtomicInteger()
    var cancelled = false
        private set

    init {
        viewModelScope.launch {
            try {
                while (true) {
                    ticks.incrementAndGet()
                    delay(25)
                }
            } finally {
                cancelled = true
            }
        }
    }
}

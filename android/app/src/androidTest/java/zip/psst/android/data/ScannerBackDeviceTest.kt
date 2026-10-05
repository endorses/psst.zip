package zip.psst.android.data

import android.view.KeyEvent
import androidx.activity.compose.setContent
import androidx.lifecycle.ViewModelProvider
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.android.MainActivity
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.i18n.ScanStage
import zip.psst.android.ui.navigation.PsstNavGraph
import zip.psst.android.ui.navigation.Routes
import zip.psst.android.ui.theme.PsstTheme
import zip.psst.android.viewmodel.ScanViewModel
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.shared.model.UrlHelper
import java.net.ServerSocket
import java.net.Socket
import java.util.Collections
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

/** A server that accepts connections but never replies reproduces an unavailable LAN server. */
@RunWith(AndroidJUnit4::class)
class ScannerBackDeviceTest {
    @Test
    fun toolbarAndSystemBackLeaveScannerWithoutWaitingForUnavailableServer() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        // Connect accessibility before composing, so the semantics tree is enabled from launch.
        val automation = instrumentation.uiAutomation
        automation.serviceInfo =
            automation.serviceInfo.apply {
                flags =
                    flags or
                        android.accessibilityservice.AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS
            }
        val app = instrumentation.targetContext.applicationContext as PsstApplication
        val server = ServerSocket(0)
        val sockets = Collections.synchronizedList(mutableListOf<Socket>())
        val acceptor =
            Thread {
                    try {
                        while (!server.isClosed) sockets.add(server.accept())
                    } catch (_: java.io.IOException) {
                        /* Test teardown closes the listener. */
                    }
                }
                .apply {
                    isDaemon = true
                    start()
                }
        val origin = "http://127.0.0.1:${server.localPort}"
        app.prefs.saveSession(origin, "scanner-test", "test-token", "test-account", "user")
        lateinit var nav: NavHostController
        lateinit var guest: ScanViewModel
        var scenario: ActivityScenario<MainActivity>? = null
        try {
            scenario = ActivityScenario.launch(MainActivity::class.java)
            scenario.onActivity { activity ->
                guest = ViewModelProvider(activity)[ScanViewModel::class.java]
                activity.setContent {
                    PsstTheme {
                        nav = rememberNavController()
                        PsstNavGraph(nav, Routes.HOME, emptyList(), {})
                    }
                }
            }
            instrumentation.waitForIdleSync()
            Thread.sleep(1000)
            instrumentation.waitForIdleSync()
            for (inspectDownload in listOf(false, true)) {
                scenario.onActivity {
                    nav.navigate(Routes.SCAN)
                    if (inspectDownload) {
                        assertTrue(
                            guest.classify(
                                UrlHelper.buildDownloadUrl(
                                    origin,
                                    "01234567-89ab-cdef-0123-456789abcdef",
                                    ByteArray(32),
                                )
                            )
                        )
                        guest.receive()
                    }
                }
                instrumentation.waitForIdleSync()
                Thread.sleep(750)
                scenario.onActivity {
                    val account =
                        ViewModelProvider(nav.getBackStackEntry(Routes.SCAN))[
                            ServerConfigViewModel::class.java]
                    assertTrue(
                        "Account verification must still be waiting",
                        account.uiState.value.isTesting,
                    )
                    if (inspectDownload) {
                        assertTrue(guest.state.value.busy)
                        assertEquals(ScanStage.INSPECTING, guest.state.value.stage)
                    }
                }
                if (inspectDownload) {
                    instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
                } else {
                    lateinit var backLabel: String
                    scenario.onActivity { backLabel = it.getString(R.string.l_back_b52b36) }
                    var back = findBack(automation.rootInActiveWindow, backLabel)
                    repeat(20) {
                        if (back == null) {
                            Thread.sleep(100)
                            back = findBack(automation.rootInActiveWindow, backLabel)
                        }
                    }
                    assertNotNull(
                        "Toolbar Back is accessible: ${labels(instrumentation.uiAutomation.rootInActiveWindow)}",
                        back,
                    )
                    assertTrue(
                        back!!.performAction(
                            android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK
                        )
                    )
                }
                instrumentation.waitForIdleSync()
                scenario.onActivity {
                    assertEquals(
                        "Back must immediately return home",
                        Routes.HOME,
                        nav.currentDestination?.route,
                    )
                    assertEquals("test-token", app.prefs.getSessionToken())
                }
                // Late connection cleanup must not re-enter the scanner.
                Thread.sleep(750)
                instrumentation.waitForIdleSync()
                scenario.onActivity { assertEquals(Routes.HOME, nav.currentDestination?.route) }
            }
        } finally {
            scenario?.close()
            server.close()
            synchronized(sockets) { sockets.forEach { it.close() } }
            acceptor.join(1000)
            app.prefs.clearSession()
            app.prefs.setServerUrl("")
        }
    }

    private fun labels(node: android.view.accessibility.AccessibilityNodeInfo?): List<String> {
        if (node == null) return emptyList()
        return listOfNotNull(node.text?.toString(), node.contentDescription?.toString()) +
            (0 until node.childCount).flatMap { labels(node.getChild(it)) }
    }

    private fun findBack(
        node: android.view.accessibility.AccessibilityNodeInfo?,
        label: String,
    ): android.view.accessibility.AccessibilityNodeInfo? {
        if (node == null) return null
        if (node.contentDescription?.toString() == label) {
            var action = node
            while (action != null && !action.isClickable) action = action.parent
            return action
        }
        for (index in 0 until node.childCount) findBack(node.getChild(index), label)?.let {
            return it
        }
        return null
    }
}

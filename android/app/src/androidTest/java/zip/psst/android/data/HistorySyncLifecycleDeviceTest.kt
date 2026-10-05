package zip.psst.android.data

import android.os.Build
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.android.PsstApplication
import zip.psst.android.viewmodel.HistoryViewModel
import zip.psst.shared.api.AuthResourceTransfer
import zip.psst.shared.api.AuthResources
import zip.psst.shared.model.InboxSummary
import java.net.ServerSocket
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

/** Actual ViewModel + Room + HTTP lifecycle against a disposable device-only server. */
class HistorySyncLifecycleDeviceTest {
    @Test
    fun completedRateLimitCannotBeBypassedByRefreshMutationOrForeground() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        check(InstrumentationRegistry.getArguments().getString("disposableStorage") == "true")
        check(Build.HARDWARE in setOf("ranchu", "goldfish"))
        val app = instrumentation.targetContext.applicationContext as PsstApplication
        val server = ServerSocket(0)
        val requests = AtomicInteger()
        val configs = AtomicInteger()
        val limitedAt = java.util.concurrent.atomic.AtomicLong()
        val retriedAt = java.util.concurrent.atomic.AtomicLong()
        val generation = "11111111-1111-1111-1111-111111111111"
        val worker =
            thread(isDaemon = true) {
                while (!server.isClosed) {
                    val socket =
                        try {
                            server.accept()
                        } catch (_: Exception) {
                            break
                        }
                    socket.use {
                        val input = it.getInputStream().bufferedReader()
                        val path = input.readLine()?.split(' ')?.getOrNull(1) ?: return@use
                        while (input.readLine()?.takeIf { line -> line.isNotEmpty() } != null) {}
                        var limited = false
                        val body =
                            if (path.startsWith("/api/v1/config")) {
                                configs.incrementAndGet()
                                """{"max_file_size":26214400,"max_file_size_ceiling":10737418240,"history_sync_version":1}"""
                            } else {
                                check(path.startsWith("/api/v1/auth/history/changes"))
                                limited = requests.incrementAndGet() == 1
                                if (limited) limitedAt.set(android.os.SystemClock.elapsedRealtime())
                                else retriedAt.set(android.os.SystemClock.elapsedRealtime())
                                """{"version":1,"generation":"$generation","changes":[],"next_cursor":"current","has_more":false}"""
                            }
                        val status =
                            if (limited) "429 Too Many Requests\r\nRetry-After: 6" else "200 OK"
                        val bytes = body.toByteArray()
                        try {
                            it.getOutputStream()
                                .write(
                                    "HTTP/1.1 $status\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n"
                                        .toByteArray()
                                )
                            it.getOutputStream().write(bytes)
                        } catch (_: java.io.IOException) {
                            // Leaving History can cancel a response while this fixture writes it.
                        }
                    }
                }
            }
        val origin = "http://127.0.0.1:${server.localPort}"
        app.prefs.saveSession(origin, "fixture", "fixture-token", "limited-account", "user")
        val access = app.prefs.historyAccess.value
        app.database
            .historySyncDao()
            .snapshot(
                AuthResources(paginated = true, syncCursor = "current", generation = generation),
                access,
                "",
                null,
            )
        lateinit var model: HistoryViewModel
        instrumentation.runOnMainSync {
            model = HistoryViewModel(app)
            model.refresh()
        }
        fun await(condition: () -> Boolean) {
            val until = System.currentTimeMillis() + 9000
            while (!condition() && System.currentTimeMillis() < until) Thread.sleep(20)
            assertTrue("Timed out waiting for rate-limit state", condition())
        }
        try {
            await { model.offline.value }
            repeat(5) { instrumentation.runOnMainSync { model.refresh() } }
            Thread.sleep(300)
            assertEquals(1, requests.get())
            repeat(5) { HistoryNotifications.changed(origin, access.accountId) }
            Thread.sleep(300)
            assertEquals(1, requests.get())
            instrumentation.runOnMainSync {
                model.stopRefreshing()
                model = HistoryViewModel(app)
                model.refresh()
            }
            Thread.sleep(300)
            assertEquals(1, requests.get())
            assertEquals(
                "Foreground must not bypass the deadline with config HTTP",
                1,
                configs.get(),
            )
            await { requests.get() == 2 }
            assertTrue(retriedAt.get() - limitedAt.get() >= 6000)
        } finally {
            instrumentation.runOnMainSync { model.stopRefreshing() }
            server.close()
            worker.join(1000)
            app.prefs.clearSession()
        }
    }

    @Test
    fun cachePrecedesResponseAndOnlyVisibleServerHistoryPolls() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        check(InstrumentationRegistry.getArguments().getString("disposableStorage") == "true")
        check(Build.HARDWARE in setOf("ranchu", "goldfish"))
        val app = instrumentation.targetContext.applicationContext as PsstApplication
        val server = ServerSocket(0)
        val gate = CountDownLatch(1)
        val requests = AtomicInteger()
        val active = AtomicInteger()
        val peak = AtomicInteger()
        val generation = "11111111-1111-1111-1111-111111111111"
        val worker =
            thread(isDaemon = true) {
                while (!server.isClosed) {
                    val socket =
                        try {
                            server.accept()
                        } catch (_: Exception) {
                            break
                        }
                    thread(isDaemon = true) {
                        socket.use {
                            val input = it.getInputStream().bufferedReader()
                            val path = input.readLine()?.split(' ')?.getOrNull(1) ?: return@use
                            while (
                                input.readLine()?.takeIf { line -> line.isNotEmpty() } != null
                            ) {}
                            val body =
                                if (path.startsWith("/api/v1/config")) {
                                    """{"max_file_size":26214400,"max_file_size_ceiling":10737418240,"history_sync_version":1}"""
                                } else if (path.startsWith("/api/v1/auth/me")) {
                                    """{"id":"fixture-account","username":"fixture","role":"user","must_change_password":false}"""
                                } else {
                                    check(path.startsWith("/api/v1/auth/history/changes"))
                                    val count = requests.incrementAndGet()
                                    peak.accumulateAndGet(active.incrementAndGet(), ::maxOf)
                                    if (count == 1) gate.await(3, TimeUnit.SECONDS)
                                    active.decrementAndGet()
                                    """{"version":1,"generation":"$generation","changes":[],"next_cursor":"current","has_more":false}"""
                                }
                            val bytes = body.toByteArray()
                            try {
                                it.getOutputStream()
                                    .write(
                                        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n"
                                            .toByteArray()
                                    )
                                it.getOutputStream().write(bytes)
                            } catch (_: java.io.IOException) {
                                // Leaving History can cancel a response while this fixture writes
                                // it.
                            }
                        }
                    }
                }
            }
        val origin = "http://127.0.0.1:${server.localPort}"
        app.prefs.saveSession(origin, "fixture", "fixture-token", "fixture-account", "user")
        val access = app.prefs.historyAccess.value
        app.database
            .historySyncDao()
            .snapshot(
                AuthResources(
                    transfers =
                        listOf(
                            AuthResourceTransfer(
                                "11111111-1111-1111-1111-111111111111",
                                "complete",
                                revision = 1,
                                fileCount = 1,
                                totalSize = 1,
                                summary = InboxSummary("ready", 1, 1, 1),
                                createdAt = "2026-10-05T00:00:00Z",
                                title = "Cached navigation entry",
                            )
                        ),
                    paginated = true,
                    syncCursor = "current",
                    generation = generation,
                ),
                access,
                "",
                null,
            )
        lateinit var model: HistoryViewModel
        instrumentation.runOnMainSync { model = HistoryViewModel(app) }
        val collection = launch(Dispatchers.Default) { model.history.collect {} }
        fun await(timeout: Long = 5000, condition: () -> Boolean) {
            val until = System.currentTimeMillis() + timeout
            while (!condition() && System.currentTimeMillis() < until) Thread.sleep(20)
            assertTrue("Timed out waiting for history state", condition())
        }
        try {
            // The app-owned model prewarms from disk before entering History, without HTTP.
            await { model.history.value.size == 1 }
            assertEquals(0, requests.get())
            instrumentation.runOnMainSync {
                model.initializeSource("all")
                model.refresh()
                assertEquals(1, model.history.value.size)
            }
            await { requests.get() == 1 }
            assertNotNull(
                "Cached page must be visible while first response is blocked",
                model.pageState.value.page,
            )
            assertFalse(model.pageState.value.isKnownEmptyFor(access))
            await { model.history.value.size == 1 }
            assertTrue(model.history.value.single().encryptionKey.isBlank())
            assertNull(app.database.transferHistoryDao().getById(model.history.value.single().id))
            repeat(5) { instrumentation.runOnMainSync { model.refresh() } }
            gate.countDown()
            await { requests.get() == 2 }
            Thread.sleep(8000)
            assertEquals(2, requests.get())
            await(5000) { requests.get() == 3 }
            assertEquals(1, peak.get())
            HistoryNotifications.changed("https://other.test", access.accountId)
            HistoryNotifications.changed(origin, "other-account")
            Thread.sleep(200)
            assertEquals(3, requests.get())
            repeat(5) { HistoryNotifications.changed(origin, access.accountId) }
            await { requests.get() == 4 }
            instrumentation.runOnMainSync { model.stopRefreshing() }
            val stopped = requests.get()
            Thread.sleep(11000)
            assertEquals(stopped, requests.get())
            instrumentation.runOnMainSync {
                model.setDeviceHistory(true)
                model.refresh()
            }
            Thread.sleep(1000)
            assertEquals(stopped, requests.get())
            instrumentation.runOnMainSync {
                model.stopRefreshing()
                model = HistoryViewModel(app)
                assertFalse(
                    "Warm reopen is unresolved until the Room page arrives",
                    model.pageState.value.isKnownEmptyFor(access),
                )
            }
            await { model.history.value.size == 1 }
            assertEquals("Disk prewarming must not start HTTP", stopped, requests.get())
            assertNotNull(model.pageState.value.page)
            assertFalse(model.pageState.value.isKnownEmptyFor(access))
            instrumentation.runOnMainSync {
                model.initializeSource("all")
                model.refresh()
                assertEquals(1, model.history.value.size)
                model.stopRefreshing()
                model.initializeSource("all")
                model.refresh()
                assertEquals(
                    "Reopening must retain visible cached rows",
                    1,
                    model.history.value.size,
                )
            }
            await { requests.get() == stopped + 1 }
            instrumentation.runOnMainSync { model.stopRefreshing() }
            val activity =
                instrumentation.startActivitySync(
                    android.content
                        .Intent(
                            instrumentation.targetContext,
                            zip.psst.android.MainActivity::class.java,
                        )
                        .addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
                ) as zip.psst.android.MainActivity
            try {
                lateinit var retained: HistoryViewModel
                instrumentation.runOnMainSync {
                    retained =
                        androidx.lifecycle
                            .ViewModelProvider(activity)[
                                "serverHistory", HistoryViewModel::class.java]
                }
                await { retained.history.value.size == 1 }
                fun labelNodes(
                    label: String
                ): List<android.view.accessibility.AccessibilityNodeInfo> {
                    val found = mutableListOf<android.view.accessibility.AccessibilityNodeInfo>()
                    fun visit(node: android.view.accessibility.AccessibilityNodeInfo) {
                        if (
                            node.text?.toString()?.contains(label) == true ||
                                node.contentDescription?.toString()?.contains(label) == true
                        )
                            found.add(node)
                        for (index in 0 until node.childCount) node.getChild(index)?.let(::visit)
                    }
                    instrumentation.uiAutomation.rootInActiveWindow?.let(::visit)
                    return found
                }
                fun clickLabel(label: String): Boolean {
                    val nodes = labelNodes(label)
                    for (node in nodes) {
                        if (
                            node.text?.toString()?.startsWith(label) != true &&
                                node.contentDescription?.toString()?.startsWith(label) != true
                        )
                            continue
                        var clickable: android.view.accessibility.AccessibilityNodeInfo? = node
                        while (clickable != null) {
                            if (
                                clickable.isClickable &&
                                    clickable.performAction(
                                        android.view.accessibility.AccessibilityNodeInfo
                                            .ACTION_CLICK
                                    )
                            )
                                return true
                            clickable = clickable.parent
                        }
                    }
                    return false
                }
                repeat(2) {
                    await { clickLabel("History") }
                    instrumentation.waitForIdleSync()
                    instrumentation.runOnMainSync {
                        assertSame(
                            retained,
                            androidx.lifecycle
                                .ViewModelProvider(activity)[
                                    "serverHistory", HistoryViewModel::class.java],
                        )
                        assertEquals(
                            "Actual navigation must keep populated presentation",
                            1,
                            retained.history.value.size,
                        )
                    }
                    // Accessibility follows the navigation animation; row state was already
                    // populated before that animation and before any HTTP reply.
                    await { labelNodes("Cached navigation entry").isNotEmpty() }
                    await { clickLabel("Back") }
                    instrumentation.waitForIdleSync()
                    assertEquals(1, retained.history.value.size)
                }
            } finally {
                instrumentation.runOnMainSync { activity.finish() }
            }
        } finally {
            instrumentation.runOnMainSync { model.stopRefreshing() }
            gate.countDown()
            server.close()
            worker.join(1000)
            collection.cancelAndJoin()
            app.prefs.clearSession()
        }
    }

    @Test
    fun corruptMetadataRebootstrapsAndKeepsPrivateKeys() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        check(InstrumentationRegistry.getArguments().getString("disposableStorage") == "true")
        check(Build.HARDWARE in setOf("ranchu", "goldfish"))
        val app = instrumentation.targetContext.applicationContext as PsstApplication
        val server = ServerSocket(0)
        val snapshots = AtomicInteger()
        val generation = "11111111-1111-1111-1111-111111111111"
        val id = "22222222-2222-2222-2222-222222222222"
        val worker =
            thread(isDaemon = true) {
                while (!server.isClosed) {
                    val socket =
                        try {
                            server.accept()
                        } catch (_: Exception) {
                            break
                        }
                    socket.use {
                        val input = it.getInputStream().bufferedReader()
                        val path = input.readLine()?.split(' ')?.getOrNull(1) ?: return@use
                        while (input.readLine()?.takeIf { line -> line.isNotEmpty() } != null) {}
                        val body =
                            when {
                                path.startsWith("/api/v1/config") ->
                                    """{"max_file_size":26214400,"max_file_size_ceiling":10737418240,"history_sync_version":1}"""
                                path.startsWith("/api/v1/auth/resources") -> {
                                    snapshots.incrementAndGet()
                                    """{"paginated":true,"transfers":[{"id":"$id","revision":2,"status":"complete","created_at":"2026-10-05T00:00:00Z","file_count":2,"total_size":2,"summary":{"state":"ready","file_count":2,"completed_files":2,"total_size":2}}],"slots":[],"sync_cursor":"current","generation":"$generation","next_cursor":null}"""
                                }
                                else ->
                                    """{"version":1,"generation":"$generation","changes":[],"next_cursor":"current","has_more":false}"""
                            }
                        val bytes = body.toByteArray()
                        try {
                            it.getOutputStream()
                                .write(
                                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n"
                                        .toByteArray()
                                )
                            it.getOutputStream().write(bytes)
                        } catch (_: java.io.IOException) {
                            // Leaving History can cancel a response while this fixture writes it.
                        }
                    }
                }
            }
        val origin = "http://127.0.0.1:${server.localPort}"
        app.prefs.saveSession(origin, "fixture", "fixture-token", "corrupt-account", "user")
        val access = app.prefs.historyAccess.value
        val cache = app.database.historySyncDao()
        cache.snapshot(
            AuthResources(
                transfers =
                    listOf(
                        AuthResourceTransfer(
                            id,
                            "complete",
                            revision = 1,
                            fileCount = 1,
                            totalSize = 1,
                            summary = InboxSummary("ready", 1, 1, 1),
                            createdAt = "2026-10-05T00:00:00Z",
                        )
                    ),
                paginated = true,
                syncCursor = "current",
                generation = generation,
            ),
            access,
            "",
            null,
        )
        app.database
            .transferHistoryDao()
            .insert(
                TransferHistoryEntity(
                    id,
                    "sent",
                    1,
                    1,
                    origin,
                    "private-key",
                    "complete",
                    accountId = access.accountId,
                )
            )
        cache.writeFact(cache.fact(access.syncScope(), "transfer", id)!!.copy(body = "{"))
        cache.writeState(HistorySyncState(access.syncScope(), generation, "bad cursor"))
        lateinit var model: HistoryViewModel
        instrumentation.runOnMainSync {
            model = HistoryViewModel(app)
            model.refresh()
        }
        try {
            val until = System.currentTimeMillis() + 5000
            while (
                model.pageState.value.page?.transfers?.singleOrNull()?.fileCount != 2L &&
                    System.currentTimeMillis() < until
            ) Thread.sleep(20)
            assertEquals(2L, model.pageState.value.page!!.transfers.single().fileCount)
            assertEquals(1, snapshots.get())
            assertEquals("current", cache.state(access.syncScope())!!.cursor)
            assertEquals(
                "private-key",
                app.database.transferHistoryDao().getById(id)!!.encryptionKey,
            )
        } finally {
            instrumentation.runOnMainSync { model.stopRefreshing() }
            server.close()
            worker.join(1000)
            app.prefs.clearSession()
        }
    }

    @Test
    fun evictedOverflowRevalidatesAtRealAnchorWithoutJumpingPages() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        check(InstrumentationRegistry.getArguments().getString("disposableStorage") == "true")
        check(Build.HARDWARE in setOf("ranchu", "goldfish"))
        val app = instrumentation.targetContext.applicationContext as PsstApplication
        val server = ServerSocket(0)
        val seen = java.util.Collections.synchronizedList(mutableListOf<String>())
        val generation = "11111111-1111-1111-1111-111111111111"
        fun id(n: Int) = "%08d-1111-1111-1111-111111111111".format(n)
        fun row(n: Int) =
            AuthResourceTransfer(
                id(n),
                "complete",
                revision = 1,
                historyAfter = "after_$n",
                historyAfterKind = "kind_$n",
                fileCount = 1,
                totalSize = 1,
                summary = InboxSummary("ready", 1, 1, 1),
                createdAt = java.time.Instant.ofEpochSecond(1_700_000_000L + n).toString(),
            )
        fun wireRow(n: Int) =
            """{"id":"${id(n)}","revision":1,"status":"complete","created_at":"${row(n).createdAt}","file_count":1,"total_size":1,"history_after":"after_$n","history_after_kind":"kind_$n","summary":{"state":"ready","file_count":1,"completed_files":1,"total_size":1}}"""
        val worker =
            thread(isDaemon = true) {
                while (!server.isClosed) {
                    val socket =
                        try {
                            server.accept()
                        } catch (_: Exception) {
                            break
                        }
                    socket.use {
                        val input = it.getInputStream().bufferedReader()
                        val path = input.readLine()?.split(' ')?.getOrNull(1) ?: return@use
                        while (input.readLine()?.takeIf { line -> line.isNotEmpty() } != null) {}
                        val body =
                            when {
                                path.startsWith("/api/v1/config") ->
                                    """{"max_file_size":26214400,"max_file_size_ceiling":10737418240,"history_sync_version":1}"""
                                path.startsWith("/api/v1/auth/resources") -> {
                                    val after =
                                        if (path.contains("after=after_21")) "after_21"
                                        else "newest"
                                    seen.add(after)
                                    val rows = if (after == "newest") 21..70 else 1..20
                                    val next = if (after == "newest") "\"after_21\"" else "null"
                                    """{"paginated":true,"transfers":[${rows.map(::wireRow).joinToString()}],"slots":[],"sync_cursor":"current","generation":"$generation","next_cursor":$next}"""
                                }
                                else ->
                                    """{"version":1,"generation":"$generation","changes":[],"next_cursor":"current","has_more":false}"""
                            }
                        val bytes = body.toByteArray()
                        try {
                            it.getOutputStream()
                                .write(
                                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n"
                                        .toByteArray()
                                )
                            it.getOutputStream().write(bytes)
                        } catch (_: java.io.IOException) {
                            // Leaving History can cancel a response while this fixture writes it.
                        }
                    }
                }
            }
        val origin = "http://127.0.0.1:${server.localPort}"
        app.prefs.saveSession(origin, "fixture", "fixture-token", "overflow-account", "user")
        val access = app.prefs.historyAccess.value
        val cache = app.database.historySyncDao()
        cache.snapshot(
            AuthResources(
                transfers = (1..50).map(::row),
                paginated = true,
                syncCursor = "current",
                generation = generation,
            ),
            access,
            "",
            null,
        )
        cache.batch(
            zip.psst.shared.api.HistoryChanges(
                1,
                generation,
                (51..70).map {
                    zip.psst.shared.api.HistoryChange(
                        "transfer",
                        id(it),
                        1,
                        "upsert",
                        AuthResources(transfers = listOf(row(it))),
                    )
                },
                "after",
                false,
            ),
            access,
            cache.state(access.syncScope())!!,
        )
        lateinit var model: HistoryViewModel
        instrumentation.runOnMainSync {
            model = HistoryViewModel(app)
            model.refresh()
        }
        fun await(condition: () -> Boolean) {
            val until = System.currentTimeMillis() + 7000
            while (!condition() && System.currentTimeMillis() < until) Thread.sleep(20)
            assertTrue(condition())
        }
        try {
            await {
                model.pageState.value.page?.nextCursor?.startsWith("local_") == true &&
                    !model.pageState.value.loading
            }
            instrumentation.runOnMainSync { model.nextPage() }
            await {
                model.pageState.value.pager.cursor?.startsWith("local_") == true &&
                    !model.pageState.value.loading
            }
            val local = model.pageState.value.pager.cursor
            assertEquals(20, model.pageState.value.page!!.transfers.size)
            cache.clearWindows(access.syncScope())
            cache.clearState(access.syncScope())
            instrumentation.runOnMainSync { model.refresh() }
            await { seen.size == 2 && !model.pageState.value.loading }
            assertEquals(listOf("newest", "after_21"), seen.toList())
            assertEquals(local, model.pageState.value.pager.cursor)
            assertEquals(
                (1..20).map(::id).toSet(),
                model.pageState.value.page!!.transfers.map { it.id }.toSet(),
            )
        } finally {
            instrumentation.runOnMainSync { model.stopRefreshing() }
            server.close()
            worker.join(1000)
            app.prefs.clearSession()
        }
    }
}

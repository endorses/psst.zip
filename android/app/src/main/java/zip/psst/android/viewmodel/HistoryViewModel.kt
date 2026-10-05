package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.room.withTransaction
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.data.*
import zip.psst.android.data.HistoryAccess
import zip.psst.android.data.InboxPager
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.localHistoryScope
import zip.psst.android.data.revokeHistoryEntry
import zip.psst.android.data.syncAccountHistory
import zip.psst.android.i18n.*
import zip.psst.shared.api.AdminTransferForbiddenException
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthResources
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.HistorySyncRateLimitedException
import zip.psst.shared.api.HistorySyncResetRequiredException
import zip.psst.shared.api.LinkDeletionException
import zip.psst.shared.api.PasswordChangeRequiredException
import zip.psst.shared.model.ServerConfig
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

data class HistoryDeletionError(val id: String, val message: UiText)

data class AccountHistoryPageState(
    val access: HistoryAccess? = null,
    val pager: InboxPager = InboxPager(),
    val page: AuthResources? = null,
    val loading: Boolean = false,
) {
    /** Missing disk data and pending private-row enrichment must never be presented as empty. */
    fun isKnownEmptyFor(currentAccess: HistoryAccess): Boolean =
        access == currentAccess &&
            !currentAccess.accountId.isNullOrBlank() &&
            !currentAccess.isAdmin &&
            !currentAccess.mustChangePassword &&
            page?.let { it.transfers.isEmpty() && it.slots.isEmpty() } == true
}

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class HistoryViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val dao = app.database.transferHistoryDao()
    private val syncCache = app.database.historySyncDao()
    private val wake = Channel<Unit>(Channel.CONFLATED)
    private val retryGate = HistoryNotifications.retryGate
    private val snapshotAnchors =
        java.util.Collections.synchronizedMap(LinkedHashMap<String, String>())

    private fun anchorKey(access: HistoryAccess, kind: String, cursor: String) =
        "${access.syncScope()}|$kind|$cursor"

    private fun rememberAnchor(
        access: HistoryAccess,
        kind: String,
        cursor: String?,
        anchor: String?,
    ) {
        if (cursor == null || anchor == null) return
        synchronized(snapshotAnchors) {
            snapshotAnchors[anchorKey(access, kind, cursor)] = anchor
            while (snapshotAnchors.size > 200) snapshotAnchors.remove(snapshotAnchors.keys.first())
        }
    }

    private val downloads = GuestDownloadStore(app)
    val filter = MutableStateFlow("all")
    val deviceRows = MutableStateFlow<List<HistoryRow>>(emptyList())
    val deviceHydratedAccess = MutableStateFlow<HistoryAccess?>(null)
    val deviceNext = MutableStateFlow<MergedHistoryCursor?>(null)
    val deviceLoading = MutableStateFlow(false)
    val deviceIssue = MutableStateFlow<UiText?>(null)
    val deviceImporting = MutableStateFlow(false)
    val deviceImportErrors = MutableStateFlow(false)
    private var deviceCursor = MergedHistoryCursor()
    private var deviceJob: Job? = null
    private var deviceRevision = 0L

    fun setFilter(value: String) {
        if (filter.value == value) return
        filter.value = value
        if (value == "downloaded") _deviceHistory.value = true
        deviceCursor = MergedHistoryCursor()
        deviceRows.value = emptyList()
        deviceHydratedAccess.value = null
        deviceNext.value = null
        _pageState.value = AccountHistoryPageState()
        revision++
        refreshJob?.cancel()
        if (_deviceHistory.value) loadDeviceHistory() else if (visible) startPolling()
    }

    fun firstDevicePage() {
        deviceCursor = MergedHistoryCursor()
        loadDeviceHistory()
    }

    fun moreDeviceHistory() {
        deviceNext.value?.let {
            deviceCursor = it
            loadDeviceHistory()
        }
    }

    fun refreshDeviceHistory() = loadDeviceHistory()

    fun refreshNewest() {
        if (_deviceHistory.value) firstDevicePage() else firstPage()
    }

    private fun loadDeviceHistory() {
        deviceJob?.cancel()
        val access = app.prefs.historyAccess.value
        val selected = filter.value
        val cursor = deviceCursor
        val requestRevision = ++deviceRevision
        deviceLoading.value = true
        deviceIssue.value = null
        deviceJob =
            viewModelScope.launch(Dispatchers.IO) {
                try {
                    val kind =
                        when (selected) {
                            "sent" -> "sent"
                            "received" -> "received"
                            else -> ""
                        }
                    val account =
                        if (
                            selected != "downloaded" &&
                                access.accountId != null &&
                                !access.isAdmin &&
                                !access.mustChangePassword
                        )
                            dao.deviceHistoryPage(
                                access.accountId,
                                localHistoryScope(access.serverUrl),
                                kind,
                                cursor.account?.createdAt ?: Long.MAX_VALUE,
                                cursor.account?.id ?: "\uffff",
                            )
                        else emptyList()
                    val guest =
                        if (selected == "all" || selected == "downloaded") {
                            downloads.importLegacyBatch()
                            downloads.page(cursor.downloads)
                        } else null
                    val page =
                        mergeHistoryPage(
                            account,
                            guest?.records.orEmpty(),
                            cursor,
                            account.size > 50,
                            guest?.next != null,
                            guest?.next,
                        )
                    if (
                        !isActive ||
                            requestRevision != deviceRevision ||
                            app.prefs.historyAccess.value != access ||
                            filter.value != selected ||
                            !_deviceHistory.value
                    )
                        return@launch
                    deviceRows.value = page.rows
                    deviceHydratedAccess.value = access
                    deviceNext.value = page.next
                    deviceImporting.value = guest?.importing == true
                    deviceImportErrors.value = guest?.importErrors == true
                } catch (error: CancellationException) {
                    throw error
                } catch (_: Exception) {
                    if (
                        requestRevision == deviceRevision &&
                            app.prefs.historyAccess.value == access &&
                            filter.value == selected
                    )
                        deviceIssue.value =
                            message(
                                R.string
                                    .l_local_history_could_not_be_read_saved_files_and_records_are_retai_0f2a42
                            )
                } finally {
                    if (
                        requestRevision == deviceRevision &&
                            app.prefs.historyAccess.value == access &&
                            filter.value == selected
                    )
                        deviceLoading.value = false
                }
            }
    }

    override fun onCleared() {
        downloads.close()
        super.onCleared()
    }

    private val _pageState = MutableStateFlow(AccountHistoryPageState())
    val pageState = _pageState.asStateFlow()
    private val _deviceHistory = MutableStateFlow(false)
    val deviceHistory = _deviceHistory.asStateFlow()
    val legacyCount = MutableStateFlow(0)
    val currentAccess = app.prefs.historyAccess
    val history: StateFlow<List<TransferHistoryEntity>> =
        combine(_pageState, app.prefs.historyAccess, _deviceHistory) { page, access, device ->
                Triple(page, access, device)
            }
            .flatMapLatest { (state, access, device) ->
                if (device) {
                    flowOf(emptyList())
                } else {
                    val ids =
                        state.page
                            ?.let {
                                it.transfers.map { row -> row.id } + it.slots.map { row -> row.id }
                            }
                            .orEmpty()
                    if (
                        device ||
                            state.access != access ||
                            access.accountId == null ||
                            ids.isEmpty()
                    )
                        flowOf(emptyList())
                    else
                        dao.observePage(ids, access.serverUrl, access.accountId).map { rows ->
                            val now = System.currentTimeMillis()
                            val privateById = rows.associateBy { it.id }
                            accountHistoryMetadata(requireNotNull(state.page), access).map {
                                incoming ->
                                val row =
                                    mergeAccountResource(privateById[incoming.id], incoming, access)
                                        ?: incoming
                                if (
                                    row.expiresAt != null &&
                                        row.expiresAt <= now &&
                                        row.status !in
                                            listOf("exhausted", "unavailable", "expired", "revoked")
                                )
                                    row.copy(status = "expired")
                                else row
                            }
                        }
                }
            }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), emptyList())

    private val _deletingIds = MutableStateFlow<Set<String>>(emptySet())
    val deletingIds = _deletingIds.asStateFlow()
    private val _deletionError = MutableStateFlow<HistoryDeletionError?>(null)
    val deletionError = _deletionError.asStateFlow()

    val offline = MutableStateFlow(false)
    val accountIssue = MutableStateFlow<UiText?>(null)
    val transferIssue = MutableStateFlow<UiText?>(null)
    private var refreshJob: Job? = null
    @Volatile private var revision = 0L
    private var visible = false
    private val deleteJobs = mutableMapOf<String, Job>()

    private var activeAccess = app.prefs.historyAccess.value

    init {
        viewModelScope.launch {
            HistoryNotifications.changes.collect { mutation ->
                if (
                    visible &&
                        !_deviceHistory.value &&
                        mutation.matches(app.prefs.historyAccess.value)
                )
                    startPolling()
            }
        }
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (access == activeAccess) return@collect
                activeAccess = access
                snapshotAnchors.clear()
                refreshJob?.cancel()
                revision++
                _pageState.value = AccountHistoryPageState()
                legacyCount.value = 0
                deviceJob?.cancel()
                deviceCursor = MergedHistoryCursor()
                deviceRows.value = emptyList()
                deviceHydratedAccess.value = null
                deviceNext.value = null
                if (_deviceHistory.value) loadDeviceHistory()
                deleteJobs.values.toList().forEach { it.cancel() }
                _deletionError.value = null
                accountIssue.value = null
                transferIssue.value = null
                if (visible && !_deviceHistory.value) startPolling()
            }
        }
    }

    fun rename(entry: TransferHistoryEntity, title: String) {
        val access = app.prefs.historyAccess.value
        if (!access.permits(entry) || entry.accountId == null) return
        viewModelScope.launch(Dispatchers.IO) {
            val client =
                ApiClient(
                    ServerConfig(entry.serverUrl),
                    sessionToken = app.prefs.getSessionToken(entry.serverUrl),
                )
            try {
                val normalized = zip.psst.shared.model.LinkTitle.normalize(title)
                val result =
                    if (entry.type == "received") client.slots.renameTitle(entry.id, normalized)
                    else client.transfers.renameTitle(entry.id, normalized)
                if (app.prefs.historyAccess.value != access) return@launch
                app.database.withTransaction {
                    dao.saveSharedTitle(entry.id, entry.accountId, entry.originScope, result.title)
                    syncCache.rename(
                        access,
                        if (entry.type == "received") "slot" else "transfer",
                        entry.id,
                        result.title,
                    )
                }
                if (_deviceHistory.value) loadDeviceHistory() else if (visible) startPolling()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                transferIssue.value =
                    failureText(error)
                        ?: message(R.string.l_could_not_rename_this_link_retry_229f97)
            } finally {
                client.close()
            }
        }
    }

    fun setDeviceHistory(value: Boolean) {
        _deviceHistory.value = value
        revision++
        refreshJob?.cancel()
        _pageState.update { it.copy(loading = false) }
        deviceJob?.cancel()
        deviceRevision++
        deviceCursor = MergedHistoryCursor()
        deviceRows.value = emptyList()
        deviceHydratedAccess.value = null
        deviceNext.value = null
        _pageState.value = AccountHistoryPageState()
        if (value) loadDeviceHistory() else if (visible) startPolling()
    }

    fun firstPage() = navigate(InboxPager())

    fun previousPage() {
        val pager = _pageState.value.pager
        if (pager.previous.isNotEmpty()) navigate(pager.back())
    }

    fun nextPage() {
        val state = _pageState.value
        val next = state.page?.nextCursor ?: return
        try {
            navigate(state.pager.next(next))
        } catch (_: IllegalArgumentException) {
            transferIssue.value =
                message(
                    R.string
                        .l_the_server_repeated_a_history_page_return_to_the_first_page_and_r_202d64
                )
        }
    }

    private fun navigate(target: InboxPager) {
        if (_pageState.value.loading || _deviceHistory.value) return
        startPolling(target)
    }

    fun refresh() {
        visible = true
        if (!_deviceHistory.value) startPolling() else loadDeviceHistory()
    }

    private fun startPolling(firstTarget: InboxPager? = null) {
        if (firstTarget == null && refreshJob?.isActive == true) {
            wake.trySend(Unit)
            return
        }
        val previousJob = refreshJob
        previousJob?.cancel()
        val requestRevision = ++revision
        refreshJob =
            viewModelScope.launch(Dispatchers.IO) {
                previousJob?.join()
                var target = firstTarget
                var supportsSync: Boolean? = null
                var failures = 0
                while (isActive) {
                    val access = app.prefs.historyAccess.value
                    val token = app.prefs.getSessionToken(access.serverUrl)
                    if (token == null || access.isAdmin || access.mustChangePassword) {
                        _pageState.update { it.copy(loading = false) }
                        return@launch
                    }
                    val pager = target ?: _pageState.value.pager
                    val kind =
                        when (filter.value) {
                            "sent" -> "transfer"
                            "received" -> "slot"
                            else -> ""
                        }
                    fun current() =
                        revision == requestRevision &&
                            app.prefs.historyAccess.value == access &&
                            app.prefs.getSessionToken(access.serverUrl) == token &&
                            !_deviceHistory.value
                    if (!current()) return@launch
                    _pageState.update { it.copy(loading = true) }
                    val client = ApiClient(ServerConfig(access.serverUrl), sessionToken = token)
                    var failed = false
                    var retryAfter = 0L
                    try {
                        suspend fun showCached() {
                            val cached = syncCache.cachedPage(access, kind, pager.cursor) ?: return
                            if (!current()) return
                            syncAccountHistory(dao, cached, access, retainNew = false) {
                                if (current()) access else HistoryAccess()
                            }
                            if (current()) {
                                _pageState.value = AccountHistoryPageState(access, pager, cached)
                                val last =
                                    (cached.transfers.map {
                                            Triple(
                                                it.createdAt,
                                                it.id,
                                                if (kind.isEmpty()) it.historyAfter
                                                else it.historyAfterKind,
                                            )
                                        } +
                                            cached.slots.map {
                                                Triple(
                                                    it.createdAt,
                                                    it.id,
                                                    if (kind.isEmpty()) it.historyAfter
                                                    else it.historyAfterKind,
                                                )
                                            })
                                        .minWithOrNull(
                                            compareBy<Triple<String?, String, String?>> {
                                                    parseHistoryExpiry(it.first) ?: 0
                                                }
                                                .thenBy { it.second }
                                        )
                                rememberAnchor(access, kind, cached.nextCursor, last?.third)
                            }
                        }
                        var corruptCache = false
                        try {
                            showCached()
                        } catch (_: HistoryCacheCorruptException) {
                            corruptCache = true
                        }
                        // Local coverage establishes visible rows before any network I/O.
                        val notBefore = retryGate.remaining(access.syncScope())
                        if (notBefore > 0) {
                            _pageState.update { it.copy(loading = false) }
                            withTimeoutOrNull(notBefore) { wake.receive() }
                            continue
                        }
                        if (supportsSync == null)
                            supportsSync =
                                kotlinx.coroutines.withTimeout(10_000L) {
                                    client.limits.get().historySyncVersion == 1
                                }
                        if (!current()) return@launch
                        val cachedWindow =
                            syncCache.window(access.syncScope(), kind, pager.cursor.orEmpty())
                        val authoritativeAfter =
                            if (pager.cursor?.startsWith("local_") == true)
                                cachedWindow?.serverAfter
                                    ?: snapshotAnchors[anchorKey(access, kind, pager.cursor)]
                            else pager.cursor
                        rememberAnchor(access, kind, pager.cursor, authoritativeAfter)
                        suspend fun fetchSnapshot(reset: Boolean = false) {
                            // A local continuation is disposable coverage and never sent to the
                            // server.
                            val after = authoritativeAfter
                            if (
                                pager.cursor?.startsWith("local_") == true &&
                                    after == null &&
                                    !reset
                            )
                                error("History coverage was evicted")
                            val page =
                                client.auth.resourcesPage(
                                    if (reset) null else after,
                                    50,
                                    kind.takeIf { it.isNotEmpty() },
                                )
                            if (!current()) return
                            uiRequire(
                                page.nextCursor == null || page.nextCursor !in pager.previous
                            ) {
                                message(R.string.l_the_server_repeated_a_history_page_639a72)
                            }
                            app.database.withTransaction {
                                if (!current())
                                    throw CancellationException("History account changed")
                                syncCache.snapshot(
                                    page,
                                    access,
                                    kind,
                                    if (reset) null else pager.cursor,
                                    reset,
                                    serverAfter = if (reset) null else after,
                                )
                                val effective =
                                    syncCache.cachedPage(
                                        access,
                                        kind,
                                        if (reset) null else pager.cursor,
                                    ) ?: page
                                syncAccountHistory(dao, effective, access, retainNew = false) {
                                    if (current()) access else HistoryAccess()
                                }
                            }
                        }
                        val persistedState = syncCache.state(access.syncScope())
                        val invalidState =
                            persistedState?.let {
                                !it.generation.matches(
                                    Regex("[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}")
                                ) ||
                                    it.cursor.length !in 1..512 ||
                                    !it.cursor.matches(Regex("[A-Za-z0-9_-]+"))
                            } == true
                        if (
                            supportsSync == true &&
                                (corruptCache || persistedState == null || invalidState)
                        ) {
                            // An absent/invalid watermark cannot bootstrap from an older page: the
                            // newest window and cursor must describe the same authoritative view.
                            fetchSnapshot(reset = true)
                            if (pager.cursor != null) {
                                if (authoritativeAfter != null) fetchSnapshot()
                                else {
                                    target = InboxPager()
                                    continue
                                }
                            }
                        } else if (
                            supportsSync != true ||
                                syncCache.cachedPage(access, kind, pager.cursor) == null
                        ) {
                            fetchSnapshot()
                        }
                        if (supportsSync == true) {
                            try {
                                // Four bounded pages per cycle; persist progress and yield to the
                                // next cycle.
                                for (batchNumber in 0 until 4) {
                                    val state = syncCache.state(access.syncScope()) ?: break
                                    val batch = client.auth.historyChanges(state.cursor, 50)
                                    if (!current())
                                        throw CancellationException("History account changed")
                                    app.database.withTransaction {
                                        if (!current())
                                            throw CancellationException("History account changed")
                                        syncCache.batch(batch, access, state)
                                        val facts =
                                            batch.changes
                                                .filter { it.action == "upsert" }
                                                .mapNotNull {
                                                    syncCache
                                                        .fact(access.syncScope(), it.kind, it.id)
                                                        ?.takeUnless { it.removed }
                                                        ?.let { fact ->
                                                            zip.psst.shared.api
                                                                .decodeCachedHistoryResources(
                                                                    fact.body
                                                                )
                                                        }
                                                }
                                        syncAccountHistory(
                                            dao,
                                            AuthResources(
                                                transfers = facts.flatMap { it.transfers },
                                                slots = facts.flatMap { it.slots },
                                            ),
                                            access,
                                            retainNew = false,
                                        ) {
                                            if (current()) access else HistoryAccess()
                                        }
                                    }
                                    if (!batch.hasMore) break
                                    kotlinx.coroutines.yield()
                                }
                            } catch (_: HistorySyncResetRequiredException) {
                                fetchSnapshot(reset = true)
                                if (pager.cursor != null) {
                                    if (authoritativeAfter != null) fetchSnapshot()
                                    else {
                                        target = InboxPager()
                                        continue
                                    }
                                }
                            }
                        }
                        showCached()
                        if (!current()) return@launch
                        accountIssue.value = null
                        transferIssue.value = null
                    } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
                        if (!isActive || !current()) throw e
                        failed = true
                        transferIssue.value =
                            message(
                                R.string
                                    .l_history_request_timed_out_the_shown_records_were_kept_retry_this__c5e131
                            )
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: AuthenticationRequiredException) {
                        if (current()) app.prefs.clearSession()
                        return@launch
                    } catch (e: Exception) {
                        if (!current()) return@launch
                        failed = true
                        if (e is HistorySyncRateLimitedException) {
                            retryAfter = e.retryAfterSeconds * 1000
                            retryGate.defer(access.syncScope(), retryAfter)
                        }
                        if (
                            e is PasswordChangeRequiredException ||
                                e is AdminTransferForbiddenException
                        ) {
                            accountIssue.value = failureText(e)
                            return@launch
                        } else
                            transferIssue.value =
                                message(
                                    R.string
                                        .l_could_not_load_this_history_page_the_shown_records_were_kept_retr_bc1e72
                                )
                    } finally {
                        client.close()
                        if (current()) _pageState.update { it.copy(loading = false) }
                    }
                    if (!current()) return@launch
                    offline.value = failed
                    target = null
                    failures = if (failed) (failures + 1).coerceAtMost(4) else 0
                    val wait =
                        historyPollingDelay(
                            failures,
                            retryAfter,
                            if (failed) kotlin.random.Random.nextLong(0, 1000) else 0,
                        )
                    withTimeoutOrNull(wait) { wake.receive() }
                }
            }
    }

    fun stopRefreshing() {
        visible = false
        revision++
        refreshJob?.cancel()
        _pageState.update { it.copy(loading = false) }
    }

    fun dismissDeletionError() {
        _deletionError.value = null
    }

    fun delete(id: String) {
        if (id in _deletingIds.value) return
        revision++
        refreshJob?.cancel()
        _pageState.update { it.copy(loading = false) }
        _deletingIds.update { it + id }
        _deletionError.value = null
        deleteJobs[id] =
            viewModelScope.launch {
                try {
                    withContext(Dispatchers.IO) {
                        val access = app.prefs.historyAccess.value
                        val removed = dao.getById(id)?.takeIf { access.permits(it) }
                        val metadata =
                            _pageState.value.page?.let {
                                accountHistoryMetadata(it, access).firstOrNull { row ->
                                    row.id == id
                                }
                            }
                        revokeHistoryEntry(
                            dao,
                            id,
                            { app.prefs.historyAccess.value },
                            metadata = metadata,
                        ) { config ->
                            ApiClient(
                                config,
                                sessionToken = app.prefs.getSessionToken(config.normalizedBaseUrl),
                            )
                        }
                        removed?.let { zip.psst.android.data.InboxKeyStore(app).delete(it) }
                    }
                } catch (error: CancellationException) {
                    throw error
                } catch (error: Exception) {
                    _deletionError.value =
                        HistoryDeletionError(
                            id,
                            if (error is LinkDeletionException) failureText(error)
                            else
                                message(
                                    R.string
                                        .l_could_not_revoke_this_link_check_the_connection_and_retry_the_his_6156db
                                ),
                        )
                } finally {
                    _deletingIds.update { it - id }
                    deleteJobs.remove(id)
                    if (visible && !_deviceHistory.value) startPolling()
                    else if (_deviceHistory.value) loadDeviceHistory()
                }
            }
    }
}

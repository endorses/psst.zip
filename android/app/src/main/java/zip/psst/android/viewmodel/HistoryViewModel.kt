package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
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
import zip.psst.shared.api.LinkDeletionException
import zip.psst.shared.api.PasswordChangeRequiredException
import zip.psst.shared.model.ServerConfig
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
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

data class HistoryDeletionError(val id: String, val message: UiText)

data class AccountHistoryPageState(
    val access: HistoryAccess? = null,
    val pager: InboxPager = InboxPager(),
    val page: AuthResources? = null,
    val loading: Boolean = false,
)

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class HistoryViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val dao = app.database.transferHistoryDao()
    private val downloads = GuestDownloadStore(app)
    val filter = MutableStateFlow("all")
    val deviceRows = MutableStateFlow<List<HistoryRow>>(emptyList())
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
                    else dao.observePage(ids, access.serverUrl, access.accountId)
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
            app.prefs.historyAccess.collect { access ->
                if (access == activeAccess) return@collect
                activeAccess = access
                refreshJob?.cancel()
                revision++
                _pageState.value = AccountHistoryPageState()
                legacyCount.value = 0
                deviceJob?.cancel()
                deviceCursor = MergedHistoryCursor()
                deviceRows.value = emptyList()
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
                dao.saveSharedTitle(entry.id, entry.accountId, entry.originScope, result.title)
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
        refreshJob?.cancel()
        val requestRevision = ++revision
        refreshJob =
            viewModelScope.launch(Dispatchers.IO) {
                var target = firstTarget
                while (isActive) {
                    val access = app.prefs.historyAccess.value
                    val token = app.prefs.getSessionToken(access.serverUrl)
                    if (token == null || access.isAdmin || access.mustChangePassword) {
                        _pageState.update { it.copy(loading = false) }
                        return@launch
                    }
                    val pager = target ?: _pageState.value.pager
                    fun current() =
                        revision == requestRevision &&
                            app.prefs.historyAccess.value == access &&
                            app.prefs.getSessionToken(access.serverUrl) == token &&
                            !_deviceHistory.value
                    if (!current()) return@launch
                    _pageState.update { it.copy(loading = true) }
                    val client = ApiClient(ServerConfig(access.serverUrl), sessionToken = token)
                    var failed = false
                    try {
                        val page =
                            client.auth.resourcesPage(
                                pager.cursor,
                                50,
                                when (filter.value) {
                                    "sent" -> "transfer"
                                    "received" -> "slot"
                                    else -> null
                                },
                            )
                        if (!current()) return@launch
                        uiRequire(page.nextCursor == null || page.nextCursor !in pager.previous) {
                            message(R.string.l_the_server_repeated_a_history_page_639a72)
                        }
                        syncAccountHistory(dao, page, access) {
                            if (current()) access else HistoryAccess()
                        }
                        if (!current()) return@launch
                        _pageState.value = AccountHistoryPageState(access, pager, page)
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
                        if (
                            e is PasswordChangeRequiredException ||
                                e is AdminTransferForbiddenException
                        )
                            accountIssue.value = failureText(e)
                        else
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
                    delay(if (failed) 15000 else 5000)
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
                        val removed = dao.getById(id)
                        revokeHistoryEntry(dao, id, { app.prefs.historyAccess.value }) { config ->
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

package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.HistoryAccess
import zip.psst.android.data.InboxPager
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.revokeHistoryEntry
import zip.psst.android.data.syncAccountHistory
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

data class HistoryDeletionError(val id: String, val message: String)

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

    private val _pageState = MutableStateFlow(AccountHistoryPageState())
    val pageState = _pageState.asStateFlow()
    private val _deviceHistory = MutableStateFlow(false)
    val deviceHistory = _deviceHistory.asStateFlow()
    val legacyCount = MutableStateFlow(0)
    val history: StateFlow<List<TransferHistoryEntity>> =
        combine(_pageState, app.prefs.historyAccess, _deviceHistory) { page, access, device ->
                Triple(page, access, device)
            }
            .flatMapLatest { (state, access, device) ->
                if (device)
                    dao.getAll().map { rows ->
                        legacyCount.value = rows.count { it.accountId == null }
                        rows.filter(access::permits)
                    }
                else {
                    val ids =
                        state.page
                            ?.let {
                                it.transfers.map { row -> row.id } + it.slots.map { row -> row.id }
                            }
                            .orEmpty()
                    if (state.access != access || access.accountId == null || ids.isEmpty())
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
    val accountIssue = MutableStateFlow<String?>(null)
    val transferIssue = MutableStateFlow<String?>(null)
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
        viewModelScope.launch {
            if (app.prefs.historyAccess.value == access)
                dao.rename(
                    entry.id,
                    entry.serverUrl,
                    entry.accountId,
                    entry.type,
                    title.trim().take(200).ifEmpty { null },
                )
        }
    }

    fun setDeviceHistory(value: Boolean) {
        _deviceHistory.value = value
        revision++
        refreshJob?.cancel()
        _pageState.update { it.copy(loading = false) }
        if (!value && visible) startPolling()
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
                "The server repeated a History page. Return to the first page and retry."
        }
    }

    private fun navigate(target: InboxPager) {
        if (_pageState.value.loading || _deviceHistory.value) return
        startPolling(target)
    }

    fun refresh() {
        visible = true
        if (!_deviceHistory.value) startPolling()
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
                        val page = client.auth.resourcesPage(pager.cursor, 50)
                        if (!current()) return@launch
                        require(page.nextCursor == null || page.nextCursor !in pager.previous) {
                            "The server repeated a History page"
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
                            "History request timed out. The shown records were kept. Retry this page."
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
                            accountIssue.value = e.message
                        else
                            transferIssue.value =
                                "Could not load this History page. The shown records were kept. Retry or return to the first page."
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
                            if (error is LinkDeletionException) error.message.orEmpty()
                            else
                                "Could not revoke this link. Check the connection and retry. The history entry has been kept.",
                        )
                } finally {
                    _deletingIds.update { it - id }
                    deleteJobs.remove(id)
                    if (visible && !_deviceHistory.value) startPolling()
                }
            }
    }
}

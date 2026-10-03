package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.historyRefreshBatch
import zip.psst.android.data.refreshHistoryEntry
import zip.psst.android.data.revokeHistoryEntry
import zip.psst.android.data.syncAccountHistory
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.LinkDeletionException
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
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class HistoryDeletionError(val id: String, val message: String)

class HistoryViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val dao = app.database.transferHistoryDao()

    val history: StateFlow<List<TransferHistoryEntity>> =
        combine(dao.getAll(), app.prefs.historyAccess) { rows, access ->
                rows.filter(access::permits)
            }
            .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    private val _deletingIds = MutableStateFlow<Set<String>>(emptySet())
    val deletingIds = _deletingIds.asStateFlow()
    private val _deletionError = MutableStateFlow<HistoryDeletionError?>(null)
    val deletionError = _deletionError.asStateFlow()

    val offline = MutableStateFlow(false)
    private var refreshJob: Job? = null
    private val deleteJobs = mutableMapOf<String, Job>()

    private var activeAccess = app.prefs.historyAccess.value

    init {
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (access == activeAccess) return@collect
                activeAccess = access
                refreshJob?.cancel()
                deleteJobs.values.toList().forEach { it.cancel() }
                _deletionError.value = null
            }
        }
    }

    fun refresh() {
        refreshJob?.cancel()
        refreshJob =
            viewModelScope.launch(Dispatchers.IO) {
                var offset = 0
                while (isActive) {
                    val access = app.prefs.historyAccess.value
                    val token = app.prefs.getSessionToken(access.serverUrl) ?: return@launch
                    val client = ApiClient(ServerConfig(access.serverUrl), sessionToken = token)
                    var failed = false
                    var remoteIds = emptySet<String>()
                    try {
                        val resources = client.auth.resources()
                        remoteIds =
                            resources.transfers.map { it.id }.toSet() +
                                resources.slots.map { it.id }
                        if (app.prefs.historyAccess.value != access) return@launch
                        syncAccountHistory(dao, resources, access) { app.prefs.historyAccess.value }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: AuthenticationRequiredException) {
                        if (
                            app.prefs.historyAccess.value == access &&
                                app.prefs.getSessionToken(access.serverUrl) == token
                        )
                            app.prefs.clearSession()
                        return@launch
                    } catch (_: Exception) {
                        failed = true
                    } finally {
                        client.close()
                    }
                    val rows = dao.getAll().first().filter(access::permits)
                    val batch =
                        if (failed) emptyList() else historyRefreshBatch(rows, offset, remoteIds)
                    offset += batch.size
                    for (row in batch) {
                        if (app.prefs.historyAccess.value != access) return@launch
                        try {
                            refreshHistoryEntry(dao, row.id, reportFailure = true)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (_: Exception) {
                            failed = true
                            break
                        }
                    }
                    offline.value = failed
                    delay(if (failed) 15000 else 5000)
                }
            }
    }

    fun stopRefreshing() {
        refreshJob?.cancel()
    }

    fun dismissDeletionError() {
        _deletionError.value = null
    }

    fun delete(id: String) {
        if (id in _deletingIds.value) return
        _deletingIds.update { it + id }
        _deletionError.value = null
        deleteJobs[id] =
            viewModelScope.launch {
                try {
                    withContext(Dispatchers.IO) {
                        revokeHistoryEntry(dao, id, { app.prefs.historyAccess.value }) { config ->
                            ApiClient(
                                config,
                                sessionToken = app.prefs.getSessionToken(config.normalizedBaseUrl),
                            )
                        }
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
                }
            }
    }
}

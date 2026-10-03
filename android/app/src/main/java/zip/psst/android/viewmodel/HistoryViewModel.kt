package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.refreshHistoryEntry
import zip.psst.android.data.revokeHistoryEntry
import zip.psst.shared.api.LinkDeletionException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

data class HistoryDeletionError(val id: String, val message: String)

class HistoryViewModel(application: Application) : AndroidViewModel(application) {

    private val dao = (application as PsstApplication).database.transferHistoryDao()

    val history: StateFlow<List<TransferHistoryEntity>> =
        dao.getAll().stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), emptyList())

    private val _deletingIds = MutableStateFlow<Set<String>>(emptySet())
    val deletingIds = _deletingIds.asStateFlow()
    private val _deletionError = MutableStateFlow<HistoryDeletionError?>(null)
    val deletionError = _deletionError.asStateFlow()

    private var refreshJob: Job? = null

    fun refresh() {
        refreshJob?.cancel()
        refreshJob =
            viewModelScope.launch(Dispatchers.IO) {
                withTimeoutOrNull(20_000L) {
                    val rows = dao.getAll().first()
                    for (batch in rows.take(20).chunked(4)) {
                        coroutineScope {
                            batch.map { async { refreshHistoryEntry(dao, it.id) } }.awaitAll()
                        }
                    }
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
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { revokeHistoryEntry(dao, id) }
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
            }
        }
    }
}

package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.refreshHistoryEntry
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

class HistoryViewModel(application: Application) : AndroidViewModel(application) {

    private val dao = (application as PsstApplication).database.transferHistoryDao()

    val history: StateFlow<List<TransferHistoryEntity>> =
        dao.getAll().stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), emptyList())

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

    fun delete(id: String) {
        viewModelScope.launch { dao.delete(id) }
    }
}

package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.refreshHistoryEntry
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.DropSlotStatus
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper
import java.time.Instant
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class TransferDetailUiState(
    val transferId: String = "",
    val type: String = "send", // "send" or "receive"
    val status: String = "pending",
    val fileCount: Int = 0,
    val totalSize: Long = 0,
    val expiresAt: String? = null,
    val shareUrl: String = "",
    val isLoading: Boolean = false,
    val error: String? = null,
)

class TransferDetailViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val _uiState = MutableStateFlow(TransferDetailUiState())
    val uiState: StateFlow<TransferDetailUiState> = _uiState.asStateFlow()

    @OptIn(ExperimentalEncodingApi::class)
    fun load(transferId: String, encryptionKey: String, type: String) {
        _uiState.value =
            TransferDetailUiState(transferId = transferId, type = type, isLoading = true)
        viewModelScope.launch {
            val dao = app.database.transferHistoryDao()
            val row = dao.getById(transferId)
            val serverUrl = row?.serverUrl ?: app.prefs.getServerUrl()
            val keyBytes =
                try {
                    Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
                        .decode(row?.encryptionKey ?: encryptionKey)
                } catch (_: Exception) {
                    ByteArray(0)
                }
            val shareUrl =
                if (type == "receive" || type == "received") {
                    UrlHelper.buildUploadUrl(serverUrl, transferId, keyBytes)
                } else {
                    UrlHelper.buildDownloadUrl(serverUrl, transferId, keyBytes)
                }
            _uiState.value = _uiState.value.copy(shareUrl = shareUrl)
            if (row != null) {
                // Keep local saved/activity facts if the remote entry has expired or is
                // unavailable.
                _uiState.value =
                    _uiState.value.copy(
                        isLoading = false,
                        status = row.status,
                        fileCount = row.fileCount,
                        totalSize = row.totalSize,
                        expiresAt = row.expiresAt?.let { Instant.ofEpochMilli(it).toString() },
                    )
                val refreshed = refreshHistoryEntry(dao, row.id) ?: return@launch
                _uiState.value =
                    _uiState.value.copy(
                        status = refreshed.status,
                        fileCount = refreshed.fileCount,
                        totalSize = refreshed.totalSize,
                        expiresAt = refreshed.expiresAt?.let { Instant.ofEpochMilli(it).toString() },
                    )
                return@launch
            }
            val client = ApiClient(ServerConfig(serverUrl))
            try {
                if (type == "receive" || type == "received") {
                    val slot = client.slots.get(transferId)
                    _uiState.value =
                        _uiState.value.copy(
                            isLoading = false,
                            status =
                                when {
                                    slot.status == DropSlotStatus.EXPIRED -> "expired"
                                    slot.completedTransfers.isEmpty() -> "waiting"
                                    else -> "has_uploads"
                                },
                            fileCount = slot.fileCount,
                            expiresAt = slot.expiresAt,
                        )
                } else {
                    val transfer = client.transfers.get(transferId)
                    _uiState.value =
                        _uiState.value.copy(
                            isLoading = false,
                            status =
                                if (transfer.downloadCount > 0) "download_started"
                                else transfer.status.name.lowercase(),
                            fileCount = transfer.fileCount,
                            totalSize = transfer.totalSize,
                            expiresAt = transfer.expiresAt,
                        )
                }
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                _uiState.value = _uiState.value.copy(isLoading = false, error = e.message)
            } finally {
                client.close()
            }
        }
    }
}

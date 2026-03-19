package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

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
        val serverUrl = app.prefs.getServerUrl()
        val keyBytes = try {
            Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).decode(encryptionKey)
        } catch (_: Exception) {
            ByteArray(0)
        }

        val shareUrl = if (type == "receive" || type == "received") {
            UrlHelper.buildUploadUrl(serverUrl, transferId, keyBytes)
        } else {
            UrlHelper.buildDownloadUrl(serverUrl, transferId, keyBytes)
        }

        _uiState.value = TransferDetailUiState(
            transferId = transferId,
            type = type,
            shareUrl = shareUrl,
            isLoading = true,
        )

        viewModelScope.launch {
            try {
                val client = ApiClient(ServerConfig(serverUrl))
                if (type == "receive" || type == "received") {
                    val slot = client.slots.get(transferId)
                    _uiState.value = _uiState.value.copy(
                        isLoading = false,
                        status = slot.status.name.lowercase(),
                        fileCount = slot.fileCount,
                        expiresAt = slot.expiresAt,
                    )
                } else {
                    val transfer = client.transfers.get(transferId)
                    _uiState.value = _uiState.value.copy(
                        isLoading = false,
                        status = transfer.status.name.lowercase(),
                        fileCount = transfer.fileCount,
                        totalSize = transfer.totalSize,
                        expiresAt = transfer.expiresAt,
                    )
                }
                client.close()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(
                    isLoading = false,
                    error = e.message,
                )
            }
        }
    }
}

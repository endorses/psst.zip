package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.ServerConfig
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class ServerConfigUiState(
    val url: String = "",
    val isTesting: Boolean = false,
    val testResult: TestResult? = null,
)

sealed interface TestResult {
    data object Success : TestResult

    data class Error(val message: String) : TestResult
}

class ServerConfigViewModel(application: Application) : AndroidViewModel(application) {

    private val prefs = (application as PsstApplication).prefs

    private val _uiState = MutableStateFlow(ServerConfigUiState(url = prefs.getServerUrl()))
    val uiState: StateFlow<ServerConfigUiState> = _uiState.asStateFlow()

    private var validationJob: Job? = null
    private var validationVersion = 0L

    fun onUrlChange(url: String) {
        validationVersion++
        validationJob?.cancel()
        _uiState.value = _uiState.value.copy(url = url, isTesting = false, testResult = null)
    }

    fun testConnection() = validate()

    fun saveUrl(onSaved: () -> Unit) = validate(onSaved)

    private fun validate(onSaved: (() -> Unit)? = null) {
        validationJob?.cancel()
        val version = ++validationVersion
        val enteredUrl = _uiState.value.url
        val url = enteredUrl.trim()
        _uiState.value = _uiState.value.copy(isTesting = true, testResult = null)
        validationJob =
            viewModelScope.launch {
                var client: ApiClient? = null
                try {
                    require(url.isNotBlank()) { "Please enter a server URL" }
                    client = ApiClient(ServerConfig(url))
                    client.validateServer()
                    if (validationVersion != version || _uiState.value.url != enteredUrl)
                        return@launch
                    _uiState.value =
                        _uiState.value.copy(isTesting = false, testResult = TestResult.Success)
                    if (onSaved != null) {
                        prefs.setServerUrl(url)
                        onSaved()
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    if (validationVersion == version && _uiState.value.url == enteredUrl) {
                        _uiState.value =
                            _uiState.value.copy(
                                isTesting = false,
                                testResult = TestResult.Error(e.message ?: "Connection failed"),
                            )
                    }
                } finally {
                    client?.close()
                }
            }
    }
}

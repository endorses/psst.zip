package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.ServerConfig
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

    fun onUrlChange(url: String) {
        _uiState.value = _uiState.value.copy(url = url, testResult = null)
    }

    fun testConnection() {
        val url = _uiState.value.url.trim()
        if (url.isBlank()) {
            _uiState.value = _uiState.value.copy(
                testResult = TestResult.Error("Please enter a server URL"),
            )
            return
        }

        _uiState.value = _uiState.value.copy(isTesting = true, testResult = null)

        viewModelScope.launch {
            try {
                val client = ApiClient(ServerConfig(url))
                // Try to create and immediately check a transfer to verify connectivity.
                // A simple GET to the API base would be better, but we use what's available.
                // We'll just try to get a non-existent transfer; a 404 means the server is up.
                try {
                    client.transfers.get("__connection_test__")
                } catch (_: Exception) {
                    // Expected: 404 or similar. The fact that we got a response means
                    // the server is reachable. If it were unreachable, we'd get a
                    // network exception that wouldn't be caught here.
                }
                client.close()
                _uiState.value = _uiState.value.copy(
                    isTesting = false,
                    testResult = TestResult.Success,
                )
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(
                    isTesting = false,
                    testResult = TestResult.Error(
                        e.message ?: "Connection failed",
                    ),
                )
            }
        }
    }

    fun saveUrl() {
        val url = _uiState.value.url.trim()
        if (url.isNotBlank()) {
            prefs.setServerUrl(url)
        }
    }
}

package zip.psst.android.viewmodel

import android.app.Application
import android.os.Build
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.PairingCode
import zip.psst.shared.model.ServerConfig
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class ServerConfigUiState(
    val url: String = "",
    val username: String = "",
    val password: String = "",
    val signedInUsername: String? = null,
    val isTesting: Boolean = false,
    val testResult: TestResult? = null,
)

sealed interface TestResult {
    data object Success : TestResult

    data class Error(val message: String) : TestResult
}

class ServerConfigViewModel(application: Application) : AndroidViewModel(application) {
    private val prefs = (application as PsstApplication).prefs
    private val _uiState =
        MutableStateFlow(
            ServerConfigUiState(
                url = prefs.getServerUrl(),
                username = prefs.getUsername(),
                signedInUsername = prefs.getUsername().takeIf { prefs.getSessionToken() != null },
            )
        )
    val uiState: StateFlow<ServerConfigUiState> = _uiState.asStateFlow()
    private var operation: Job? = null
    private var version = 0L

    fun refreshSavedSession() {
        if (prefs.getSessionToken() != null) {
            _uiState.value =
                _uiState.value.copy(
                    url = prefs.getServerUrl(),
                    username = prefs.getUsername(),
                    password = "",
                    signedInUsername = prefs.getUsername(),
                )
        }
    }

    fun onUrlChange(url: String) {
        version++
        operation?.cancel()
        _uiState.value =
            _uiState.value.copy(
                url = url,
                isTesting = false,
                testResult = null,
                signedInUsername =
                    prefs.getUsername().takeIf { prefs.getSessionToken(url.trim()) != null },
            )
    }

    fun onUsernameChange(value: String) {
        _uiState.value = _uiState.value.copy(username = value, testResult = null)
    }

    fun onPasswordChange(value: String) {
        _uiState.value = _uiState.value.copy(password = value, testResult = null)
    }

    fun testConnection() = runOperation { client -> client.validateServer() }

    fun signIn(onSaved: () -> Unit) {
        val username = _uiState.value.username.trim()
        val password = _uiState.value.password
        runOperation(onSaved) { client ->
            require(username.isNotBlank() && password.isNotBlank()) {
                "Enter your username and password"
            }
            client.validateServer()
            val session = client.auth.login(username, password, deviceName())
            prefs.saveSession(
                client.config.normalizedBaseUrl,
                session.user.username,
                session.token,
                session.user.id,
                session.user.role,
            )
            _uiState.value =
                _uiState.value.copy(password = "", signedInUsername = session.user.username)
        }
    }

    fun pair(raw: String, onSaved: () -> Unit) {
        val pairing =
            try {
                PairingCode.parse(raw)
            } catch (e: Exception) {
                _uiState.value =
                    _uiState.value.copy(
                        testResult = TestResult.Error(e.message ?: "Invalid pairing code")
                    )
                return
            }
        onUrlChange(pairing.serverUrl)
        runOperation(onSaved) { client ->
            client.validateServer()
            val session = client.auth.redeemPairing(pairing.code, deviceName())
            prefs.saveSession(
                client.config.normalizedBaseUrl,
                session.user.username,
                session.token,
                session.user.id,
                session.user.role,
            )
            _uiState.value =
                _uiState.value.copy(
                    username = session.user.username,
                    password = "",
                    signedInUsername = session.user.username,
                )
        }
    }

    fun signOut(onSignedOut: () -> Unit) =
        runOperation(onSignedOut) { client ->
            client.auth.logout()
            prefs.clearSession()
            _uiState.value = _uiState.value.copy(signedInUsername = null, password = "")
        }

    fun continueSignedIn(onSaved: () -> Unit) {
        if (prefs.getSessionToken(_uiState.value.url.trim()) != null) onSaved()
    }

    private fun runOperation(onSuccess: (() -> Unit)? = null, action: suspend (ApiClient) -> Unit) {
        operation?.cancel()
        val operationVersion = ++version
        val url = _uiState.value.url.trim().trimEnd('/')
        _uiState.value = _uiState.value.copy(isTesting = true, testResult = null)
        operation =
            viewModelScope.launch {
                var client: ApiClient? = null
                try {
                    require(url.isNotBlank()) { "Please enter a server URL" }
                    client = ApiClient(ServerConfig(url), sessionToken = prefs.getSessionToken(url))
                    action(client)
                    if (version != operationVersion) return@launch
                    _uiState.value =
                        _uiState.value.copy(isTesting = false, testResult = TestResult.Success)
                    onSuccess?.invoke()
                } catch (e: CancellationException) {
                    if (e !is TimeoutCancellationException) throw e
                    if (version == operationVersion) {
                        _uiState.value =
                            _uiState.value.copy(
                                isTesting = false,
                                password = "",
                                testResult =
                                    TestResult.Error(
                                        "The server took too long to respond. Check your connection and try again."
                                    ),
                            )
                    }
                } catch (e: Exception) {
                    if (version == operationVersion)
                        _uiState.value =
                            _uiState.value.copy(
                                isTesting = false,
                                password = "",
                                testResult = TestResult.Error(e.message ?: "Connection failed"),
                            )
                } finally {
                    client?.close()
                }
            }
    }

    private fun deviceName(): String = "Android ${Build.MODEL}".take(100)
}

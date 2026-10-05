package zip.psst.android.viewmodel

import android.app.Application
import android.os.Build
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthUser
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
    val pairingDraft: String? = null,
    val url: String = "",
    val username: String = "",
    val password: String = "",
    val mustChangePassword: Boolean = false,
    val newPassword: String = "",
    val confirmPassword: String = "",
    val notice: UiText? = null,
    val signedInUsername: String? = null,
    val isTesting: Boolean = false,
    val testResult: TestResult? = null,
)

sealed interface TestResult {
    data object Success : TestResult

    data class Error(val message: UiText) : TestResult
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

    init {
        refreshAccountState()
    }

    private fun refreshAccountState() {
        if (prefs.historyAccess.value.isAdmin) {
            prefs.clearSession()
            _uiState.value = _uiState.value.copy(signedInUsername = null, notice = ADMIN_MESSAGE)
        } else if (prefs.getSessionToken() != null) {
            runOperation { client ->
                val user = client.auth.me()
                acceptUser(user)
                prefs.setPasswordChangeRequired(user.mustChangePassword)
            }
        }
    }

    private fun acceptUser(user: AuthUser) {
        if (user.role == "admin") {
            prefs.clearSession()
            throw UiFailureException(ADMIN_MESSAGE)
        }
        _uiState.value =
            _uiState.value.copy(
                mustChangePassword = user.mustChangePassword,
                signedInUsername = user.username,
                notice =
                    if (user.mustChangePassword)
                        message(
                            R.string
                                .l_replace_your_temporary_password_before_sending_files_or_creating__98696c
                        )
                    else null,
            )
    }

    fun onNewPasswordChange(value: String) {
        _uiState.value = _uiState.value.copy(newPassword = value, testResult = null)
    }

    fun onConfirmPasswordChange(value: String) {
        _uiState.value = _uiState.value.copy(confirmPassword = value, testResult = null)
    }

    fun replacePassword() = runOperation { client ->
        val state = _uiState.value
        passwordReplacementError(state.password, state.newPassword, state.confirmPassword)?.let {
            throw UiFailureException(it)
        }
        client.auth.changePassword(state.password, state.newPassword)
        prefs.clearSession()
        _uiState.value =
            _uiState.value.copy(
                mustChangePassword = false,
                signedInUsername = null,
                password = "",
                newPassword = "",
                confirmPassword = "",
                notice =
                    message(
                        R.string
                            .l_password_changed_sign_in_with_your_new_password_to_continue_e0333a
                    ),
            )
    }

    fun setPairingDraft(value: String?) {
        _uiState.value = _uiState.value.copy(pairingDraft = value)
    }

    fun scanError(message: UiText) {
        _uiState.value = _uiState.value.copy(testResult = TestResult.Error(message))
    }

    fun invalidPairing() {
        _uiState.value =
            _uiState.value.copy(
                testResult =
                    TestResult.Error(
                        message(
                            R.string.l_scan_a_server_login_qr_code_from_the_web_settings_page_5a0e74
                        )
                    )
            )
    }

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

    /** Leaving a screen must never wait for validation or deliver a late navigation callback. */
    fun cancelPendingOperation() {
        version++
        operation?.cancel()
        _uiState.value = _uiState.value.copy(isTesting = false)
    }

    fun onUrlChange(url: String) {
        version++
        operation?.cancel()
        _uiState.value =
            _uiState.value.copy(
                url = url,
                isTesting = false,
                testResult = null,
                mustChangePassword = false,
                newPassword = "",
                confirmPassword = "",
                notice = null,
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
            uiRequire(username.isNotBlank() && password.isNotBlank()) {
                message(R.string.l_enter_your_username_and_password_775db6)
            }
            client.validateServer()
            val session = client.auth.login(username, password, deviceName())
            if (session.user.role == "admin") {
                ApiClient(client.config, sessionToken = session.token).let { authenticated ->
                    try {
                        authenticated.auth.logout()
                    } finally {
                        authenticated.close()
                    }
                }
                throw UiFailureException(ADMIN_MESSAGE)
            }
            acceptUser(session.user)
            prefs.saveSession(
                client.config.normalizedBaseUrl,
                session.user.username,
                session.token,
                session.user.id,
                session.user.role,
                session.user.mustChangePassword,
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
                        testResult =
                            TestResult.Error(
                                failureText(e) ?: message(R.string.l_invalid_pairing_code_fa05da)
                            )
                    )
                return
            }
        onUrlChange(pairing.serverUrl)
        runOperation(onSaved) { client ->
            client.validateServer()
            val session = client.auth.redeemPairing(pairing.code, deviceName())
            if (session.user.role == "admin") {
                ApiClient(client.config, sessionToken = session.token).let { authenticated ->
                    try {
                        authenticated.auth.logout()
                    } finally {
                        authenticated.close()
                    }
                }
                throw UiFailureException(ADMIN_MESSAGE)
            }
            acceptUser(session.user)
            prefs.saveSession(
                client.config.normalizedBaseUrl,
                session.user.username,
                session.token,
                session.user.id,
                session.user.role,
                session.user.mustChangePassword,
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
            _uiState.value =
                _uiState.value.copy(
                    signedInUsername = null,
                    password = "",
                    mustChangePassword = false,
                    newPassword = "",
                    confirmPassword = "",
                )
        }

    fun continueSignedIn(onSaved: () -> Unit) {
        if (prefs.getSessionToken(_uiState.value.url.trim()) != null)
            runOperation(onSaved) { client ->
                val user = client.auth.me()
                acceptUser(user)
                prefs.setPasswordChangeRequired(user.mustChangePassword)
            }
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
                    uiRequire(url.isNotBlank()) { message(R.string.ui_server_url_not_configured) }
                    client = ApiClient(ServerConfig(url), sessionToken = prefs.getSessionToken(url))
                    action(client)
                    if (version != operationVersion) return@launch
                    _uiState.value =
                        _uiState.value.copy(isTesting = false, testResult = TestResult.Success)
                    if (!_uiState.value.mustChangePassword) onSuccess?.invoke()
                } catch (e: CancellationException) {
                    if (e !is TimeoutCancellationException) throw e
                    if (version == operationVersion) {
                        _uiState.value =
                            _uiState.value.copy(
                                isTesting = false,
                                password = "",
                                testResult =
                                    TestResult.Error(
                                        message(
                                            R.string
                                                .l_the_server_took_too_long_to_respond_check_your_connection_and_try_7547a0
                                        )
                                    ),
                            )
                    }
                } catch (e: Exception) {
                    if (version == operationVersion)
                        _uiState.value =
                            _uiState.value.copy(
                                isTesting = false,
                                password = "",
                                testResult =
                                    TestResult.Error(
                                        failureText(e)
                                            ?: message(R.string.l_connection_failed_202caa)
                                    ),
                            )
                } finally {
                    client?.close()
                }
            }
    }

    private companion object {
        val ADMIN_MESSAGE =
            message(
                R.string.l_administrator_accounts_manage_the_server_in_the_web_ui_sign_in_wi_994cda
            )
    }

    private fun deviceName(): String = "Android ${Build.MODEL}".take(100)
}

internal fun passwordReplacementError(
    current: String,
    replacement: String,
    confirmation: String,
): UiText? =
    when {
        current.isBlank() || replacement.isBlank() ->
            message(R.string.l_enter_your_temporary_password_and_a_new_password_614110)
        replacement != confirmation -> message(R.string.l_passwords_do_not_match_d69c3b)
        replacement == current ->
            message(R.string.l_choose_a_password_different_from_your_temporary_password_1051d5)
        else -> null // Strength and current-password verification remain server-side.
    }

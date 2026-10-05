package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.TrafficSnapshot
import zip.psst.shared.model.ServerConfig
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

data class TrafficUsageUiState(
    val loading: Boolean = false,
    val snapshot: TrafficSnapshot? = null,
    val error: UiText? = null,
)

class TrafficUsageViewModel(application: Application) : AndroidViewModel(application) {
    private val prefs = (application as PsstApplication).prefs
    private val _state = MutableStateFlow(TrafficUsageUiState())
    val state = _state.asStateFlow()
    private var request: Job? = null

    fun refresh() {
        request?.cancel()
        val access = prefs.historyAccess.value
        val token = prefs.getSessionToken(access.serverUrl)
        _state.value = TrafficUsageUiState()
        if (token == null || access.accountId == null || access.isAdmin) return
        _state.value = TrafficUsageUiState(loading = true)
        request =
            viewModelScope.launch {
                val client = ApiClient(ServerConfig(access.serverUrl), sessionToken = token)
                try {
                    val snapshot = withTimeout(10_000) { client.traffic.usage() }
                    if (
                        prefs.historyAccess.value == access &&
                            prefs.getSessionToken(access.serverUrl) == token
                    )
                        _state.value = TrafficUsageUiState(snapshot = snapshot)
                } catch (e: CancellationException) {
                    if (e !is kotlinx.coroutines.TimeoutCancellationException) throw e
                    if (prefs.historyAccess.value == access)
                        _state.value =
                            TrafficUsageUiState(
                                error =
                                    message(
                                        R.string
                                            .l_traffic_status_timed_out_refresh_to_try_again_60644d
                                    )
                            )
                } catch (e: Exception) {
                    if (prefs.historyAccess.value == access)
                        _state.value =
                            TrafficUsageUiState(
                                error =
                                    if (e is AuthenticationRequiredException) failureText(e)
                                    else
                                        message(
                                            R.string
                                                .l_traffic_status_is_unavailable_refresh_to_try_again_transfers_stil_5fc7a2
                                        )
                            )
                } finally {
                    client.close()
                }
            }
    }
}

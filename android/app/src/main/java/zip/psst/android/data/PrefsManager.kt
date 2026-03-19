package zip.psst.android.data

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class PrefsManager(context: Context) {

    private val prefs: SharedPreferences =
        context.getSharedPreferences("psst_prefs", Context.MODE_PRIVATE)

    private val _serverUrl = MutableStateFlow(prefs.getString(KEY_SERVER_URL, "") ?: "")
    val serverUrl: StateFlow<String> = _serverUrl.asStateFlow()

    fun getServerUrl(): String = prefs.getString(KEY_SERVER_URL, "") ?: ""

    fun setServerUrl(url: String) {
        prefs.edit().putString(KEY_SERVER_URL, url).apply()
        _serverUrl.value = url
    }

    fun hasServerUrl(): Boolean = getServerUrl().isNotBlank()

    companion object {
        private const val KEY_SERVER_URL = "server_url"
    }
}

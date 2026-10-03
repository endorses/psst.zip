package zip.psst.android.data

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class PrefsManager(context: Context) {
    private val sessions = SessionStorage(context)

    private val prefs: SharedPreferences =
        context.getSharedPreferences("psst_prefs", Context.MODE_PRIVATE)

    private val _serverUrl = MutableStateFlow(prefs.getString(KEY_SERVER_URL, "") ?: "")
    val serverUrl: StateFlow<String> = _serverUrl.asStateFlow()

    private val _appearance =
        MutableStateFlow(Appearance.fromPreference(prefs.getString(KEY_APPEARANCE, null)))
    val appearance: StateFlow<Appearance> = _appearance.asStateFlow()

    fun setAppearance(appearance: Appearance) {
        prefs.edit().putString(KEY_APPEARANCE, appearance.preferenceValue).apply()
        _appearance.value = appearance
    }

    private val _historyAccess = MutableStateFlow(readHistoryAccess())
    val historyAccess: StateFlow<HistoryAccess> = _historyAccess.asStateFlow()

    fun getSessionToken(serverUrl: String = getServerUrl()): String? {
        if (sessions.accountId().isNullOrBlank()) return null
        val token = sessions.token(serverUrl)
        if (token == null && serverUrl == getServerUrl()) _historyAccess.value = HistoryAccess()
        return token
    }

    fun getUsername(): String = sessions.username()

    fun getAccountId(): String? = historyAccess.value.accountId

    fun saveSession(url: String, username: String, token: String, accountId: String, role: String) {
        require(accountId.isNotBlank()) { "The server returned an invalid account" }
        sessions.save(url, username, token, accountId, role)
        setServerUrl(url)
    }

    fun clearSession() {
        sessions.clear()
        _historyAccess.value = HistoryAccess()
    }

    private fun readHistoryAccess(): HistoryAccess {
        val url = getServerUrl()
        if (sessions.accountId().isNullOrBlank() || sessions.token(url) == null)
            return HistoryAccess()
        return HistoryAccess(url, sessions.accountId(), sessions.isAdmin())
    }

    fun getServerUrl(): String = prefs.getString(KEY_SERVER_URL, "") ?: ""

    fun setServerUrl(url: String) {
        prefs.edit().putString(KEY_SERVER_URL, url).apply()
        _serverUrl.value = url
        _historyAccess.value = readHistoryAccess()
    }

    fun hasServerUrl(): Boolean = getServerUrl().isNotBlank()

    companion object {
        private const val KEY_SERVER_URL = "server_url"
        private const val KEY_APPEARANCE = "appearance"
    }
}

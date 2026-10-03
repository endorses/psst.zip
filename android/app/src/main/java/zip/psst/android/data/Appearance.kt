package zip.psst.android.data

enum class Appearance(val preferenceValue: String) {
    SYSTEM("system"),
    LIGHT("light"),
    DARK("dark");

    fun isDark(systemIsDark: Boolean): Boolean =
        when (this) {
            SYSTEM -> systemIsDark
            LIGHT -> false
            DARK -> true
        }

    companion object {
        fun fromPreference(value: String?): Appearance =
            entries.firstOrNull { it.preferenceValue == value } ?: SYSTEM
    }
}

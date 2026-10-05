package zip.psst.android.data

/** Language selection is independent of the account and transfer state. */
enum class AppLanguage(val tag: String) {
    SYSTEM(""),
    ENGLISH("en"),
    GERMAN("de");

    companion object {
        fun fromTag(tag: String?): AppLanguage =
            when (tag?.substringBefore(',')?.substringBefore('-')?.lowercase()) {
                "de" -> GERMAN
                "en" -> ENGLISH
                else -> SYSTEM
            }

        fun resolve(preference: AppLanguage, systemTags: List<String>): String =
            if (preference != SYSTEM) preference.tag
            else
                systemTags.firstNotNullOfOrNull { tag ->
                    when (tag.substringBefore('-').lowercase()) {
                        "de" -> "de"
                        "en" -> "en"
                        else -> null
                    }
                } ?: "en"
    }
}

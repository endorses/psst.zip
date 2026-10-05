package zip.psst.android.i18n

import android.content.Context
import androidx.annotation.StringRes

/** Stable resource identity and arguments; never retain the rendered language in UI state. */
data class UiText(
    @StringRes val resource: Int,
    val arguments: List<Any?> = emptyList(),
    val quantity: Int? = null,
    val literal: String? = null,
) {
    fun resolve(context: Context): String {
        literal?.let {
            return it
        }
        val template =
            if (quantity == null) context.getString(resource)
            else context.resources.getQuantityString(resource, quantity)
        val locale = context.resources.configuration.locales[0]
        val args =
            arguments
                .mapIndexed { index, it ->
                    when (it) {
                        is UiText -> it.resolve(context)
                        is UiByteCount ->
                            UiFormatting.bytes(it.bytes, context.resources.configuration.locales[0])
                        is UiRetryTime ->
                            UiFormatting.timestamp(
                                it.epochMillis,
                                context.resources.configuration.locales[0],
                            )
                        is Number ->
                            if (template.contains("%${index + 1}\$s"))
                                UiFormatting.number(it, locale)
                            else it
                        else -> it
                    }
                }
                .toTypedArray()
        return if (quantity == null) context.getString(resource, *args)
        else context.resources.getQuantityString(resource, quantity, *args)
    }

    fun text(): String = literal ?: resolve(UiStrings.context())
}

fun message(@StringRes resource: Int, vararg arguments: Any?): UiText =
    UiText(resource, arguments.toList())

/** Known local failures are stable values too; arbitrary remote exception prose is never shown. */
open class UiFailureException(val display: UiText) : IllegalArgumentException()

object UiStrings {
    lateinit var application: Context
    private val revision = androidx.compose.runtime.mutableIntStateOf(0)

    fun configurationChanged() {
        revision.intValue++
    }

    fun context(): Context {
        revision.intValue // Snapshot read makes ordinary resource helpers reactive in Compose too.
        val selected = androidx.appcompat.app.AppCompatDelegate.getApplicationLocales()
        val device = android.content.res.Resources.getSystem().configuration.locales
        val desired =
            if (selected.isEmpty)
                (0 until device.size())
                    .map { device[it] }
                    .firstOrNull { it.language in listOf("en", "de") } ?: java.util.Locale.ENGLISH
            else selected[0]!!
        val regional =
            (0 until device.size())
                .map { device[it] }
                .firstOrNull { it.language == desired.language }
        val locale = if (desired.country.isEmpty()) regional ?: desired else desired
        val configuration = android.content.res.Configuration(application.resources.configuration)
        configuration.setLocales(android.os.LocaleList(locale))
        return application.createConfigurationContext(configuration)
    }
}

fun tr(@StringRes resource: Int, vararg arguments: Any?): String =
    message(resource, *arguments).resolve(UiStrings.context())

fun plural(resource: Int, count: Long, vararg arguments: Any?): String =
    pluralMessage(resource, count, *arguments).resolve(UiStrings.context())

fun pluralMessage(resource: Int, count: Long, vararg arguments: Any?): UiText =
    UiText(resource, arguments.toList(), count.coerceIn(0, Int.MAX_VALUE.toLong()).toInt())

fun String.text(): String = this

inline fun uiRequire(
    condition: Boolean,
    display: () -> UiText = { message(zip.psst.android.R.string.failure_unknown) },
) {
    if (!condition) throw UiFailureException(display())
}

inline fun uiCheck(
    condition: Boolean,
    display: () -> UiText = { message(zip.psst.android.R.string.failure_unknown) },
) {
    if (!condition) throw UiFailureException(display())
}

inline fun <T : Any> uiRequireNotNull(
    value: T?,
    display: () -> UiText = { message(zip.psst.android.R.string.failure_unknown) },
): T = value ?: throw UiFailureException(display())

fun userText(value: String): UiText = UiText(0, literal = value)

fun displayBytes(value: Long): String =
    UiFormatting.bytes(value, UiStrings.context().resources.configuration.locales[0])

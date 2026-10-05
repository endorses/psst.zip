package zip.psst.android.i18n

import java.text.DateFormat
import java.text.NumberFormat
import java.util.Date
import java.util.Locale

/** Display only. Wire numbers, storage values, timestamps and identifiers never use these. */
object UiFormatting {
    fun number(value: Number, locale: Locale): String =
        NumberFormat.getNumberInstance(locale).format(value)

    fun bytes(value: Long, locale: Locale): String {
        val units = arrayOf("B", "KiB", "MiB", "GiB", "TiB")
        var amount = value.coerceAtLeast(0).toDouble()
        var unit = 0
        while (amount >= 1024 && unit < units.lastIndex) {
            amount /= 1024
            unit++
        }
        val formatter =
            NumberFormat.getNumberInstance(locale).apply {
                minimumFractionDigits = 0
                maximumFractionDigits = if (unit == 0) 0 else 1
            }
        return formatter.format(amount) + " " + units[unit]
    }

    fun percentage(value: Double, locale: Locale): String =
        NumberFormat.getPercentInstance(locale).format(value)

    fun timestamp(millis: Long, locale: Locale): String =
        DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT, locale)
            .format(Date(millis))
}

data class UiByteCount(val bytes: Long)

data class UiRetryTime(val epochMillis: Long)

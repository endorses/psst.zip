package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.UiText
import java.io.File
import java.util.Locale
import javax.xml.parsers.DocumentBuilderFactory

/** Test-only rendering of the actual native en/de resources; production always uses Resources. */
fun UiText?.english(): String? = localized("en")

fun UiText?.localized(language: String): String? {
    require(language in setOf("en", "de"))
    val directory = if (language == "de") "values-de" else "values"
    if (this == null) return null
    literal?.let {
        return it
    }
    val kind = if (quantity == null) R.string::class.java else R.plurals::class.java
    val key = kind.fields.first { it.getInt(null) == resource }.name
    val file =
        listOf(
                "src/main/res/$directory/strings.xml",
                "app/src/main/res/$directory/strings.xml",
                "android/app/src/main/res/$directory/strings.xml",
            )
            .map(::File)
            .first { it.exists() }
    val nodes =
        DocumentBuilderFactory.newInstance()
            .newDocumentBuilder()
            .parse(file)
            .documentElement
            .childNodes
    for (i in 0 until nodes.length) {
        val node = nodes.item(i)
        if (node.attributes?.getNamedItem("name")?.nodeValue != key) continue
        var text = node.textContent
        if (quantity != null) {
            val items = node.childNodes
            val category = if (quantity == 1) "one" else "other"
            text =
                (0 until items.length)
                    .map(items::item)
                    .first { it.attributes?.getNamedItem("quantity")?.nodeValue == category }
                    .textContent
        }
        val args = arguments.map { if (it is UiText) it.localized(language) else it }.toTypedArray()
        return String.format(
            Locale.forLanguageTag(language),
            text.replace("\\'", "'").replace("\\n", "\n"),
            *args,
        )
    }
    error("Missing native resource $key")
}

package zip.psst.shared.model

import kotlinx.serialization.Serializable

/** Optional shared descriptive metadata. Never derive this from private filenames. */
@Serializable
data class LinkTitle(val title: String?) {
    companion object {
        @Throws(Exception::class)
        fun normalize(value: String?): String? {
            if (value == null) return null
            var scalars = 0
            var index = 0
            while (index < value.length) {
                val char = value[index]
                require(char.code !in 0..31 && char.code !in 127..159) {
                    "Use a title without control characters"
                }
                if (char.isHighSurrogate()) {
                    require(index + 1 < value.length && value[index + 1].isLowSurrogate()) {
                        "Use a valid Unicode title"
                    }
                    index++
                } else require(!char.isLowSurrogate()) { "Use a valid Unicode title" }
                index++
            }
            val title =
                value.trim {
                    it in
                        " \u0085\u00A0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200A\u2028\u2029\u202F\u205F\u3000"
                }
            if (title.isEmpty()) return null
            index = 0
            while (index < title.length) {
                scalars++
                if (title[index].isHighSurrogate()) index++
                index++
            }
            require(scalars <= 200 && title.encodeToByteArray().size <= 800) {
                "Use a title of at most 200 Unicode characters"
            }
            return title
        }
    }
}

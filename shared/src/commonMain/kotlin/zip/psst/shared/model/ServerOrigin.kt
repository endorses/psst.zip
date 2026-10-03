package zip.psst.shared.model

/** Strict origin-only parsing, shared by pairing and QR links. Never includes input in errors. */
object ServerOrigin {
    fun normalize(raw: String): String? {
        if (raw.length > 2048 || raw.any { it.isWhitespace() || it.code < 32 }) return null
        val match =
            Regex(
                    "^(https?)://(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+)(?::([0-9]{1,5}))?/?$",
                    RegexOption.IGNORE_CASE,
                )
                .matchEntire(raw) ?: return null
        val scheme = match.groupValues[1].lowercase()
        val host = match.groupValues[2].lowercase()
        val normalizedHost =
            if (host.startsWith("[")) {
                val ipv6 = normalizeIpv6(host.substring(1, host.length - 1)) ?: return null
                "[$ipv6]"
            } else {
                val domain = host.removeSuffix(".")
                if (
                    domain.length !in 1..253 ||
                        domain.split('.').any {
                            it.length !in 1..63 ||
                                !Regex("[a-z0-9](?:[a-z0-9-]*[a-z0-9])?").matches(it)
                        }
                )
                    return null
                // Avoid ambiguous legacy numeric-host interpretations by HTTP stacks.
                if (domain.all { it.isDigit() || it == '.' } && parseIpv4(domain) == null)
                    return null
                domain
            }
        val portText = match.groupValues[3]
        val port = if (portText.isEmpty()) null else portText.toIntOrNull() ?: return null
        if (port != null && port !in 1..65535) return null
        val suffix =
            if (
                port == null ||
                    (scheme == "http" && port == 80) ||
                    (scheme == "https" && port == 443)
            )
                ""
            else ":$port"
        return "$scheme://$normalizedHost$suffix"
    }

    private fun parseIpv4(value: String): List<Int>? {
        val parts = value.split('.')
        if (parts.size != 4) return null
        return parts.map {
            if (
                it.isEmpty() ||
                    (it.length > 1 && it.startsWith('0')) ||
                    it.any { c -> !c.isDigit() }
            )
                return null
            val n = it.toIntOrNull() ?: return null
            if (n !in 0..255) return null
            n
        }
    }

    private fun normalizeIpv6(value: String): String? {
        var address = value
        if (address.contains('.')) {
            val tail = address.substringAfterLast(':')
            val bytes = parseIpv4(tail) ?: return null
            address =
                address.substringBeforeLast(':') +
                    ":${((bytes[0] shl 8) + bytes[1]).toString(16)}:${((bytes[2] shl 8) + bytes[3]).toString(16)}"
        }
        val sides = address.split("::")
        if (sides.size > 2) return null
        fun groups(side: String): List<Int>? {
            if (side.isEmpty()) return emptyList()
            return side.split(':').map {
                if (!Regex("[0-9a-f]{1,4}").matches(it)) return null
                it.toInt(16)
            }
        }
        val left = groups(sides[0]) ?: return null
        val right = if (sides.size == 2) groups(sides[1]) ?: return null else emptyList()
        val missing = 8 - left.size - right.size
        if ((sides.size == 1 && missing != 0) || (sides.size == 2 && missing < 1)) return null
        // Expanded, lowercase form is deterministic across equivalent scanned IPv6 origins.
        return (left + List(missing) { 0 } + right).joinToString(":") { it.toString(16) }
    }
}

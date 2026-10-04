package zip.psst.shared.model

/** Contact-only reporting. References never store links, keys, filenames or message text. */
enum class AbuseResourceKind {
    TRANSFER,
    SLOT,
}

class AbuseReportReference
private constructor(val origin: String, val kind: AbuseResourceKind?, val resourceId: String?) {
    val text: String
        get() =
            "Instance: $origin" +
                if (kind != null && resourceId != null)
                    "\nResource: ${kind.name.lowercase()} $resourceId"
                else ""

    companion object {
        /** Extract report identity even if the key is invalid; never retain query or fragment. */
        fun fromRawLink(raw: String): AbuseReportReference? {
            if (raw.length > ScanInputClassifier.MAX_INPUT_LENGTH) return null
            val match =
                Regex(
                        "^(https?://[^/?#]+)/([du])/([0-9a-fA-F-]+)(?:[?#].*)?$",
                        RegexOption.IGNORE_CASE,
                    )
                    .matchEntire(raw.trim()) ?: return null
            return create(
                match.groupValues[1],
                if (match.groupValues[2].lowercase() == "d") AbuseResourceKind.TRANSFER
                else AbuseResourceKind.SLOT,
                match.groupValues[3],
            )
        }

        fun create(
            origin: String,
            kind: AbuseResourceKind? = null,
            resourceId: String? = null,
        ): AbuseReportReference? {
            val safeOrigin = ServerOrigin.normalize(origin) ?: return null
            if ((kind == null) != (resourceId == null)) return null
            if (resourceId != null && !UrlHelper.isResourceId(resourceId)) return null
            return AbuseReportReference(safeOrigin, kind, resourceId?.lowercase())
        }

        fun fromLink(link: ParsedUrl): AbuseReportReference? =
            create(
                link.origin,
                if (link.type == UrlType.DOWNLOAD) AbuseResourceKind.TRANSFER
                else AbuseResourceKind.SLOT,
                link.id,
            )
    }
}

object AbuseContact {
    /** A single ASCII mailbox, never a mailto URI, display name, or list of recipients. */
    fun normalize(email: String): String? {
        if (email.length !in 3..254 || email.any { it.code !in 33..126 }) return null
        val parts = email.split('@')
        if (parts.size != 2) return null
        val local = parts[0]
        if (
            local.length !in 1..64 ||
                local.startsWith('.') ||
                local.endsWith('.') ||
                ".." in local ||
                !Regex("[A-Za-z0-9._+%\\-]+").matches(local)
        )
            return null
        val labels = parts[1].split('.')
        if (
            labels.size < 2 ||
                labels.any {
                    it.length !in 1..63 ||
                        !Regex("[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?").matches(it)
                }
        )
            return null
        return "$local@${parts[1].lowercase()}"
    }
}

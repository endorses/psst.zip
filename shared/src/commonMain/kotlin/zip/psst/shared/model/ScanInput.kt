package zip.psst.shared.model

import zip.psst.shared.api.PairingCode

/** Classified locally, before permissions, account changes, or network requests. */
enum class ScanInputKind {
    DOWNLOAD,
    UPLOAD,
    PAIRING,
}

/**
 * Intentionally not a data class: its default description must never expose keys or pairing codes.
 */
class ScanInput
internal constructor(
    val kind: ScanInputKind,
    val link: ParsedUrl? = null,
    val pairing: PairingCode? = null,
)

object ScanInputClassifier {
    const val MAX_INPUT_LENGTH = 4096

    fun classify(raw: String): ScanInput? {
        if (raw.length > MAX_INPUT_LENGTH) return null
        val input = raw.trim()
        if (input.startsWith("{")) {
            val pairing =
                try {
                    PairingCode.parse(input)
                } catch (_: Exception) {
                    return null
                }
            return ScanInput(ScanInputKind.PAIRING, pairing = pairing)
        }
        val link = UrlHelper.parse(input) ?: return null
        if (link.type == UrlType.UPLOAD && link.receiveVersion != 2) return null
        return ScanInput(
            if (link.type == UrlType.DOWNLOAD) ScanInputKind.DOWNLOAD else ScanInputKind.UPLOAD,
            link = link,
        )
    }
}

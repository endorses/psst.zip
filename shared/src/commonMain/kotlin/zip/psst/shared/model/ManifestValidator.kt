package zip.psst.shared.model

/**
 * Validation is performed on authenticated plaintext, before any blob request or filesystem write.
 */
object ManifestValidator {
    @Throws(Exception::class)
    fun validate(manifest: Manifest): Long {
        require(manifest.files.size in 1..TransferLimits.MAX_FILES) {
            "Unsupported number of files in this transfer"
        }
        val ids = mutableSetOf<String>()
        var total = 0L
        for (file in manifest.files) {
            require(UrlHelper.isResourceId(file.blobId) && ids.add(file.blobId.lowercase())) {
                "Invalid or duplicate file identifier"
            }
            require(file.size in 0..TransferLimits.MAX_FILE_BYTES.toLong()) {
                "A file exceeds this app's supported size limit"
            }
            safeFilename(file.name)
            require(file.size <= Long.MAX_VALUE - total) { "Invalid transfer size" }
            total += file.size
        }
        return total
    }

    @Throws(Exception::class)
    fun validateForTransfer(manifest: Manifest, transfer: Transfer): Long {
        require(transfer.status == TransferStatus.COMPLETE) {
            "This transfer is not ready to download"
        }
        val total = validate(manifest)
        require(
            transfer.fileCount == manifest.files.size &&
                transfer.totalSize == total + 28L * manifest.files.size
        ) {
            "The transfer details do not match its encrypted file list"
        }
        return total
    }

    /**
     * No directories or traversal; remove control/bidi characters and portable filesystem hazards.
     */
    @Throws(Exception::class)
    fun safeFilename(name: String): String {
        require(
            name.isNotBlank() &&
                name.length <= 1024 &&
                '/' !in name &&
                '\\' !in name &&
                '\u0000' !in name
        ) {
            "A file has an unsafe name"
        }
        val safe =
            name
                .map {
                    if (
                        it.code < 32 ||
                            it.code in 127..159 ||
                            it.code in 0x202A..0x202E ||
                            it.code in 0x2066..0x2069 ||
                            it in ":*?\"<>|"
                    )
                        '_'
                    else it
                }
                .joinToString("")
                .trim()
                .trimEnd('.')
        require(safe.isNotBlank() && safe != "." && safe != "..") { "A file has an unsafe name" }
        // Bound UTF-8 bytes for both Android's and Apple's filesystem component limits, retaining
        // extension.
        if (safe.encodeToByteArray().size <= 200) return safe
        val extension =
            safe
                .substringAfterLast('.', "")
                .takeIf { it.length in 1..16 && it.all { c -> c.isLetterOrDigit() } }
                ?.let { ".$it" } ?: ""
        var stem = safe.substringBeforeLast('.', safe)
        while ((stem + extension).encodeToByteArray().size > 200) stem = stem.dropLast(1)
        return stem.trimEnd { it.isHighSurrogate() } + extension
    }
}

package zip.psst.shared.zip

/**
 * Represents a single entry (file) within a ZIP archive.
 */
data class ZipEntry(
    val name: String,
    val data: ByteArray,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other == null || other !is ZipEntry) return false
        return name == other.name && data.contentEquals(other.data)
    }

    override fun hashCode(): Int {
        var result = name.hashCode()
        result = 31 * result + data.contentHashCode()
        return result
    }
}

/**
 * Platform-specific ZIP bundling and extraction.
 *
 * Android uses java.util.zip; iOS uses a pure-Kotlin minimal ZIP implementation
 * to avoid Foundation's NSData memory pressure in share extensions.
 */
expect object ZipBundle {
    /**
     * Bundle multiple files into a ZIP archive.
     *
     * @param entries list of files to include
     * @return ZIP archive bytes
     */
    fun create(entries: List<ZipEntry>): ByteArray

    /**
     * Extract files from a ZIP archive.
     *
     * @param zipData raw ZIP bytes
     * @return list of extracted file entries
     */
    fun extract(zipData: ByteArray): List<ZipEntry>
}

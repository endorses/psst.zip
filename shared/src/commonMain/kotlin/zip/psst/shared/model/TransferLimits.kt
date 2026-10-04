package zip.psst.shared.model

/** Protocol selection/manifest bounds. File encryption streams authenticated 4 MiB frames. */
object TransferLimits {
    const val MAX_FILES: Int = 100
    // Legacy whole-buffer download helpers only; chunked streaming uses ChunkedFileCrypto limits.
    const val MAX_FILE_BYTES: Int = 25 * 1024 * 1024
    const val MAX_MANIFEST_BYTES: Int = 1024 * 1024
}

package zip.psst.shared.model

/** Whole-file encryption currently needs bounded in-memory buffers. */
object TransferLimits {
    const val MAX_FILE_BYTES: Int = 25 * 1024 * 1024
    const val MAX_MANIFEST_BYTES: Int = 1024 * 1024
}

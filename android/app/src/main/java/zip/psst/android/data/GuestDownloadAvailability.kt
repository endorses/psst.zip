package zip.psst.android.data

/** Local copies remain usable regardless of server limits. Only absent outputs need a GET. */
internal data class GuestDownloadAvailability(
    val missing: Int,
    val exhausted: Int,
    val unknown: Int,
) {
    val allMissingExhausted: Boolean
        get() = missing > 0 && exhausted == missing

    val partiallyExhausted: Boolean
        get() = exhausted > 0 && exhausted < missing
}

internal fun guestDownloadAvailability(
    record: GuestDownload,
    attempts: Map<String, Long?>,
    exists: (SavedGuestFile) -> Boolean,
): GuestDownloadAvailability {
    val local = record.saved.filter(exists).map { it.blobId.lowercase() }.toSet()
    val missing = record.files.filter { it.blobId.lowercase() !in local }
    return GuestDownloadAvailability(
        missing.size,
        missing.count { attempts[it.blobId.lowercase()] == 0L },
        missing.count {
            attempts.containsKey(it.blobId.lowercase()) && attempts[it.blobId.lowercase()] == null
        },
    )
}

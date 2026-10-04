package zip.psst.android.data

/** Keep discovery bounded even when most directory entries are unrelated or already indexed. */
internal fun <T> Iterator<T>.visitGuestImportBatch(visit: (T) -> Unit): Boolean {
    repeat(64) {
        if (!hasNext()) return false
        visit(next())
    }
    return hasNext()
}

internal fun guestLegacyEntry(rawName: String): Pair<String, String>? {
    val name = rawName.removeSuffix(".bak")
    val kind = name.substringAfterLast('.', "")
    val identity = name.substringBeforeLast('.', "")
    return if (kind in setOf("json", "receipt", "upload") && validIdentity(identity))
        kind to identity
    else null
}

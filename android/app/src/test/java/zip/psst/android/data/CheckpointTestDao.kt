package zip.psst.android.data

/** In-memory exact checkpoint operations shared by DAO contract tests. */
abstract class CheckpointTestDao : TransferHistoryDao {
    override suspend fun writeStatus(id: String, status: String) =
        error("Test overrides updateStatus")

    override suspend fun writeTitleIfEmpty(id: String, title: String) =
        error("Test overrides setTitleIfEmpty")

    override suspend fun writeRename(
        id: String,
        serverUrl: String,
        accountId: String,
        type: String,
        title: String?,
    ) = error("Test overrides rename")

    private val children = mutableMapOf<Triple<String, String, String>, InboxChildCheckpoint>()
    private val files = mutableMapOf<List<String>, InboxFileCheckpoint>()

    override suspend fun updateMetadata(entity: TransferHistoryMetadata) =
        error("Tests override update directly")

    override suspend fun checkpointChild(scope: String, slotId: String, childId: String) =
        children[Triple(scope, slotId, childId)]

    override suspend fun putCheckpointChild(row: InboxChildCheckpoint) {
        children[Triple(row.scope, row.slotId, row.childId)] = row
    }

    override suspend fun insertCheckpointFile(row: InboxFileCheckpoint): Long {
        val key = listOf(row.scope, row.slotId, row.childId, row.blobId)
        if (key in files) return -1L
        files[key] = row
        return files.size.toLong()
    }

    override suspend fun checkpointFile(
        scope: String,
        slotId: String,
        childId: String,
        blobId: String,
    ) = files[listOf(scope, slotId, childId, blobId)]

    override suspend fun checkpointSavedChildren(scope: String, slotId: String, ids: List<String>) =
        children.values
            .filter { it.scope == scope && it.slotId == slotId && it.childId in ids && it.saved }
            .map { it.childId }
            .take(100)

    override suspend fun checkpointSavedFiles(scope: String, slotId: String, ids: List<String>) =
        files.values
            .filter { it.scope == scope && it.slotId == slotId && it.childId in ids }
            .map { "${it.childId}/${it.blobId}" }
            .take(10000)

    val archives = mutableMapOf<Pair<String, String>, TransferHistoryEntity>()

    override suspend fun archiveCheckpointSource(
        id: String,
        scope: String,
        accountId: String?,
        originScope: String,
    ) {
        val row = requireNotNull(getById(id))
        if (row.accountId == accountId && row.originScope == originScope)
            archives.putIfAbsent(scope to id, row)
    }

    override suspend fun clearArchivedCheckpointSource(id: String, scope: String) {
        if (scope to id in archives)
            update(
                requireNotNull(getById(id))
                    .copy(
                        receivedTransfersJson = "{}",
                        savedFileIdsJson = "[]",
                        savedTransferIdsJson = "[]",
                    )
            )
    }

    private fun legacy(id: String, scope: String, source: Int): ByteArray {
        val row = requireNotNull(archives[scope to id])
        return when (source) {
            0 -> row.receivedTransfersJson
            1 -> row.savedFileIdsJson
            else -> row.savedTransferIdsJson
        }.toByteArray()
    }

    override suspend fun checkpointLegacyChunk(
        id: String,
        scope: String,
        source: Int,
        offset: Long,
        maximum: Int,
    ): ByteArray {
        val sourceBytes = legacy(id, scope, source)
        return sourceBytes.copyOfRange(
            offset.toInt().coerceAtMost(sourceBytes.size),
            (offset + maximum).toInt().coerceAtMost(sourceBytes.size),
        )
    }
}

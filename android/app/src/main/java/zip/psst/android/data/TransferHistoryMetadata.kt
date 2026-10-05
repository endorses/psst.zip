package zip.psst.android.data

/** Partial Room updates deliberately leave all original legacy JSON columns untouched. */
data class TransferHistoryMetadata(
    val id: String,
    val type: String,
    val fileCount: Int,
    val totalSize: Long,
    val serverUrl: String,
    val encryptionKey: String,
    val status: String,
    val createdAt: Long,
    val expiresAt: Long?,
    val deletionToken: String?,
    val accountId: String?,
    val title: String?,
    val automaticTitle: String?,
    val sharedTitle: String?,
    val originScope: String,
    val summaryUpdating: Boolean,
    val checkpointState: String,
    val checkpointColumn: Int,
    val checkpointOffset: Long,
    val checkpointKnownFiles: Long,
    val checkpointKnownBytes: Long,
    val checkpointSavedFiles: Long,
    val maxDownloads: Int?,
    val maxFiles: Int?,
    val reservedFiles: Long?,
)

internal fun TransferHistoryEntity.metadata() =
    TransferHistoryMetadata(
        id,
        type,
        fileCount,
        totalSize,
        serverUrl,
        encryptionKey,
        status,
        createdAt,
        expiresAt,
        deletionToken,
        accountId,
        title,
        automaticTitle,
        sharedTitle,
        originScope,
        summaryUpdating,
        checkpointState,
        checkpointColumn,
        checkpointOffset,
        checkpointKnownFiles,
        checkpointKnownBytes,
        checkpointSavedFiles,
        maxDownloads,
        maxFiles,
        reservedFiles,
    )

internal const val HISTORY_METADATA_PROJECTION =
    "id, type, fileCount, totalSize, serverUrl, encryptionKey, status, createdAt, expiresAt, deletionToken, accountId, title, automaticTitle, sharedTitle, originScope, summaryUpdating, checkpointState, checkpointColumn, checkpointOffset, checkpointKnownFiles, checkpointKnownBytes, checkpointSavedFiles, maxDownloads, maxFiles, reservedFiles, '[]' AS savedFileIdsJson, '{}' AS receivedTransfersJson, '[]' AS savedTransferIdsJson"

internal val INBOX_CHECKPOINT_SCHEMA =
    listOf(
        "CREATE TABLE inbox_legacy_checkpoint (scope TEXT NOT NULL,slotId TEXT NOT NULL,receivedTransfersJson TEXT NOT NULL,savedFileIdsJson TEXT NOT NULL,savedTransferIdsJson TEXT NOT NULL,PRIMARY KEY(scope,slotId),FOREIGN KEY(slotId) REFERENCES transfer_history(id) ON UPDATE NO ACTION ON DELETE CASCADE)",
        "CREATE INDEX index_inbox_legacy_checkpoint_slotId ON inbox_legacy_checkpoint(slotId)",
        "ALTER TABLE transfer_history ADD COLUMN checkpointState TEXT NOT NULL DEFAULT 'pending'",
        "ALTER TABLE transfer_history ADD COLUMN checkpointColumn INTEGER NOT NULL DEFAULT 0",
        "ALTER TABLE transfer_history ADD COLUMN checkpointOffset INTEGER NOT NULL DEFAULT 0",
        "ALTER TABLE transfer_history ADD COLUMN checkpointKnownFiles INTEGER NOT NULL DEFAULT 0",
        "ALTER TABLE transfer_history ADD COLUMN checkpointKnownBytes INTEGER NOT NULL DEFAULT 0",
        "ALTER TABLE transfer_history ADD COLUMN checkpointSavedFiles INTEGER NOT NULL DEFAULT 0",
        "CREATE TABLE inbox_child_checkpoint (scope TEXT NOT NULL,slotId TEXT NOT NULL,childId TEXT NOT NULL,fileCount INTEGER NOT NULL,plaintextSize INTEGER,saved INTEGER NOT NULL,savedFiles INTEGER NOT NULL,PRIMARY KEY(scope,slotId,childId),FOREIGN KEY(slotId) REFERENCES transfer_history(id) ON UPDATE NO ACTION ON DELETE CASCADE)",
        "CREATE INDEX index_inbox_child_checkpoint_slotId ON inbox_child_checkpoint(slotId)",
        "CREATE TABLE inbox_file_checkpoint (scope TEXT NOT NULL,slotId TEXT NOT NULL,childId TEXT NOT NULL,blobId TEXT NOT NULL,uri TEXT,PRIMARY KEY(scope,slotId,childId,blobId),FOREIGN KEY(slotId) REFERENCES transfer_history(id) ON UPDATE NO ACTION ON DELETE CASCADE)",
        "CREATE INDEX index_inbox_file_checkpoint_slotId ON inbox_file_checkpoint(slotId)",
    )

internal val HISTORY_LINK_POLICY_SCHEMA =
    listOf(
        "ALTER TABLE transfer_history ADD COLUMN maxDownloads INTEGER",
        "ALTER TABLE transfer_history ADD COLUMN maxFiles INTEGER",
        "ALTER TABLE transfer_history ADD COLUMN reservedFiles INTEGER",
    )

internal val HISTORY_SHARED_TITLE_SCHEMA =
    listOf("ALTER TABLE transfer_history ADD COLUMN sharedTitle TEXT", ACCOUNT_FILTERED_INDEX_SQL)

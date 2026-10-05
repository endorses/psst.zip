package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.LinkDeletionException
import zip.psst.shared.model.ServerConfig

/** Local history is removed only after the original server confirms revocation. */
suspend fun revokeHistoryEntry(
    dao: TransferHistoryDao,
    id: String,
    currentAccess: () -> HistoryAccess,
    metadata: TransferHistoryEntity? = null,
    clientFactory: (ServerConfig) -> ApiClient = { ApiClient(it) },
) {
    val access = currentAccess()
    val stored = dao.getById(id)
    val row =
        if (metadata != null && stored?.let(access::permits) != true) metadata else stored ?: return
    require(row.id == id)
    uiRequire(access.permits(row)) {
        message(R.string.l_this_history_entry_belongs_to_another_account_or_server_132262)
    }
    val client = clientFactory(ServerConfig(row.serverUrl))
    try {
        uiRequire(currentAccess() == access) {
            message(R.string.l_your_account_changed_open_history_again_a10d12)
        }
        when (row.type) {
            "received",
            "receive" -> client.slots.delete(row.id, row.deletionToken)
            "sent",
            "send" -> client.transfers.delete(row.id, row.deletionToken)
            else -> error(message(R.string.l_unknown_history_entry_type_d90e5c))
        }
        uiRequire(currentAccess() == access) {
            message(R.string.l_your_account_changed_open_history_again_a10d12)
        }
        if (stored == row) dao.delete(id)
    } catch (error: LinkDeletionException) {
        if (
            row.accountId == null &&
                row.deletionToken == null &&
                error.statusCode in listOf(401, 403)
        ) {
            throw LinkDeletionException(
                "This older transfer has no deletion token. Ask the server administrator to remove it or enable legacy deletion, then retry. The history entry has been kept.",
                error.statusCode,
            )
        }
        throw error
    } finally {
        client.close()
    }
}

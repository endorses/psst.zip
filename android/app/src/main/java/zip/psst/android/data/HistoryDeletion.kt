package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.LinkDeletionException
import zip.psst.shared.model.ServerConfig

/** Local history is removed only after the original server confirms revocation. */
suspend fun revokeHistoryEntry(
    dao: TransferHistoryDao,
    id: String,
    currentAccess: () -> HistoryAccess,
    clientFactory: (ServerConfig) -> ApiClient = { ApiClient(it) },
) {
    val row = dao.getById(id) ?: return
    val access = currentAccess()
    require(access.permits(row)) { "This history entry belongs to another account or server" }
    val client = clientFactory(ServerConfig(row.serverUrl))
    try {
        require(currentAccess() == access) { "Your account changed. Open History again." }
        when (row.type) {
            "received",
            "receive" -> client.slots.delete(row.id, row.deletionToken)
            "sent",
            "send" -> client.transfers.delete(row.id, row.deletionToken)
            else -> error("Unknown history entry type")
        }
        require(currentAccess() == access) { "Your account changed. Open History again." }
        dao.delete(id)
    } catch (error: LinkDeletionException) {
        if (row.deletionToken == null && error.statusCode in listOf(401, 403)) {
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

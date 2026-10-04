import Foundation
import Shared

/// Indexed receipt retries; owner-scoped work never borrows another account's session.
@MainActor
final class DownloadAcknowledgements {
    static let shared = DownloadAcknowledgements()
    private struct Pending: Codable {
        let serverURL: String
        let transferID: String
        var ownerID: String?
    }
    private let storage = DeviceRetryStorage(kind: "download-receipts")
    private var isFlushing = false
    private init() {}

    func enqueue(serverURL: String, transferID: String, ownerID: String? = nil) throws {
        do {
            try storage.queue().enqueue(.init(origin: serverURL, transferID: transferID, ownerID: ownerID))
            DeviceRetryStatus.shared.receiptError = nil
        } catch {
            DeviceRetryStatus.shared.receiptError = Self.storageMessage
            throw error
        }
    }
    private static let storageMessage =
        "Delivery confirmations could not be saved or read. Saved files and existing confirmation records are preserved. Restore storage access and retry."
    func flush() async {
        guard !isFlushing else { return }
        isFlushing = true
        defer {
            isFlushing = false
            DeviceRetryStatus.shared.importingReceipts = false
        }
        do {
            DeviceRetryStatus.shared.importingReceipts = true
            while true {
                try Task.checkCancellation()
                let complete = try storage.migrate(
                    read: {
                        guard let value = AppConstants.sharedDefaults.object(forKey: "pendingDownloadAcknowledgements") else { return nil }
                        guard let data = value as? Data else { throw AccountError.storage }
                        return data
                    },
                    decode: { data in
                        let old = try JSONDecoder().decode(Pending.self, from: data)
                        return .init(origin: old.serverURL, transferID: old.transferID, ownerID: old.ownerID)
                    })
                if complete { break }
                await Task.yield()
            }
            DeviceRetryStatus.shared.importingReceipts = false
            let queue = try storage.queue()
            var scopes = ["anonymous"]
            if let session = SecretStore.session, session.canTransfer {
                scopes.append(DeviceRetryQueue.identity([session.serverURL, session.userID]))
            }
            let jobs = try queue.batch(scopes: scopes)
            var unavailable = false
            for receipt in jobs.jobs {
                try Task.checkCancellation()
                var token: String?
                if let ownerID = receipt.ownerID {
                    guard let session = SecretStore.session, session.canTransfer,
                        session.userID == ownerID, session.serverURL == receipt.origin
                    else { continue }
                    token = session.token
                }
                let client = ApiClient(config: ServerConfig(baseUrl: receipt.origin), httpClient: HttpClientFactoryKt.createPlatformHttpClient(), sessionToken: token)
                defer { client.close() }
                do {
                    try await client.transfers.acknowledgeDownload(transferId: receipt.transferID)
                    try queue.remove(receipt)
                } catch {
                    try Task.checkCancellation()
                    try queue.rotate(receipt)
                    unavailable = true
                }
            }
            DeviceRetryStatus.shared.receiptError =
                jobs.hadInvalidJobs
                ? "Some saved confirmation records could not be read. They are preserved; other confirmations will continue. Restore local storage or retry."
                : unavailable ? "Some delivery confirmations are waiting for a connection. Saved files remain available. Retry when connected." : nil
        } catch is CancellationError {
            // Committed import progress and queued jobs remain intact.
        } catch { DeviceRetryStatus.shared.receiptError = Self.storageMessage }
    }
}

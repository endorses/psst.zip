import Foundation
import Shared

/// Retry delivery receipts without downloading already decrypted files again.
@MainActor
final class DownloadAcknowledgements {
    static let shared = DownloadAcknowledgements()

    private struct Pending: Codable, Equatable {
        let serverURL: String
        let transferID: String
    }

    private let storageKey = "pendingDownloadAcknowledgements"
    private var isFlushing = false
    private var pending: [Pending] = []

    private init() {
        let data = AppConstants.sharedDefaults.data(forKey: storageKey) ?? Data()
        pending = (try? JSONDecoder().decode([Pending].self, from: data)) ?? []
    }

    func enqueue(serverURL: String, transferID: String) {
        let receipt = Pending(serverURL: serverURL, transferID: transferID)
        guard !pending.contains(receipt) else { return }
        pending.append(receipt)
        persist()
    }

    func flush() async {
        guard !isFlushing else { return }
        isFlushing = true
        defer { isFlushing = false }
        // Each request has a five-second timeout in the shared API.
        // Rotate unsuccessful entries so an offline host cannot starve other hosts.
        var attempted: [Pending] = []
        for _ in 0 ..< 4 {
            guard !Task.isCancelled,
                  let receipt = pending.first(where: { !attempted.contains($0) }) else { return }
            attempted.append(receipt)
            let client = ApiClient(
                config: ServerConfig(baseUrl: receipt.serverURL),
                httpClient: HttpClientFactoryKt.createPlatformHttpClient()
            )
            do {
                try await client.transfers.acknowledgeDownload(transferId: receipt.transferID)
                pending.removeAll { $0 == receipt }
            } catch {
                if Task.isCancelled {
                    client.close()
                    return
                }
                pending.removeAll { $0 == receipt }
                pending.append(receipt)
            }
            client.close()
            persist()
        }
    }

    private func persist() {
        if let data = try? JSONEncoder().encode(pending) {
            AppConstants.sharedDefaults.set(data, forKey: storageKey)
        }
    }
}

import Foundation
import Shared
import UniformTypeIdentifiers

/// States for the share extension upload flow.
enum ShareExtensionState: Equatable {
    case idle
    case encrypting
    case uploading(progress: Double)
    case complete(shareURL: String)
    case failed(String)
}

/// Manages the encrypt-and-upload flow within the share extension's memory constraints.
@Observable
final class ShareExtensionViewModel {
    var state: ShareExtensionState = .idle
    var fileCount: Int = 0

    /// The share URL to copy or hand off to the main app.
    var shareURL: String? {
        if case let .complete(url) = state {
            return url
        }
        return nil
    }

    @MainActor
    func uploadFiles(fileURLs: [URL]) async {
        fileCount = fileURLs.count

        let configStore = AppConstants.sharedDefaults
        guard let serverURL = configStore.string(forKey: AppConstants.serverURLKey),
              !serverURL.isEmpty
        else {
            state = .failed("Server not configured. Open the Psst app to set up your server.")
            return
        }

        let serverConfig = ServerConfig(baseUrl: serverURL)
        let client = ApiClient(
            config: serverConfig,
            httpClient: HttpClientFactoryKt.createPlatformHttpClient()
        )
        defer { client.close() }

        state = .encrypting

        do {
            // Generate encryption key
            let key = try CryptoProvider.shared.generateKey()

            let transfer = try await client.transfers.create()
            let transferId = transfer.id
            let fileMetadatas = try await BufferedUpload.send(
                fileURLs: fileURLs, client: client, transferId: transferId, key: key,
                limit: 10 * 1024 * 1024
            ) { progress in
                self.state = .uploading(progress: progress)
            }
            let totalBytes = fileMetadatas.reduce(Int64(0)) { $0 + $1.size }

            // Create and encrypt manifest
            let manifest = Manifest(files: fileMetadatas)
            let manifestJSON = try ManifestSerializer.encode(manifest: manifest)
            let manifestBytes = manifestJSON.data(using: .utf8) ?? Data()
            let manifestNonce = try CryptoProvider.shared.generateNonce()
            let encryptedManifest = try CryptoProvider.shared.encrypt(
                key: key,
                nonce: manifestNonce,
                plaintext: manifestBytes.toKotlinByteArray()
            )
            let manifestBlob = manifestNonce.toData() + encryptedManifest.toData()

            try await client.transfers.uploadManifest(
                transferId: transferId,
                manifestBytes: manifestBlob.toKotlinByteArray()
            )

            // Complete transfer
            try await client.transfers.complete(transferId: transferId)

            // Build share URL
            let url = UrlHelper.shared.buildDownloadUrl(
                baseUrl: serverURL,
                transferId: transferId,
                key: key
            )

            // Also save to transfer history via App Group
            saveToHistory(
                transferId: transferId,
                fileCount: fileURLs.count,
                totalSize: totalBytes,
                shareURL: url,
                expiresAt: transfer.expiresAt
            )

            state = .complete(shareURL: url)

        } catch {
            state = .failed(error.localizedDescription)
        }
    }

    private func saveToHistory(
        transferId: String,
        fileCount: Int,
        totalSize: Int64,
        shareURL: String,
        expiresAt: String?
    ) {
        let record = TransferRecord(
            id: transferId,
            direction: .sent,
            state: .complete,
            createdAt: Date(),
            expiresAt: expiresAt.flatMap { ISO8601DateFormatter().date(from: $0) },
            fileCount: fileCount,
            totalSize: totalSize,
            shareURL: shareURL
        )

        // Load existing history, append, and save back.
        let defaults = AppConstants.sharedDefaults
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601

        var records: [TransferRecord] = []
        if let data = defaults.data(forKey: AppConstants.transferHistoryKey),
           let existing = try? decoder.decode([TransferRecord].self, from: data)
        {
            records = existing
        }
        records.insert(record, at: 0)
        if let data = try? encoder.encode(records) {
            defaults.set(data, forKey: AppConstants.transferHistoryKey)
        }
    }
}

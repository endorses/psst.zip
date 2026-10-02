import Foundation
import Shared
import UniformTypeIdentifiers

/// States for the send (upload) flow.
enum SendState: Equatable {
    case idle
    case encrypting
    case uploading(progress: Double)
    case complete
    case failed(String)
}

/// Manages the send flow: pick files -> encrypt -> upload -> show QR.
@Observable
final class SendViewModel {
    let fileURLs: [URL]
    private(set) var state: SendState = .idle
    private(set) var shareURL: String?
    private(set) var expiresAt: Date?
    private(set) var fileNames: [String] = []

    private let serverConfig: ServerConfigManager
    private let historyStore: TransferHistoryStore

    init(
        fileURLs: [URL],
        serverConfig: ServerConfigManager,
        historyStore: TransferHistoryStore
    ) {
        self.fileURLs = fileURLs
        self.serverConfig = serverConfig
        self.historyStore = historyStore
        fileNames = fileURLs.map(\.lastPathComponent)
    }

    /// Execute the full send flow: encrypt files, create transfer, upload, display link.
    @MainActor
    func startUpload() async {
        guard serverConfig.isConfigured else {
            state = .failed("Server not configured")
            return
        }

        let client = serverConfig.makeApiClient()
        defer { client.close() }

        state = .encrypting

        do {
            // Generate encryption key
            let key = try CryptoProvider.shared.generateKey()

            let transfer = try await client.transfers.create()
            let transferId = transfer.id
            let fileMetadatas = try await BufferedUpload.send(
                fileURLs: fileURLs, client: client, transferId: transferId, key: key,
                limit: BufferedUpload.maxFileBytes
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

            // Upload manifest
            try await client.transfers.uploadManifest(
                transferId: transferId,
                manifestBytes: manifestBlob.toKotlinByteArray()
            )

            // Complete transfer
            try await client.transfers.complete(transferId: transferId)

            // Build share URL
            let url = UrlHelper.shared.buildDownloadUrl(
                baseUrl: serverConfig.serverURL,
                transferId: transferId,
                key: key
            )
            shareURL = url

            // Parse expiry
            if let expiresAtString = transfer.expiresAt {
                expiresAt = ISO8601DateFormatter().date(from: expiresAtString)
            }

            // Save to history
            historyStore.add(TransferRecord(
                id: transferId,
                direction: .sent,
                state: .complete,
                createdAt: Date(),
                expiresAt: expiresAt,
                fileCount: fileURLs.count,
                totalSize: Int64(totalBytes),
                shareURL: url
            ))

            state = .complete

        } catch {
            state = .failed(error.localizedDescription)
        }
    }

    private func mimeType(for filename: String) -> String {
        let pathExtension = (filename as NSString).pathExtension
        if let utType = UTType(filenameExtension: pathExtension) {
            return utType.preferredMIMEType ?? "application/octet-stream"
        }
        return "application/octet-stream"
    }
}

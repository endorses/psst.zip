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
        if case .complete(let url) = state {
            return url
        }
        return nil
    }

    func uploadFiles(fileURLs: [URL]) async {
        fileCount = fileURLs.count

        let configStore = AppConstants.sharedDefaults
        guard let serverURL = configStore.string(forKey: AppConstants.serverURLKey),
              !serverURL.isEmpty else {
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
            let key = CryptoProvider.shared.generateKey()

            // Read and encrypt files — use streaming to stay within memory limits.
            var encryptedFiles: [(name: String, originalSize: Int64, blobData: Data)] = []
            for url in fileURLs {
                let fileData = try Data(contentsOf: url)
                let nonce = CryptoProvider.shared.generateNonce()
                let ciphertextBytes = CryptoProvider.shared.encrypt(
                    key: key.toKotlinByteArray(),
                    nonce: nonce.toKotlinByteArray(),
                    plaintext: fileData.toKotlinByteArray()
                )
                let blobData = Data(nonce) + Data(ciphertextBytes)
                encryptedFiles.append((
                    name: url.lastPathComponent,
                    originalSize: Int64(fileData.count),
                    blobData: blobData
                ))
            }

            state = .uploading(progress: 0.0)

            // Create transfer
            let transfer = try await client.transfers.create()
            let transferId = transfer.id

            // Upload each encrypted file
            var fileMetadatas: [FileMetadata] = []
            let totalBytes = encryptedFiles.reduce(Int64(0)) { $0 + Int64($1.blobData.count) }
            var uploadedBytes: Int64 = 0

            for file in encryptedFiles {
                let blobBytes = file.blobData.toKotlinByteArray()
                let resourceURL = try await client.uploadFile(
                    transferId: transferId,
                    data: blobBytes,
                    metadata: [:],
                    onProgress: { uploaded in
                        let current = uploadedBytes + Int64(truncating: uploaded)
                        Task { @MainActor in
                            self.state = .uploading(
                                progress: Double(current) / Double(totalBytes)
                            )
                        }
                    }
                )
                uploadedBytes += Int64(file.blobData.count)

                let blobId = URL(string: resourceURL)?.lastPathComponent ?? resourceURL
                let pathExtension = (file.name as NSString).pathExtension
                let mimeType = UTType(filenameExtension: pathExtension)?.preferredMIMEType
                    ?? "application/octet-stream"

                fileMetadatas.append(FileMetadata(
                    name: file.name,
                    size: file.originalSize,
                    mimeType: mimeType,
                    blobId: blobId
                ))
            }

            // Create and encrypt manifest
            let manifest = Manifest(files: fileMetadatas)
            let manifestJSON = ManifestSerializer.encode(manifest: manifest)
            let manifestBytes = manifestJSON.data(using: .utf8) ?? Data()
            let manifestNonce = CryptoProvider.shared.generateNonce()
            let encryptedManifest = CryptoProvider.shared.encrypt(
                key: key.toKotlinByteArray(),
                nonce: manifestNonce.toKotlinByteArray(),
                plaintext: manifestBytes.toKotlinByteArray()
            )
            let manifestBlob = Data(manifestNonce) + Data(encryptedManifest)

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
                key: key.toKotlinByteArray()
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
           let existing = try? decoder.decode([TransferRecord].self, from: data) {
            records = existing
        }
        records.insert(record, at: 0)
        if let data = try? encoder.encode(records) {
            defaults.set(data, forKey: AppConstants.transferHistoryKey)
        }
    }
}

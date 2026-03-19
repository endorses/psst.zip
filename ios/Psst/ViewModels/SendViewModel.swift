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
        self.fileNames = fileURLs.map { $0.lastPathComponent }
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
            let key = CryptoProvider.shared.generateKey()

            // Read and encrypt each file
            var encryptedFiles: [(name: String, data: Data, blobData: Data)] = []
            for url in fileURLs {
                let accessing = url.startAccessingSecurityScopedResource()
                defer {
                    if accessing { url.stopAccessingSecurityScopedResource() }
                }
                let fileData = try Data(contentsOf: url)
                let nonce = CryptoProvider.shared.generateNonce()
                let ciphertextBytes = CryptoProvider.shared.encrypt(
                    key: key.toKotlinByteArray(),
                    nonce: nonce.toKotlinByteArray(),
                    plaintext: fileData.toKotlinByteArray()
                )
                // Prepend nonce to ciphertext for the blob
                let blobData = Data(nonce) + Data(ciphertextBytes)
                encryptedFiles.append((
                    name: url.lastPathComponent,
                    data: fileData,
                    blobData: blobData
                ))
            }

            state = .uploading(progress: 0.0)

            // Create transfer on server
            let transfer = try await client.transfers.create()
            let transferId = transfer.id

            // Upload encrypted files via tus
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

                // Extract blob ID from resource URL (last path component)
                let blobId = URL(string: resourceURL)?.lastPathComponent ?? resourceURL

                fileMetadatas.append(FileMetadata(
                    name: file.name,
                    size: Int64(file.data.count),
                    mimeType: mimeType(for: file.name),
                    blobId: blobId
                ))
            }

            // Create and encrypt manifest
            let manifest = Manifest(files: fileMetadatas)
            let manifestJSON = ManifestSerializer.shared.encode(manifest: manifest)
            let manifestBytes = manifestJSON.data(using: .utf8) ?? Data()
            let manifestNonce = CryptoProvider.shared.generateNonce()
            let encryptedManifest = CryptoProvider.shared.encrypt(
                key: key.toKotlinByteArray(),
                nonce: manifestNonce.toKotlinByteArray(),
                plaintext: manifestBytes.toKotlinByteArray()
            )
            let manifestBlob = Data(manifestNonce) + Data(encryptedManifest)

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
                key: key.toKotlinByteArray()
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

// MARK: - Data / KotlinByteArray bridging

extension Data {
    func toKotlinByteArray() -> KotlinByteArray {
        let bytes = KotlinByteArray(size: Int32(count))
        withUnsafeBytes { rawBuffer in
            guard let baseAddress = rawBuffer.baseAddress else { return }
            let pointer = baseAddress.assumingMemoryBound(to: Int8.self)
            for i in 0 ..< count {
                bytes.set(index: Int32(i), value: pointer[i])
            }
        }
        return bytes
    }
}

extension KotlinByteArray {
    func toData() -> Data {
        var bytes = [UInt8](repeating: 0, count: Int(size))
        for i in 0 ..< Int(size) {
            bytes[i] = UInt8(bitPattern: get(index: Int32(i)))
        }
        return Data(bytes)
    }
}

// MARK: - Placeholder for SKIE-generated manifest serializer

/// This wraps kotlinx.serialization for the Manifest type.
/// SKIE exposes this as a regular Swift class.
enum ManifestSerializer {
    static let shared = ManifestSerializer.self

    static func encode(manifest: Manifest) -> String {
        // SKIE bridges kotlinx.serialization; the actual call would be:
        // Manifest.Companion.serializer() with Json.encodeToString
        // For now, manual JSON construction as a bridge layer.
        var filesJSON: [String] = []
        for file in manifest.files {
            let entry = """
            {"name":"\(file.name)","size":\(file.size),"mime_type":"\(file.mimeType)","blob_id":"\(file.blobId)"}
            """
            filesJSON.append(entry)
        }
        return """
        {"files":[\(filesJSON.joined(separator: ","))]}
        """
    }

    static func decode(json: String) -> Manifest? {
        guard let data = json.data(using: .utf8) else { return nil }
        struct ManifestJSON: Codable {
            struct FileJSON: Codable {
                let name: String
                let size: Int64
                let mime_type: String
                let blob_id: String
            }
            let files: [FileJSON]
        }
        guard let decoded = try? JSONDecoder().decode(ManifestJSON.self, from: data) else {
            return nil
        }
        let files = decoded.files.map { f in
            FileMetadata(name: f.name, size: f.size, mimeType: f.mime_type, blobId: f.blob_id)
        }
        return Manifest(files: files)
    }
}

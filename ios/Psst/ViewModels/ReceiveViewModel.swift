import Foundation
import Shared

/// States for the receive (drop slot) flow.
enum ReceiveState: Equatable {
    case idle
    case creating
    case waiting
    case downloading(progress: Double)
    case decrypting
    case complete
    case failed(String)
}

/// Manages the receive flow: create drop slot -> display QR -> listen for uploads -> download & decrypt.
@Observable
final class ReceiveViewModel {
    private(set) var state: ReceiveState = .idle
    private(set) var uploadURL: String?
    private(set) var expiresAt: Date?
    private(set) var receivedFileURLs: [URL] = []

    private var slotId: String?
    private var encryptionKey: KotlinByteArray?
    private let serverConfig: ServerConfigManager
    private let historyStore: TransferHistoryStore
    private var sseTask: Task<Void, Never>?

    init(
        serverConfig: ServerConfigManager,
        historyStore: TransferHistoryStore
    ) {
        self.serverConfig = serverConfig
        self.historyStore = historyStore
    }

    deinit {
        sseTask?.cancel()
    }

    /// Create a drop slot on the server, generate a key, and start listening for uploads.
    @MainActor
    func createDropSlot() async {
        guard serverConfig.isConfigured else {
            state = .failed("Server not configured")
            return
        }

        state = .creating

        let client = serverConfig.makeApiClient()

        do {
            // Generate encryption key
            let key = CryptoProvider.shared.generateKey()
            self.encryptionKey = key.toKotlinByteArray()

            // Create slot
            let slot = try await client.slots.create()
            self.slotId = slot.id

            // Build upload URL
            let url = UrlHelper.shared.buildUploadUrl(
                baseUrl: serverConfig.serverURL,
                slotId: slot.id,
                key: key.toKotlinByteArray()
            )
            self.uploadURL = url

            // Parse expiry
            if let expiresAtString = slot.expiresAt {
                expiresAt = ISO8601DateFormatter().date(from: expiresAtString)
            }

            state = .waiting

            // Start SSE listener for upload notifications
            startListening(client: client, slotId: slot.id)

        } catch {
            client.close()
            state = .failed(error.localizedDescription)
        }
    }

    private func startListening(client: ApiClient, slotId: String) {
        sseTask = Task { [weak self] in
            do {
                for try await event in client.slots.events(slotId: slotId).asAsyncSequence() {
                    guard !Task.isCancelled else { break }
                    if event.event == "upload_complete" || event.event == "file_uploaded" {
                        await self?.handleUploadNotification(client: client, slotId: slotId)
                    }
                }
            } catch {
                // SSE stream ended or errored. Fall back to polling.
                await self?.pollForUploads(client: client, slotId: slotId)
            }
        }
    }

    @MainActor
    private func handleUploadNotification(client: ApiClient, slotId: String) async {
        guard let encryptionKey else { return }

        state = .downloading(progress: 0.0)

        do {
            // Get slot status to find uploaded files
            let slot = try await client.slots.get(slotId: slotId)

            guard slot.fileCount > 0 else { return }

            // Download the manifest
            // For drop slots, the manifest endpoint is at the slot level.
            // The uploader uses the same transfer structure scoped to the slot.
            let manifestBytes = try await client.transfers.downloadManifest(transferId: slotId)
            let manifestData = Data(manifestBytes)

            state = .decrypting

            // Parse nonce + ciphertext from manifest
            let nonce = manifestData.prefix(12)
            let ciphertext = manifestData.dropFirst(12)

            let decryptedManifestBytes = CryptoProvider.shared.decrypt(
                key: encryptionKey,
                nonce: Data(nonce).toKotlinByteArray(),
                ciphertext: Data(ciphertext).toKotlinByteArray()
            )
            let manifestJSON = String(data: Data(decryptedManifestBytes), encoding: .utf8) ?? ""
            guard let manifest = ManifestSerializer.decode(json: manifestJSON) else {
                state = .failed("Failed to parse file manifest")
                return
            }

            // Download and decrypt each file
            var savedURLs: [URL] = []
            let tempDir = FileManager.default.temporaryDirectory
                .appendingPathComponent("psst-received-\(slotId)")
            try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)

            for (index, file) in manifest.files.enumerated() {
                let progress = Double(index) / Double(manifest.files.count)
                state = .downloading(progress: progress)

                let encryptedData = try await client.transfers.downloadFile(
                    transferId: slotId,
                    fileId: file.blobId
                )
                let blob = Data(encryptedData)
                let fileNonce = blob.prefix(12)
                let fileCiphertext = blob.dropFirst(12)

                let decrypted = CryptoProvider.shared.decrypt(
                    key: encryptionKey,
                    nonce: Data(fileNonce).toKotlinByteArray(),
                    ciphertext: Data(fileCiphertext).toKotlinByteArray()
                )

                let fileURL = tempDir.appendingPathComponent(file.name)
                try Data(decrypted).write(to: fileURL)
                savedURLs.append(fileURL)
            }

            receivedFileURLs = savedURLs

            // Save to history
            historyStore.add(TransferRecord(
                id: slotId,
                direction: .received,
                state: .complete,
                createdAt: Date(),
                expiresAt: expiresAt,
                fileCount: manifest.files.count,
                totalSize: manifest.files.reduce(0) { $0 + $1.size },
                shareURL: nil
            ))

            state = .complete
            client.close()

        } catch {
            state = .failed(error.localizedDescription)
        }
    }

    private func pollForUploads(client: ApiClient, slotId: String) async {
        while !Task.isCancelled {
            do {
                let slot = try await client.slots.get(slotId: slotId)
                if slot.status == .hasUploads && slot.fileCount > 0 {
                    await handleUploadNotification(client: client, slotId: slotId)
                    return
                }
                try await Task.sleep(for: .seconds(3))
            } catch {
                break
            }
        }
    }
}

// MARK: - SKIE Flow bridge

/// Extension to bridge Kotlin Flow<SlotEvent> to Swift AsyncSequence via SKIE.
/// SKIE automatically provides this, but we declare the usage pattern here.
extension Shared.SlotApi {
    // SKIE generates: func events(slotId:) -> some AsyncSequence<SlotEvent>
    // The actual bridge is handled by the SKIE Gradle plugin at compile time.
}

import Foundation
import Shared

enum ReceiveState: Equatable {
    case idle
    case creating
    case waiting
    case downloading(progress: Double)
    case decrypting
    case complete
    case failed(String)
}

/// Poll completed child transfers; a slot ID is never a transfer ID.
@Observable
@MainActor
final class ReceiveViewModel {
    private(set) var state: ReceiveState = .idle
    private(set) var uploadURL: String?
    private(set) var expiresAt: Date?
    private(set) var receivedFileURLs: [URL] = []

    private let serverConfig: ServerConfigManager
    private let historyStore: TransferHistoryStore
    private var receiveTask: Task<Void, Never>?

    init(serverConfig: ServerConfigManager, historyStore: TransferHistoryStore) {
        self.serverConfig = serverConfig
        self.historyStore = historyStore
    }

    deinit { receiveTask?.cancel() }

    func createDropSlot() async {
        guard serverConfig.isConfigured else {
            state = .failed("Server not configured")
            return
        }
        receiveTask?.cancel()
        receivedFileURLs = []
        state = .creating
        let client = serverConfig.makeApiClient()
        do {
            let key = try CryptoProvider.shared.generateKey()
            let slot = try await client.slots.create()
            uploadURL = UrlHelper.shared.buildUploadUrl(
                baseUrl: serverConfig.serverURL, slotId: slot.id, key: key
            )
            expiresAt = slot.expiresAt.flatMap { ISO8601DateFormatter().date(from: $0) }
            state = .waiting
            receiveTask = Task { [weak self] in
                defer { client.close() }
                do {
                    while !Task.isCancelled {
                        let status = try await client.slots.get(slotId: slot.id)
                        if !status.completedTransfers.isEmpty {
                            try await self?.download(client: client, transfers: status.completedTransfers, key: key)
                            return
                        }
                        try await Task.sleep(for: .seconds(3))
                    }
                } catch is CancellationError {
                    // A new slot or dismissal cancelled this receive operation.
                } catch {
                    self?.state = .failed(error.localizedDescription)
                }
            }
        } catch {
            client.close()
            state = .failed(error.localizedDescription)
        }
    }

    private func decrypt(_ bytes: KotlinByteArray, key: KotlinByteArray) throws -> Data {
        let blob = bytes.toData()
        guard blob.count >= 28 else {
            throw NSError(domain: "Psst", code: 2, userInfo: [NSLocalizedDescriptionKey: "Invalid encrypted file"])
        }
        return try CryptoProvider.shared.decrypt(
            key: key,
            nonce: Data(blob.prefix(12)).toKotlinByteArray(),
            ciphertext: Data(blob.dropFirst(12)).toKotlinByteArray()
        ).toData()
    }

    private func download(client: ApiClient, transfers: [SlotTransfer], key: KotlinByteArray) async throws {
        var savedURLs: [URL] = []
        for transfer in transfers {
            state = .decrypting
            let bytes = try await client.transfers.downloadManifest(transferId: transfer.transferId)
            let json = try String(decoding: decrypt(bytes, key: key), as: UTF8.self)
            guard let manifest = ManifestSerializer.decode(json: json) else {
                throw NSError(domain: "Psst", code: 3, userInfo: [NSLocalizedDescriptionKey: "Invalid file manifest"])
            }
            guard !manifest.files.isEmpty, manifest.files.count == Int(transfer.fileCount) else {
                throw NSError(domain: "Psst", code: 3, userInfo: [NSLocalizedDescriptionKey: "Manifest file count mismatch"])
            }
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent("psst-received-\(UUID().uuidString)")
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            for (index, file) in manifest.files.enumerated() {
                try Task.checkCancellation()
                guard file.size <= Int64(BufferedUpload.maxFileBytes) else {
                    throw NSError(domain: "Psst", code: 4, userInfo: [NSLocalizedDescriptionKey: "Files must be no larger than 25 MiB."])
                }
                state = .downloading(progress: Double(index) / Double(max(1, manifest.files.count)))
                let encrypted = try await client.transfers.downloadFile(transferId: transfer.transferId, fileId: file.blobId)
                let plaintext = try decrypt(encrypted, key: key)
                guard plaintext.count == file.size else {
                    throw NSError(domain: "Psst", code: 5, userInfo: [NSLocalizedDescriptionKey: "File size does not match manifest"])
                }
                // Prefix each basename so duplicates cannot overwrite files or escape the directory.
                let basename = (file.name.replacingOccurrences(of: "\\", with: "/") as NSString).lastPathComponent
                let destination = directory.appendingPathComponent("\(index + 1)-\(basename)")
                try plaintext.write(to: destination, options: .atomic)
                savedURLs.append(destination)
            }
            historyStore.add(TransferRecord(
                id: transfer.transferId, direction: .received, state: .complete,
                createdAt: Date(), expiresAt: expiresAt, fileCount: manifest.files.count,
                totalSize: manifest.files.reduce(0) { $0 + $1.size }, shareURL: nil
            ))
            DownloadAcknowledgements.shared.enqueue(
                serverURL: client.config.baseUrl, transferID: transfer.transferId
            )
            Task { await DownloadAcknowledgements.shared.flush() }
        }
        receivedFileURLs = savedURLs
        state = .complete
    }
}

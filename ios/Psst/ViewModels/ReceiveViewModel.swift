import Foundation
import Shared

enum ReceiveState: Equatable { case idle, creating, waiting, downloading(progress: Double), decrypting, complete, failed(String) }

@Observable
@MainActor
final class ReceiveViewModel {
    private(set) var state: ReceiveState = .idle
    private(set) var record: TransferRecord?
    private(set) var arrivals: [SlotTransfer] = []
    private(set) var connectionError: String?
    private(set) var savingError: String?
    private(set) var lastUpdated: Date?
    private(set) var receivedFileURLs: [URL] = []
    var uploadURL: String? {
        record?.fullLink
    }

    var expiresAt: Date? {
        record?.expiresAt
    }

    var canSave: Bool {
        guard let record, !record.isExpired, record.state != .revoked else { return false }
        return arrivals.contains { !(record.savedTransfers ?? []).contains($0.transferId) }
    }

    var isSaving: Bool {
        if case .downloading = state {
            return true
        }
        if case .decrypting = state {
            return true
        }
        return false
    }

    private let serverConfig: ServerConfigManager
    private let historyStore: TransferHistoryStore
    private var refreshing = false
    private var saveClient: ApiClient?
    private var saveTask: Task<Void, Never>?
    init(serverConfig: ServerConfigManager, historyStore: TransferHistoryStore, record: TransferRecord? = nil) {
        self.serverConfig = serverConfig
        self.historyStore = historyStore
        self.record = record
        if let record {
            state = .waiting
            receivedFileURLs = (record.savedFiles ?? [:]).values.compactMap { savedURL($0) }
        }
    }

    private func savedURL(_ name: String) -> URL? {
        let url = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent(name)
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    func createDropSlot() async {
        guard record == nil else { return }
        state = .creating
        do {
            let session = try serverConfig.requireSession()
            let client = serverConfig.makeApiClient(session: session)
            defer { client.close() }
            let key = try CryptoProvider.shared.generateKey()
            let slot = try await client.slots.create()
            guard UUID(uuidString: slot.id) != nil else { throw AccountError.request }
            let link = UrlHelper.shared.buildUploadUrl(baseUrl: session.serverURL, slotId: slot.id, key: key)
            let entry = TransferRecord(id: slot.id, direction: .received, state: .inProgress, createdAt: Date(),
                                       expiresAt: ServerTimestamp.parse(slot.expiresAt), fileCount: 0,
                                       totalSize: 0, shareURL: nil, serverURL: session.serverURL, ownerID: session.userID, isSlot: true)
            try entry.saveSecrets(link: link, deletionToken: slot.deleteToken)
            try historyStore.add(entry)
            try serverConfig.check(session)
            record = entry
            state = .waiting
        } catch { state = .failed(String(localized: "Could not create a receive link. Sign in or reconnect, then retry creating it.")) }
    }

    func refresh() async -> Bool {
        guard let record, !isSaving, !refreshing else { return true }
        refreshing = true
        defer { refreshing = false }
        do {
            let session = try serverConfig.requireSession()
            guard record.belongs(to: session) else { throw AccountError.changed }
            let client = serverConfig.makeApiClient(session: session)
            defer { client.close() }
            _ = try await AccountHTTP.request(server: session.serverURL, path: "slots/" + record.id)
            let status = try await client.slots.get(slotId: record.id)
            try serverConfig.check(session)
            arrivals = status.completedTransfers
            var updated = record
            updated.fileCount = Int(status.fileCount)
            updated.state = updated.fileCount == 0 ? .inProgress : (canSave ? .complete : .saved)
            try historyStore.update(updated)
            self.record = updated
            lastUpdated = Date()
            connectionError = nil
            return true
        } catch AccountError.unavailable {
            var updated = record
            updated.state = updated.isExpired ? .expired : .revoked
            try? historyStore.update(updated)
            self.record = updated
            connectionError = String(localized: "This link expired or was revoked.")
            return false
        } catch {
            if !Task.isCancelled {
                connectionError = String(localized: "Offline — reconnect to update arrivals.")
            }
            return false
        }
    }

    /// Awaited by SwiftUI .task: canceled when the view becomes inactive; failures back off to 30 s.
    func monitor() async {
        var delay: UInt64 = 3
        while !Task.isCancelled {
            let ok = await refresh()
            delay = ok ? 3 : min(30, delay * 2)
            do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
        }
    }

    func save() {
        guard !isSaving, canSave else { return }
        state = .decrypting
        saveTask = Task { await saveFiles() }
    }

    func cancelSaving() {
        saveTask?.cancel()
        saveClient?.close()
    }

    func saveFiles() async {
        guard var entry = record else { return }
        var checkpoint = ReceiveCheckpoint(record: entry, fileExists: { self.savedURL($0) != nil })
        savingError = nil
        do {
            let session = try serverConfig.requireSession()
            guard entry.belongs(to: session), let fragment = URLComponents(string: entry.fullLink ?? "")?.fragment,
                  let data = Self.decodeKey(fragment) else { throw AccountError.changed }
            let key = data.toKotlinByteArray()
            let client = serverConfig.makeApiClient(session: session)
            saveClient = client
            defer { client.close()
                saveClient = nil
            }
            let status = try await client.slots.get(slotId: entry.id)
            try serverConfig.check(session)
            let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            let relativeDirectory = "Received/" + session.userID + "/" + entry.id
            let directory = documents.appendingPathComponent(relativeDirectory)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            for transfer in status.completedTransfers where !(entry.savedTransfers ?? []).contains(transfer.transferId) {
                state = .decrypting
                let bytes = try await client.transfers.downloadManifest(transferId: transfer.transferId)
                try serverConfig.check(session)
                guard let manifest = try ManifestSerializer.decode(json: String(decoding: decrypt(bytes, key: key), as: UTF8.self)),
                      !manifest.files.isEmpty, manifest.files.count == Int(transfer.fileCount) else { throw AccountError.request }
                for (index, file) in manifest.files.enumerated() {
                    try serverConfig.check(session)
                    if !checkpoint.needsFile(transferID: transfer.transferId, blobID: file.blobId) {
                        continue
                    }
                    guard file.size <= BufferedUpload.maxFileBytes else { throw AccountError.request }
                    state = .downloading(progress: Double(index) / Double(manifest.files.count))
                    let bytes = try await client.transfers.downloadFile(transferId: transfer.transferId, fileId: file.blobId)
                    try serverConfig.check(session)
                    let plain = try decrypt(bytes, key: key)
                    guard plain.count == file.size else { throw AccountError.request }
                    let basename = (file.name.replacingOccurrences(of: "\\", with: "/") as NSString).lastPathComponent
                    let relative = relativeDirectory + "/" + file.blobId + "-" + basename
                    let destination = documents.appendingPathComponent(relative)
                    try plain.write(to: destination, options: [.atomic, .completeFileProtection])
                    checkpoint.saved(transferID: transfer.transferId, blobID: file.blobId, path: relative, size: file.size, title: file.name)
                    entry = checkpoint.record
                    try historyStore.update(entry)
                    record = entry
                    receivedFileURLs = (entry.savedFiles ?? [:]).values.compactMap { savedURL($0) }
                }
                guard checkpoint.completed(transferID: transfer.transferId, blobIDs: manifest.files.map(\.blobId)) else { throw AccountError.request }
                entry = checkpoint.record
                try historyStore.update(entry)
                record = entry
                DownloadAcknowledgements.shared.enqueue(serverURL: session.serverURL, transferID: transfer.transferId)
                await DownloadAcknowledgements.shared.flush()
            }
            entry.state = .saved
            try historyStore.update(entry)
            record = entry
            state = .complete
        } catch {
            state = .waiting
            savingError = String(localized: "Saving stopped. Already saved files are safe. Retry saving to continue this receive link.")
        }
    }

    nonisolated static func decodeKey(_ value: String) -> Data? {
        let raw = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        guard let key = Data(base64Encoded: raw + String(repeating: "=", count: (4 - raw.count % 4) % 4)), key.count == 32 else { return nil }
        return key
    }

    private func decrypt(_ bytes: KotlinByteArray, key: KotlinByteArray) throws -> Data {
        let blob = bytes.toData()
        guard blob.count >= 28 else { throw AccountError.request }
        return try CryptoProvider.shared.decrypt(key: key, nonce: Data(blob.prefix(12)).toKotlinByteArray(), ciphertext: Data(blob.dropFirst(12)).toKotlinByteArray()).toData()
    }
}

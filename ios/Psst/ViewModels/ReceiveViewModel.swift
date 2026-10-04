import CryptoKit
import Foundation
import Shared

enum ReceiveState: Equatable { case idle, creating, waiting, downloading(progress: Double), decrypting, complete, failed(String) }

struct InboxSaveConsent {
    let total: Int64
    let fileCount: Int
    let fingerprints: [String: Data]
}

@Observable
@MainActor
final class ReceiveViewModel {
    private(set) var state: ReceiveState = .idle
    private(set) var record: TransferRecord?
    private(set) var arrivals: [SlotTransfer] = []
    private(set) var connectionError: String?
    private(set) var savingError: String?
    private(set) var pendingConsent: InboxSaveConsent?
    private(set) var lastUpdated: Date?
    private(set) var receivedFileURLs: [URL] = []
    var uploadURL: String? {
        record?.receiveProtocol == 2 ? record?.fullLink : nil
    }

    var expiresAt: Date? {
        record?.expiresAt
    }

    var canSave: Bool {
        guard let record, !record.isExpired, record.state != .revoked, record.canDecryptInbox else { return false }
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
    private let localName: String?
    private let maxFiles: Int32
    private var refreshing = false
    private var saveClient: ApiClient?
    private var saveTask: Task<Void, Never>?
    init(serverConfig: ServerConfigManager, historyStore: TransferHistoryStore, record: TransferRecord? = nil, localName: String? = nil, maxFiles: Int32 = 0) {
        self.serverConfig = serverConfig
        self.historyStore = historyStore
        self.record = record
        self.localName = localName?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.maxFiles = maxFiles
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
        guard record == nil, state != .creating else { return }
        state = .creating
        var allocation: (server: String, id: String, token: String, vaultID: String)?
        do {
            let session = try serverConfig.requireSession()
            let client = serverConfig.makeApiClient(session: session)
            defer { client.close() }
            let keys = ReceiveCrypto.generateKeyPair()
            let publicKey = keys.publicKey.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
            let slot = try await client.slots.create(recipientPublicKey: publicKey, maxFiles: maxFiles)
            guard UUID(uuidString: slot.id) != nil else { throw AccountError.request }
            let link = UrlHelper.shared.buildReceiveUrl(baseUrl: session.serverURL, slotId: slot.id, publicKey: keys.publicKey.toKotlinByteArray())
            var entry = TransferRecord(id: slot.id, direction: .received, state: .inProgress, createdAt: Date(),
                                       expiresAt: ServerTimestamp.parse(slot.expiresAt), fileCount: 0,
                                       totalSize: 0, shareURL: nil, serverURL: session.serverURL, ownerID: session.userID, customTitle: localName?.isEmpty == false ? String(localName!.prefix(200)) : nil, isSlot: true, receiveProtocol: 0, maxFiles: Int(maxFiles), reservedFiles: 0)
            allocation = (session.serverURL, slot.id, slot.deleteToken ?? session.token, entry.vaultID)
            record = entry
            try entry.saveSecrets(link: link, deletionToken: slot.deleteToken, receivePrivateKey: keys.privateKey)
            try historyStore.add(entry)
            record = entry
            let confirmed = try await client.slots.get(slotId: slot.id)
            guard confirmed.receiveProtocol == 2, confirmed.recipientPublicKey == publicKey, confirmed.maxFiles == maxFiles else {
                try await historyStore.revoke(entry, session: session)
                record = nil
                throw LinkLimitError.unsupportedServer
            }
            try serverConfig.check(session)
            entry.receiveProtocol = 2
            try historyStore.update(entry)
            record = entry
            state = .waiting
            allocation = nil
        } catch {
            if let allocation {
                let removed = await Task.detached {
                    do {
                        _ = try await AccountHTTP.request(server: allocation.server, path: "slots/" + allocation.id, method: "DELETE", token: allocation.token)
                        return true
                    } catch { return false }
                }.value
                if removed {
                    if let record {
                        try? historyStore.remove(record)
                    }
                    SecretStore.remove(allocation.vaultID)
                    record = nil
                } else if var entry = record {
                    entry.state = .failed
                    try? historyStore.update(entry)
                    record = entry
                }
            }
            await serverConfig.refreshAccount()
            state = .failed(TransferIncident.from(error)?.localizedDescription ?? (error as? LinkLimitError)?.localizedDescription ?? serverConfig.accountMessage ?? (record == nil ? String(localized: "Could not create a receive link. Sign in or reconnect, then retry creating it.") : String(localized: "Receive link creation stopped. Its record remains in History so you can revoke it.")))
        }
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
            let status = try await client.slots.get(slotId: record.id)
            try serverConfig.check(session)
            arrivals = status.completedTransfers
            var updated = record
            updated.fileCount = Int(status.fileCount)
            updated.receiveProtocol = Int(status.receiveProtocol)
            updated.maxFiles = Int(status.maxFiles)
            updated.reservedFiles = status.reservedFiles
            updated.state = updated.fileCount == 0 ? .inProgress : (canSave ? .complete : .saved)
            try historyStore.update(updated)
            self.record = updated
            lastUpdated = Date()
            connectionError = nil
            return true
        } catch AccountError.unavailable, TransferIncident.revoked {
            var updated = record
            updated.state = updated.isExpired ? .expired : .revoked
            try? historyStore.update(updated)
            self.record = updated
            connectionError = String(localized: "This link expired or was revoked.")
            return false
        } catch {
            if let incident = TransferIncident.from(error) {
                if incident == .revoked {
                    var updated = record
                    updated.state = .revoked
                    try? historyStore.update(updated)
                    self.record = updated
                }
                connectionError = incident.localizedDescription
                return false
            }
            // Ktor errors cross the Swift bridge without AccountError status mapping.
            // Confirm only failed reads through the native HTTP status mapper, using
            // the current matching account; ordinary polling remains one request.
            if !Task.isCancelled, let session = serverConfig.session, record.belongs(to: session) {
                do {
                    _ = try await AccountHTTP.request(server: session.serverURL, path: "slots/" + record.id, token: session.token)
                } catch AccountError.unavailable, TransferIncident.revoked {
                    var updated = record
                    updated.state = updated.isExpired ? .expired : .revoked
                    try? historyStore.update(updated)
                    self.record = updated
                    connectionError = String(localized: "This link expired or was revoked.")
                    return false
                } catch { /* Preserve the last known record on authentication/network failure. */ }
            }
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
            guard let session = serverConfig.session, record?.belongs(to: session) == true,
                  !serverConfig.needsSignIn else { return }
            if record?.state == .revoked || record?.isExpired == true {
                return
            }
            delay = ok ? 3 : min(30, delay * 2)
            do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
        }
    }

    func save() {
        guard !isSaving, canSave else { return }
        state = .decrypting
        saveTask = Task { await saveFiles() }
    }

    func confirmSaving() {
        guard let consent = pendingConsent, !isSaving else { return }
        pendingConsent = nil
        state = .decrypting
        saveTask = Task { await saveFiles(approved: consent.fingerprints) }
    }

    func cancelSaving() {
        pendingConsent = nil
        saveTask?.cancel()
        saveClient?.close()
    }

    func saveFiles(approved: [String: Data]? = nil) async {
        guard var entry = record else { return }
        var checkpoint = ReceiveCheckpoint(record: entry, fileExists: { self.savedURL($0) != nil })
        savingError = nil
        pendingConsent = nil
        do {
            let session = try serverConfig.requireSession()
            guard entry.belongs(to: session), entry.canDecryptInbox,
                  let fragment = URLComponents(string: entry.fullLink ?? "")?.fragment else { throw AccountError.changed }
            let modern = fragment.hasPrefix("v2.")
            guard let linkKey = Self.decodeKey(modern ? String(fragment.dropFirst(3)) : fragment) else { throw AccountError.request }
            let privateKey = entry.capabilities?.receivePrivateKey
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
            var prepared: [(id: String, key: KotlinByteArray, manifest: Manifest)] = []
            var fingerprints: [String: Data] = [:]
            var remainingSizes: [Int64] = []
            for transfer in status.completedTransfers where !(entry.savedTransfers ?? []).contains(transfer.transferId) {
                state = .decrypting
                let bytes = try await client.transfers.downloadManifest(transferId: transfer.transferId)
                try serverConfig.check(session)
                let encryptedManifest: Data
                let key: KotlinByteArray
                if modern {
                    guard status.receiveProtocol == 2, Self.decodeKey(status.recipientPublicKey) == linkKey,
                          let privateKey else { throw ReceiveCryptoError.invalidKey }
                    let envelope = try ReceiveCrypto.decodeEnvelope(bytes.toData())
                    key = try ReceiveCrypto.openSubmissionKey(envelope.wrappedKey, privateKey: privateKey, publicKey: linkKey, slotID: entry.id, transferID: transfer.transferId).toKotlinByteArray()
                    encryptedManifest = envelope.encryptedManifest
                } else {
                    guard status.receiveProtocol == 1 else { throw ReceiveCryptoError.invalidEnvelope }
                    key = linkKey.toKotlinByteArray()
                    encryptedManifest = bytes.toData()
                }
                guard let manifest = try ManifestSerializer.decode(json: String(decoding: decrypt(encryptedManifest.toKotlinByteArray(), key: key), as: UTF8.self)),
                      !manifest.files.isEmpty, manifest.files.count == Int(transfer.fileCount) else { throw AccountError.request }
                let metadata = try await client.transfers.get(transferId: transfer.transferId)
                _ = try ManifestValidator.shared.validateForTransfer(manifest: manifest, transfer: metadata)
                fingerprints[transfer.transferId] = Data(SHA256.hash(data: bytes.toData()))
                remainingSizes.append(contentsOf: manifest.files.filter { checkpoint.needsFile(transferID: transfer.transferId, blobID: $0.blobId) }.map(\.size))
                prepared.append((transfer.transferId, key, manifest))
            }
            let remaining = try ReceiveSafety.total(remainingSizes)
            if remaining > ReceiveSafety.automaticBytes, approved != fingerprints {
                pendingConsent = InboxSaveConsent(total: remaining, fileCount: remainingSizes.count, fingerprints: fingerprints)
                state = .waiting
                return
            }
            try ReceiveSafety.checkSpace(at: directory, additional: remaining)
            for preparedTransfer in prepared {
                let transferID = preparedTransfer.id, key = preparedTransfer.key, manifest = preparedTransfer.manifest
                for (index, file) in manifest.files.enumerated() {
                    try serverConfig.check(session)
                    if !checkpoint.needsFile(transferID: transferID, blobID: file.blobId) {
                        continue
                    }
                    state = .downloading(progress: Double(index) / Double(manifest.files.count))
                    let temporary = try await StreamedFiles.receive(client: client, transferID: transferID, file: file, key: key)
                    defer { try? FileManager.default.removeItem(at: temporary) }
                    try serverConfig.check(session)
                    let basename = try ManifestValidator.shared.safeFilename(name: file.name)
                    let relative = relativeDirectory + "/" + file.blobId + "-" + basename
                    let destination = documents.appendingPathComponent(relative)
                    if FileManager.default.fileExists(atPath: destination.path) {
                        guard try StreamedFiles.digest(destination) == StreamedFiles.digest(temporary) else { throw AccountError.request }
                    } else {
                        try FileManager.default.moveItem(at: temporary, to: destination)
                    }
                    checkpoint.saved(transferID: transferID, blobID: file.blobId, path: relative, size: file.size, title: file.name)
                    entry = checkpoint.record
                    try historyStore.update(entry)
                    record = entry
                    receivedFileURLs = (entry.savedFiles ?? [:]).values.compactMap { savedURL($0) }
                }
                guard checkpoint.completed(transferID: transferID, blobIDs: manifest.files.map(\.blobId)) else { throw AccountError.request }
                entry = checkpoint.record
                try historyStore.update(entry)
                record = entry
                DownloadAcknowledgements.shared.enqueue(serverURL: session.serverURL, transferID: transferID, ownerID: session.userID)
                await DownloadAcknowledgements.shared.flush()
            }
            entry.state = .saved
            try historyStore.update(entry)
            record = entry
            state = .complete
        } catch {
            state = .waiting
            var incident = TransferIncident.from(error)
            if incident == nil, let session = serverConfig.session, entry.belongs(to: session) {
                incident = await TransferTrafficRecovery.inspect(error, server: session.serverURL,
                                                                 resource: .slot, id: entry.id,
                                                                 direction: .download, token: session.token)
            }
            savingError = incident?.localizedDescription ?? String(localized: "Saving stopped. Already saved files are safe. Retry saving to continue this receive link.")
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

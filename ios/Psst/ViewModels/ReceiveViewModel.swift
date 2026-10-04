import CryptoKit
import Foundation
import Shared

enum ReceiveState: Equatable {
    case idle, creating, waiting
    case downloading(progress: Double)
    case decrypting, complete
    case failed(String)
}

struct InboxSaveConsent {
    let total: Int64
    let fileCount: Int
    let fingerprints: [String: Data]
    let scope: InboxSaveScope
    let session: DeviceSession
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
    private(set) var pageWindow = InboxPageWindow()
    private(set) var totalReceivedFiles: Int64?
    private(set) var refreshing = false
    private var checkpoints: [String: ReceiveCheckpoint] = [:]
    private var pageID = UUID()
    private var loadedSession: DeviceSession?
    private var refreshID = UUID()
    var pageFileCount: Int { arrivals.reduce(0) { $0 + Int($1.fileCount) } }
    var canBrowse: Bool { !isSaving && !refreshing && pendingConsent == nil && loadedSession == serverConfig.session && !serverConfig.needsSignIn }
    var canGoNext: Bool { canBrowse && pageWindow.nextCursor != nil }
    var canGoPrevious: Bool { canBrowse && pageWindow.canGoBack }

    var uploadURL: String? {
        record?.receiveProtocol == 2 ? record?.fullLink : nil
    }

    var expiresAt: Date? {
        record?.expiresAt
    }

    var canSave: Bool {
        guard let record, let session = serverConfig.session, record.belongs(to: session),
            loadedSession == session, !serverConfig.needsSignIn, pageWindow.loaded,
            !record.isExpired, record.state != .revoked, record.canDecryptInbox
        else { return false }
        return arrivals.contains { !isSaved($0, in: record) }
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
        }
    }

    private func savedURL(_ name: String, expectedSize: Int64? = nil) -> URL? {
        guard ReceiveCheckpointStorage.validPath(name) else { return nil }
        let url = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent(name)
        guard FileManager.default.fileExists(atPath: url.path) else { return nil }
        if let expectedSize {
            guard let actual = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, Int64(actual) == expectedSize else { return nil }
        }
        return url
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
            let publicKey = keys.publicKey.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(
                of: "=", with: "")
            let slot = try await client.slots.create(recipientPublicKey: publicKey, maxFiles: maxFiles)
            guard UUID(uuidString: slot.id) != nil else { throw AccountError.request }
            let link = UrlHelper.shared.buildReceiveUrl(baseUrl: session.serverURL, slotId: slot.id, publicKey: keys.publicKey.toKotlinByteArray())
            var entry = TransferRecord(
                id: slot.id, direction: .received, state: .inProgress, createdAt: Date(),
                expiresAt: ServerTimestamp.parse(slot.expiresAt), fileCount: 0,
                totalSize: 0, shareURL: nil, serverURL: session.serverURL, ownerID: session.userID, customTitle: localName?.isEmpty == false ? String(localName!.prefix(200)) : nil,
                isSlot: true, receiveProtocol: 0, maxFiles: Int(maxFiles), reservedFiles: 0)
            allocation = (session.serverURL, slot.id, slot.deleteToken ?? session.token, entry.vaultID)
            record = entry
            try entry.saveSecrets(link: link, deletionToken: slot.deleteToken, receivePrivateKey: keys.privateKey)
            try historyStore.add(entry)
            record = entry
            let confirmed = try await client.slots.getPage(slotId: slot.id, after: nil, limit: 50)
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
            state = .failed(
                TransferIncident.from(error)?.localizedDescription ?? (error as? LinkLimitError)?.localizedDescription ?? serverConfig.accountMessage
                    ?? (record == nil
                        ? String(localized: "Could not create a receive link. Sign in or reconnect, then retry creating it.")
                        : String(localized: "Receive link creation stopped. Its record remains in History so you can revoke it.")))
        }
    }

    private func isSaved(_ transfer: SlotTransfer, in entry: TransferRecord) -> Bool {
        checkpoints[transfer.transferId]?.isSaved(fileCount: Int(transfer.fileCount)) == true
    }

    private func loadCheckpoints(_ entry: TransferRecord, transfers: [SlotTransfer]) throws {
        let loaded = try historyStore.receiveCheckpoints(parent: entry, transferIDs: transfers.map(\.transferId), fileExists: { self.savedURL($0, expectedSize: $1) != nil })
        checkpoints = loaded
        receivedFileURLs = loaded.values.flatMap(\.savedPaths).compactMap { savedURL($0) }
    }

    private func reloadCheckpoint(_ entry: TransferRecord, transferID: String) throws {
        let loaded = try historyStore.receiveCheckpoints(parent: entry, transferIDs: [transferID], fileExists: { self.savedURL($0, expectedSize: $1) != nil })
        checkpoints[transferID] = loaded[transferID]
        receivedFileURLs = checkpoints.values.flatMap(\.savedPaths).compactMap { savedURL($0) }
    }

    private func historyState(_ entry: TransferRecord, status: DropSlot, window: InboxPageWindow, savedCheckpoints: [String: ReceiveCheckpoint]? = nil) -> TransferState {
        let complete = status.completedTransfers.filter { $0.fileCount > 0 }
        let visible = Dictionary(uniqueKeysWithValues: complete.map { ($0.transferId, Int($0.fileCount)) })
        let evidence = savedCheckpoints ?? checkpoints
        let saved = Set(complete.filter { evidence[$0.transferId]?.isSaved(fileCount: Int($0.fileCount)) == true }.map(\.transferId))
        let total = status.summary?.ready == true ? status.summary?.completedFiles?.int64Value : nil
        if InboxSaveScope.coversInbox(cursor: window.cursor, next: status.nextCursor, total: total, visible: visible, saved: saved) { return .saved }
        return (total ?? Int64(entry.fileCount)) == 0 && complete.isEmpty ? .inProgress : .complete
    }

    func refresh() async -> Bool { await loadPage(pageWindow) }

    func nextPage() async {
        guard canGoNext, let next = try? pageWindow.forward() else { return }
        _ = await loadPage(next)
    }

    func previousPage() async {
        guard canGoPrevious, let previous = try? pageWindow.backward() else { return }
        _ = await loadPage(previous)
    }

    func firstPage() async {
        guard canBrowse else { return }
        _ = await loadPage(InboxPageWindow())
    }

    private func loadPage(_ target: InboxPageWindow) async -> Bool {
        guard let record, !isSaving, !refreshing, pendingConsent == nil else { return true }
        guard let session = try? serverConfig.requireSession(), record.belongs(to: session) else { return false }
        let requestID = UUID()
        refreshID = requestID
        refreshing = true
        defer { if refreshID == requestID { refreshing = false } }
        func current() -> Bool {
            refreshID == requestID && self.record?.localID == record.localID && JobIdentity(session: session).accepts(SecretStore.session, cancelled: Task.isCancelled)
                && serverConfig.session == session
        }
        do {
            let client = serverConfig.makeApiClient(session: session)
            defer { client.close() }
            let status = try await client.slots.getPage(slotId: record.id, after: target.cursor, limit: 50)
            try serverConfig.check(session)
            guard current() else { return false }
            var nextWindow = target
            try nextWindow.accept(next: status.nextCursor)
            var updated = record
            let total = status.summary?.ready == true ? status.summary?.completedFiles?.int64Value : nil
            if let total { updated.fileCount = Int(clamping: total) }
            updated.serverSummaryKnown = status.summary?.ready == true
            updated.receiveProtocol = Int(status.receiveProtocol)
            updated.maxFiles = Int(status.maxFiles)
            updated.reservedFiles = status.reservedFiles
            let nextTransfers = status.completedTransfers.filter { $0.fileCount > 0 }
            let nextCheckpoints = try historyStore.receiveCheckpoints(
                parent: updated, transferIDs: nextTransfers.map(\.transferId), fileExists: { self.savedURL($0, expectedSize: $1) != nil })
            updated.state = historyState(updated, status: status, window: nextWindow, savedCheckpoints: nextCheckpoints)
            try historyStore.update(updated)
            // Publish the entire page only after its durable metadata update.
            // A failed navigation keeps the previous rows and checkpoint evidence.
            checkpoints = nextCheckpoints
            receivedFileURLs = nextCheckpoints.values.flatMap(\.savedPaths).compactMap { savedURL($0) }
            self.record = updated
            if pageWindow.cursor != nextWindow.cursor { savingError = nil }
            pageWindow = nextWindow
            pageID = UUID()
            loadedSession = session
            totalReceivedFiles = total
            arrivals = nextTransfers
            if state == .complete { state = .waiting }
            lastUpdated = Date()
            connectionError = nil
            return true
        } catch {
            guard current() else { return false }
            var unavailable = false
            if let accountError = error as? AccountError {
                switch accountError {
                case .unavailable: unavailable = true
                default: break
                }
            }
            var incident = TransferIncident.from(error)
            // Use only a bounded first-page request to map bridged Ktor failures.
            // Do not reintroduce the legacy full-inbox lookup on error paths.
            if incident == nil, !unavailable {
                do {
                    _ = try await AccountHTTP.request(server: session.serverURL, path: "slots/" + record.id + "/inbox?limit=1", token: session.token)
                } catch AccountError.unavailable { unavailable = true } catch { incident = TransferIncident.from(error) }
            }
            guard current() else { return false }
            if unavailable || incident == .revoked {
                var updated = record
                updated.state = updated.isExpired ? .expired : .revoked
                try? historyStore.update(updated)
                self.record = updated
                connectionError = String(localized: "This link expired or was revoked.")
            } else {
                connectionError = incident?.localizedDescription ?? String(localized: "Could not update this page. Your last loaded page is still available. Reconnect to retry.")
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
                !serverConfig.needsSignIn
            else { return }
            if record?.state == .revoked || record?.isExpired == true {
                return
            }
            delay = ok ? 3 : min(30, delay * 2)
            do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
        }
    }

    func save() {
        guard !isSaving, !refreshing, pendingConsent == nil, canSave, let record, let session = loadedSession else { return }
        let scope = InboxSaveScope(
            pageID: pageID, cursor: pageWindow.cursor,
            transferIDs: Set(arrivals.filter { !isSaved($0, in: record) }.map(\.transferId)))
        state = .decrypting
        saveTask = Task { await saveFiles(scope: scope, session: session) }
    }

    func confirmSaving() {
        guard let consent = pendingConsent, !isSaving,
            consent.session == serverConfig.session,
            consent.scope.accepts(pageID: pageID, cursor: pageWindow.cursor, available: Set(arrivals.map(\.transferId)))
        else {
            pendingConsent = nil
            return
        }
        pendingConsent = nil
        state = .decrypting
        saveTask = Task { await saveFiles(scope: consent.scope, session: consent.session, approved: consent.fingerprints) }
    }

    func cancelSaving() {
        pendingConsent = nil
        saveTask?.cancel()
        saveClient?.close()
    }

    private func saveFiles(scope: InboxSaveScope, session: DeviceSession, approved: [String: Data]? = nil) async {
        guard var entry = record else { return }
        savingError = nil
        pendingConsent = nil
        do {
            try serverConfig.check(session)
            guard scope.accepts(pageID: pageID, cursor: pageWindow.cursor, available: Set(arrivals.map(\.transferId))) else { throw AccountError.changed }
            guard entry.belongs(to: session), entry.canDecryptInbox,
                let fragment = URLComponents(string: entry.fullLink ?? "")?.fragment
            else { throw AccountError.changed }
            let modern = fragment.hasPrefix("v2.")
            guard let linkKey = Self.decodeKey(modern ? String(fragment.dropFirst(3)) : fragment) else { throw AccountError.request }
            let privateKey = entry.capabilities?.receivePrivateKey
            let client = serverConfig.makeApiClient(session: session)
            saveClient = client
            defer {
                client.close()
                saveClient = nil
            }
            let status = try await client.slots.getPage(slotId: entry.id, after: scope.cursor, limit: 50)
            try serverConfig.check(session)
            guard scope.accepts(pageID: pageID, cursor: pageWindow.cursor, available: Set(status.completedTransfers.map(\.transferId))) else { throw AccountError.changed }
            try loadCheckpoints(entry, transfers: status.completedTransfers.filter { $0.fileCount > 0 })
            let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            let relativeDirectory = "Received/" + session.userID + "/" + entry.id
            let directory = documents.appendingPathComponent(relativeDirectory)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            var prepared: [(id: String, key: KotlinByteArray, manifest: Manifest)] = []
            var fingerprints: [String: Data] = [:]
            var remainingSizes: [Int64] = []
            for transfer in status.completedTransfers where scope.transferIDs.contains(transfer.transferId) && !isSaved(transfer, in: entry) {
                state = .decrypting
                let bytes = try await client.transfers.downloadManifest(transferId: transfer.transferId)
                try serverConfig.check(session)
                let encryptedManifest: Data
                let key: KotlinByteArray
                if modern {
                    guard status.receiveProtocol == 2, Self.decodeKey(status.recipientPublicKey) == linkKey,
                        let privateKey
                    else { throw ReceiveCryptoError.invalidKey }
                    let envelope = try ReceiveCrypto.decodeEnvelope(bytes.toData())
                    key = try ReceiveCrypto.openSubmissionKey(envelope.wrappedKey, privateKey: privateKey, publicKey: linkKey, slotID: entry.id, transferID: transfer.transferId)
                        .toKotlinByteArray()
                    encryptedManifest = envelope.encryptedManifest
                } else {
                    guard status.receiveProtocol == 1 else { throw ReceiveCryptoError.invalidEnvelope }
                    key = linkKey.toKotlinByteArray()
                    encryptedManifest = bytes.toData()
                }
                guard let manifest = try ManifestSerializer.decode(json: String(decoding: decrypt(encryptedManifest.toKotlinByteArray(), key: key), as: UTF8.self)),
                    !manifest.files.isEmpty, manifest.files.count == Int(transfer.fileCount)
                else { throw AccountError.request }
                let metadata = try await client.transfers.get(transferId: transfer.transferId)
                _ = try ManifestValidator.shared.validateForTransfer(manifest: manifest, transfer: metadata)
                fingerprints[transfer.transferId] = Data(SHA256.hash(data: bytes.toData()))
                guard let checkpoint = checkpoints[transfer.transferId] else { throw AccountError.storage }
                remainingSizes.append(contentsOf: manifest.files.filter { checkpoint.needsFile(blobID: $0.blobId) }.map(\.size))
                prepared.append((transfer.transferId, key, manifest))
            }
            try serverConfig.check(session)
            let remaining = try ReceiveSafety.total(remainingSizes)
            if remaining > ReceiveSafety.automaticBytes, approved != fingerprints {
                pendingConsent = InboxSaveConsent(total: remaining, fileCount: remainingSizes.count, fingerprints: fingerprints, scope: scope, session: session)
                state = .waiting
                return
            }
            try ReceiveSafety.checkSpace(at: directory, additional: remaining)
            for preparedTransfer in prepared {
                let transferID = preparedTransfer.id
                let key = preparedTransfer.key
                let manifest = preparedTransfer.manifest
                for (index, file) in manifest.files.enumerated() {
                    try serverConfig.check(session)
                    guard let checkpoint = checkpoints[transferID] else { throw AccountError.storage }
                    if !checkpoint.needsFile(blobID: file.blobId) {
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
                    entry = try historyStore.saveReceivedFile(parent: entry, transferID: transferID, blobID: file.blobId, path: relative, size: file.size, title: basename)
                    record = entry
                    try reloadCheckpoint(entry, transferID: transferID)
                }
                guard let checkpoint = checkpoints[transferID], checkpoint.readyToAcknowledge(blobIDs: manifest.files.map(\.blobId)) else { throw AccountError.request }
                // Persist the independent receipt first. A kill before the child
                // completion marker retries safely instead of losing the receipt.
                try DownloadAcknowledgements.shared.enqueue(serverURL: session.serverURL, transferID: transferID, ownerID: session.userID)
                try historyStore.completeReceivedTransfer(
                    parent: entry, transferID: transferID, blobIDs: manifest.files.map(\.blobId), fileExists: { self.savedURL($0, expectedSize: $1) != nil })
                try reloadCheckpoint(entry, transferID: transferID)
                await DownloadAcknowledgements.shared.flush()
            }
            try serverConfig.check(session)
            entry.serverSummaryKnown = status.summary?.ready == true
            entry.state = historyState(entry, status: status, window: pageWindow)
            try historyStore.update(entry)
            record = entry
            state = .complete
        } catch {
            state = .waiting
            guard JobIdentity(session: session).accepts(SecretStore.session, cancelled: Task.isCancelled) && serverConfig.session == session else { return }
            var incident = TransferIncident.from(error)
            if incident == nil, let session = serverConfig.session, entry.belongs(to: session) {
                incident = await TransferTrafficRecovery.inspect(
                    error, server: session.serverURL,
                    resource: .slot, id: entry.id,
                    direction: .download, token: session.token)
            }
            guard JobIdentity(session: session).accepts(SecretStore.session, cancelled: Task.isCancelled) && serverConfig.session == session else { return }
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

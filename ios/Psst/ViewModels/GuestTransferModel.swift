import Foundation
import Shared

/// Always creates a fresh isolated transport, even when the origin matches the signed-in account.
enum GuestNetwork {
    static func client(_ origin: String) throws -> ApiClient {
        try ApiClient.companion.anonymous(origin: origin)
    }
}

struct ReceiveConsent {
    let record: GuestDownload
    let manifest: [GuestFile]
    let total: Int64
    let allowRedownload: Bool
    var availableOnly = false
    var unavailableCount = 0
}

@Observable
@MainActor
final class GuestTransferModel {
    private(set) var active = false
    private(set) var stage = ""
    private(set) var bytes: Int64 = 0
    private(set) var total: Int64 = 0
    private(set) var fileNumber = 0
    private(set) var error: String?
    private(set) var currentID: String?
    private(set) var uploadComplete = false
    private(set) var cleanupPending = false
    private(set) var pendingConsent: ReceiveConsent?
    private var task: Task<Void, Never>?
    private var client: ApiClient?
    private var run = UUID()
    private let store: GuestDownloadStore
    init(store: GuestDownloadStore) {
        self.store = store
    }

    func cancel() {
        pendingConsent = nil
        guard active else { return }
        task?.cancel()
        client?.close()
    }

    func receive(_ link: ParsedUrl) {
        guard !active else { return }
        do {
            let record = try store.prepare(origin: link.origin, transferID: link.id, key: link.key.toData())
            resume(record)
        } catch { self.error = (error as? GuestError)?.localizedDescription ?? "Could not save the transfer securely. Check device storage and try again." }
    }

    func resetPresentation() {
        guard !active else { return }
        pendingConsent = nil
        currentID = nil
        uploadComplete = false
        cleanupPending = false
        error = nil
        fileNumber = 0
    }

    func show(_ record: GuestDownload) {
        guard !active else { return }
        currentID = record.id
        error = nil
    }

    func confirmReceive() {
        guard let consent = pendingConsent, !active else { return }
        pendingConsent = nil
        begin(consent.record, allowRedownload: consent.allowRedownload, approved: consent.manifest, availableOnly: consent.availableOnly)
    }

    func resume(_ requested: GuestDownload, allowRedownload: Bool = false) {
        begin(requested, allowRedownload: allowRedownload, approved: nil)
    }

    private func begin(_ requested: GuestDownload, allowRedownload: Bool, approved: [GuestFile]?, availableOnly: Bool = false) {
        guard !active else { return }
        let record: GuestDownload
        do { record = try store.find(requested.id) ?? requested } catch {
            self.error = "Local received history could not be read. Restore storage access and retry."
            return
        }
        currentID = record.id
        pendingConsent = nil
        error = nil
        uploadComplete = false
        guard allowRedownload || !store.requiresRedownloadConsent(record) else {
            error = GuestError.redownloadConsent.localizedDescription
            return
        }
        if record.complete, record.files.allSatisfy({ store.url($0) != nil }) {
            stage = "Saved"
            task = Task { await store.flushReceipts() }
            return
        }
        active = true
        let identifier = UUID()
        run = identifier
        task = Task { await download(record, identifier: identifier, allowRedownload: allowRedownload, approved: approved, availableOnly: availableOnly) }
    }

    private func download(_ original: GuestDownload, identifier: UUID, allowRedownload: Bool, approved: [GuestFile]?, availableOnly: Bool) async {
        var record = original
        defer {
            self.client?.close()
            self.client = nil
            active = false
        }
        do {
            let client = try GuestNetwork.client(record.origin)
            self.client = client
            try await store.reconcile(original.id)
            record = try store.find(original.id) ?? original
            guard let key = SecretStore.read(record.keyReference), key.count == 32 else { throw GuestError.missingKey }
            stage = "Inspecting transfer"
            let transfer = try await client.transfers.get(transferId: record.transferID)
            guard transfer.id.lowercased() == record.transferID.lowercased() else { throw GuestError.invalidManifest }
            record.sharedTitle = try SharedLinkTitle.normalize(transfer.title)
            record.exhausted = transfer.status == .exhausted
            try store.update(record)
            if record.exhausted == true { throw GuestError.downloadLimit }
            guard transfer.status == .complete else { throw GuestError.notReady }
            try Task.checkCancellation()
            let encrypted = try await client.transfers.downloadManifest(transferId: record.transferID)
            stage = "Decrypting file list"
            let plain = try GuestFiles.decrypt(encrypted.toData(), key: key)
            guard let manifest = ManifestSerializer.decode(json: String(decoding: plain, as: UTF8.self)) else { throw GuestError.invalidManifest }
            _ = try ManifestValidator.shared.validateForTransfer(manifest: manifest, transfer: transfer)
            let incoming = try manifest.files.map {
                try GuestFile(
                    id: $0.blobId, name: GuestFiles.filename($0.name), size: $0.size, mime: $0.mimeType, encoding: $0.encoding, chunkSize: $0.chunkSize,
                    encryptionID: $0.encryptionId)
            }
            let totalBytes = try ReceiveSafety.total(incoming.map(\.size))
            if record.files.isEmpty {
                record.files = incoming
                try store.update(record)
            } else {
                let expected = record.files.map { file in
                    GuestFile(id: file.id, name: file.name, size: file.size, mime: file.mime, encoding: file.encoding, chunkSize: file.chunkSize, encryptionID: file.encryptionID)
                }
                if incoming != expected {
                    guard !record.files.contains(where: { store.url($0) != nil }) else { throw ReceiveSafetyError.changed }
                    record.files = incoming
                    try store.update(record)
                }
            }
            record.remainingDownloads = Self.remainingAttempts(transfer)
            try store.update(record)
            let missing = record.files.filter { store.url($0) == nil }
            let unavailable = missing.filter { record.remainingDownloads?[$0.id] == 0 }
            let eligible = missing.filter { record.remainingDownloads?[$0.id] != 0 }
            guard !eligible.isEmpty || missing.isEmpty else { throw GuestError.downloadLimit }
            if (!unavailable.isEmpty && !availableOnly) || (totalBytes > ReceiveSafety.automaticBytes && approved != incoming) {
                pendingConsent = try ReceiveConsent(
                    record: record, manifest: incoming,
                    total: ReceiveSafety.total(eligible.map(\.size)), allowRedownload: allowRedownload,
                    availableOnly: !unavailable.isEmpty, unavailableCount: unavailable.count)
                stage = "Ready to receive"
                return
            }
            let remaining = try ReceiveSafety.total(eligible.map(\.size))
            try ReceiveSafety.checkSpace(at: store.documents, additional: remaining)
            for index in record.files.indices {
                try Task.checkCancellation()
                if store.url(record.files[index]) != nil {
                    continue
                }
                let file = record.files[index]
                if availableOnly && record.remainingDownloads?[file.id] == 0 {
                    continue
                }
                // A local copy can also disappear after the initial resume check.
                guard allowRedownload || !file.saved else { throw GuestError.redownloadConsent }
                fileNumber = index + 1
                bytes = 0
                total = file.size
                stage = "Downloading"
                let metadata = FileMetadata(
                    name: file.name, size: file.size, mimeType: file.mime, blobId: file.id,
                    encoding: file.encoding, chunkSize: file.chunkSize, encryptionId: file.encryptionID)
                let temporary = try await StreamedFiles.receive(client: client, transferID: record.transferID, file: metadata, key: key.toKotlinByteArray()) { received in
                    guard self.run == identifier, self.active, self.fileNumber == index + 1 else { return }
                    self.bytes = received
                }
                defer { try? FileManager.default.removeItem(at: temporary) }
                try Task.checkCancellation()
                stage = "Saving"
                try store.publish(temporary, index: index, record: &record)
            }
            try Task.checkCancellation()
            guard !record.files.isEmpty else { throw GuestError.invalidManifest }
            guard record.files.allSatisfy({ store.url($0) != nil }) else {
                stage = "Available files saved"
                error = GuestError.downloadLimit.localizedDescription
                await refreshAttempts(record)
                return
            }
            await refreshAttempts(record)
            record = try store.find(record.id) ?? record
            record.complete = true
            record.receiptPending = !record.receiptDelivered
            try store.update(record)
            stage = "Saved"
            await store.flushReceipts()
        } catch {
            let incident = await TransferTrafficRecovery.inspect(
                error, server: record.origin,
                resource: .transfer, id: record.transferID,
                direction: .download)
            if !Task.isCancelled {
                await refreshAttempts(record)
            }
            stage = Task.isCancelled ? "Stopped" : "Could not finish receiving"
            self.error =
                incident?.localizedDescription ?? (error as? ReceiveSafetyError)?.localizedDescription ?? (error as? GuestError)?.localizedDescription
                ?? "Already saved files are safe. Check your connection and free storage, then retry. The link may have expired, been revoked, or reached its download limit; retry cannot restore an unavailable file."
        }
    }

    private static func remainingAttempts(_ transfer: Transfer) -> [String: Int64] {
        var result: [String: Int64] = [:]
        for file in transfer.files {
            if let remaining = file.remainingDownloads, remaining.int64Value >= 0 {
                result[file.id] = remaining.int64Value
            }
        }
        return result
    }

    func refreshAttempts(_ requested: GuestDownload) async {
        do {
            let client = try GuestNetwork.client(requested.origin)
            defer { client.close() }
            let transfer = try await client.transfers.get(transferId: requested.transferID)
            guard transfer.id == requested.transferID,
                var record = try store.find(requested.id)
            else { return }
            record.remainingDownloads = Self.remainingAttempts(transfer)
            record.sharedTitle = try SharedLinkTitle.normalize(transfer.title)
            record.exhausted = transfer.status == .exhausted
            try store.update(record)
        } catch { /* Keep last known counters; a network failure is not proof of exhaustion. */  }
    }

    func send(_ urls: [URL], to link: ParsedUrl) {
        guard !active, !uploadComplete, !urls.isEmpty else { return }
        active = true
        error = nil
        currentID = nil
        uploadComplete = false
        cleanupPending = false
        fileNumber = 0
        let identifier = UUID()
        run = identifier
        task = Task { await upload(urls, to: link, identifier: identifier) }
    }

    private func upload(_ urls: [URL], to link: ParsedUrl, identifier: UUID) async {
        var allocation: (String, String)?
        var finished = false
        defer {
            client?.close()
            client = nil
            active = false
            GuestUploadCleanup.activeID = nil
        }
        do {
            guard link.receiveVersion == 2 else { throw ReceiveCryptoError.invalidEnvelope }
            let anonymous = try GuestNetwork.client(link.origin)
            defer { anonymous.close() }
            let limit = try await Int(anonymous.limits.get().maxFileSize)
            let sizes = try BufferedUpload.sizes(urls, limit: limit)
            stage = "Preparing upload"
            client = anonymous
            // Refresh after sizing the entire batch, immediately before server allocation.
            let availability = try await anonymous.slots.availability(slotId: link.id)
            try GuestUploadPreflight.validate(availability, link: link, urls: urls, sizes: sizes)
            try Task.checkCancellation()
            let transfer = try await anonymous.slots.createTransfer(slotId: link.id)
            guard UUID(uuidString: transfer.id) != nil, let capability = transfer.deleteToken, !capability.isEmpty else { throw AccountError.request }
            allocation = (transfer.id, capability)
            GuestUploadCleanup.activeID = link.origin + "|" + transfer.id
            // Persist a resource-scoped cleanup capability before cancellation can abandon the allocation.
            try GuestUploadCleanup.enqueue(origin: link.origin, transferID: transfer.id, capability: capability)
            try Task.checkCancellation()
            let scoped = try ApiClient.companion.slotUpload(origin: link.origin, capability: capability)
            client = scoped
            let key = try CryptoProvider.shared.generateKey()
            let wrappedKey = try ReceiveCrypto.sealSubmissionKey(key.toData(), publicKey: link.key.toData(), slotID: link.id, transferID: transfer.id)
            let metadata = try await BufferedUpload.send(
                fileURLs: urls, client: scoped, transferId: transfer.id, key: key,
                limit: limit, expectedSizes: sizes, check: { try Task.checkCancellation() }, preparing: { _ in self.stage = "Encrypting" },
                progress: { progress in
                    guard self.run == identifier, self.active, self.task?.isCancelled != true else { return }
                    self.stage = "Uploading"
                    self.bytes = progress.sent
                    self.total = progress.total
                })
            let manifest = try ManifestSerializer.encode(manifest: Manifest(files: metadata))
            let nonce = try CryptoProvider.shared.generateNonce()
            let encrypted = try CryptoProvider.shared.encrypt(key: key, nonce: nonce, plaintext: Data(manifest.utf8).toKotlinByteArray())
            try Task.checkCancellation()
            let envelope = try ReceiveCrypto.encodeEnvelope(wrappedKey: wrappedKey, encryptedManifest: nonce.toData() + encrypted.toData())
            try await scoped.transfers.uploadManifest(transferId: transfer.id, manifestBytes: envelope.toKotlinByteArray())
            try await scoped.transfers.complete(transferId: transfer.id)
            finished = true
            try GuestUploadCleanup.remove(origin: link.origin, transferID: transfer.id)
            allocation = nil
            uploadComplete = true
            stage = "Files sent"
        } catch {
            stage = "Upload stopped"
            let incident = await TransferTrafficRecovery.inspect(
                error, server: link.origin,
                resource: allocation == nil ? .slot : .transfer,
                id: allocation?.0 ?? link.id, direction: .upload,
                token: allocation?.1)
            self.error =
                (error as? GuestUploadSelectionError)?.localizedDescription ?? incident?.localizedDescription
                ?? "Could not send these files. Check the connection, file sizes and whether the receive link is still available. Select Send to retry."
            if finished {
                uploadComplete = true
                stage = "Files sent"
                self.error = nil
                return
            }
            if let allocation {
                let origin = link.origin
                let result = await Task.detached {
                    try? await GuestUploadCleanup.attempt(origin: origin, transferID: allocation.0, capability: allocation.1)
                }.value
                if let finalized = result {
                    try? GuestUploadCleanup.remove(origin: origin, transferID: allocation.0)
                    if finalized {
                        uploadComplete = true
                        stage = "Files sent"
                        self.error = nil
                    }
                }
                cleanupPending = result == nil
            }
        }
    }
}

/// Indexed cleanup metadata; each resource capability remains exclusively in Keychain.
@MainActor
enum GuestUploadCleanup {
    struct Entry: Codable, Sendable {
        let origin: String
        let transferID: String
        let capability: String
    }
    private static let storage = DeviceRetryStorage(kind: "guest-upload-cleanup")
    private static var flushing = false
    static var activeID: String?
    private static let storageMessage = "Unfinished upload cleanup could not be read or saved. Existing cleanup credentials are preserved. Restore storage access and retry."
    private static func reference(origin: String, transferID: String) -> String {
        "guest-upload-cleanup-v2:" + DeviceRetryQueue.identity([origin, transferID])
    }
    private static func job(origin: String, transferID: String) -> DeviceRetryQueue.Job {
        .init(origin: origin, transferID: transferID, keyReference: reference(origin: origin, transferID: transferID))
    }
    private static func saveCapability(_ capability: String, reference: String) throws {
        guard !capability.isEmpty, capability.utf8.count <= 8192 else { throw AccountError.storage }
        let bytes = Data(capability.utf8)
        if let old = try SecretStore.readStrict(reference) {
            guard old == bytes else { throw AccountError.storage }
        } else {
            try SecretStore.write(bytes, name: reference)
        }
    }
    static func enqueue(origin: String, transferID: String, capability: String) throws {
        do {
            let record = job(origin: origin, transferID: transferID)
            try DeviceRetryQueue.validate(record)
            try saveCapability(capability, reference: record.keyReference!)
            try storage.queue().enqueue(record)
            DeviceRetryStatus.shared.cleanupError = nil
        } catch {
            DeviceRetryStatus.shared.cleanupError = storageMessage
            throw error
        }
    }
    static func remove(origin: String, transferID: String) throws {
        do {
            let record = job(origin: origin, transferID: transferID)
            // Commit retirement first, so a crash/import cannot resurrect a completed job.
            try storage.queue().remove(record)
            SecretStore.remove(record.keyReference!)
        } catch {
            DeviceRetryStatus.shared.cleanupError = storageMessage
            throw error
        }
    }

    /// Returns true when a lost finalization response concealed a successful upload.
    nonisolated static func attempt(origin: String, transferID: String, capability: String) async throws -> Bool {
        let client = try ApiClient.companion.slotUpload(origin: origin, capability: capability)
        defer { client.close() }
        do {
            struct Status: Decodable { let status: String }
            let data = try await AccountHTTP.request(server: origin, path: "transfers/" + transferID + "/upload-status", token: capability)
            let status = try JSONDecoder().decode(Status.self, from: data)
            if status.status == "complete" {
                return true
            }
            if status.status == "expired" || status.status == "revoked" {
                return false
            }
        } catch AccountError.unavailable, TransferIncident.revoked { return false }
        try await client.transfers.delete(transferId: transferID, deleteToken: capability)
        return false
    }

    static func flush() async {
        guard !flushing else { return }
        flushing = true
        defer {
            flushing = false
            DeviceRetryStatus.shared.importingCleanup = false
        }
        do {
            DeviceRetryStatus.shared.importingCleanup = true
            while true {
                try Task.checkCancellation()
                let complete = try storage.migrate(
                    read: { try SecretStore.readStrict("guest-upload-cleanup") },
                    decode: { data in
                        let old = try JSONDecoder().decode(Entry.self, from: data)
                        return job(origin: old.origin, transferID: old.transferID)
                    },
                    prepare: { data, record in
                        let old = try JSONDecoder().decode(Entry.self, from: data)
                        try saveCapability(old.capability, reference: record.keyReference!)
                    })
                if complete { break }
                await Task.yield()
            }
            DeviceRetryStatus.shared.importingCleanup = false
            let queue = try storage.queue()
            // One extra indexed row lets an active upload be skipped without starving a retry.
            let entries = try queue.batch(scopes: ["anonymous"], limit: 5)
            var attempted = 0
            var unavailable = false
            for entry in entries.jobs {
                try Task.checkCancellation()
                guard attempted < 4 else { break }
                guard activeID != entry.origin + "|" + entry.transferID else { continue }
                attempted += 1
                do {
                    guard let reference = entry.keyReference,
                        reference == self.reference(origin: entry.origin, transferID: entry.transferID),
                        let bytes = try SecretStore.readStrict(reference), bytes.count <= 8192,
                        let capability = String(data: bytes, encoding: .utf8), !capability.isEmpty
                    else { throw AccountError.storage }
                    _ = try await attempt(origin: entry.origin, transferID: entry.transferID, capability: capability)
                    try remove(origin: entry.origin, transferID: entry.transferID)
                } catch {
                    try Task.checkCancellation()
                    try queue.rotate(entry)
                    unavailable = true
                }
            }
            DeviceRetryStatus.shared.cleanupError =
                entries.hadInvalidJobs
                ? "Some saved cleanup records could not be read. They are preserved; other cleanup will continue. Restore local storage or retry."
                : unavailable ? "Some unfinished upload cleanup is still pending. Check the connection and device storage, then retry." : nil
        } catch is CancellationError {
            // A later foreground pass resumes committed migration and retries.
        } catch { DeviceRetryStatus.shared.cleanupError = storageMessage }
    }
}

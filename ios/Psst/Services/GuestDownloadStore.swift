import CryptoKit
import Foundation
import Shared

/// Device-local downloads never acquire account ownership or deletion capabilities.
struct GuestFile: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var name: String
    var size: Int64
    var mime: String
    var relativePath: String?
    var digest: String?
    var saved = false
    var encoding = "chunked-v1"
    var chunkSize: Int32 = 4_194_304
    var encryptionID = ""
}

/// Old local entries remain openable; absent frame metadata never enables legacy wire decoding.
extension GuestFile {
    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(String.self, forKey: .id)
        name = try values.decode(String.self, forKey: .name)
        size = try values.decode(Int64.self, forKey: .size)
        mime = try values.decode(String.self, forKey: .mime)
        relativePath = try values.decodeIfPresent(String.self, forKey: .relativePath)
        digest = try values.decodeIfPresent(String.self, forKey: .digest)
        saved = try values.decodeIfPresent(Bool.self, forKey: .saved) ?? false
        encoding = try values.decodeIfPresent(String.self, forKey: .encoding) ?? ""
        chunkSize = try values.decodeIfPresent(Int32.self, forKey: .chunkSize) ?? 0
        encryptionID = try values.decodeIfPresent(String.self, forKey: .encryptionID) ?? ""
    }
}

struct GuestDownload: Codable, Identifiable, Equatable, Sendable {
    var id: String
    var origin: String
    var transferID: String
    var createdAt = Date()
    var files: [GuestFile] = []
    var sharedTitle: String? = nil
    var exhausted: Bool? = nil
    var complete = false
    var receiptPending = false
    var receiptDelivered = false
    var remainingDownloads: [String: Int64]? = nil
    var keyReference: String {
        "guest-download-" + id
    }

    static func identity(origin: String, transferID: String) -> String {
        SHA256.hash(data: Data((origin + "|download|" + transferID).utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

@Observable
@MainActor
final class GuestDownloadStore {
    private(set) var revision = 0
    private(set) var isReady = false
    private(set) var importedRecords: Int64 = 0
    private(set) var error: String?
    private struct Receipt: Codable, Equatable {
        let origin: String
        let transferID: String
    }

    private let file: URL
    let documents: URL
    private var database: HistoryRecordDatabase?
    private var legacySourceExpected = false
    private var migrating = false
    private var flushing = false
    private var reconciling = false
    private static let scope = "guest-device"
    private static let migrationKey = "guest-history-v1"
    private static let storageMessage =
        "Local received history could not be opened or imported. Existing files, history and keys have been preserved. Restore storage access and retry."

    init(root: URL? = nil) {
        let manager = FileManager.default
        documents = root ?? manager.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let support = root ?? manager.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        file = support.appendingPathComponent("guest-downloads.json")
        do {
            try openDatabase()
            try migrationStep()
        } catch { self.error = Self.storageMessage }
    }

    private func openDatabase() throws {
        guard database == nil else { return }
        let directory = file.deletingLastPathComponent().appendingPathComponent("guest-history.store", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.protectionKey: FileProtectionType.complete])
        database = try HistoryRecordDatabase(url: directory.appendingPathComponent("records.sqlite3"))
        legacySourceExpected = FileManager.default.fileExists(atPath: file.path)
    }

    private func migrationStep() throws {
        guard let database else { throw AccountError.storage }
        let existing = try database.migrationProgress(key: Self.migrationKey)
        if let existing, existing.complete {
            importedRecords = existing.processed
            isReady = true
            return
        }
        if !FileManager.default.fileExists(atPath: file.path) {
            guard existing == nil, !legacySourceExpected else { throw AccountError.storage }
            isReady = true
            return
        }
        let progress = try database.migrateJSONSnapshotBatch(source: file, key: Self.migrationKey, limit: 25) { section, data in
            if section == "records" {
                let record = try JSONDecoder().decode(GuestDownload.self, from: data)
                let row = try self.stored(record)
                if record.receiptPending {
                    try database.importIfAbsent(self.receiptRow(Receipt(origin: record.origin, transferID: record.transferID)))
                }
                if !record.files.isEmpty {
                    try database.importIfAbsent(self.reconcileRow(record.id))
                }
                return row
            }
            guard section == "receipts" else { throw AccountError.storage }
            return try self.receiptRow(JSONDecoder().decode(Receipt.self, from: data))
        }
        importedRecords = progress.processed
        isReady = progress.complete
        revision &+= 1
    }

    func finishMigration() async {
        guard !migrating else { return }
        migrating = true
        defer { migrating = false }
        error = nil
        do {
            try openDatabase()
            while !isReady, !Task.isCancelled {
                try migrationStep()
                await Task.yield()
            }
        } catch { self.error = Self.storageMessage }
    }

    private func readyDatabase() throws -> HistoryRecordDatabase {
        guard isReady, let database else { throw AccountError.storage }
        return database
    }

    private func stored(_ record: GuestDownload) throws -> HistoryRecordDatabase.Record {
        guard record.id == GuestDownload.identity(origin: record.origin, transferID: record.transferID), record.files.count <= 100,
              Set(record.files.map(\.id)).count == record.files.count
        else { throw AccountError.storage }
        return try HistoryRecordDatabase.Record(
            id: "guest|" + record.id, scope: Self.scope, kind: "guest", created: record.createdAt.timeIntervalSince1970, body: JSONEncoder().encode(record)
        )
    }

    private func receiptRow(_ receipt: Receipt, created: Double = -Date().timeIntervalSince1970) throws -> HistoryRecordDatabase.Record {
        let id = GuestDownload.identity(origin: receipt.origin, transferID: receipt.transferID)
        return try HistoryRecordDatabase.Record(id: "receipt|" + id, scope: Self.scope, kind: "receipt", created: created, body: JSONEncoder().encode(receipt))
    }

    private func reconcileRow(_ id: String, created: Double = -Date().timeIntervalSince1970) -> HistoryRecordDatabase.Record {
        HistoryRecordDatabase.Record(id: "reconcile|" + id, scope: Self.scope, kind: "reconcile", created: created, body: Data(id.utf8))
    }

    func find(_ id: String) throws -> GuestDownload? {
        _ = revision
        guard let row = try readyDatabase().read("guest|" + id) else { return nil }
        return try JSONDecoder().decode(GuestDownload.self, from: row.body)
    }

    struct Page {
        let records: [GuestDownload]
        let next: HistoryRecordDatabase.Cursor?
    }

    func page(after: HistoryRecordDatabase.Cursor? = nil) throws -> Page {
        _ = revision
        let value = try readyDatabase().page(scopes: [Self.scope], kinds: ["guest"], after: after, limit: 50)
        return try Page(records: value.records.map { try JSONDecoder().decode(GuestDownload.self, from: $0.body) }, next: value.next)
    }

    var hasPendingReceipts: Bool {
        _ = revision
        return (try? readyDatabase().hasAny(scopes: [Self.scope], kinds: ["receipt"])) ?? false
    }

    func update(_ record: GuestDownload) throws {
        try readyDatabase().transaction { database in
            var record = record
            if let existing = try find(record.id) {
                if existing.receiptDelivered {
                    record.receiptDelivered = true
                    record.receiptPending = false
                }
                for index in record.files.indices {
                    let value = record.files[index]
                    if let saved = existing.files.first(where: { $0.id == value.id }), saved.saved,
                       saved.relativePath == value.relativePath, saved.digest == value.digest, saved.size == value.size
                    {
                        record.files[index].saved = true
                    }
                }
            }
            try database.write(stored(record))
            if record.receiptPending {
                let receipt = try receiptRow(Receipt(origin: record.origin, transferID: record.transferID))
                if try database.read(receipt.id) == nil {
                    try database.write(receipt)
                }
            }
            if !record.complete, record.files.contains(where: { $0.relativePath != nil }) {
                let job = reconcileRow(record.id)
                if try database.read(job.id) == nil {
                    try database.write(job)
                }
            }
        }
        revision &+= 1
    }

    func prepare(origin: String, transferID: String, key: Data) throws -> GuestDownload {
        let id = GuestDownload.identity(origin: origin, transferID: transferID)
        let record = try find(id) ?? GuestDownload(id: id, origin: origin, transferID: transferID)
        if let existing = try SecretStore.readStrict(record.keyReference), existing != key,
           !record.files.isEmpty || record.complete || record.receiptPending || record.receiptDelivered
        {
            throw GuestError.conflictingKey
        }
        try SecretStore.write(key, name: record.keyReference)
        try update(record)
        return record
    }

    func remove(_ record: GuestDownload) throws {
        try readyDatabase().transaction { database in
            try database.remove("guest|" + record.id)
            try database.remove("reconcile|" + record.id)
        }
        revision &+= 1
        SecretStore.remove(record.keyReference)
        // Saved files and the independent receipt job intentionally remain.
    }

    /// Missing previously published output needs consent even when the transfer is only partial.
    func requiresRedownloadConsent(_ record: GuestDownload) -> Bool {
        record.files.contains { $0.saved && url($0) == nil }
    }

    func url(_ file: GuestFile) -> URL? {
        guard file.saved, let path = file.relativePath, let url = safeURL(path),
              FileManager.default.isReadableFile(atPath: url.path),
              let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, Int64(size) == file.size
        else { return nil }
        return url
    }

    private func safeURL(_ path: String) -> URL? {
        guard !path.hasPrefix("/"), !path.split(separator: "/").contains("..") else { return nil }
        let result = documents.appendingPathComponent(path).standardizedFileURL
        guard result.path.hasPrefix(documents.standardizedFileURL.path + "/") else { return nil }
        return result
    }

    /// An intent is persisted before publication; its digest reconciles a kill between rename and checkpoint.
    func save(_ data: Data, index: Int, record: inout GuestDownload) throws {
        let temporary = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: temporary) }
        try data.write(to: temporary, options: [.atomic, .completeFileProtection])
        try publish(temporary, index: index, record: &record)
    }

    func publish(_ temporary: URL, index: Int, record: inout GuestDownload) throws {
        guard record.files.indices.contains(index),
              let size = try temporary.resourceValues(forKeys: [.fileSizeKey]).fileSize,
              Int64(size) == record.files[index].size
        else { throw GuestError.invalidManifest }
        let directory = "Received/Guest/" + record.id
        try FileManager.default.createDirectory(at: documents.appendingPathComponent(directory), withIntermediateDirectories: true)
        let safe = try GuestFiles.filename(record.files[index].name)
        let relative = directory + "/" + UUID().uuidString + "-" + safe
        let destination = documents.appendingPathComponent(relative)
        record.files[index].relativePath = relative
        record.files[index].digest = try StreamedFiles.digest(temporary)
        record.files[index].saved = false
        try update(record)
        try Task.checkCancellation()
        try FileManager.default.moveItem(at: temporary, to: destination)
        record.files[index].saved = true
        try update(record)
    }

    /// Reconcile one exact transfer. Digest IO runs off the main actor and the
    /// commit re-reads current metadata so a suspended check cannot restore deletion
    /// or overwrite a newer publication checkpoint.
    func reconcile(_ id: String) async throws {
        guard let snapshot = try find(id) else { return }
        var published: [GuestFile] = []
        for value in snapshot.files {
            try Task.checkCancellation()
            guard let path = value.relativePath, let destination = safeURL(path) else { continue }
            let worker = Task.detached { () throws -> Bool in
                try Task.checkCancellation()
                try? FileManager.default.removeItem(at: destination.appendingPathExtension("pending"))
                guard !value.saved else { return false }
                let size: Int?
                do { size = try destination.resourceValues(forKeys: [.fileSizeKey]).fileSize } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
                    return false
                }
                guard let size, Int64(size) == value.size else { return false }
                return try StreamedFiles.digest(destination) == value.digest
            }
            let saved = try await withTaskCancellationHandler(operation: { try await worker.value }, onCancel: { worker.cancel() })
            if saved {
                published.append(value)
            }
        }
        try Task.checkCancellation()
        try readyDatabase().transaction { database in
            guard let row = try database.read("guest|" + id) else { return }
            var current = try JSONDecoder().decode(GuestDownload.self, from: row.body)
            let unchanged = current == snapshot
            for value in published {
                if let index = current.files.firstIndex(where: { $0 == value }) {
                    current.files[index].saved = true
                }
            }
            if !current.files.isEmpty, current.files.allSatisfy({ url($0) != nil }) {
                current.complete = true
                current.receiptPending = !current.receiptDelivered
            }
            if try current != (JSONDecoder().decode(GuestDownload.self, from: row.body)) {
                try update(current)
            }
            if unchanged {
                try database.remove("reconcile|" + id)
            }
        }
        revision &+= 1
    }

    func reconcilePending() async {
        guard isReady, !reconciling else { return }
        reconciling = true
        defer { reconciling = false }
        do {
            let database = try readyDatabase()
            let jobs = try database.page(scopes: [Self.scope], kinds: ["reconcile"], limit: 4).records
            for job in jobs {
                try Task.checkCancellation()
                let id = String(decoding: job.body, as: UTF8.self)
                do {
                    if try find(id) == nil {
                        try database.remove(job.id)
                    } else {
                        try await reconcile(id)
                    }
                } catch {
                    if error is CancellationError {
                        throw error
                    }
                    try database.write(reconcileRow(id))
                    self.error = "A saved-file checkpoint could not be checked. Existing files are preserved; retry when storage is available."
                }
            }
        } catch {
            if !(error is CancellationError) {
                self.error = Self.storageMessage
            }
        }
    }

    func flushReceipts(deliver: ((String, String) async throws -> Void)? = nil) async {
        guard !flushing, isReady else { return }
        flushing = true
        defer { flushing = false }
        do {
            let database = try readyDatabase()
            let jobs = try database.page(scopes: [Self.scope], kinds: ["receipt"], limit: 4).records
            for job in jobs {
                try Task.checkCancellation()
                let receipt: Receipt
                do { receipt = try JSONDecoder().decode(Receipt.self, from: job.body) } catch {
                    try database.write(
                        HistoryRecordDatabase.Record(
                            id: job.id, scope: job.scope, kind: job.kind,
                            created: -Date().timeIntervalSince1970, body: job.body
                        )
                    )
                    self.error = "A delivery confirmation could not be read. Its original job has been preserved; other confirmations can continue."
                    continue
                }
                do {
                    if let deliver {
                        try await deliver(receipt.origin, receipt.transferID)
                    } else {
                        let client = try GuestNetwork.client(receipt.origin)
                        defer { client.close() }
                        try await client.transfers.acknowledgeDownload(transferId: receipt.transferID)
                    }
                    try database.transaction { database in
                        let id = GuestDownload.identity(origin: receipt.origin, transferID: receipt.transferID)
                        if var record = try find(id) {
                            record.receiptPending = false
                            record.receiptDelivered = true
                            try database.write(stored(record))
                        }
                        try database.remove(job.id)
                    }
                    revision &+= 1
                } catch {
                    if error is CancellationError {
                        throw error
                    }
                    // Oldest-first timestamps rotate failed hosts behind other jobs.
                    try database.write(receiptRow(receipt))
                }
            }
        } catch {
            if !(error is CancellationError) {
                self.error = Self.storageMessage
            }
        }
    }
}

enum GuestFiles {
    static func decrypt(_ data: Data, key: Data) throws -> Data {
        guard key.count == 32, data.count >= 28 else { throw GuestError.invalidManifest }
        return try CryptoProvider.shared.decrypt(
            key: key.toKotlinByteArray(), nonce: Data(data.prefix(12)).toKotlinByteArray(), ciphertext: Data(data.dropFirst(12)).toKotlinByteArray()
        ).toData()
    }

    static func digest(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// Presentation only; do not rewrite authenticated manifests or persisted resume identity.
    static func displayName(_ name: String) -> String {
        (try? filename(name)) ?? L10n.text("File")
    }

    static func filename(_ name: String) throws -> String {
        try ManifestValidator.shared.safeFilename(name: name)
    }
}

enum GuestError: LocalizedError {
    case input, conflictingKey, invalidManifest, missingKey, notReady, redownloadConsent, downloadLimit
    var errorDescription: String? {
        switch self {
        case .input: "This is not a supported psst.zip link or server login code. Scan again or paste a complete link."
        case .conflictingKey: "This transfer is already stored with a different key. Its existing files and key were preserved."
        case .invalidManifest: "The encrypted file list is invalid or exceeds this app’s limits."
        case .missingKey: "This transfer’s key is unavailable. Scan or paste the original link again."
        case .redownloadConsent: "A previously saved file is missing. Choose Download missing files to receive it again."
        case .downloadLimit: "Download limit reached. Already saved files remain available. Ask the sender for a new link for missing files."
        case .notReady: "This transfer is not ready or has expired. Ask the sender for an available download link."
        }
    }
}

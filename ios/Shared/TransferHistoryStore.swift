import Foundation

/// Main app and share extension address individual records in the same SQLite store.
/// Migration keeps its original JSON/preferences and checkpoints each import batch.
@Observable
@MainActor
final class TransferHistoryStore {
    private(set) var revision = 0
    private(set) var isReady = false
    private(set) var migrationError: String?
    private(set) var importedRecords: Int64 = 0
    private let defaults: UserDefaults
    private let fileURL: URL?
    private var database: HistoryRecordDatabase?
    private var migrating = false
    private var legacySourceExpected = false

    init(
        defaults: UserDefaults = AppConstants.sharedDefaults,
        fileURL: URL? = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: AppConstants.appGroupIdentifier)?.appendingPathComponent("transferHistory-v2.json")
    ) {
        self.defaults = defaults
        self.fileURL = fileURL
        do {
            try openDatabase()
            try migrationStep()
        } catch { migrationError = Self.storageMessage }
    }

    private static let storageMessage = "Local history could not be opened or imported. Existing records and keys have been preserved. Restore storage access and retry."
    private func decoder() -> JSONDecoder {
        let result = JSONDecoder()
        result.dateDecodingStrategy = .iso8601
        return result
    }
    private func encoder() -> JSONEncoder {
        let result = JSONEncoder()
        result.dateEncodingStrategy = .iso8601
        return result
    }
    private func openDatabase() throws {
        guard database == nil else { return }
        guard let fileURL else { throw AccountError.storage }
        let directory = fileURL.deletingLastPathComponent().appendingPathComponent(fileURL.lastPathComponent + ".store", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.protectionKey: FileProtectionType.complete])
        let opened = try HistoryRecordDatabase(url: directory.appendingPathComponent("records.sqlite3"))
        // Older UserDefaults APIs expose Data as one allocation. Copy it once;
        // parsing/import after this point streams the source and retains it.
        if try opened.migrationProgress(key: "account-history-v2")?.complete != true,
            !FileManager.default.fileExists(atPath: fileURL.path), let data = defaults.data(forKey: AppConstants.transferHistoryKey)
        {
            if defaults.string(forKey: "legacyHistoryServerURL") == nil, let original = defaults.string(forKey: AppConstants.serverURLKey) {
                defaults.set(original, forKey: "legacyHistoryServerURL")
            }
            var coordinationError: NSError?
            var copyError: Error?
            NSFileCoordinator().coordinate(writingItemAt: fileURL, options: .forReplacing, error: &coordinationError) { target in
                guard !FileManager.default.fileExists(atPath: target.path) else { return }
                do { try data.write(to: target, options: [.atomic, .completeFileProtection]) } catch { copyError = error }
            }
            if let coordinationError { throw coordinationError }
            if let copyError { throw copyError }
        }
        legacySourceExpected = FileManager.default.fileExists(atPath: fileURL.path)
        database = opened
    }

    private func migrationStep() throws {
        guard let database, let fileURL else { throw AccountError.storage }
        let existing = try database.migrationProgress(key: "account-history-v2")
        if let existing, existing.complete {
            importedRecords = existing.processed
            isReady = try normalizeCheckpoints(database)
            return
        }
        if !FileManager.default.fileExists(atPath: fileURL.path) {
            guard existing == nil, !legacySourceExpected else { throw AccountError.storage }
            isReady = try normalizeCheckpoints(database)
            return
        }
        let original = defaults.string(forKey: "legacyHistoryServerURL")
        let progress = try database.migrateJSONBatch(source: fileURL, key: "account-history-v2", limit: 25) { data in
            var record = try self.decoder().decode(TransferRecord.self, from: data)
            if record.ownerID == nil && record.serverURL == nil {
                if var origin = URLComponents(string: record.shareURL ?? "") {
                    origin.path = ""
                    origin.query = nil
                    origin.fragment = nil
                    record.serverURL = try? AccountHTTP.origin(origin.string ?? "")
                }
                if record.serverURL == nil, let original { record.serverURL = try? AccountHTTP.origin(original) }
            }
            let original = try self.stored(record)
            return try self.stored(ReceiveCheckpointStorage.stage(database, record: record, original: original))
        }
        importedRecords = progress.processed
        if progress.complete { isReady = try normalizeCheckpoints(database) } else { isReady = false }
        revision &+= 1
    }

    private func normalizeCheckpoints(_ database: HistoryRecordDatabase) throws -> Bool {
        try ReceiveCheckpointStorage.migrateBatch(database, decode: { try self.decoder().decode(TransferRecord.self, from: $0) }, encode: stored)
    }

    func finishMigration() async {
        guard !migrating else { return }
        migrating = true
        defer { migrating = false }
        migrationError = nil
        do {
            try openDatabase()
            while !isReady, !Task.isCancelled {
                try migrationStep()
                await Task.yield()
            }
        } catch { migrationError = Self.storageMessage }
    }

    private func readyDatabase() throws -> HistoryRecordDatabase {
        guard isReady, let database else { throw AccountError.storage }
        return database
    }
    private func stored(_ record: TransferRecord) throws -> HistoryRecordDatabase.Record {
        HistoryRecordDatabase.Record(
            id: record.localID,
            scope: record.ownerID.flatMap { owner in record.serverURL.map { $0 + "|" + owner } } ?? "legacy",
            kind: record.isSlot == true ? "slot" : "transfer", created: record.createdAt.timeIntervalSince1970, body: try encoder().encode(record))
    }
    func reload() { revision &+= 1 }
    func record(_ localID: String) throws -> TransferRecord? {
        _ = revision
        guard let row = try readyDatabase().read(localID) else { return nil }
        return try decoder().decode(TransferRecord.self, from: row.body)
    }
    func records(ids: [String], session: DeviceSession) throws -> [TransferRecord] {
        guard ids.count <= 100, session.canTransfer else { throw AccountError.changed }
        _ = revision
        return try readyDatabase().transaction { database in
            var remaining = HistoryRecordDatabase.maximumPageBytes
            return try ids.compactMap { id in
                guard let row = try database.read(id) else { return nil }
                guard row.body.count <= remaining else { throw AccountError.storage }
                remaining -= row.body.count
                let record = try decoder().decode(TransferRecord.self, from: row.body)
                return record.belongs(to: session) ? record : nil
            }
        }
    }
    struct Page {
        let records: [TransferRecord]
        let next: HistoryRecordDatabase.Cursor?
    }
    func page(session: DeviceSession, kinds: [String] = ["transfer", "slot"], after: HistoryRecordDatabase.Cursor? = nil) throws -> Page {
        guard session.canTransfer else { throw AccountError.changed }
        _ = revision
        let value = try readyDatabase().page(scopes: [session.accountID], kinds: kinds, after: after, limit: 50)
        return Page(records: try value.records.map { try decoder().decode(TransferRecord.self, from: $0.body) }, next: value.next)
    }
    var hasLegacyRecords: Bool {
        _ = revision
        return (try? readyDatabase().hasAny(scopes: ["legacy"], kinds: ["transfer", "slot"])) ?? false
    }
    /// Convenience for small first-page views; callers needing more must use page().
    func visible(for session: DeviceSession?) -> [TransferRecord] {
        guard let session else { return [] }
        return (try? page(session: session).records) ?? []
    }
    func add(_ record: TransferRecord) throws { try update(record) }
    func update(_ record: TransferRecord) throws {
        try mutate(ids: [record.localID]) { values in
            values = [record.preservingLocalName(from: values.first)]
        }
    }
    func applySnapshot(_ records: [TransferRecord]) throws {
        try mutate(ids: records.map(\.localID)) { values in
            let existing = Dictionary(values.map { ($0.localID, $0) }, uniquingKeysWith: { first, _ in first })
            values = records.map { $0.preservingLocalName(from: existing[$0.localID]) }
        }
    }
    func rename(_ record: TransferRecord, name: String, session: DeviceSession) throws {
        guard record.belongs(to: session), session.canTransfer, SecretStore.session == session else { throw AccountError.changed }
        let normalized = name.trimmingCharacters(in: .whitespacesAndNewlines)
        try mutate(ids: [record.localID]) { values in
            guard !values.isEmpty else { return }
            values[0].customTitle = normalized.isEmpty ? nil : String(normalized.prefix(200))
        }
    }
    func remove(_ record: TransferRecord) throws {
        try readyDatabase().remove(record.localID)
        revision &+= 1
        SecretStore.remove(record.vaultID)
    }
    /// Read/modify only the explicit identities inside one cross-process writer transaction.
    func mutate(ids: [String], _ change: (inout [TransferRecord]) -> Void) throws {
        guard ids.count <= 100 else { throw AccountError.storage }
        let requested = Set(ids)
        try readyDatabase().transaction { database in
            var remaining = HistoryRecordDatabase.maximumPageBytes
            var values = try requested.compactMap { id -> TransferRecord? in
                guard let row = try database.read(id) else { return nil }
                guard row.body.count <= remaining else { throw AccountError.storage }
                remaining -= row.body.count
                return try decoder().decode(TransferRecord.self, from: row.body)
            }
            let prior = Dictionary(uniqueKeysWithValues: values.map { ($0.localID, $0) })
            change(&values)
            guard values.count <= 100, values.allSatisfy({ requested.contains($0.localID) }), Set(values.map(\.localID)).count == values.count else { throw AccountError.storage }
            var remainingWriteBytes = HistoryRecordDatabase.maximumPageBytes
            for var record in values {
                guard (record.savedFiles ?? [:]).isEmpty, (record.savedTransfers ?? []).isEmpty else { throw AccountError.storage }
                if record.isSlot == true {
                    record.totalSize = max(record.totalSize, prior[record.localID]?.totalSize ?? 0)
                    record = ReceiveCheckpointStorage.stripped(record)
                }
                let row = try stored(record)
                guard row.body.count <= remainingWriteBytes else { throw AccountError.storage }
                remainingWriteBytes -= row.body.count
                try database.write(row)
            }
            for id in requested.subtracting(values.map(\.localID)) { try database.remove(id) }
        }
        revision &+= 1
    }
    /// Load only the currently visible inbox children. Every child is capped at
    /// 100 indexed files and combined path bytes remain below the page budget.
    func receiveCheckpoints(parent: TransferRecord, transferIDs: [String], fileExists: @escaping (String, Int64?) -> Bool) throws -> [String: ReceiveCheckpoint] {
        guard transferIDs.count <= 50, Set(transferIDs).count == transferIDs.count else { throw AccountError.storage }
        return try readyDatabase().transaction { database in
            guard let row = try database.read(parent.localID) else { throw AccountError.storage }
            let current = try decoder().decode(TransferRecord.self, from: row.body)
            var result: [String: ReceiveCheckpoint] = [:]
            var remaining = HistoryRecordDatabase.maximumPageBytes
            for id in transferIDs {
                let value = try ReceiveCheckpointStorage.load(database, parent: current, transferID: id, fileExists: fileExists)
                for path in value.paths.values {
                    guard path.utf8.count <= remaining else { throw AccountError.storage }
                    remaining -= path.utf8.count
                }
                result[id] = value
            }
            return result
        }
    }

    @discardableResult
    func saveReceivedFile(parent: TransferRecord, transferID: String, blobID: String, path: String, size: Int64, title: String) throws -> TransferRecord {
        let result = try readyDatabase().transaction { database in
            guard let row = try database.read(parent.localID) else { throw AccountError.storage }
            var current = try decoder().decode(TransferRecord.self, from: row.body)
            guard current.checkpointVersion == 1 else { throw AccountError.storage }
            let inserted = try ReceiveCheckpointStorage.save(
                database, parent: current,
                file: .init(transferID: transferID, blobID: blobID, path: path, size: size))
            if inserted {
                let total = current.totalSize.addingReportingOverflow(size)
                guard !total.overflow, total.partialValue >= 0 else { throw AccountError.storage }
                current.totalSize = total.partialValue
            }
            current.title = current.title ?? String(title.prefix(1024))
            try database.write(stored(current))
            return current
        }
        revision &+= 1
        return result
    }

    func completeReceivedTransfer(parent: TransferRecord, transferID: String, blobIDs: [String], fileExists: @escaping (String, Int64?) -> Bool) throws {
        try readyDatabase().transaction { database in
            guard let row = try database.read(parent.localID) else { throw AccountError.storage }
            let current = try decoder().decode(TransferRecord.self, from: row.body)
            let checkpoint = try ReceiveCheckpointStorage.load(database, parent: current, transferID: transferID, fileExists: fileExists)
            guard checkpoint.readyToAcknowledge(blobIDs: blobIDs) else { throw AccountError.storage }
            try ReceiveCheckpointStorage.complete(database, parent: current, transferID: transferID)
        }
        revision &+= 1
    }

    func revoke(_ record: TransferRecord, session: DeviceSession) async throws {
        guard record.canManage(as: session), SecretStore.session == session else { throw AccountError.changed }
        let path = (record.isSlot == true ? "slots/" : "transfers/") + record.id
        _ = try await AccountHTTP.request(server: session.serverURL, path: path, method: "DELETE", token: record.capabilities?.deletionToken ?? session.token)
        guard SecretStore.session == session else { throw AccountError.changed }
        try remove(record)
    }
}

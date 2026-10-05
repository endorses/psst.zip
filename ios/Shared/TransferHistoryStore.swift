import Foundation

/// Main app and share extension address individual records in the same SQLite store.
/// Migration keeps its original JSON/preferences and checkpoints each import batch.
@Observable @MainActor final class TransferHistoryStore {
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
        fileURL: URL? = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: AppConstants.appGroupIdentifier)?
            .appendingPathComponent("transferHistory-v2.json")
    ) {
        self.defaults = defaults
        self.fileURL = fileURL
        do {
            try openDatabase()
            try migrationStep()
        } catch { migrationError = Self.storageMessage(for: error) }
    }

    private static let storageMessage =
        "Local history could not be opened or imported. Existing records and keys have been preserved. Restore storage access and retry."
    private static func storageMessage(for error: Error) -> String {
        switch error {
        case ReceiveHistoryStream.Failure.scalarTooLarge:
            return
                "A local history scalar or checkpoint entry exceeds the recovery limit. Original history and keys are preserved; recovery is paused."
        case ReceiveHistoryStream.Failure.invalidCheckpoint:
            return
                "A saved inbox checkpoint has an invalid identity or unsafe path. Original history and keys are preserved; recovery is paused."
        case ReceiveHistoryStream.Failure.malformed, is DecodingError:
            return
                "Local history contains malformed JSON or metadata. Original history and keys are preserved; recovery is paused."
        case HistoryRecordDatabase.Failure.migrationSourceChanged,
            HistoryJSONStream.Failure.sourceChanged, ReceiveHistoryStream.Failure.sourceChanged:
            return
                "The original local history source changed during recovery. Restore the unchanged original and retry. Records and keys are preserved."
        default: return storageMessage
        }
    }
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
        let directory = fileURL.deletingLastPathComponent().appendingPathComponent(
            fileURL.lastPathComponent + ".store", isDirectory: true)
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true,
            attributes: [.protectionKey: FileProtectionType.complete])
        let opened = try HistoryRecordDatabase(url: directory.appendingPathComponent("records.sqlite3"))
        // Older UserDefaults APIs expose Data as one allocation. Copy it once;
        // parsing/import after this point streams the source and retains it.
        if try opened.migrationProgress(key: "account-history-v2")?.complete != true,
            !FileManager.default.fileExists(atPath: fileURL.path),
            let data = defaults.data(forKey: AppConstants.transferHistoryKey)
        {
            if defaults.string(forKey: "legacyHistoryServerURL") == nil,
                let original = defaults.string(forKey: AppConstants.serverURLKey)
            {
                defaults.set(original, forKey: "legacyHistoryServerURL")
            }
            var coordinationError: NSError?
            var copyError: Error?
            NSFileCoordinator().coordinate(
                writingItemAt: fileURL, options: .forReplacing, error: &coordinationError
            ) { target in
                guard !FileManager.default.fileExists(atPath: target.path) else { return }
                do { try data.write(to: target, options: [.atomic, .completeFileProtection]) } catch {
                    copyError = error
                }
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
        let progress = try database.migrateReceiveJSONBatch(
            source: fileURL, key: "account-history-v2",
            decode: { data in
                var record = try self.decoder().decode(TransferRecord.self, from: data)
                if record.ownerID == nil && record.serverURL == nil {
                    if var origin = URLComponents(string: record.shareURL ?? "") {
                        origin.path = ""
                        origin.query = nil
                        origin.fragment = nil
                        record.serverURL = try? AccountHTTP.origin(origin.string ?? "")
                    }
                    if record.serverURL == nil, let original {
                        record.serverURL = try? AccountHTTP.origin(original)
                    }
                }
                return try self.stored(ReceiveCheckpointStorage.stripped(record))
            }, validateEntry: ReceiveCheckpointStorage.validateEntry,
            promote: { current, entry in
                try ReceiveCheckpointStorage.promote(
                    database, parent: self.decoder().decode(TransferRecord.self, from: current.body),
                    entry: entry)
            })
        importedRecords = progress.processed
        if progress.complete { isReady = try normalizeCheckpoints(database) } else { isReady = false }
        revision &+= 1
    }

    private func normalizeCheckpoints(_ database: HistoryRecordDatabase) throws -> Bool {
        try ReceiveCheckpointStorage.migrateBatch(
            database, decode: { try self.decoder().decode(TransferRecord.self, from: $0) }, encode: stored
        )
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
        } catch { migrationError = Self.storageMessage(for: error) }
    }

    private func readyDatabase() throws -> HistoryRecordDatabase {
        guard isReady, let database else { throw AccountError.storage }
        return database
    }
    private func stored(_ record: TransferRecord) throws -> HistoryRecordDatabase.Record {
        HistoryRecordDatabase.Record(
            id: record.localID,
            scope: record.ownerID.flatMap { owner in record.serverURL.map { $0 + "|" + owner } }
                ?? "legacy", kind: record.isSlot == true ? "slot" : "transfer",
            created: record.createdAt.timeIntervalSince1970, body: try encoder().encode(record))
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
    func page(
        session: DeviceSession, kinds: [String] = ["transfer", "slot"],
        after: HistoryRecordDatabase.Cursor? = nil
    ) throws -> Page {
        guard session.canTransfer else { throw AccountError.changed }
        _ = revision
        let value = try readyDatabase().page(
            scopes: [session.accountID], kinds: kinds, after: after, limit: 50)
        return Page(
            records: try value.records.map { try decoder().decode(TransferRecord.self, from: $0.body) },
            next: value.next)
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
        NotificationCenter.default.post(
            name: .historyMutation, object: record.serverURL, userInfo: ["ownerID": record.ownerID ?? ""])
    }
    func applySnapshot(_ records: [TransferRecord]) throws {
        try mutate(ids: records.map(\.localID)) { values in
            let existing = Dictionary(
                values.map { ($0.localID, $0) }, uniquingKeysWith: { first, _ in first })
            values = records.map { $0.preservingLocalName(from: existing[$0.localID]) }
        }
    }
    func rename(_ record: TransferRecord, name: String, session: DeviceSession) async throws {
        guard record.belongs(to: session), session.canTransfer, SecretStore.session == session,
            UUID(uuidString: record.id) != nil
        else { throw AccountError.changed }
        let title = try SharedLinkTitle.normalize(name)
        let path = (record.isSlot == true ? "slots/" : "transfers/") + record.id + "/title"
        let data = try await AccountHTTP.request(
            server: session.serverURL, path: path, method: "PATCH", token: session.token,
            body: ["title": title ?? ""], maximumBytes: 4096, timeout: 10)
        struct Reply: Decodable {
            let title: String?
            enum CodingKeys: String, CodingKey { case title }
            init(from decoder: Decoder) throws {
                title = try decoder.container(keyedBy: CodingKeys.self).decode(String?.self, forKey: .title)
            }
        }
        let saved = try SharedLinkTitle.normalize(JSONDecoder().decode(Reply.self, from: data).title)
        guard saved == title, SecretStore.session == session, !Task.isCancelled else {
            throw AccountError.changed
        }
        try mutate(ids: [record.localID]) { values in
            guard !values.isEmpty else { return }
            values[0].sharedTitle = saved
        }
        NotificationCenter.default.post(
            name: .historyMutation, object: session.serverURL, userInfo: ["ownerID": session.userID])
    }
    func remove(_ record: TransferRecord) throws {
        let database = try readyDatabase()
        try database.transaction { database in
            // Metadata-only actions do not create permanent private tombstones.
            if try database.read(record.localID) != nil { try database.remove(record.localID) }
        }
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
            guard values.count <= 100, values.allSatisfy({ requested.contains($0.localID) }),
                Set(values.map(\.localID)).count == values.count
            else { throw AccountError.storage }
            var remainingWriteBytes = HistoryRecordDatabase.maximumPageBytes
            for var record in values {
                guard (record.savedFiles ?? [:]).isEmpty, (record.savedTransfers ?? []).isEmpty else {
                    throw AccountError.storage
                }
                if record.isSlot == true {
                    record.totalSize = max(record.totalSize, prior[record.localID]?.totalSize ?? 0)
                    record = ReceiveCheckpointStorage.stripped(record)
                }
                let row = try stored(record)
                guard row.body.count <= remainingWriteBytes else { throw AccountError.storage }
                remainingWriteBytes -= row.body.count
                try database.write(row)
            }
            for id in Set(prior.keys).subtracting(values.map(\.localID)) { try database.remove(id) }
        }
        revision &+= 1
    }
    /// Load only the currently visible inbox children. Every child is capped at
    /// 100 indexed files and combined path bytes remain below the page budget.
    func receiveCheckpoints(
        parent: TransferRecord, transferIDs: [String], fileExists: @escaping (String, Int64?) -> Bool
    ) throws -> [String: ReceiveCheckpoint] {
        guard transferIDs.count <= 50, Set(transferIDs).count == transferIDs.count else {
            throw AccountError.storage
        }
        return try readyDatabase().transaction { database in
            guard let row = try database.read(parent.localID) else { throw AccountError.storage }
            let current = try decoder().decode(TransferRecord.self, from: row.body)
            var result: [String: ReceiveCheckpoint] = [:]
            var remaining = HistoryRecordDatabase.maximumPageBytes
            for id in transferIDs {
                let value = try ReceiveCheckpointStorage.load(
                    database, parent: current, transferID: id, fileExists: fileExists)
                for path in value.paths.values {
                    guard path.utf8.count <= remaining else { throw AccountError.storage }
                    remaining -= path.utf8.count
                }
                result[id] = value
            }
            return result
        }
    }

    @discardableResult func saveReceivedFile(
        parent: TransferRecord, transferID: String, blobID: String, path: String, size: Int64,
        title: String
    ) throws -> TransferRecord {
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

    func completeReceivedTransfer(
        parent: TransferRecord, transferID: String, blobIDs: [String],
        fileExists: @escaping (String, Int64?) -> Bool
    ) throws {
        try readyDatabase().transaction { database in
            guard let row = try database.read(parent.localID) else { throw AccountError.storage }
            let current = try decoder().decode(TransferRecord.self, from: row.body)
            let checkpoint = try ReceiveCheckpointStorage.load(
                database, parent: current, transferID: transferID, fileExists: fileExists)
            guard checkpoint.readyToAcknowledge(blobIDs: blobIDs) else { throw AccountError.storage }
            try ReceiveCheckpointStorage.complete(database, parent: current, transferID: transferID)
        }
        revision &+= 1
    }

    func revoke(_ record: TransferRecord, session: DeviceSession) async throws {
        guard record.canManage(as: session), SecretStore.session == session else {
            throw AccountError.changed
        }
        let path = (record.isSlot == true ? "slots/" : "transfers/") + record.id
        _ = try await AccountHTTP.request(
            server: session.serverURL, path: path, method: "DELETE",
            token: record.capabilities?.deletionToken ?? session.token)
        guard SecretStore.session == session else { throw AccountError.changed }
        try remove(record)
        NotificationCenter.default.post(
            name: .historyMutation, object: session.serverURL, userInfo: ["ownerID": session.userID])
    }
}

extension Notification.Name {
    static let historyMutation = Notification.Name("zip.psst.ios.historyMutation")
}

@MainActor
extension TransferHistoryStore {
    struct Coverage: Codable {
        let identities: [String]
        let next: String?
        let generation: String?
        var navigationStale: Bool? = nil
    }
    func historySyncState(session: DeviceSession) throws -> HistoryRecordDatabase.SyncState? {
        let state = try readyDatabase().syncState(scope: session.accountID)
        if let state {
            guard UUID(uuidString: state.generation) != nil, HistorySnapshot.validCursor(state.cursor)
            else { throw HistorySync.Failure.invalidCache }
        }
        return state
    }
    func clearServerMetadata(session: DeviceSession) throws {
        try readyDatabase().clearServerMetadata(scope: session.accountID)
        revision &+= 1
    }
    private func coverageKey(kind: String?, after: String?) -> String {
        (kind ?? "all") + "|" + (after ?? "first")
    }
    func cachedServerPage(session: DeviceSession, kind: String?, after: String?) throws -> (
        Coverage, [TransferRecord]
    )? {
        let database = try readyDatabase()
        return try database.transaction { database in
            guard
                let data = try database.serverCoverage(
                    scope: session.accountID, page: coverageKey(kind: kind, after: after))
            else { return nil }
            let coverage: Coverage
            do { coverage = try JSONDecoder().decode(Coverage.self, from: data) } catch {
                throw HistorySync.Failure.invalidCache
            }
            guard coverage.identities.count <= 100,
                Set(coverage.identities).count == coverage.identities.count,
                HistorySnapshot.validCursor(coverage.next),
                coverage.generation == nil || UUID(uuidString: coverage.generation!) != nil
            else { throw HistorySync.Failure.invalidCache }
            var facts: [HistoryRecordDatabase.ServerFact] = []
            for identity in coverage.identities {
                guard let fact = try database.serverFact(scope: session.accountID, identity: identity) else {
                    // Eviction must not turn an incomplete page into an empty authoritative one.
                    return nil
                }
                facts.append(fact)
            }
            let page = try decodedServerFacts(facts)
            let prefix = "resource|" + session.serverURL + "|" + session.userID + "|"
            let local = try records(ids: page.identities.map { prefix + $0 }, session: session)
            let rows = projectedResourcePage(page, session: session, local: local).sorted {
                $0.createdAt == $1.createdAt ? $0.localID > $1.localID : $0.createdAt > $1.createdAt
            }
            return (coverage, rows)
        }
    }
    func cacheServerPage(
        _ page: ResourceList, session: DeviceSession, kind: String?, after: String?,
        expected: HistoryRecordDatabase.SyncState?, reset: Bool = false
    ) throws {
        let database = try readyDatabase()
        let state = page.generation.flatMap { generation in
            page.sync_cursor.map { HistoryRecordDatabase.SyncState(generation: generation, cursor: $0) }
        }
        guard
            page.generation == nil
                || (page.transfers.allSatisfy { $0.revision != nil }
                    && page.slots.allSatisfy { $0.revision != nil })
        else { throw HistorySync.Failure.invalidBatch }
        if let expected, let generation = page.generation, expected.generation != generation, !reset {
            throw HistorySync.Failure.resetRequired
        }
        // An older page contributes rows/coverage, never replaces the account's newer feed cursor.
        let nextState = expected == nil || reset ? state : expected
        let facts = try serverFacts(page)
        let accepted = try facts.filter { fact in
            guard !reset,
                let prior = try database.serverFact(scope: session.accountID, identity: fact.identity)
            else { return true }
            _ = try decodedServerFacts([prior])
            return prior.revision < fact.revision || (prior.revision == fact.revision && prior.body != nil)
        }
        let acceptedIDs = Set(accepted.map(\.identity))
        let safe = ResourceList(
            transfers: page.transfers.filter { acceptedIDs.contains($0.id + "|transfer") },
            slots: page.slots.filter { acceptedIDs.contains($0.id + "|slot") })
        let coverage = Coverage(
            identities: page.transfers.map { $0.id + "|transfer" } + page.slots.map { $0.id + "|slot" },
            next: page.next_cursor, generation: page.generation)
        try database.applyServerFacts(
            scope: session.accountID, expected: expected, state: nextState, facts: facts,
            page: coverageKey(kind: kind, after: after), coverage: JSONEncoder().encode(coverage),
            reset: reset
        ) {
            try self.mergeResourcePage(safe, session: session)
        }
        revision &+= 1
    }
    func applyHistoryChanges(
        _ batch: HistorySync.Batch, session: DeviceSession, expected: HistoryRecordDatabase.SyncState
    ) throws {
        try batch.validate(cursor: expected.cursor, generation: expected.generation, limit: 100)
        let database = try readyDatabase()
        for change in batch.changes {
            if let prior = try database.serverFact(scope: session.accountID, identity: change.id + "|" + change.kind) {
                _ = try decodedServerFacts([prior])
            }
        }
        let page = ResourceList(
            transfers: batch.changes.compactMap(\.transfer), slots: batch.changes.compactMap(\.slot))
        var facts = try serverFacts(page)
        for change in batch.changes where change.action == "remove" {
            let identity = change.id + "|" + change.kind
            let created =
                try database.serverFact(scope: session.accountID, identity: identity)?.created
                ?? Date().timeIntervalSince1970
            facts.append(
                .init(
                    identity: identity, kind: change.kind, revision: change.revision, created: created,
                    body: nil))
        }
        let next = HistoryRecordDatabase.SyncState(
            generation: batch.generation, cursor: batch.next_cursor)
        try database.applyServerFacts(
            scope: session.accountID, expected: expected, state: next, facts: facts
        ) {
            let accepted = try batch.changes.filter { change in
                guard let fact = try database.serverFact(scope: session.accountID, identity: change.id + "|" + change.kind) else {
                    return false
                }
                return fact.revision == change.revision && ((change.action == "remove") == (fact.body == nil))
            }
            try self.mergeResourcePage(
                ResourceList(
                    transfers: accepted.compactMap(\.transfer), slots: accepted.compactMap(\.slot)),
                session: session)
            let removed = accepted.filter { $0.action == "remove" }.map {
                "resource|" + session.serverURL + "|" + session.userID + "|" + $0.id + "|" + $0.kind
            }
            if !removed.isEmpty {
                try self.mutate(ids: removed) { records in
                    for index in records.indices { records[index].state = .revoked }
                }
            }
            try self.refreshNewestCoverage(session: session)
        }
        revision &+= 1
    }
    /// A bounded newest window. If arrivals change its membership, navigation
    /// uses the server-issued row anchor (or revalidates old-server snapshots), avoiding gaps.
    func refreshNewestCoverage(session: DeviceSession) throws {
        let database = try readyDatabase()
        try database.transaction { database in
            let state = try database.syncState(scope: session.accountID)
            for kind in [nil, "transfer", "slot"] as [String?] {
                let key = coverageKey(kind: kind, after: nil)
                guard let data = try database.serverCoverage(scope: session.accountID, page: key) else {
                    continue
                }
                let old: Coverage
                do { old = try JSONDecoder().decode(Coverage.self, from: data) } catch {
                    throw HistorySync.Failure.invalidCache
                }
                let facts = try database.newestServerFacts(scope: session.accountID, kind: kind, limit: 51)
                let shown = Array(facts.prefix(50))
                let ids = shown.map(\.identity)
                guard Set(ids) != Set(old.identities) else { continue }
                var anchor: String?
                if let last = shown.last, let body = last.body, facts.count > 50 || old.next != nil {
                    _ = try decodedServerFacts([last])
                    if last.kind == "transfer" {
                        let row: ResourceList.Transfer
                        do { row = try JSONDecoder().decode(ResourceList.Transfer.self, from: body) } catch {
                            throw HistorySync.Failure.invalidCache
                        }
                        anchor = kind == nil ? row.history_after : row.history_after_kind
                    } else {
                        let row: ResourceList.Slot
                        do { row = try JSONDecoder().decode(ResourceList.Slot.self, from: body) } catch {
                            throw HistorySync.Failure.invalidCache
                        }
                        anchor = kind == nil ? row.history_after : row.history_after_kind
                    }
                }
                let needsNext = facts.count > 50 || old.next != nil
                let next = Coverage(
                    identities: ids, next: needsNext ? anchor ?? old.next : nil,
                    generation: old.generation, navigationStale: needsNext && anchor == nil)
                try database.applyServerFacts(
                    scope: session.accountID, expected: state, state: state, facts: [], page: key,
                    coverage: JSONEncoder().encode(next))
            }
        }
    }
    private func serverFacts(_ page: ResourceList) throws -> [HistoryRecordDatabase.ServerFact] {
        try page.transfers.map {
            .init(
                identity: $0.id + "|transfer", kind: "transfer", revision: $0.revision ?? 0,
                created: ServerTimestamp.parse($0.created_at)?.timeIntervalSince1970 ?? 0,
                body: try JSONEncoder().encode($0))
        }
            + page.slots.map {
                .init(
                    identity: $0.id + "|slot", kind: "slot", revision: $0.revision ?? 0,
                    created: ServerTimestamp.parse($0.created_at)?.timeIntervalSince1970 ?? 0,
                    body: try JSONEncoder().encode($0))
            }
    }

    private func decodedServerFacts(_ facts: [HistoryRecordDatabase.ServerFact]) throws -> ResourceList {
        var transfers: [ResourceList.Transfer] = []
        var slots: [ResourceList.Slot] = []
        do {
            for fact in facts {
                guard ["transfer", "slot"].contains(fact.kind), (0...9_007_199_254_740_991).contains(fact.revision), fact.created.isFinite
                else {
                    throw HistorySync.Failure.invalidCache
                }
                guard let body = fact.body else { continue }
                if fact.kind == "transfer" {
                    let row = try JSONDecoder().decode(ResourceList.Transfer.self, from: body)
                    guard row.id + "|transfer" == fact.identity, (row.revision ?? 0) == fact.revision else {
                        throw HistorySync.Failure.invalidCache
                    }
                    transfers.append(row)
                } else {
                    let row = try JSONDecoder().decode(ResourceList.Slot.self, from: body)
                    guard row.id + "|slot" == fact.identity, (row.revision ?? 0) == fact.revision else {
                        throw HistorySync.Failure.invalidCache
                    }
                    slots.append(row)
                }
            }
        } catch { throw HistorySync.Failure.invalidCache }
        return ResourceList(transfers: transfers, slots: slots)
    }
}

import Foundation
import SQLite3

/// Shared by the app and extension. Every mutation is serialized by SQLite's
/// writer lock; an in-process recursive lock protects this connection as well.
/// Bodies are opaque metadata, capped at 16 MiB per record and per output page.
final class HistoryRecordDatabase: @unchecked Sendable {
    struct Record: Equatable, Sendable {
        let id: String
        let scope: String
        let kind: String
        let created: Double
        let body: Data
    }

    struct Cursor: Codable, Equatable, Sendable {
        let created: Double
        let id: String
        fileprivate let scopes: [String]
        fileprivate let kinds: [String]
    }

    struct Page: Sendable {
        let records: [Record]
        let next: Cursor?
    }

    struct MigrationProgress: Equatable, Sendable {
        let processed: Int64
        let inserted: Int64
        let complete: Bool
    }

    enum Failure: Error {
        case invalidRecord, invalidPage, tooLarge, unsupportedSchema, migrationSourceChanged
        case sqlite(Int32)
    }

    static let maximumRecordBytes = 16 * 1024 * 1024
    static let maximumPageBytes = maximumRecordBytes
    private let lock = NSRecursiveLock()
    private var connection: OpaquePointer?
    private var transactionDepth = 0
    private let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

    init(url: URL) throws {
        guard url.isFileURL else { throw Failure.invalidRecord }
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        var handle: OpaquePointer?
        let result = sqlite3_open_v2(url.path, &handle, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_FULLMUTEX | SQLITE_OPEN_NOFOLLOW, nil)
        guard result == SQLITE_OK, let handle else {
            if let handle { sqlite3_close_v2(handle) }
            throw Failure.sqlite(result)
        }
        connection = handle
        do {
            sqlite3_busy_timeout(handle, 2_000)
            try execute("PRAGMA journal_mode=WAL")
            try execute("PRAGMA synchronous=FULL")
            // Installed below after table creation for all supported versions.
            try transaction { database in
                let version = try database.integer("PRAGMA user_version")
                guard (0...2).contains(version) else { throw Failure.unsupportedSchema }
                if version == 0 {
                    try database.execute(
                        """
                        CREATE TABLE records (
                            id TEXT PRIMARY KEY NOT NULL,
                            scope TEXT NOT NULL,
                            kind TEXT NOT NULL,
                            sort_created REAL NOT NULL,
                            body_bytes INTEGER NOT NULL,
                            body BLOB NOT NULL
                        );
                        CREATE INDEX records_page ON records(scope,kind,sort_created,id,body_bytes);
                        CREATE TABLE tombstones(id TEXT PRIMARY KEY NOT NULL);
                        CREATE TABLE migrations (
                            id TEXT PRIMARY KEY NOT NULL,
                            source TEXT NOT NULL,
                            offset INTEGER NOT NULL,
                            processed INTEGER NOT NULL,
                            inserted INTEGER NOT NULL,
                            complete INTEGER NOT NULL
                        );
                        PRAGMA user_version=1;
                        """)
                }
                if version < 2 { try database.execute("ALTER TABLE migrations ADD COLUMN stream_state TEXT NOT NULL DEFAULT ''; PRAGMA user_version=2;") }
                try database.execute("CREATE INDEX IF NOT EXISTS records_kind_identity ON records(kind,id);")
                try database.execute(
                    "CREATE TRIGGER IF NOT EXISTS receive_source_immutable_update BEFORE UPDATE ON records WHEN OLD.kind='source' AND OLD.scope='receive-checkpoint-migration' BEGIN SELECT RAISE(ABORT,'immutable receive source'); END; CREATE TRIGGER IF NOT EXISTS receive_source_immutable_delete BEFORE DELETE ON records WHEN OLD.kind='source' AND OLD.scope='receive-checkpoint-migration' BEGIN SELECT RAISE(ABORT,'immutable receive source'); END;"
                )
            }
        } catch {
            if let connection { sqlite3_close_v2(connection) }
            connection = nil
            throw error
        }
    }

    deinit { if let connection { sqlite3_close_v2(connection) } }

    func read(_ id: String) throws -> Record? {
        try synchronized {
            guard Self.valid(id, maximum: 4096) else { throw Failure.invalidRecord }
            return try readRecord(id)
        }
    }

    func write(_ record: Record) throws {
        try Self.validate(record)
        try transaction { database in
            try database.run("DELETE FROM tombstones WHERE id=?", strings: [record.id])
            try database.insert(record, replacing: true)
        }
    }

    /// Imports a derived legacy job without replacing current work or reviving
    /// deleted work. Safe inside a migration decode callback: its savepoint is
    /// committed or rolled back with the enclosing batch and progress marker.
    @discardableResult func importIfAbsent(_ record: Record) throws -> Bool {
        try Self.validate(record)
        return try transaction { database in
            try database.insert(record, replacing: false)
            return sqlite3_changes(database.connection) > 0
        }
    }

    /// Tombstones prevent a later resumed import from resurrecting a locally
    /// deleted identity. They contain no record body, names, links or file paths.
    func remove(_ id: String) throws {
        guard Self.valid(id, maximum: 4096) else { throw Failure.invalidRecord }
        try transaction { database in
            try database.run("INSERT OR IGNORE INTO tombstones(id) VALUES(?)", strings: [id])
            try database.run("DELETE FROM records WHERE id=?", strings: [id])
        }
    }

    /// The callback may read, write and remove atomically. Nested calls use
    /// savepoints, so a caught inner failure cannot publish a half mutation.
    func transaction<T>(_ body: (HistoryRecordDatabase) throws -> T) throws -> T { try withTransaction(immediate: true, body) }

    func hasAny(scopes: [String], kinds: [String]) throws -> Bool {
        let filters = try Self.filters(scopes: scopes, kinds: kinds)
        return try synchronized {
            for scope in filters.0 {
                for kind in filters.1 {
                    if try integer("SELECT EXISTS(SELECT 1 FROM records INDEXED BY records_page WHERE scope=? AND kind=?)", strings: [scope, kind]) == 1 { return true }
                }
            }
            return false
        }
    }

    struct MigrationEntry {
        let id: String
        let kind: String
    }
    struct MigrationPage {
        let entries: [MigrationEntry]
        let nextID: String?
    }

    /// Migration discovery only: a bounded primary-key seek across all scopes.
    /// Opaque bodies are not loaded while looking for records needing upgrade.
    func migrationPage(afterID: String? = nil, limit: Int = 25) throws -> MigrationPage {
        guard (1...100).contains(limit), afterID.map({ Self.valid($0, maximum: 4096) }) ?? true else { throw Failure.invalidPage }
        return try synchronized {
            let sql = "SELECT id,kind FROM records" + (afterID == nil ? "" : " WHERE id>?") + " ORDER BY id LIMIT ?"
            let statement = try prepare(sql)
            defer { sqlite3_finalize(statement) }
            if let afterID { try bind(afterID, to: statement, at: 1) }
            try check(sqlite3_bind_int(statement, afterID == nil ? 1 : 2, Int32(limit + 1)))
            var entries: [MigrationEntry] = []
            while try step(statement) { entries.append(MigrationEntry(id: try text(statement, 0, maximum: 4096), kind: try text(statement, 1, maximum: 128))) }
            let more = entries.count > limit
            if more { entries.removeLast() }
            return MigrationPage(entries: entries, nextID: more ? entries.last?.id : nil)
        }
    }

    func receiveParentPage(afterID: String?) throws -> MigrationPage {
        try synchronized {
            let statement = try prepare(
                "SELECT id,kind FROM records INDEXED BY records_kind_identity WHERE kind='slot'" + (afterID == nil ? "" : " AND id>?") + " ORDER BY id LIMIT 2")
            defer { sqlite3_finalize(statement) }
            if let afterID { try bind(afterID, to: statement, at: 1) }
            var entries: [MigrationEntry] = []
            while try step(statement) { entries.append(MigrationEntry(id: try text(statement, 0, maximum: 4096), kind: "slot")) }
            let more = entries.count > 1
            if more { entries.removeLast() }
            return MigrationPage(entries: entries, nextID: more ? entries.last?.id : nil)
        }
    }

    /// Each exact scope/kind pair seeks at most limit+1 lightweight index rows.
    /// Bodies are loaded only for the final page, within the total byte budget.
    func page(scopes: [String], kinds: [String], after: Cursor? = nil, limit: Int = 50) throws -> Page {
        let filters = try Self.filters(scopes: scopes, kinds: kinds)
        guard (1...100).contains(limit) else { throw Failure.invalidPage }
        if let after {
            guard after.scopes.map({ Data($0.utf8) }) == filters.0.map({ Data($0.utf8) }), after.kinds.map({ Data($0.utf8) }) == filters.1.map({ Data($0.utf8) }),
                after.created.isFinite, Self.valid(after.id, maximum: 4096)
            else { throw Failure.invalidPage }
        }
        return try withTransaction(immediate: false) { database in
            struct Candidate {
                let id: String
                let sortCreated: Double
                let bytes: Int
            }
            var candidates: [Candidate] = []
            for scope in filters.0 {
                for kind in filters.1 {
                    var sql = "SELECT id,sort_created,body_bytes FROM records INDEXED BY records_page WHERE scope=? AND kind=?"
                    if after != nil { sql += " AND (sort_created,id)>(?,?)" }
                    sql += " ORDER BY sort_created,id LIMIT ?"
                    let statement = try database.prepare(sql)
                    defer { sqlite3_finalize(statement) }
                    try database.bind(scope, to: statement, at: 1)
                    try database.bind(kind, to: statement, at: 2)
                    var index: Int32 = 3
                    if let after {
                        try database.check(sqlite3_bind_double(statement, index, -after.created))
                        index += 1
                        try database.bind(after.id, to: statement, at: index)
                        index += 1
                    }
                    try database.check(sqlite3_bind_int(statement, index, Int32(limit + 1)))
                    while try database.step(statement) {
                        let id = try database.text(statement, 0, maximum: 4096)
                        let created = sqlite3_column_double(statement, 1)
                        let bytes = sqlite3_column_int64(statement, 2)
                        guard created.isFinite, bytes >= 0, bytes <= Self.maximumRecordBytes else { throw Failure.tooLarge }
                        candidates.append(Candidate(id: id, sortCreated: created, bytes: Int(bytes)))
                    }
                    candidates.sort { $0.sortCreated == $1.sortCreated ? $0.id.utf8.lexicographicallyPrecedes($1.id.utf8) : $0.sortCreated < $1.sortCreated }
                    if candidates.count > limit + 1 { candidates.removeSubrange((limit + 1)..<candidates.count) }
                }
            }
            candidates.sort { $0.sortCreated == $1.sortCreated ? $0.id.utf8.lexicographicallyPrecedes($1.id.utf8) : $0.sortCreated < $1.sortCreated }
            var result: [Record] = []
            var bytes = 0
            for candidate in candidates.prefix(limit) {
                if candidate.bytes > Self.maximumPageBytes - bytes { break }
                guard let record = try database.readRecord(candidate.id) else { throw Failure.invalidRecord }
                bytes += candidate.bytes
                result.append(record)
            }
            var next: Cursor?
            if result.count < candidates.count, let last = result.last { next = Cursor(created: last.created, id: last.id, scopes: filters.0, kinds: filters.1) }
            return Page(records: result, next: next)
        }
    }

    /// Imports at most 100 objects per atomic batch. The source file is never
    /// removed here. A failed decode, oversized record, source change or commit
    /// rolls back the batch and its progress; newer rows and tombstones win.
    func migrateJSONBatch(source: URL, key: String, limit: Int = 100, decode: (Data) throws -> Record) throws -> MigrationProgress {
        try migrateJSON(source: source, key: key, limit: limit, snapshot: false) { _, data in try decode(data) }
    }

    /// Streams either a legacy root array (named "records") or a snapshot with
    /// records/receipts arrays, in source order. Each batch shares one marker.
    func migrateJSONSnapshotBatch(source: URL, key: String, limit: Int = 100, decode: (String, Data) throws -> Record) throws -> MigrationProgress {
        try migrateJSON(source: source, key: key, limit: limit, snapshot: true, decode: decode)
    }

    private func migrateJSON(source: URL, key: String, limit: Int, snapshot: Bool, decode: (String, Data) throws -> Record) throws -> MigrationProgress {
        guard Self.valid(key, maximum: 4096), (1...100).contains(limit) else { throw Failure.invalidPage }
        return try transaction { database in
            var offset: Int64 = 0
            var processed: Int64 = 0
            var inserted: Int64 = 0
            var previousSource: String?
            var streamState = ""
            let statement = try database.prepare("SELECT source,offset,processed,inserted,complete,stream_state FROM migrations WHERE id=?")
            defer { sqlite3_finalize(statement) }
            try database.bind(key, to: statement, at: 1)
            if try database.step(statement) {
                previousSource = try database.text(statement, 0, maximum: 8192)
                offset = sqlite3_column_int64(statement, 1)
                processed = sqlite3_column_int64(statement, 2)
                inserted = sqlite3_column_int64(statement, 3)
                guard offset >= 0, processed >= 0, inserted >= 0, inserted <= processed else { throw Failure.invalidRecord }
                if sqlite3_column_int(statement, 4) == 1 { return MigrationProgress(processed: processed, inserted: inserted, complete: true) }
                guard sqlite3_column_int(statement, 4) == 0 else { throw Failure.invalidRecord }
                streamState = try database.text(statement, 5, maximum: 1024, allowEmpty: true)
            }
            let stream = try HistoryJSONStream(url: source, maximumObjectBytes: Self.maximumRecordBytes, snapshot: snapshot)
            defer { stream.close() }
            if let previousSource, previousSource != stream.identity { throw Failure.migrationSourceChanged }
            try stream.resume(at: offset, state: streamState)
            var complete = false
            for _ in 0..<limit {
                guard let data = try stream.next() else {
                    complete = true
                    break
                }
                let record = try decode(stream.elementArray, data)
                try Self.validate(record)
                try database.insert(record, replacing: false)
                guard processed < Int64.max, inserted < Int64.max else { throw Failure.tooLarge }
                processed += 1
                inserted += Int64(sqlite3_changes(database.connection))
            }
            // Peek only for punctuation/EOF, never materialize another record.
            if !complete { complete = try stream.finishIfAtEnd() }
            try stream.verifyUnchanged()
            let save = try database.prepare(
                "INSERT INTO migrations(id,source,offset,processed,inserted,complete,stream_state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET offset=excluded.offset,processed=excluded.processed,inserted=excluded.inserted,complete=excluded.complete,stream_state=excluded.stream_state"
            )
            defer { sqlite3_finalize(save) }
            try database.bind(key, to: save, at: 1)
            try database.bind(stream.identity, to: save, at: 2)
            try database.check(sqlite3_bind_int64(save, 3, stream.offset))
            try database.check(sqlite3_bind_int64(save, 4, processed))
            try database.check(sqlite3_bind_int64(save, 5, inserted))
            try database.check(sqlite3_bind_int(save, 6, complete ? 1 : 0))
            try database.bind(stream.checkpoint(), to: save, at: 7)
            _ = try database.step(save)
            return MigrationProgress(processed: processed, inserted: inserted, complete: complete)
        }
    }

    /// Account history uses entry-level checkpoints even inside an oversized
    /// inbox object. Staging, promotion and parser progress share one transaction.
    func migrateReceiveJSONBatch(
        source: URL, key: String, decode: (Data) throws -> Record, validateEntry: (ReceiveHistoryStream.Entry) throws -> Void,
        promote: (Record, ReceiveHistoryStream.Entry) throws -> Void
    ) throws -> MigrationProgress {
        guard Self.valid(key, maximum: 1024) else { throw Failure.invalidRecord }
        return try transaction { database in
            let statement = try database.prepare("SELECT source,stream_state,complete,processed,inserted,offset FROM migrations WHERE id=?")
            defer { sqlite3_finalize(statement) }
            try database.bind(key, to: statement, at: 1)
            var state = ReceiveHistoryStream.State()
            var previous: String?
            if try database.step(statement) {
                if sqlite3_column_int(statement, 2) == 1 {
                    return MigrationProgress(processed: sqlite3_column_int64(statement, 3), inserted: sqlite3_column_int64(statement, 4), complete: true)
                }
                previous = try database.text(statement, 0, maximum: 8192)
                let saved = try database.text(statement, 1, maximum: 2_097_152, allowEmpty: true)
                // An earlier completed-object checkpoint is still a valid array
                // boundary. Entry-level state is used after the first new batch.
                if saved.isEmpty {
                    state.offset = sqlite3_column_int64(statement, 5)
                    state.phase = state.offset == 0 ? "root" : "object"
                    state.processed = sqlite3_column_int64(statement, 3)
                    state.inserted = sqlite3_column_int64(statement, 4)
                } else {
                    state = try JSONDecoder().decode(ReceiveHistoryStream.State.self, from: Data(saved.utf8))
                }
            }
            let sourceStream = try HistoryJSONStream(url: source, maximumObjectBytes: Self.maximumRecordBytes)
            defer { sourceStream.close() }
            if let previous, previous != sourceStream.identity { throw Failure.migrationSourceChanged }
            let parser = ReceiveHistoryStream(state: state, read: { try sourceStream.readRange(at: $0, count: $1) })
            var budget = 32
            let beginning = parser.state.offset
            while budget > 0, parser.state.phase != "done", parser.state.offset - beginning < 262144 {
                if parser.state.phase != "ready" {
                    if let entry = try parser.next() {
                        let scope = "receive-stage|" + key + "|" + String(parser.state.objectStart)
                        try validateEntry(entry)
                        try ReceiveHistoryStream.stageEntry(database, entry: entry, scope: scope, index: parser.state.staged)
                        budget -= 1
                        continue
                    }
                    if parser.state.phase != "ready" { break }
                }
                let record = try decode(parser.metadata())
                let scope = "receive-stage|" + key + "|" + String(parser.state.objectStart)
                if parser.state.parentID == nil {
                    try Self.validate(record)
                    guard parser.state.processed < Int64.max, parser.state.inserted < Int64.max else { throw Failure.tooLarge }
                    parser.state.parentID = record.id
                    parser.state.processed += 1
                    if try database.importIfAbsent(record) { parser.state.inserted += 1 }
                    // Keep the immutable file/range identity alongside staged
                    // checkpoints; the original JSON is never rewritten.
                    try ReceiveHistoryStream.retainRange(
                        database, scope: scope, identity: sourceStream.identity, url: source, start: parser.state.objectStart, end: parser.state.objectEnd)
                    budget -= 1
                } else if parser.state.parentID != record.id {
                    throw Failure.invalidRecord
                }
                while budget > 0, parser.state.promoted < parser.state.staged {
                    let index = parser.state.promoted + 1
                    let entry = try ReceiveHistoryStream.stagedEntry(database, scope: scope, index: index)
                    if let current = try database.read(record.id) { try promote(current, entry) }
                    parser.state.promoted = index
                    budget -= 1
                }
                if parser.state.promoted == parser.state.staged { try parser.advance() }
            }
            try sourceStream.verifyUnchanged()
            let save = try database.prepare(
                "INSERT INTO migrations(id,source,offset,processed,inserted,complete,stream_state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET offset=excluded.offset,processed=excluded.processed,inserted=excluded.inserted,complete=excluded.complete,stream_state=excluded.stream_state"
            )
            defer { sqlite3_finalize(save) }
            try database.bind(key, to: save, at: 1)
            try database.bind(sourceStream.identity, to: save, at: 2)
            try database.check(sqlite3_bind_int64(save, 3, parser.state.offset))
            try database.check(sqlite3_bind_int64(save, 4, parser.state.processed))
            try database.check(sqlite3_bind_int64(save, 5, parser.state.inserted))
            try database.check(sqlite3_bind_int(save, 6, parser.state.phase == "done" ? 1 : 0))
            try database.bind(String(decoding: try JSONEncoder().encode(parser.state), as: UTF8.self), to: save, at: 7)
            _ = try database.step(save)
            return MigrationProgress(processed: parser.state.processed, inserted: parser.state.inserted, complete: parser.state.phase == "done")
        }
    }

    /// SQLite copies the retained original inside its engine. Swift never loads
    /// it as a Data object; subsequent reads use the public incremental BLOB API.
    @discardableResult func archiveReceiveBody(id: String, sourceID: String, scope: String) throws -> Bool {
        try synchronized {
            let sql = "INSERT OR IGNORE INTO records(id,scope,kind,sort_created,body_bytes,body) SELECT ?,?,'source',0,body_bytes,body FROM records WHERE id=?"
            let statement = try prepare(sql)
            defer { sqlite3_finalize(statement) }
            try bind(sourceID, to: statement, at: 1)
            try bind(scope, to: statement, at: 2)
            try bind(id, to: statement, at: 3)
            _ = try step(statement)
            return sqlite3_changes(connection) > 0
        }
    }

    func withReceiveBody<T>(id: String, _ body: (String, @escaping (Int64, Int) throws -> Data) throws -> T) throws -> T {
        try synchronized {
            let statement = try prepare("SELECT rowid,body_bytes,length(body),typeof(body) FROM records WHERE id=? AND kind='source'")
            defer { sqlite3_finalize(statement) }
            try bind(id, to: statement, at: 1)
            guard try step(statement), try text(statement, 3, maximum: 16) == "blob" else { throw Failure.invalidRecord }
            let rowID = sqlite3_column_int64(statement, 0)
            let length = sqlite3_column_int64(statement, 1)
            guard length >= 0, length <= Int64(Int32.max), length == sqlite3_column_int64(statement, 2), let connection else { throw Failure.invalidRecord }
            var blob: OpaquePointer?
            try check(sqlite3_blob_open(connection, "main", "records", "body", rowID, 0, &blob))
            guard let blob else { throw Failure.invalidRecord }
            defer { sqlite3_blob_close(blob) }
            guard sqlite3_blob_bytes(blob) == Int32(length) else { throw Failure.invalidRecord }
            return try body("sqlite|" + id + "|" + String(rowID) + "|" + String(length)) { offset, count in
                guard offset >= 0, offset <= length, (1...65536).contains(count) else { throw Failure.invalidRecord }
                let count = min(count, Int(length - offset))
                var data = Data(count: count)
                if count > 0 { try data.withUnsafeMutableBytes { try self.check(sqlite3_blob_read(blob, $0.baseAddress, Int32(count), Int32(offset))) } }
                return data
            }
        }
    }

    /// Completion belongs to the committed database, so a completed marker is
    /// still authoritative after its legacy source has been moved or removed.
    func migrationProgress(key: String) throws -> MigrationProgress? {
        guard Self.valid(key, maximum: 4096) else { throw Failure.invalidRecord }
        return try synchronized {
            let statement = try prepare("SELECT processed,inserted,complete FROM migrations WHERE id=?")
            defer { sqlite3_finalize(statement) }
            try bind(key, to: statement, at: 1)
            guard try step(statement) else { return nil }
            let processed = sqlite3_column_int64(statement, 0)
            let inserted = sqlite3_column_int64(statement, 1)
            let complete = sqlite3_column_int(statement, 2)
            guard processed >= 0, inserted >= 0, inserted <= processed, complete == 0 || complete == 1 else { throw Failure.invalidRecord }
            return MigrationProgress(processed: processed, inserted: inserted, complete: complete == 1)
        }
    }

    private static func valid(_ value: String, maximum: Int) -> Bool { !value.isEmpty && value.utf8.count <= maximum && !value.utf8.contains(0) }

    private static func validate(_ record: Record) throws {
        guard valid(record.id, maximum: 4096), valid(record.scope, maximum: 4096), valid(record.kind, maximum: 128), record.created.isFinite else { throw Failure.invalidRecord }
        guard record.body.count <= maximumRecordBytes else { throw Failure.tooLarge }
    }

    private static func filters(scopes: [String], kinds: [String]) throws -> ([String], [String]) {
        guard !scopes.isEmpty, !kinds.isEmpty, scopes.count <= 8, kinds.count <= 8, scopes.allSatisfy({ valid($0, maximum: 4096) }), kinds.allSatisfy({ valid($0, maximum: 128) })
        else { throw Failure.invalidPage }
        func normalized(_ values: [String]) -> [String] {
            var unique: [Data: String] = [:]
            for value in values { unique[Data(value.utf8)] = value }
            return unique.values.sorted { $0.utf8.lexicographicallyPrecedes($1.utf8) }
        }
        return (normalized(scopes), normalized(kinds))
    }

    private func synchronized<T>(_ body: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body()
    }

    private func withTransaction<T>(immediate: Bool, _ body: (HistoryRecordDatabase) throws -> T) throws -> T {
        try synchronized {
            let depth = transactionDepth
            let savepoint = "history_\(depth)"
            try execute(depth == 0 ? (immediate ? "BEGIN IMMEDIATE" : "BEGIN") : "SAVEPOINT \(savepoint)")
            transactionDepth += 1
            defer { transactionDepth -= 1 }
            do {
                let result = try body(self)
                try execute(depth == 0 ? "COMMIT" : "RELEASE \(savepoint)")
                return result
            } catch {
                let originalError = error
                do {
                    if depth == 0 {
                        try execute("ROLLBACK")
                    } else {
                        try execute("ROLLBACK TO \(savepoint)")
                        try execute("RELEASE \(savepoint)")
                    }
                } catch {
                    // Do not reuse a connection whose transaction outcome is
                    // uncertain. Closing rolls back any remaining transaction.
                    if let connection { sqlite3_close_v2(connection) }
                    connection = nil
                    throw error
                }
                throw originalError
            }
        }
    }

    private func readRecord(_ id: String) throws -> Record? {
        let statement = try prepare("SELECT id,scope,kind,sort_created,body_bytes,body FROM records WHERE id=?")
        defer { sqlite3_finalize(statement) }
        try bind(id, to: statement, at: 1)
        guard try step(statement) else { return nil }
        let count = sqlite3_column_int64(statement, 4)
        guard count >= 0, count <= Self.maximumRecordBytes else { throw Failure.tooLarge }
        let actual = sqlite3_column_bytes(statement, 5)
        guard Int64(actual) == count, sqlite3_column_type(statement, 5) == SQLITE_BLOB else { throw Failure.invalidRecord }
        let data: Data
        if actual == 0 {
            data = Data()
        } else {
            guard let bytes = sqlite3_column_blob(statement, 5) else { throw Failure.invalidRecord }
            data = Data(bytes: bytes, count: Int(actual))
        }
        let record = Record(
            id: try text(statement, 0, maximum: 4096), scope: try text(statement, 1, maximum: 4096), kind: try text(statement, 2, maximum: 128),
            created: -sqlite3_column_double(statement, 3), body: data)
        try Self.validate(record)
        return record
    }

    private func insert(_ record: Record, replacing: Bool) throws {
        let sql: String
        if replacing {
            sql =
                "INSERT INTO records(id,scope,kind,sort_created,body_bytes,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET scope=excluded.scope,kind=excluded.kind,sort_created=excluded.sort_created,body_bytes=excluded.body_bytes,body=excluded.body"
        } else {
            sql = "INSERT OR IGNORE INTO records(id,scope,kind,sort_created,body_bytes,body) SELECT ?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM tombstones WHERE id=?)"
        }
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        try bind(record.id, to: statement, at: 1)
        try bind(record.scope, to: statement, at: 2)
        try bind(record.kind, to: statement, at: 3)
        try check(sqlite3_bind_double(statement, 4, -record.created))
        try check(sqlite3_bind_int64(statement, 5, Int64(record.body.count)))
        if record.body.isEmpty {
            try check(sqlite3_bind_zeroblob(statement, 6, 0))
        } else {
            try record.body.withUnsafeBytes { bytes in try check(sqlite3_bind_blob(statement, 6, bytes.baseAddress, Int32(bytes.count), transient)) }
        }
        if !replacing { try bind(record.id, to: statement, at: 7) }
        _ = try step(statement)
    }

    private func prepare(_ sql: String) throws -> OpaquePointer {
        guard let connection else { throw Failure.sqlite(SQLITE_MISUSE) }
        var statement: OpaquePointer?
        try check(sqlite3_prepare_v2(connection, sql, -1, &statement, nil))
        guard let statement else { throw Failure.sqlite(SQLITE_MISUSE) }
        return statement
    }

    private func check(_ result: Int32) throws { guard result == SQLITE_OK else { throw Failure.sqlite(result) } }

    private func execute(_ sql: String) throws {
        guard let connection else { throw Failure.sqlite(SQLITE_MISUSE) }
        try check(sqlite3_exec(connection, sql, nil, nil, nil))
    }

    private func step(_ statement: OpaquePointer) throws -> Bool {
        switch sqlite3_step(statement) {
        case SQLITE_ROW: return true
        case SQLITE_DONE: return false
        case let code: throw Failure.sqlite(code)
        }
    }

    private func bind(_ value: String, to statement: OpaquePointer, at index: Int32) throws { try check(sqlite3_bind_text(statement, index, value, -1, transient)) }

    private func text(_ statement: OpaquePointer, _ index: Int32, maximum: Int, allowEmpty: Bool = false) throws -> String {
        guard sqlite3_column_type(statement, index) == SQLITE_TEXT, sqlite3_column_bytes(statement, index) <= maximum, let pointer = sqlite3_column_text(statement, index) else {
            throw Failure.invalidRecord
        }
        let data = Data(bytes: pointer, count: Int(sqlite3_column_bytes(statement, index)))
        guard let value = String(data: data, encoding: .utf8), (allowEmpty && value.isEmpty) || Self.valid(value, maximum: maximum) else { throw Failure.invalidRecord }
        return value
    }

    private func integer(_ sql: String, strings: [String] = []) throws -> Int64 {
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        for (index, string) in strings.enumerated() { try bind(string, to: statement, at: Int32(index + 1)) }
        guard try step(statement) else { throw Failure.sqlite(SQLITE_ERROR) }
        return sqlite3_column_int64(statement, 0)
    }

    private func run(_ sql: String, strings: [String]) throws {
        let statement = try prepare(sql)
        defer { sqlite3_finalize(statement) }
        for (index, string) in strings.enumerated() { try bind(string, to: statement, at: Int32(index + 1)) }
        _ = try step(statement)
    }
}

import CryptoKit
import Foundation

/// Indexed per-file and per-child local state in the history transaction domain.
/// Legacy bodies are immutable and at most 16 MiB; migration decodes that capped
/// body per batch, then imports at most 32 entries. Originals remain retained.
enum ReceiveCheckpointStorage {
    struct File: Codable {
        let transferID: String
        let blobID: String
        let path: String
        let size: Int64?
    }
    private struct Child: Codable {
        let transferID: String
        var fileCount = 0
        var complete = false
    }
    private struct Discovery: Codable {
        var afterID: String?
        var complete = false
    }
    private struct Job: Codable {
        let parentID: String
        let sourceID: String
        var afterFile: String?
        var filesComplete = false
        var afterTransfer: String?
    }
    private static let migrationScope = "receive-checkpoint-migration"
    private static let discoveryID = "receive-system|discovery-v1"
    private static func hash(_ value: String) -> String { SHA256.hash(data: Data(value.utf8)).map { String(format: "%02x", $0) }.joined() }
    private static func scope(_ parent: TransferRecord) -> String { "receive|" + hash(parent.localID) }
    private static func fileKind(_ transfer: String) -> String { "receive-file|" + hash(transfer) }
    private static func fileID(_ parent: TransferRecord, _ transfer: String, _ blob: String) -> String {
        "receive-file|" + hash(parent.localID + "\u{0}" + transfer + "\u{0}" + blob)
    }
    private static func childID(_ parent: TransferRecord, _ transfer: String) -> String { "receive-child|" + hash(parent.localID + "\u{0}" + transfer) }
    private static func validID(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 128 && value.utf8.allSatisfy { (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45 || $0 == 95 }
    }
    static func validPath(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 4096 && !value.hasPrefix("/") && !value.utf8.contains(0)
            && !value.split(separator: "/", omittingEmptySubsequences: false).contains(where: { $0 == ".." || $0 == "." || $0.isEmpty })
    }
    private static func row<T: Encodable>(_ value: T, id: String, scope: String, kind: String) throws -> HistoryRecordDatabase.Record {
        .init(id: id, scope: scope, kind: kind, created: 0, body: try JSONEncoder().encode(value))
    }
    static func load(_ db: HistoryRecordDatabase, parent: TransferRecord, transferID: String, fileExists: @escaping (String, Int64?) -> Bool) throws -> ReceiveCheckpoint {
        guard parent.isSlot == true, parent.checkpointVersion == 1, validID(transferID) else { throw AccountError.storage }
        return try db.transaction { db in
            let page = try db.page(scopes: [scope(parent)], kinds: [fileKind(transferID)], limit: 100)
            guard page.next == nil else { throw AccountError.storage }
            var paths: [String: String] = [:]
            var sizes: [String: Int64] = [:]
            for row in page.records {
                let file = try JSONDecoder().decode(File.self, from: row.body)
                guard file.transferID == transferID, validID(file.blobID), validPath(file.path), paths[file.blobID] == nil else { throw AccountError.storage }
                paths[file.blobID] = file.path
                if let size = file.size {
                    guard size >= 0 else { throw AccountError.storage }
                    sizes[file.blobID] = size
                }
            }
            var complete = false
            if let row = try db.read(childID(parent, transferID)) {
                let child = try JSONDecoder().decode(Child.self, from: row.body)
                guard child.transferID == transferID, child.fileCount == paths.count else { throw AccountError.storage }
                complete = child.complete
            } else if !paths.isEmpty {
                throw AccountError.storage
            }
            return ReceiveCheckpoint(slotID: parent.id, transferID: transferID, paths: paths, sizes: sizes, complete: complete, fileExists: fileExists)
        }
    }
    @discardableResult
    static func save(_ db: HistoryRecordDatabase, parent: TransferRecord, file: File, importing: Bool = false) throws -> Bool {
        guard parent.isSlot == true, validID(file.transferID), validID(file.blobID), validPath(file.path), file.size.map({ $0 >= 0 }) ?? true else { throw AccountError.storage }
        let id = fileID(parent, file.transferID, file.blobID)
        let childKey = childID(parent, file.transferID)
        let existing = try db.read(id)
        if importing, existing != nil { return false }
        let existingChild = try db.read(childKey)
        var child = try existingChild.map { try JSONDecoder().decode(Child.self, from: $0.body) } ?? Child(transferID: file.transferID)
        if importing, existingChild == nil {
            guard try db.importIfAbsent(row(child, id: childKey, scope: scope(parent), kind: "receive-child")) else { return false }
        }
        guard child.transferID == file.transferID, (0...100).contains(child.fileCount) else { throw AccountError.storage }
        let value = try row(file, id: id, scope: scope(parent), kind: fileKind(file.transferID))
        if existing == nil {
            guard child.fileCount < 100 else { throw AccountError.storage }
            if importing, try !db.importIfAbsent(value) { return false }
            if !importing { try db.write(value) }
            child.fileCount += 1
        } else {
            try db.write(value)
        }
        try db.write(row(child, id: childKey, scope: scope(parent), kind: "receive-child"))
        return existing == nil
    }
    static func complete(_ db: HistoryRecordDatabase, parent: TransferRecord, transferID: String, importing: Bool = false) throws {
        guard validID(transferID) else { throw AccountError.storage }
        let key = childID(parent, transferID)
        let existing = try db.read(key)
        var child = try existing.map { try JSONDecoder().decode(Child.self, from: $0.body) } ?? Child(transferID: transferID)
        guard child.transferID == transferID else { throw AccountError.storage }
        child.complete = true
        let value = try row(child, id: key, scope: scope(parent), kind: "receive-child")
        if importing, existing == nil { try db.importIfAbsent(value) } else { try db.write(value) }
    }
    static func stripped(_ record: TransferRecord) -> TransferRecord {
        var result = record
        result.savedFiles = nil
        result.savedTransfers = nil
        if result.isSlot == true { result.checkpointVersion = 1 }
        return result
    }
    /// Preserve an immutable source before reducing its live parent metadata.
    static func stage(_ db: HistoryRecordDatabase, record: TransferRecord, original: HistoryRecordDatabase.Record) throws -> TransferRecord {
        guard record.isSlot == true else { return record }
        if !(record.savedFiles ?? [:]).isEmpty || !(record.savedTransfers ?? []).isEmpty {
            let sourceID = "receive-source|" + hash(record.localID)
            let inserted = try db.importIfAbsent(.init(id: sourceID, scope: migrationScope, kind: "source", created: 0, body: original.body))
            if inserted {
                let job = Job(parentID: record.localID, sourceID: sourceID)
                try db.write(row(job, id: "receive-job|" + hash(record.localID), scope: migrationScope, kind: "job"))
            }
        }
        return stripped(record)
    }
    /// Returns true only after all existing parent records and their jobs are
    /// normalized. The wrapper blocks reads/writes until this is committed.
    static func migrateBatch(
        _ db: HistoryRecordDatabase, decode: (Data) throws -> TransferRecord,
        encode: (TransferRecord) throws -> HistoryRecordDatabase.Record
    ) throws -> Bool {
        try db.transaction { db in
            var discovery = try db.read(discoveryID).map { try JSONDecoder().decode(Discovery.self, from: $0.body) } ?? Discovery()
            if !discovery.complete {
                let page = try db.migrationPage(afterID: discovery.afterID, limit: 1)
                for entry in page.entries where entry.kind == "slot" {
                    guard let original = try db.read(entry.id) else { continue }
                    let parent = try decode(original.body)
                    if parent.checkpointVersion != 1 || parent.savedFiles != nil || parent.savedTransfers != nil {
                        try db.write(encode(stage(db, record: parent, original: original)))
                    }
                }
                discovery.afterID = page.nextID
                discovery.complete = page.nextID == nil
                try db.write(row(discovery, id: discoveryID, scope: migrationScope, kind: "discovery"))
                if try !discovery.complete || db.hasAny(scopes: [migrationScope], kinds: ["job"]) { return false }
            }
            guard let queued = try db.page(scopes: [migrationScope], kinds: ["job"], limit: 1).records.first else { return true }
            var job = try JSONDecoder().decode(Job.self, from: queued.body)
            guard let source = try db.read(job.sourceID) else { throw AccountError.storage }
            let legacy = try decode(source.body)
            guard legacy.localID == job.parentID else { throw AccountError.storage }
            guard let current = try db.read(job.parentID) else {
                try db.remove(queued.id)
                return false
            }
            let parent = try decode(current.body)
            var budget = 32
            if !job.filesComplete {
                let keys = (legacy.savedFiles ?? [:]).keys.sorted { $0.utf8.lexicographicallyPrecedes($1.utf8) }
                for key in keys where job.afterFile.map({ $0.utf8.lexicographicallyPrecedes(key.utf8) }) ?? true {
                    if budget == 0 { break }
                    let parts = key.split(separator: "/", omittingEmptySubsequences: false)
                    guard parts.count == 2, let path = legacy.savedFiles?[key] else { throw AccountError.storage }
                    try save(db, parent: parent, file: File(transferID: String(parts[0]), blobID: String(parts[1]), path: path, size: nil), importing: true)
                    job.afterFile = key
                    budget -= 1
                }
                job.filesComplete = keys.last == job.afterFile || keys.isEmpty
            }
            var transfersComplete = false
            if job.filesComplete {
                let transfers = Set(legacy.savedTransfers ?? []).sorted { $0.utf8.lexicographicallyPrecedes($1.utf8) }
                for transfer in transfers where job.afterTransfer.map({ $0.utf8.lexicographicallyPrecedes(transfer.utf8) }) ?? true {
                    if budget == 0 { break }
                    try complete(db, parent: parent, transferID: transfer, importing: true)
                    job.afterTransfer = transfer
                    budget -= 1
                }
                transfersComplete = transfers.last == job.afterTransfer || transfers.isEmpty
            }
            if job.filesComplete && transfersComplete { try db.remove(queued.id) } else { try db.write(row(job, id: queued.id, scope: migrationScope, kind: "job")) }
            return false
        }
    }
}

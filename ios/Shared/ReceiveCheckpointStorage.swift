import CryptoKit
import Foundation

/// Indexed per-file and per-child local state in the history transaction domain.
/// Immutable originals are read in 16 KiB slices. Parser progress and at most
/// 32 staged/promoted checkpoints commit together; source bodies remain retained.
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
        var imported: Bool? = nil
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
        var stream: ReceiveHistoryStream.State?
        var identity: String?
        var compatibilityScanned: Int64?
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
    @discardableResult static func save(_ db: HistoryRecordDatabase, parent: TransferRecord, file: File, importing: Bool = false) throws -> Bool {
        guard parent.isSlot == true, validID(file.transferID), validID(file.blobID), validPath(file.path), file.size.map({ $0 >= 0 }) ?? true else { throw AccountError.storage }
        let id = fileID(parent, file.transferID, file.blobID)
        let childKey = childID(parent, file.transferID)
        let existing = try db.read(id)
        if importing, existing != nil { return false }
        let existingChild = try db.read(childKey)
        var child = try existingChild.map { try JSONDecoder().decode(Child.self, from: $0.body) } ?? Child(transferID: file.transferID, imported: importing ? true : nil)
        if importing, existingChild == nil { guard try db.importIfAbsent(row(child, id: childKey, scope: scope(parent), kind: "receive-child")) else { return false } }
        if !importing { child.imported = nil }
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
        if importing, existing != nil, child.imported != true { return }
        if !importing { child.imported = nil }
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
    private static func recognizeOldImportedChild(_ db: HistoryRecordDatabase, parent: TransferRecord, entry: ReceiveHistoryStream.Entry, afterFile: String, stagingScope: String)
        throws
    {
        guard let key = entry.key, !afterFile.utf8.lexicographicallyPrecedes(key.utf8) else { return }
        let parts = key.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 2 else { throw ReceiveHistoryStream.Failure.invalidCheckpoint }
        let transfer = String(parts[0])
        guard let saved = try db.read(fileID(parent, transfer, String(parts[1]))) else { return }
        let file = try JSONDecoder().decode(File.self, from: saved.body)
        // Old imports had no length. A newer save/path edit is never adopted.
        guard file.transferID == transfer, file.blobID == String(parts[1]), file.size == nil, file.path == entry.value, let row = try db.read(childID(parent, transfer)) else {
            return
        }
        var child = try JSONDecoder().decode(Child.self, from: row.body)
        guard child.transferID == transfer else { throw AccountError.storage }
        let files = try db.page(scopes: [scope(parent)], kinds: [fileKind(transfer)], limit: 100)
        guard files.next == nil else { throw AccountError.storage }
        for saved in files.records {
            let value = try JSONDecoder().decode(File.self, from: saved.body)
            let sourceKey = transfer + "/" + value.blobID
            guard value.size == nil, !afterFile.utf8.lexicographicallyPrecedes(sourceKey.utf8), let staged = try db.read(stagingScope + "|key|" + sourceKey),
                try JSONDecoder().decode(ReceiveHistoryStream.Entry.self, from: staged.body).value == value.path
            else { return }
        }
        if child.imported == nil {
            child.imported = true
            try db.write(Self.row(child, id: row.id, scope: row.scope, kind: row.kind))
        }
    }
    static func validateEntry(_ entry: ReceiveHistoryStream.Entry) throws {
        if let key = entry.key {
            let parts = key.split(separator: "/", omittingEmptySubsequences: false)
            guard parts.count == 2, validID(String(parts[0])), validID(String(parts[1])), validPath(entry.value) else { throw ReceiveHistoryStream.Failure.invalidCheckpoint }
        } else {
            guard validID(entry.value) else { throw ReceiveHistoryStream.Failure.invalidCheckpoint }
        }
    }
    static func promote(_ db: HistoryRecordDatabase, parent: TransferRecord, entry: ReceiveHistoryStream.Entry) throws {
        try validateEntry(entry)
        guard parent.isSlot == true else { throw ReceiveHistoryStream.Failure.invalidCheckpoint }
        if let key = entry.key {
            let parts = key.split(separator: "/", omittingEmptySubsequences: false)
            try save(db, parent: parent, file: File(transferID: String(parts[0]), blobID: String(parts[1]), path: entry.value, size: nil), importing: true)
        } else {
            try complete(db, parent: parent, transferID: entry.value, importing: true)
        }
    }
    /// Discovery copies a single original in SQLite, without materializing its
    /// body in Swift. Staging/promotion then resumes inside that object's BLOB.
    static func migrateBatch(_ db: HistoryRecordDatabase, decode: (Data) throws -> TransferRecord, encode: (TransferRecord) throws -> HistoryRecordDatabase.Record) throws -> Bool {
        try db.transaction { db in
            var discovery = try db.read(discoveryID).map { try JSONDecoder().decode(Discovery.self, from: $0.body) } ?? Discovery()
            // Process queued jobs before discovering another parent. A discovery
            // checkpoint and immutable source/job are committed atomically.
            if let queued = try db.page(scopes: [migrationScope], kinds: ["job"], limit: 1).records.first {
                var job = try JSONDecoder().decode(Job.self, from: queued.body)
                let scope = "receive-stage|" + hash(job.sourceID)
                try db.withReceiveBody(id: job.sourceID) { identity, read in
                    if let original = job.identity, original != identity { throw ReceiveHistoryStream.Failure.sourceChanged }
                    job.identity = identity
                    let parser = ReceiveHistoryStream(state: job.stream ?? .init(), objectOnly: true, read: read)
                    var budget = 32
                    let beginning = parser.state.offset
                    while budget > 0, parser.state.phase != "done", parser.state.offset - beginning < 262144 {
                        if parser.state.phase != "ready" {
                            if let entry = try parser.next() {
                                try validateEntry(entry)
                                try ReceiveHistoryStream.stageEntry(db, entry: entry, scope: scope, index: parser.state.staged)
                                budget -= 1
                                continue
                            }
                            if parser.state.phase != "ready" { break }
                        }
                        let original = try decode(parser.metadata())
                        guard original.localID == job.parentID, original.isSlot == true else { throw ReceiveHistoryStream.Failure.invalidCheckpoint }
                        if parser.state.parentID == nil {
                            parser.state.parentID = original.localID
                            // Read ordinary bounded current metadata when present;
                            // an oversized legacy parent is reduced only after its
                            // complete scalar metadata has validated.
                            do {
                                if let current = try db.read(job.parentID) {
                                    let value = try decode(current.body)
                                    if value.checkpointVersion != 1 || value.savedFiles != nil || value.savedTransfers != nil { try db.write(encode(stripped(value))) }
                                }
                            } catch HistoryRecordDatabase.Failure.tooLarge { try db.write(encode(stripped(original))) }
                            budget -= 1
                        }
                        // Older builds persisted sorted map progress and created
                        // unsized child rows before reaching completion arrays. A
                        // bounded compatibility pass recognizes only those exact
                        // source-matching rows, before source-order promotion.
                        if let afterFile = job.afterFile {
                            var scanned = job.compatibilityScanned ?? 0
                            while budget > 0, scanned < parser.state.staged {
                                let index = scanned + 1
                                let entry = try ReceiveHistoryStream.stagedEntry(db, scope: scope, index: index)
                                if let current = try db.read(job.parentID) {
                                    try recognizeOldImportedChild(db, parent: decode(current.body), entry: entry, afterFile: afterFile, stagingScope: scope)
                                }
                                scanned = index
                                budget -= 1
                            }
                            job.compatibilityScanned = scanned
                            if scanned < parser.state.staged { break }
                        }
                        while budget > 0, parser.state.promoted < parser.state.staged {
                            let index = parser.state.promoted + 1
                            let entry = try ReceiveHistoryStream.stagedEntry(db, scope: scope, index: index)
                            if let current = try db.read(job.parentID) { try promote(db, parent: decode(current.body), entry: entry) }
                            parser.state.promoted = index
                            budget -= 1
                        }
                        if parser.state.promoted == parser.state.staged { try parser.advance() }
                    }
                    job.stream = parser.state
                }
                if job.stream?.phase == "done" { try db.remove(queued.id) } else { try db.write(row(job, id: queued.id, scope: migrationScope, kind: "job")) }
                return false
            }
            if discovery.complete { return true }
            let page = try db.receiveParentPage(afterID: discovery.afterID)
            for entry in page.entries where entry.kind == "slot" {
                let sourceID = "receive-source|" + hash(entry.id)
                if try db.archiveReceiveBody(id: entry.id, sourceID: sourceID, scope: migrationScope) {
                    try db.write(row(Job(parentID: entry.id, sourceID: sourceID), id: "receive-job|" + hash(entry.id), scope: migrationScope, kind: "job"))
                }
            }
            discovery.afterID = page.nextID
            discovery.complete = page.nextID == nil
            try db.write(row(discovery, id: discoveryID, scope: migrationScope, kind: "discovery"))
            return try discovery.complete && !db.hasAny(scopes: [migrationScope], kinds: ["job"])
        }
    }
}

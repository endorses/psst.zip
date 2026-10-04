import Foundation

/// Small indexed metadata jobs. Secrets belong in Keychain, never in these bodies.
final class DeviceRetryQueue {
    struct Job: Codable, Equatable {
        let origin: String
        let transferID: String
        var ownerID: String?
        var keyReference: String?

        var scope: String { ownerID.map { DeviceRetryQueue.identity([origin, $0]) } ?? "anonymous" }
        var id: String { DeviceRetryQueue.identity([origin, transferID, ownerID ?? ""]) }
    }
    struct LegacySource {
        let data: Data
        let fingerprint: String
    }
    private struct Progress: Codable {
        let fingerprint: String
        let offset: Int
        let count: Int
        let complete: Bool
    }
    enum Failure: Error { case invalid, sourceChanged, oversized }
    private let database: HistoryRecordDatabase
    private let kind: String
    private let marker = "migration"
    static let maximumJobBytes = 16_384

    init(url: URL, kind: String) throws {
        self.database = try HistoryRecordDatabase(url: url)
        self.kind = kind
    }
    static func identity(_ parts: [String]) -> String { parts.map { "\($0.utf8.count):\($0)" }.joined() }
    static func validate(_ job: Job) throws {
        guard !job.origin.isEmpty, job.origin.utf8.count <= 2048, !job.transferID.isEmpty, job.transferID.utf8.count <= 256,
            job.ownerID == nil || (!job.ownerID!.isEmpty && job.ownerID!.utf8.count <= 256),
            job.keyReference == nil || (!job.keyReference!.isEmpty && job.keyReference!.utf8.count <= 4096),
            let url = URLComponents(string: job.origin), ["https", "http"].contains(url.scheme), url.host?.isEmpty == false,
            url.user == nil, url.password == nil, url.query == nil, url.fragment == nil,
            !job.id.utf8.contains(0)
        else { throw Failure.invalid }
    }
    private func row(_ job: Job, order: Double) throws -> HistoryRecordDatabase.Record {
        try Self.validate(job)
        let body = try JSONEncoder().encode(job)
        guard body.count <= Self.maximumJobBytes else { throw Failure.oversized }
        return .init(id: job.id, scope: job.scope, kind: kind, created: order, body: body)
    }
    func find(_ id: String) throws -> Job? {
        guard let value = try database.read(id), value.kind == kind else { return nil }
        guard value.body.count <= Self.maximumJobBytes else { throw Failure.oversized }
        let job = try JSONDecoder().decode(Job.self, from: value.body)
        try Self.validate(job)
        guard job.id == value.id, job.scope == value.scope else { throw Failure.invalid }
        return job
    }
    func enqueue(_ job: Job, now: Double = Date().timeIntervalSince1970) throws {
        try database.transaction { db in
            if let existing = try db.read(job.id), existing.kind == kind {
                guard try find(job.id) == job else { throw Failure.invalid }
                return
            }
            try db.write(row(job, order: -now))
        }
    }
    /// Keep a tiny terminal marker so interrupted legacy import cannot restore a removed job.
    func remove(_ job: Job) throws {
        try Self.validate(job)
        try database.write(.init(id: job.id, scope: "retired", kind: "retired", created: 0, body: Data()))
    }
    struct Batch {
        let jobs: [Job]
        let hadInvalidJobs: Bool
    }
    func batch(scopes: [String], limit: Int = 4) throws -> Batch {
        guard (1...5).contains(limit) else { throw Failure.invalid }
        let rows = try database.page(scopes: scopes, kinds: [kind], limit: limit).records
        var jobs: [Job] = []
        var invalid = false
        for raw in rows {
            do {
                guard let job = try find(raw.id) else { continue }
                jobs.append(job)
            } catch {
                // Preserve malformed metadata and move it behind healthy work.
                // Never turn a bad local record into credentials or a request.
                try database.transaction { db in
                    guard let current = try db.read(raw.id), current == raw else { return }
                    try db.write(
                        .init(
                            id: raw.id, scope: raw.scope, kind: raw.kind,
                            created: min(-Date().timeIntervalSince1970, raw.created - 0.000_001), body: raw.body))
                }
                invalid = true
            }
        }
        return Batch(jobs: jobs, hadInvalidJobs: invalid)
    }
    func rotate(_ job: Job, now: Double = Date().timeIntervalSince1970) throws {
        try database.transaction { db in
            guard let existing = try db.read(job.id), existing.kind == kind else { return }
            try db.write(row(job, order: min(-now, existing.created - 0.000_001)))
        }
    }
    var migrationComplete: Bool {
        get throws {
            guard let row = try database.read(marker) else { return false }
            return try JSONDecoder().decode(Progress.self, from: row.body).complete
        }
    }
    /// The legacy API allocates one Data value. Parsing and committed work remain
    /// incremental; callers release that value after the completion marker commits.
    func migrate(
        source: () throws -> LegacySource?, limit: Int = 25,
        decode: (Data) throws -> Job, prepare: (Data, Job) throws -> Void = { _, _ in }
    ) throws -> Bool {
        guard (1...100).contains(limit) else { throw Failure.invalid }
        return try database.transaction { db in
            let previous = try db.read(marker).map { try JSONDecoder().decode(Progress.self, from: $0.body) }
            if previous?.complete == true { return true }
            let loaded = try source()
            if previous != nil && loaded == nil { throw Failure.sourceChanged }
            let bytes = loaded?.data ?? Data("[]".utf8)
            let fingerprint = loaded?.fingerprint ?? "absent"
            guard fingerprint.utf8.count <= 128, previous == nil || previous?.fingerprint == fingerprint else { throw Failure.sourceChanged }
            var parser = RetryLegacyArray(data: bytes, offset: previous?.offset ?? 0)
            var count = previous?.count ?? 0
            for _ in 0..<limit {
                guard let body = try parser.next() else { break }
                let job = try decode(body)
                try Self.validate(job)
                if try db.read(job.id) == nil {
                    try prepare(body, job)
                    // Earlier legacy work precedes new jobs, preserving FIFO retries.
                    try db.write(row(job, order: -Double(count)))
                }
                guard count < Int.max else { throw Failure.oversized }
                count += 1
            }
            let complete = try parser.atEnd()
            let progress = Progress(fingerprint: fingerprint, offset: parser.offset, count: count, complete: complete)
            try db.write(.init(id: marker, scope: "migration", kind: "migration", created: 0, body: JSONEncoder().encode(progress)))
            return complete
        }
    }
}

/// Root arrays only, one bounded object at a time; offsets always mark complete records.
private struct RetryLegacyArray {
    let data: Data
    var offset: Int
    mutating func whitespace() throws {
        var count = 0
        while offset < data.count, [9, 10, 13, 32].contains(data[offset]) {
            offset += 1
            count += 1
            guard count <= 1_048_576 else { throw DeviceRetryQueue.Failure.oversized }
        }
    }
    mutating func start() throws {
        guard offset >= 0, offset <= data.count else { throw DeviceRetryQueue.Failure.invalid }
        if offset == 0 {
            try whitespace()
            guard offset < data.count, data[offset] == 91 else { throw DeviceRetryQueue.Failure.invalid }
            offset += 1
        }
        try whitespace()
    }
    mutating func atEnd() throws -> Bool {
        try start()
        guard offset < data.count else { throw DeviceRetryQueue.Failure.invalid }
        if data[offset] != 93 { return false }
        var tail = offset + 1
        while tail < data.count, [9, 10, 13, 32].contains(data[tail]) {
            tail += 1
            guard tail - offset <= 1_048_576 else { throw DeviceRetryQueue.Failure.oversized }
        }
        guard tail == data.count else { throw DeviceRetryQueue.Failure.invalid }
        return true
    }
    mutating func next() throws -> Data? {
        if try atEnd() { return nil }
        guard data[offset] == 123 else { throw DeviceRetryQueue.Failure.invalid }
        let begin = offset
        var stack: [UInt8] = []
        var string = false
        var escaped = false
        repeat {
            guard offset < data.count, offset - begin < DeviceRetryQueue.maximumJobBytes else { throw DeviceRetryQueue.Failure.oversized }
            let byte = data[offset]
            offset += 1
            if string {
                if escaped { escaped = false } else if byte == 92 { escaped = true } else if byte == 34 { string = false }
            } else {
                switch byte {
                case 34: string = true
                case 123, 91:
                    guard stack.count < 16 else { throw DeviceRetryQueue.Failure.oversized }
                    stack.append(byte == 123 ? 125 : 93)
                case 125, 93:
                    guard stack.popLast() == byte else { throw DeviceRetryQueue.Failure.invalid }
                default: break
                }
            }
        } while !stack.isEmpty
        let result = data.subdata(in: begin..<offset)
        try whitespace()
        guard offset < data.count else { throw DeviceRetryQueue.Failure.invalid }
        if data[offset] == 44 {
            offset += 1
            try whitespace()
            guard offset < data.count, data[offset] == 123 else { throw DeviceRetryQueue.Failure.invalid }
        } else if data[offset] != 93 {
            throw DeviceRetryQueue.Failure.invalid
        }
        return result
    }
}

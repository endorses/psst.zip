import Foundation

/// Versioned, bounded metadata only. Private keys, manifests and files never enter this feed.
enum HistorySync {
    enum Failure: Error { case resetRequired, invalidBatch, invalidCache, retryAfter(TimeInterval) }
    static let maximumBytes = 1_048_576
    struct Change: Decodable, Sendable {
        let kind: String
        let id: String
        let revision: Int64
        let action: String
        let transfer: ResourceList.Transfer?
        let slot: ResourceList.Slot?
        enum CodingKeys: String, CodingKey { case kind, id, revision, action, resource }
        init(from decoder: Decoder) throws {
            let fields = try decoder.container(keyedBy: CodingKeys.self)
            kind = try fields.decode(String.self, forKey: .kind)
            id = try fields.decode(String.self, forKey: .id)
            revision = try fields.decode(Int64.self, forKey: .revision)
            action = try fields.decode(String.self, forKey: .action)
            guard ["transfer", "slot"].contains(kind), UUID(uuidString: id) != nil,
                (0...9_007_199_254_740_991).contains(revision), ["upsert", "remove"].contains(action)
            else { throw Failure.invalidBatch }
            if action == "upsert" {
                transfer =
                    kind == "transfer"
                    ? try fields.decode(ResourceList.Transfer.self, forKey: .resource) : nil
                slot = kind == "slot" ? try fields.decode(ResourceList.Slot.self, forKey: .resource) : nil
                guard (transfer?.id ?? slot?.id) == id, (transfer?.revision ?? slot?.revision) == revision
                else { throw Failure.invalidBatch }
            } else {
                guard try !fields.contains(.resource) || fields.decodeNil(forKey: .resource) else {
                    throw Failure.invalidBatch
                }
                transfer = nil
                slot = nil
            }
        }
    }
    struct Batch: Decodable, Sendable {
        let version: Int
        let generation: String
        let changes: [Change]
        let next_cursor: String
        let has_more: Bool
        func validate(cursor: String, generation: String, limit: Int) throws {
            guard version == 1, self.generation == generation, UUID(uuidString: generation) != nil,
                changes.count <= limit, HistorySnapshot.validCursor(next_cursor),
                !has_more || next_cursor != cursor,
                changes.isEmpty || next_cursor != cursor,
                Set(changes.map { $0.kind + "|" + $0.id }).count == changes.count
            else { throw Failure.invalidBatch }
        }
    }
    static func load(
        cursor: String, generation: String, timeoutNanoseconds: UInt64 = 10_000_000_000,
        fetch: @escaping @Sendable (String) async throws -> Data
    ) async throws -> Batch {
        guard HistorySnapshot.validCursor(cursor), UUID(uuidString: generation) != nil,
            (1...10_000_000_000).contains(timeoutNanoseconds)
        else {
            throw Failure.invalidBatch
        }
        return try await withThrowingTaskGroup(of: Batch.self) { group in
            group.addTask {
                let data = try await fetch("auth/history/changes?cursor=" + cursor + "&limit=50")
                try Task.checkCancellation()
                guard data.count <= maximumBytes else { throw HistorySnapshot.Failure.tooLarge }
                let batch = try JSONDecoder().decode(Batch.self, from: data)
                try batch.validate(cursor: cursor, generation: generation, limit: 50)
                return batch
            }
            group.addTask {
                try await Task.sleep(nanoseconds: timeoutNanoseconds)
                throw HistorySnapshot.Failure.timedOut
            }
            defer { group.cancelAll() }
            guard let result = try await group.next() else { throw Failure.invalidBatch }
            return result
        }
    }
    /// Retry-After is a protocol value, independent of language and device locale.
    static func retryDelay(_ raw: String, now: Date = Date()) -> TimeInterval? {
        if let seconds = Double(raw), seconds.isFinite { return seconds > 0 ? seconds : nil }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        guard let date = formatter.date(from: raw) else { return nil }
        let seconds = date.timeIntervalSince(now)
        return seconds > 0 ? seconds : nil
    }
    static func supports(_ data: Data) throws -> Bool {
        struct Config: Decodable { let history_sync_version: Int? }
        guard data.count <= 65_536 else { throw HistorySnapshot.Failure.tooLarge }
        return try JSONDecoder().decode(Config.self, from: data).history_sync_version == 1
    }
}

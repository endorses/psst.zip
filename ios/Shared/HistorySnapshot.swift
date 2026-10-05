import Foundation

struct ResourceList: Codable, Sendable {
    struct Summary: Codable, Sendable {
        let state: String
        let completed_files: Int64?
        let file_count: Int64?
        let total_size: Int64?
        enum CodingKeys: String, CodingKey { case state, completed_files, file_count, total_size }
        func encode(to encoder: Encoder) throws {
            var fields = encoder.container(keyedBy: CodingKeys.self)
            try fields.encode(state, forKey: .state)
            try fields.encode(completed_files, forKey: .completed_files)
            try fields.encode(file_count, forKey: .file_count)
            try fields.encode(total_size, forKey: .total_size)
        }
        init(from decoder: Decoder) throws {
            let fields = try decoder.container(keyedBy: CodingKeys.self)
            state = try fields.decode(String.self, forKey: .state)
            completed_files = try fields.decode(Int64?.self, forKey: .completed_files)
            file_count = try fields.decode(Int64?.self, forKey: .file_count)
            total_size = try fields.decode(Int64?.self, forKey: .total_size)
            switch state {
            case "ready":
                guard let completed_files, let file_count, let total_size,
                    completed_files >= 0, file_count >= completed_files, total_size >= 0
                else { throw HistorySnapshot.Failure.invalidPage }
            case "updating":
                guard completed_files == nil, file_count == nil, total_size == nil else {
                    throw HistorySnapshot.Failure.invalidPage
                }
            default: throw HistorySnapshot.Failure.invalidPage
            }
        }
    }

    struct Transfer: Codable, Sendable {
        let id: String
        let status: String
        let title: String?
        let file_count: Int64?
        let total_size: Int64?
        let expires_at: String?
        let created_at: String?
        let downloaded_at: String?
        let download_count: Int?
        let max_downloads: Int?
        let revision: Int64?
        let history_after: String?
        let history_after_kind: String?
        let summary: Summary
        enum CodingKeys: String, CodingKey {
            case id, status, title, file_count, total_size, expires_at, created_at, downloaded_at,
                download_count, max_downloads, revision, history_after, history_after_kind, summary
        }
        func encode(to encoder: Encoder) throws {
            var fields = encoder.container(keyedBy: CodingKeys.self)
            try fields.encode(id, forKey: .id)
            try fields.encode(status, forKey: .status)
            try fields.encode(title, forKey: .title)
            try fields.encode(file_count, forKey: .file_count)
            try fields.encode(total_size, forKey: .total_size)
            try fields.encode(expires_at, forKey: .expires_at)
            try fields.encode(created_at, forKey: .created_at)
            try fields.encode(downloaded_at, forKey: .downloaded_at)
            try fields.encode(download_count, forKey: .download_count)
            try fields.encode(max_downloads, forKey: .max_downloads)
            try fields.encode(revision, forKey: .revision)
            try fields.encode(history_after, forKey: .history_after)
            try fields.encode(history_after_kind, forKey: .history_after_kind)
            try fields.encode(summary, forKey: .summary)
        }
        init(from decoder: Decoder) throws {
            let f = try decoder.container(keyedBy: CodingKeys.self)
            id = try f.decode(String.self, forKey: .id)
            status = try f.decode(String.self, forKey: .status)
            title = try SharedLinkTitle.normalize(f.decodeIfPresent(String.self, forKey: .title))
            file_count = try f.decode(Int64?.self, forKey: .file_count)
            total_size = try f.decode(Int64?.self, forKey: .total_size)
            expires_at = try f.decodeIfPresent(String.self, forKey: .expires_at)
            created_at = try f.decodeIfPresent(String.self, forKey: .created_at)
            downloaded_at = try f.decodeIfPresent(String.self, forKey: .downloaded_at)
            download_count = try f.decodeIfPresent(Int.self, forKey: .download_count)
            max_downloads = try f.decodeIfPresent(Int.self, forKey: .max_downloads)
            history_after = try f.decodeIfPresent(String.self, forKey: .history_after)
            history_after_kind = try f.decodeIfPresent(String.self, forKey: .history_after_kind)
            guard HistorySnapshot.validCursor(history_after),
                HistorySnapshot.validCursor(history_after_kind)
            else { throw HistorySnapshot.Failure.invalidPage }
            revision = try f.decodeIfPresent(Int64.self, forKey: .revision)
            guard revision == nil || (0...9_007_199_254_740_991).contains(revision!) else {
                throw HistorySnapshot.Failure.invalidPage
            }
            summary = try f.decode(Summary.self, forKey: .summary)
            guard UUID(uuidString: id) != nil,
                ["pending", "complete", "expired", "revoked", "exhausted"].contains(status),
                file_count == summary.file_count, total_size == summary.total_size,
                (download_count ?? 0) >= 0, (0...2_147_483_647).contains(max_downloads ?? 0),
                [expires_at, created_at, downloaded_at].allSatisfy({ $0 == nil || $0!.utf8.count <= 64 })
            else { throw HistorySnapshot.Failure.invalidPage }
        }
    }

    struct Slot: Codable, Sendable {
        let id: String
        let status: String
        let title: String?
        let expires_at: String?
        let created_at: String?
        let file_count: Int64?
        let completed_files: Int64?
        let total_size: Int64?
        let receive_protocol: Int?
        let max_files: Int?
        let reserved_files: Int64?
        let revision: Int64?
        let history_after: String?
        let history_after_kind: String?
        let summary: Summary
        enum CodingKeys: String, CodingKey {
            case id, status, title, expires_at, created_at, file_count, completed_files, total_size,
                receive_protocol, max_files, reserved_files, revision, history_after, history_after_kind,
                summary
        }

        func encode(to encoder: Encoder) throws {
            var fields = encoder.container(keyedBy: CodingKeys.self)
            try fields.encode(id, forKey: .id)
            try fields.encode(status, forKey: .status)
            try fields.encode(title, forKey: .title)
            try fields.encode(expires_at, forKey: .expires_at)
            try fields.encode(created_at, forKey: .created_at)
            try fields.encode(file_count, forKey: .file_count)
            try fields.encode(completed_files, forKey: .completed_files)
            try fields.encode(total_size, forKey: .total_size)
            try fields.encode(receive_protocol, forKey: .receive_protocol)
            try fields.encode(max_files, forKey: .max_files)
            try fields.encode(reserved_files, forKey: .reserved_files)
            try fields.encode(revision, forKey: .revision)
            try fields.encode(history_after, forKey: .history_after)
            try fields.encode(history_after_kind, forKey: .history_after_kind)
            try fields.encode(summary, forKey: .summary)
        }
        init(from decoder: Decoder) throws {
            let f = try decoder.container(keyedBy: CodingKeys.self)
            id = try f.decode(String.self, forKey: .id)
            status = try f.decode(String.self, forKey: .status)
            title = try SharedLinkTitle.normalize(f.decodeIfPresent(String.self, forKey: .title))
            expires_at = try f.decodeIfPresent(String.self, forKey: .expires_at)
            created_at = try f.decodeIfPresent(String.self, forKey: .created_at)
            file_count = try f.decode(Int64?.self, forKey: .file_count)
            completed_files = try f.decode(Int64?.self, forKey: .completed_files)
            total_size = try f.decode(Int64?.self, forKey: .total_size)
            receive_protocol = try f.decodeIfPresent(Int.self, forKey: .receive_protocol)
            max_files = try f.decodeIfPresent(Int.self, forKey: .max_files)
            reserved_files = try f.decodeIfPresent(Int64.self, forKey: .reserved_files)
            history_after = try f.decodeIfPresent(String.self, forKey: .history_after)
            history_after_kind = try f.decodeIfPresent(String.self, forKey: .history_after_kind)
            guard HistorySnapshot.validCursor(history_after),
                HistorySnapshot.validCursor(history_after_kind)
            else { throw HistorySnapshot.Failure.invalidPage }
            revision = try f.decodeIfPresent(Int64.self, forKey: .revision)
            guard revision == nil || (0...9_007_199_254_740_991).contains(revision!) else {
                throw HistorySnapshot.Failure.invalidPage
            }
            summary = try f.decode(Summary.self, forKey: .summary)
            guard UUID(uuidString: id) != nil,
                ["waiting", "has_uploads", "expired", "revoked"].contains(status),
                file_count == summary.file_count, completed_files == summary.completed_files,
                total_size == summary.total_size,
                (0...2_147_483_647).contains(max_files ?? 0), (reserved_files ?? 0) >= 0,
                max_files == nil || max_files == 0 || reserved_files == nil
                    || reserved_files! <= Int64(max_files!),
                [expires_at, created_at].allSatisfy({ $0 == nil || $0!.utf8.count <= 64 })
            else { throw HistorySnapshot.Failure.invalidPage }
        }
    }

    let transfers: [Transfer]
    let slots: [Slot]
    let next_cursor: String?
    let sync_cursor: String?
    let generation: String?
    let paginated = true
    enum CodingKeys: String, CodingKey {
        case transfers, slots, next_cursor, paginated, sync_cursor, generation
    }
    init(
        transfers: [Transfer], slots: [Slot], next: String? = nil, sync: String? = nil,
        generation: String? = nil
    ) {
        self.transfers = transfers
        self.slots = slots
        next_cursor = next
        sync_cursor = sync
        self.generation = generation
    }
    func encode(to encoder: Encoder) throws {
        var fields = encoder.container(keyedBy: CodingKeys.self)
        try fields.encode(true, forKey: .paginated)
        try fields.encode(transfers, forKey: .transfers)
        try fields.encode(slots, forKey: .slots)
        try fields.encode(next_cursor, forKey: .next_cursor)
        try fields.encode(sync_cursor, forKey: .sync_cursor)
        try fields.encode(generation, forKey: .generation)
    }
    init(from decoder: Decoder) throws {
        let f = try decoder.container(keyedBy: CodingKeys.self)
        guard try f.decode(Bool.self, forKey: .paginated) else {
            throw HistorySnapshot.Failure.invalidPage
        }
        transfers = try f.decode([Transfer].self, forKey: .transfers)
        slots = try f.decode([Slot].self, forKey: .slots)
        next_cursor = try f.decode(String?.self, forKey: .next_cursor)
        sync_cursor = try f.decodeIfPresent(String.self, forKey: .sync_cursor)
        generation = try f.decodeIfPresent(String.self, forKey: .generation)
        guard (sync_cursor == nil) == (generation == nil), HistorySnapshot.validCursor(sync_cursor),
            generation == nil || UUID(uuidString: generation!) != nil
        else { throw HistorySnapshot.Failure.invalidPage }
        guard Set(transfers.map(\.id)).count == transfers.count,
            Set(slots.map(\.id)).count == slots.count
        else { throw HistorySnapshot.Failure.invalidPage }
    }

    var identities: Set<String> {
        Set(transfers.map { $0.id + "|transfer" } + slots.map { $0.id + "|slot" })
    }
}

enum HistorySnapshot {
    enum Failure: Error { case invalidPage, repeatedCursor, tooLarge, timedOut }
    static let maximumBytes = 1_048_576

    static func validCursor(_ cursor: String?) -> Bool {
        guard let cursor else { return true }
        return !cursor.isEmpty && cursor.utf8.count <= 512
            && cursor.utf8.allSatisfy {
                (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45
                    || $0 == 95
            }
    }

    /// One page only. A continuation on an empty filtered page requires explicit navigation.
    static func load(
        after: String? = nil, limit: Int = 50, kind: String? = nil,
        fetch: @escaping @Sendable (String) async throws -> Data
    ) async throws -> ResourceList {
        guard (1...100).contains(limit), validCursor(after),
            kind == nil || kind == "transfer" || kind == "slot"
        else { throw Failure.invalidPage }
        return try await withThrowingTaskGroup(of: ResourceList.self) { group in
            group.addTask {
                var path = "auth/resources?limit=\(limit)"
                if let kind { path += "&kind=" + kind }
                if let after {
                    path += "&after=" + after
                }
                let data = try await fetch(path)
                try Task.checkCancellation()
                guard data.count <= maximumBytes else { throw Failure.tooLarge }
                let page = try JSONDecoder().decode(ResourceList.self, from: data)
                guard page.transfers.count + page.slots.count <= limit, validCursor(page.next_cursor),
                    kind != "transfer" || page.slots.isEmpty, kind != "slot" || page.transfers.isEmpty
                else { throw Failure.invalidPage }
                if let next = page.next_cursor, next == after {
                    throw Failure.repeatedCursor
                }
                return page
            }
            group.addTask {
                try await Task.sleep(nanoseconds: 10_000_000_000)
                throw Failure.timedOut
            }
            defer { group.cancelAll() }
            guard let result = try await group.next() else { throw Failure.invalidPage }
            return result
        }
    }
}

import Foundation

struct ResourceList: Decodable, Sendable {
    struct Transfer: Decodable, Sendable {
        let id: String
        let status: String
        let file_count: Int
        let total_size: Int64
        let expires_at: String?
        let created_at: String?
        let downloaded_at: String?
        let download_count: Int?
        let max_downloads: Int?
    }

    struct Slot: Decodable, Sendable {
        let id: String
        let status: String
        let expires_at: String?
        let created_at: String?
        let file_count: Int
        let completed_files: Int
        let total_size: Int64
        let receive_protocol: Int?
        let max_files: Int?
        let reserved_files: Int64?
    }

    let transfers: [Transfer]
    let slots: [Slot]
    let next_cursor: String?
}

enum HistorySnapshot {
    enum Failure: Error { case invalidPage, repeatedCursor, tooLarge, timedOut }

    static func load(fetch: @escaping @Sendable (String) async throws -> Data) async throws -> ResourceList {
        try await withThrowingTaskGroup(of: ResourceList.self) { group in
            group.addTask {
                var transfers: [ResourceList.Transfer] = []
                var slots: [ResourceList.Slot] = []
                var cursors = Set<String>()
                var next: String?
                for _ in 0 ..< 100 {
                    try Task.checkCancellation()
                    var query = URLComponents()
                    query.queryItems = [URLQueryItem(name: "limit", value: "100")]
                    if let next {
                        query.queryItems?.append(URLQueryItem(name: "after", value: next))
                    }
                    let data = try await fetch("auth/resources?" + (query.percentEncodedQuery ?? "").replacingOccurrences(of: "+", with: "%2B"))
                    guard data.count <= 1_048_576 else { throw Failure.tooLarge }
                    let page = try JSONDecoder().decode(ResourceList.self, from: data)
                    guard page.transfers.count + page.slots.count <= 100,
                          page.transfers.allSatisfy({ UUID(uuidString: $0.id) != nil && $0.file_count >= 0 && $0.total_size >= 0 }),
                          page.slots.allSatisfy({ UUID(uuidString: $0.id) != nil && $0.file_count >= 0 && $0.completed_files >= 0 && $0.completed_files <= $0.file_count && $0.total_size >= 0 })
                    else { throw Failure.invalidPage }
                    transfers.append(contentsOf: page.transfers)
                    slots.append(contentsOf: page.slots)
                    next = page.next_cursor
                    if let next {
                        guard !next.isEmpty, next.utf8.count <= 512, cursors.insert(next).inserted else { throw Failure.repeatedCursor }
                    } else {
                        return ResourceList(transfers: transfers, slots: slots, next_cursor: nil)
                    }
                }
                throw Failure.tooLarge
            }
            group.addTask {
                try await Task.sleep(nanoseconds: 30_000_000_000)
                throw Failure.timedOut
            }
            defer { group.cancelAll() }
            guard let result = try await group.next() else { throw Failure.invalidPage }
            return result
        }
    }
}

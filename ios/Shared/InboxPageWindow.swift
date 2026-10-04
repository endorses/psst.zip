import Foundation

/// A bounded cursor trail, independent of how many submissions an inbox retains.
/// A page can be empty after filtering expired entries and still have a next cursor.
struct InboxPageWindow: Equatable {
    enum Failure: Error { case invalidCursor, noPage }
    private(set) var cursor: String?
    private(set) var nextCursor: String?
    private(set) var number = 1
    private(set) var loaded = false
    private(set) var previous: [String?] = []

    var canGoBack: Bool { !previous.isEmpty }

    mutating func accept(next: String?) throws {
        if let next {
            guard !next.isEmpty, next.utf8.count <= 512, next != cursor,
                !previous.contains(where: { $0 == next })
            else { throw Failure.invalidCursor }
        }
        nextCursor = next
        loaded = true
    }

    func forward() throws -> Self {
        guard loaded, let nextCursor, number < Int.max else { throw Failure.noPage }
        var result = self
        result.previous.append(cursor)
        if result.previous.count > 100 { result.previous.removeFirst() }
        result.cursor = nextCursor
        result.nextCursor = nil
        result.loaded = false
        result.number += 1
        return result
    }

    func backward() throws -> Self {
        guard !previous.isEmpty else { throw Failure.noPage }
        var result = self
        result.cursor = result.previous.removeLast()
        result.nextCursor = nil
        result.loaded = false
        result.number -= 1
        return result
    }
}

/// Approval belongs to this exact page and set of submissions, not whichever
/// files happen to arrive while the owner reviews a storage/data confirmation.
struct InboxSaveScope: Equatable {
    let pageID: UUID
    let cursor: String?
    let transferIDs: Set<String>

    func accepts(pageID: UUID, cursor: String?, available: Set<String>) -> Bool {
        self.pageID == pageID && self.cursor == cursor && !transferIDs.isEmpty && transferIDs.count <= 50 && transferIDs.isSubset(of: available)
    }

    /// Partial pages, unknown totals, filtered completed entries and incomplete
    /// local copies cannot establish that every received file has been saved.
    static func coversInbox(cursor: String?, next: String?, total: Int64?, visible: [String: Int], saved: Set<String>) -> Bool {
        guard cursor == nil, next == nil, let total, total > 0,
            visible.count <= 50, visible.values.allSatisfy({ $0 > 0 && $0 <= 100 }),
            Set(visible.keys).isSubset(of: saved)
        else { return false }
        return visible.values.reduce(Int64(0)) { $0 + Int64($1) } == total
    }
}

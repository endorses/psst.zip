import Foundation

/// Two keyset streams, at most one page buffered per source and 100 visible rows.
/// Work is committed by the caller only after both source reads succeed.
struct BoundedHistoryMerge<Element, Cursor> {
    struct Source {
        var items: [Element] = []
        var next: Cursor?
        var started = false
        var ended = false
    }
    var left = Source(), right = Source()
    private(set) var visible: [Element] = []
    private(set) var trimmed = false
    var hasMore: Bool { !left.ended || !right.ended || !left.items.isEmpty || !right.items.isEmpty }
    mutating func load(left loadLeft: (Cursor?) throws -> ([Element], Cursor?), right loadRight: (Cursor?) throws -> ([Element], Cursor?), precedes: (Element, Element) -> Bool)
        throws
    {
        func fill(_ source: inout Source, _ fetch: (Cursor?) throws -> ([Element], Cursor?)) throws {
            guard source.items.isEmpty, !source.ended else { return }
            let (items, next) = try fetch(source.started ? source.next : nil)
            guard items.count <= 50, !items.isEmpty || next == nil else { throw MergeError.invalidPage }
            source.items = items
            source.next = next
            source.started = true
            source.ended = next == nil
        }
        var added: [Element] = []
        while added.count < 50 {
            try fill(&left, loadLeft)
            try fill(&right, loadRight)
            guard !left.items.isEmpty || !right.items.isEmpty else { break }
            if let a = left.items.first, let b = right.items.first {
                added.append(precedes(a, b) ? left.items.removeFirst() : right.items.removeFirst())
            } else if !left.items.isEmpty {
                added.append(left.items.removeFirst())
            } else {
                added.append(right.items.removeFirst())
            }
        }
        visible += added
        if visible.count > 100 {
            visible.removeFirst(visible.count - 100)
            trimmed = true
        }
    }
    mutating func updateVisible(_ update: (Element) throws -> Element?) rethrows { visible = try visible.compactMap(update) }
    enum MergeError: Error { case invalidPage }
}

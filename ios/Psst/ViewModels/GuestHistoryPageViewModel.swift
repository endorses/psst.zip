import Foundation

@Observable
@MainActor
final class GuestHistoryPageViewModel {
    private(set) var records: [GuestDownload] = []
    private(set) var next: HistoryRecordDatabase.Cursor?
    private(set) var number = 1
    private(set) var error: String?
    private var cursor: HistoryRecordDatabase.Cursor?
    private var previous: [HistoryRecordDatabase.Cursor?] = []
    var canGoBack: Bool { !previous.isEmpty }

    func refresh(store: GuestDownloadStore) { load(store: store, cursor: cursor, trail: previous, number: number) }
    func first(store: GuestDownloadStore) { load(store: store, cursor: nil, trail: [], number: 1) }
    func forward(store: GuestDownloadStore) {
        guard let next, number < Int.max else { return }
        var trail = previous + [cursor]
        if trail.count > 100 { trail.removeFirst() }
        load(store: store, cursor: next, trail: trail, number: number + 1)
    }
    func backward(store: GuestDownloadStore) {
        guard !previous.isEmpty else { return }
        var trail = previous
        let target = trail.removeLast()
        load(store: store, cursor: target, trail: trail, number: number - 1)
    }
    private func load(store: GuestDownloadStore, cursor: HistoryRecordDatabase.Cursor?, trail: [HistoryRecordDatabase.Cursor?], number: Int) {
        do {
            let page = try store.page(after: cursor)
            records = page.records
            next = page.next
            self.cursor = cursor
            previous = trail
            self.number = number
            error = nil
        } catch { self.error = "Downloaded history could not be read. The previous page has been kept. Restore storage access and retry." }
    }
}

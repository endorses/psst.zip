import Foundation
import Observation

@Observable
@MainActor
final class MergedDeviceHistoryViewModel {
    private var merge = BoundedHistoryMerge<HistoryEntry, HistoryRecordDatabase.Cursor>()
    private(set) var session: DeviceSession?
    private(set) var filter: HistoryFilter = .all
    private(set) var error: String?
    private(set) var loaded = false
    var records: [HistoryEntry] { merge.visible }
    var hasMore: Bool { merge.hasMore }
    var trimmed: Bool { merge.trimmed }
    func reset() {
        merge = .init()
        error = nil
        session = nil
        loaded = false
    }
    func resume(history: TransferHistoryStore, guests: GuestDownloadStore, session: DeviceSession?, filter: HistoryFilter) {
        if !loaded || self.session != session || self.filter != filter {
            first(history: history, guests: guests, session: session, filter: filter)
        } else {
            refreshVisible(history: history, guests: guests)
        }
    }
    func first(history: TransferHistoryStore, guests: GuestDownloadStore, session: DeviceSession?, filter: HistoryFilter) {
        if self.session != session || self.filter != filter { reset() }
        self.session = session
        self.filter = filter
        load(.init(), history: history, guests: guests)
    }
    func more(history: TransferHistoryStore, guests: GuestDownloadStore) { load(merge, history: history, guests: guests) }
    func refreshVisible(history: TransferHistoryStore, guests: GuestDownloadStore) {
        do {
            var updated = merge
            try updated.updateVisible { entry in
                switch entry {
                case .account(let row):
                    guard let current = try history.record(row.localID), let session = self.session, current.canManage(as: session) else { return nil }
                    return .account(current)
                case .downloaded(let row): return try guests.find(row.id).map(HistoryEntry.downloaded)
                }
            }
            merge = updated
        } catch { self.error = "Local history could not be refreshed. The previous entries are preserved." }
    }
    private func load(_ initial: BoundedHistoryMerge<HistoryEntry, HistoryRecordDatabase.Cursor>, history: TransferHistoryStore, guests: GuestDownloadStore) {
        do {
            var next = initial
            try next.load(
                left: { cursor in
                    guard self.filter != .downloaded, let session = self.session, session.canTransfer else { return ([], nil) }
                    let kinds = self.filter == .sent ? ["transfer"] : self.filter == .receive ? ["slot"] : ["transfer", "slot"]
                    let page = try history.page(session: session, kinds: kinds, after: cursor)
                    return (page.records.map(HistoryEntry.account), page.next)
                },
                right: { cursor in
                    guard self.filter == .all || self.filter == .downloaded else { return ([], nil) }
                    let page = try guests.page(after: cursor)
                    return (page.records.map(HistoryEntry.downloaded), page.next)
                }, precedes: { a, b in a.date == b.date ? a.localOrderKey.utf8.lexicographicallyPrecedes(b.localOrderKey.utf8) : a.date > b.date })
            merge = next
            loaded = true
            error = nil
            refreshVisible(history: history, guests: guests)
        } catch { self.error = "Local history could not be read. The previous entries are preserved. Restore storage access and retry." }
    }
}

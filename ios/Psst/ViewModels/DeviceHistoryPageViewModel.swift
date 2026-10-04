import Foundation

@Observable
@MainActor
final class DeviceHistoryPageViewModel {
    private(set) var records: [TransferRecord] = []
    private(set) var session: DeviceSession?
    private(set) var next: HistoryRecordDatabase.Cursor?
    private(set) var number = 1
    private(set) var error: String?
    private var cursor: HistoryRecordDatabase.Cursor?
    private var previous: [HistoryRecordDatabase.Cursor?] = []
    private var kinds = ["transfer", "slot"]
    var canGoBack: Bool { !previous.isEmpty }

    func reset() {
        records = []
        session = nil
        next = nil
        cursor = nil
        previous = []
        number = 1
        error = nil
    }
    func refresh(history: TransferHistoryStore, session: DeviceSession?, filter: HistoryFilter) {
        guard let session, session.canTransfer else {
            reset()
            return
        }
        let selected = filter == .sent ? ["transfer"] : filter == .receive ? ["slot"] : ["transfer", "slot"]
        if self.session != session || kinds != selected {
            reset()
            kinds = selected
        }
        load(history: history, session: session, cursor: cursor, trail: previous, number: number)
    }
    func first(history: TransferHistoryStore, session: DeviceSession) {
        load(history: history, session: session, cursor: nil, trail: [], number: 1)
    }
    func forward(history: TransferHistoryStore, session: DeviceSession) {
        guard self.session == session, let next, number < Int.max else { return }
        var trail = previous + [cursor]
        if trail.count > 100 { trail.removeFirst() }
        load(history: history, session: session, cursor: next, trail: trail, number: number + 1)
    }
    func backward(history: TransferHistoryStore, session: DeviceSession) {
        guard self.session == session, !previous.isEmpty else { return }
        var trail = previous
        let target = trail.removeLast()
        load(history: history, session: session, cursor: target, trail: trail, number: number - 1)
    }
    private func load(history: TransferHistoryStore, session: DeviceSession, cursor: HistoryRecordDatabase.Cursor?, trail: [HistoryRecordDatabase.Cursor?], number: Int) {
        do {
            let page = try history.page(session: session, kinds: kinds, after: cursor)
            self.session = session
            self.cursor = cursor
            self.previous = trail
            self.number = number
            records = page.records
            next = page.next
            error = nil
        } catch { self.error = "Local links could not be read. The previous page has been kept. Restore storage access and retry." }
    }
}

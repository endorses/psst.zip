import Foundation

@Observable
@MainActor
final class HistoryPageViewModel {
    private(set) var window = InboxPageWindow()
    private(set) var identities: Set<String> = []
    private(set) var records: [TransferRecord] = []
    private(set) var loadedSession: DeviceSession?
    private(set) var loading = false
    private(set) var stale = false
    private(set) var lastUpdated: Date?
    private var requestID = UUID()
    private var kind: String?
    func setFilter(_ filter: HistoryFilter) {
        let next = filter == .sent ? "transfer" : filter == .receive ? "slot" : nil
        if next != kind {
            invalidate()
            kind = next
        }
    }

    func invalidate() {
        requestID = UUID()
        window = InboxPageWindow()
        identities = []
        records = []
        loadedSession = nil
        loading = false
        stale = false
        lastUpdated = nil
    }

    func contains(_ record: TransferRecord, session: DeviceSession?) -> Bool {
        session == loadedSession && session != nil && identities.contains(record.id + (record.isSlot == true ? "|slot" : "|transfer"))
    }

    func refreshLocal(history: TransferHistoryStore, session: DeviceSession?) {
        guard let session, loadedSession == session else { return }
        do {
            let prefix = "resource|" + session.serverURL + "|" + session.userID + "|"
            records = try history.records(ids: identities.map { prefix + $0 }, session: session)
        } catch { stale = true }
    }

    @discardableResult
    func refresh(history: TransferHistoryStore, session: DeviceSession) async -> Bool {
        await load(window, history: history, session: session)
    }

    func next(history: TransferHistoryStore, session: DeviceSession) async {
        guard !loading, loadedSession == session, let target = try? window.forward() else { return }
        _ = await load(target, history: history, session: session)
    }

    func previous(history: TransferHistoryStore, session: DeviceSession) async {
        guard !loading, loadedSession == session, let target = try? window.backward() else { return }
        _ = await load(target, history: history, session: session)
    }

    func first(history: TransferHistoryStore, session: DeviceSession) async {
        _ = await load(InboxPageWindow(), history: history, session: session)
    }

    private func load(_ target: InboxPageWindow, history: TransferHistoryStore, session: DeviceSession) async -> Bool {
        guard !loading, session.canTransfer, SecretStore.session == session else { return false }
        let id = UUID()
        requestID = id
        loading = true
        defer { if requestID == id { loading = false } }
        func current() -> Bool { requestID == id && SecretStore.session == session && !Task.isCancelled }
        do {
            let list = try await HistorySnapshot.load(after: target.cursor, kind: kind) { path in
                try await AccountHTTP.request(server: session.serverURL, path: path, token: session.token, maximumBytes: HistorySnapshot.maximumBytes, timeout: 10)
            }
            guard current() else { return false }
            var committed = target
            try committed.accept(next: list.next_cursor)
            try history.mergeResourcePage(list, session: session)
            let prefix = "resource|" + session.serverURL + "|" + session.userID + "|"
            let records = try history.records(ids: list.identities.map { prefix + $0 }, session: session)
            self.records = records
            window = committed
            identities = list.identities
            loadedSession = session
            stale = false
            lastUpdated = Date()
            return true
        } catch {
            guard current() else { return false }
            stale = true
            return false
        }
    }
}

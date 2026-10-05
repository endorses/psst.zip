import Foundation

@Observable
@MainActor
final class HistoryPageViewModel {
    // Keep a server-imposed account deadline across view reconstruction/foregrounding.
    // Local UI actions may retry transport failures, but cannot override Retry-After.
    private static var retryDeadlines = HistoryCooldowns()
    private let now: () -> Date
    private var failures = 0
    private(set) var nextRefreshAt: Date?
    private(set) var scheduleID = UUID()
    private(set) var window = InboxPageWindow()
    private(set) var identities: Set<String> = []
    private(set) var records: [TransferRecord] = []
    private(set) var loadedSession: DeviceSession?
    private(set) var loading = false
    private(set) var stale = false
    private(set) var lastUpdated: Date?
    private(set) var retryAfter: TimeInterval = 0
    private var requestID = UUID()
    private var kind: String?
    private var capability: Bool?
    private var capabilityScope: String?
    private var pendingRefresh = false
    private var navigationStale = false
    private var activeTask: Task<Bool, Never>?
    private var activeTaskID = UUID()
    private var activeSession: DeviceSession?
    private var cacheRecoveryRequested = false

    init(now: @escaping () -> Date = { Date() }) { self.now = now }

    /// One visible-view scheduler observes this deadline after every completed
    /// manual, mutation, filter/navigation or routine request.
    func routineDelay() -> TimeInterval { max(0, (nextRefreshAt ?? now()).timeIntervalSince(now())) }
    func resumeRoutine() {
        nextRefreshAt = now()
        scheduleID = UUID()
    }

    func setFilter(_ filter: HistoryFilter) {
        let next = filter == .sent ? "transfer" : filter == .receive ? "slot" : nil
        if next != kind {
            invalidate()
            kind = next
        }
    }
    func cancel() {
        activeTaskID = UUID()
        activeTask?.cancel()
        activeTask = nil
        activeSession = nil
        requestID = UUID()
        loading = false
        pendingRefresh = false
    }
    func invalidate() {
        cancel()
        window = InboxPageWindow()
        identities = []
        records = []
        loadedSession = nil
        stale = false
        lastUpdated = nil
        navigationStale = false
        nextRefreshAt = nil
        failures = 0
        scheduleID = UUID()
    }
    func contains(_ record: TransferRecord, session: DeviceSession?) -> Bool {
        session == loadedSession && session != nil
            && identities.contains(record.id + (record.isSlot == true ? "|slot" : "|transfer"))
    }
    func hasLoadedPage(for session: DeviceSession?) -> Bool {
        session != nil && loadedSession == session
    }
    func refreshLocal(history: TransferHistoryStore, session: DeviceSession?) {
        guard let session, loadedSession == session else { return }
        showCache(history: history, session: session, target: window)
    }
    private func showCache(
        history: TransferHistoryStore, session: DeviceSession, target: InboxPageWindow
    ) {
        do {
            guard
                let (coverage, rows) = try history.cachedServerPage(
                    session: session, kind: kind, after: target.cursor)
            else { return }
            var committed = target
            try committed.accept(next: coverage.next)
            window = committed
            records = rows
            identities = Set(rows.map { $0.id + ($0.isSlot == true ? "|slot" : "|transfer") })
            loadedSession = session
            navigationStale = coverage.navigationStale == true
        } catch { stale = true }
    }
    @discardableResult
    func refresh(history: TransferHistoryStore, session: DeviceSession) async -> Bool {
        if let activeSession, activeSession != session { invalidate() }
        if activeTask != nil {
            pendingRefresh = true
            return true
        }
        return await run(window, history: history, session: session, sync: true)
    }
    func next(history: TransferHistoryStore, session: DeviceSession) async {
        guard !loading, loadedSession == session else { return }
        if window.cursor == nil, navigationStale {
            guard await run(window, history: history, session: session, sync: false) else { return }
        }
        guard let target = try? window.forward() else { return }
        _ = await run(target, history: history, session: session, sync: false)
    }
    func previous(history: TransferHistoryStore, session: DeviceSession) async {
        guard !loading, loadedSession == session, let target = try? window.backward() else { return }
        _ = await run(target, history: history, session: session, sync: false)
    }
    func first(history: TransferHistoryStore, session: DeviceSession) async {
        _ = await run(InboxPageWindow(), history: history, session: session, sync: false)
    }
    /// Own cancellation of every entry point, including manual and mutation refreshes.
    private func run(
        _ target: InboxPageWindow, history: TransferHistoryStore, session: DeviceSession, sync: Bool
    ) async -> Bool {
        guard activeTask == nil else { return false }
        guard session.canTransfer, SecretStore.session == session else {
            invalidate()
            return false
        }
        let target = loadedSession != nil && loadedSession != session ? InboxPageWindow() : target
        if loadedSession != nil, loadedSession != session { invalidate() }
        if let deadline = Self.retryDeadlines.deadline(for: session.accountID, now: now()) {
            showCache(history: history, session: session, target: target)
            retryAfter = max(0, deadline.timeIntervalSince(now()))
            stale = true
            nextRefreshAt = deadline
            scheduleID = UUID()
            return false
        }
        let id = UUID()
        activeTaskID = id
        activeSession = session
        let task = Task { @MainActor in
            var result = false
            for attempt in 0..<2 {
                pendingRefresh = false
                cacheRecoveryRequested = false
                result = await load(
                    attempt == 0 ? target : window, history: history, session: session,
                    sync: attempt == 0 ? sync : true)
                guard !Task.isCancelled, activeTaskID == id else { return false }
                if !cacheRecoveryRequested && (!pendingRefresh || !result) { break }
                await Task.yield()
            }
            return result
        }
        activeTask = task
        let result = await withTaskCancellationHandler {
            await task.value
        } onCancel: {
            task.cancel()
        }
        if activeTaskID == id {
            activeTask = nil
            activeSession = nil
            failures = result ? 0 : min(4, failures + 1)
            let backoff = result ? 10.0 : min(60.0, 10.0 * pow(2.0, Double(max(0, failures - 1))))
            let jitter = result ? 0.0 : Double.random(in: 0...1)
            nextRefreshAt = now().addingTimeInterval(max(backoff + jitter, retryAfter))
            scheduleID = UUID()
        }
        return result
    }
    private func load(
        _ target: InboxPageWindow, history: TransferHistoryStore, session: DeviceSession, sync: Bool
    ) async -> Bool {
        guard !loading, session.canTransfer, SecretStore.session == session else { return false }
        let id = UUID()
        requestID = id
        loading = true
        retryAfter = 0
        // No HTTP is needed before presenting a known page.
        showCache(history: history, session: session, target: target)
        defer { if requestID == id { loading = false } }
        func current() -> Bool {
            requestID == id && SecretStore.session == session && !Task.isCancelled
        }
        do {
            if capabilityScope != session.accountID {
                capability = nil
                capabilityScope = session.accountID
            }
            if capability == nil {
                let config = try await AccountHTTP.request(
                    server: session.serverURL, path: "config", maximumBytes: 65_536, timeout: 10)
                guard current() else { return false }
                capability = try HistorySync.supports(config)
            }
            var state = try history.historySyncState(session: session)
            var reset = false
            if sync, capability == true, let original = state {
                do {
                    var cursor = original
                    for _ in 0..<3 {
                        let batch = try await HistorySync.load(
                            cursor: cursor.cursor, generation: cursor.generation
                        ) { path in
                            try await AccountHTTP.request(
                                server: session.serverURL, path: path, token: session.token,
                                maximumBytes: HistorySync.maximumBytes, timeout: 10)
                        }
                        guard current() else { return false }
                        if !batch.changes.isEmpty || batch.next_cursor != cursor.cursor {
                            try history.applyHistoryChanges(batch, session: session, expected: cursor)
                        }
                        cursor = .init(generation: batch.generation, cursor: batch.next_cursor)
                        showCache(history: history, session: session, target: target)
                        if !batch.has_more { break }
                        await Task.yield()
                    }
                    state = cursor
                } catch HistorySync.Failure.resetRequired { reset = true }
            }
            if capability != true || state == nil || reset {
                let bootstrap = try await snapshot(after: nil, kind: nil, session: session)
                guard current() else { return false }
                if capability == true, bootstrap.sync_cursor == nil {
                    throw HistorySync.Failure.invalidBatch
                }
                let firstSync = state == nil
                try history.cacheServerPage(
                    bootstrap, session: session, kind: nil, after: nil, expected: state, reset: reset)
                state = try history.historySyncState(session: session)
                if firstSync, state != nil { pendingRefresh = true }
            }
            // Older/matching pages load only on explicit navigation or absent coverage.
            let cached = try history.cachedServerPage(session: session, kind: kind, after: target.cursor)
            if !sync || cached == nil
                || capability != true && (kind != nil || target.cursor != nil)
                || reset && (kind != nil || target.cursor != nil)
            {
                let list = try await snapshot(after: target.cursor, kind: kind, session: session)
                guard current() else { return false }
                try history.cacheServerPage(
                    list, session: session, kind: kind, after: target.cursor, expected: state)
            }
            showCache(history: history, session: session, target: target)
            guard current() else { return false }
            stale = false
            lastUpdated = Date()
            return true
        } catch {
            guard current() else { return false }
            if case HistorySync.Failure.retryAfter(let seconds) = error {
                retryAfter = seconds
                Self.retryDeadlines.record(scope: session.accountID, until: now().addingTimeInterval(seconds), now: now())
            }
            if case HistorySync.Failure.invalidCache = error {
                do {
                    try history.clearServerMetadata(session: session)
                    cacheRecoveryRequested = true
                } catch { cacheRecoveryRequested = false }
            }
            stale = true
            return false
        }
    }
    private func snapshot(after: String?, kind: String?, session: DeviceSession) async throws
        -> ResourceList
    {
        try await HistorySnapshot.load(after: after, kind: kind) { path in
            try await AccountHTTP.request(
                server: session.serverURL, path: path, token: session.token,
                maximumBytes: HistorySnapshot.maximumBytes, timeout: 10)
        }
    }
}

import SwiftUI

struct HistoryView: View {
    @Environment(TransferHistoryStore.self) private var history
    @Environment(ServerConfigManager.self) private var config
    @Environment(\.scenePhase) private var scenePhase
    @Environment(GuestDownloadStore.self) private var guests
    @Environment(GuestTransferModel.self) private var guestTransfer
    @Binding var filter: HistoryFilter
    var onSend: () -> Void = {}
    @State private var removing: GuestDownload?
    @State private var deleting: TransferRecord?
    @State private var failedDeletion: TransferRecord?
    @State private var error: String?
    var page: HistoryPageViewModel
    @State private var devicePage = MergedDeviceHistoryViewModel()
    @State private var showDevice = false
    @State private var sessionGeneration = UUID()
    @State private var visible = false
    @State private var busy = false
    @State private var renaming: TransferRecord?
    @State private var renameText = ""
    private var deviceMode: Bool {
        showDevice || config.session?.canTransfer != true
    }

    private var records: [HistoryEntry] {
        if deviceMode {
            return devicePage.session == config.session ? devicePage.records : []
        }
        return (page.loadedSession == config.session ? page.records : []).map(HistoryEntry.account)
    }

    private var working: Bool {
        busy || page.loading
    }
    private var hasLoadedHistory: Bool {
        deviceMode
            ? devicePage.loaded && devicePage.session == config.session
            : page.hasLoadedPage(for: config.session)
    }

    var body: some View {
        NavigationStack {
            List {
                if config.session?.canTransfer == true {
                    Picker(L10n.text("History source"), selection: $showDevice) {
                        Text(L10n.text("Server")).tag(false)
                        Text(L10n.text("On this device")).tag(true)
                    }.pickerStyle(.segmented).disabled(working)
                }
                Picker(L10n.text("History filter"), selection: $filter) {
                    ForEach(HistoryFilter.allCases, id: \.self) { Text(L10n.text($0.rawValue)).tag($0) }
                }.pickerStyle(.menu)
                if history.hasLegacyRecords {
                    Text(
                        L10n.text(
                            "Pre-account history is preserved. Manage pre-account server resources from the administrator website."
                        )
                    ).font(.footnote).foregroundStyle(
                        PsstTheme.secondary
                    )
                }
                if page.stale, !deviceMode {
                    Label(L10n.text("Offline — showing last known status"), systemImage: "wifi.slash")
                        .foregroundStyle(PsstTheme.warning)
                }
                if hasLoadedHistory, records.isEmpty {
                    ContentUnavailableView(
                        L10n.text(deviceMode ? "No transfers yet" : "No entries on this page"),
                        systemImage: "clock",
                        description: Text(
                            L10n.text(
                                filter == .downloaded
                                    ? "Files downloaded on this device will appear here, including while signed out."
                                    : "Choose another filter or page to see more entries.")))
                }
                ForEach(records) { entry in
                    switch entry {
                    case let .downloaded(record):
                        NavigationLink {
                            ScrollView { GuestDownloadDetail(record: record).padding() }.navigationTitle(
                                L10n.text("Downloaded"))
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                Label(L10n.text("Downloaded"), systemImage: "arrow.down.doc").font(.caption)
                                Text(verbatim: downloadedTitle(record)).font(
                                    .headline
                                )
                                .lineLimit(1).truncationMode(.middle)
                                Text(
                                    L10n.text(
                                        L10n.date(record.createdAt)
                                            + L10n.format(" · %lld files", Int64(record.files.count)))
                                )
                                .font(.caption)
                                Text(L10n.text(record.complete ? "Saved on this device" : "Partially saved")).font(
                                    .subheadline)
                            }
                        }.swipeActions {
                            Button(L10n.text("Remove from history"), role: .destructive) { removing = record }
                                .disabled(guestTransfer.active)
                        }
                    case let .account(record):
                        NavigationLink {
                            HistoryDetail(onSend: onSend, record: record)
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                if record.ownerID == nil {
                                    Text(L10n.text("Legacy item")).font(.caption)
                                }
                                Label(
                                    L10n.text(record.isSlot == true ? "Receive link" : "Sent"),
                                    systemImage: record.isSlot == true ? "tray.and.arrow.down" : "paperplane"
                                ).font(.caption)
                                Text(verbatim: record.safeDisplayTitle).font(.headline).lineLimit(1).truncationMode(
                                    .middle
                                ).accessibilityLabel(Text(verbatim: record.safeDisplayTitle))
                                Text(L10n.text(L10n.date(record.createdAt) + " · " + record.summary)).font(.caption)
                                Text(L10n.text(record.statusText)).font(.caption)

                            }.padding(.vertical, 4)
                        }.swipeActions {
                            Button(L10n.text("Revoke and delete"), role: .destructive) { deleting = record }
                                .disabled(working)
                        }
                        .contextMenu {
                            Button(L10n.text("Rename shared title")) {
                                renameText = record.sharedTitle ?? record.customTitle ?? ""
                                renaming = record
                            }
                        }
                        .swipeActions(edge: .leading) {
                            Button(L10n.text("Rename")) {
                                renameText = record.sharedTitle ?? record.customTitle ?? ""
                                renaming = record
                            }
                        }
                    }
                }
                if deviceMode {
                    if let message = devicePage.error ?? guests.error {
                        Text(L10n.text(message)).foregroundStyle(PsstTheme.error)
                    }
                    if devicePage.hasMore {
                        Button(L10n.text("Load more")) { devicePage.more(history: history, guests: guests) }
                            .disabled(working)
                    }
                    if devicePage.trimmed {
                        Button(L10n.text("Back to newest")) { reloadLocal() }.disabled(working)
                    }
                    if guests.hasPendingReceipts {
                        Button(L10n.text("Retry delivery confirmations")) {
                            Task { await guests.flushReceipts() }
                        }.disabled(working)
                    }
                } else if page.window.canGoBack || page.window.nextCursor != nil {
                    HStack {
                        if page.window.canGoBack {
                            Button(L10n.text("Previous")) { navigate(.previous) }
                        }
                        Spacer()
                        Text(L10n.text(L10n.format("Page %lld", Int64(page.window.number)))).font(.caption)
                        Spacer()
                        if page.window.nextCursor != nil {
                            Button(L10n.text("Next")) { navigate(.next) }
                        }
                    }.disabled(working)
                    if page.window.number > 1 {
                        Button(L10n.text("First page")) { navigate(.first) }.disabled(working)
                    }
                }
                if let error {
                    Text(L10n.text(error)).foregroundStyle(PsstTheme.error)
                    if let failedDeletion {
                        Button(L10n.text("Retry revocation")) { revoke(failedDeletion) }.disabled(working)
                    }
                }
            }
            .navigationTitle(L10n.text("History"))
            .alert(
                L10n.text("Rename shared title"),
                isPresented: Binding(
                    get: { renaming != nil },
                    set: {
                        if !$0 {
                            renaming = nil
                        }
                    }
                )
            ) {
                TextField(L10n.text("Name (optional)"), text: $renameText)
                Button(L10n.text("Save")) {
                    if let record = renaming, let session = config.session {
                        let name = renameText
                        busy = true
                        Task {
                            defer { busy = false }
                            do { try await history.rename(record, name: name, session: session) } catch {
                                if config.session == session {
                                    self.error =
                                        (error as? SharedLinkTitle.Failure)?.localizedDescription
                                        ?? "Could not save the shared title. Reconnect and retry."
                                }
                            }
                        }
                    }
                    renaming = nil
                }
                Button(L10n.text("Cancel"), role: .cancel) { renaming = nil }
            } message: {
                Text(L10n.text("Shown to people using this link. Leave empty to clear the shared title."))
            }
            .confirmationDialog(
                L10n.text("Remove from history? Saved files remain and the sender’s link keeps working."),
                isPresented: Binding(
                    get: { removing != nil },
                    set: {
                        if !$0 {
                            removing = nil
                        }
                    }
                ), titleVisibility: .visible
            ) {
                Button(L10n.text("Remove from history"), role: .destructive) {
                    if let removing {
                        do { try guests.remove(removing) } catch {
                            self.error = "Could not update local history. Free storage and retry."
                        }
                    }
                    removing = nil
                }
            }
            .refreshable { _ = await refresh() }
            .toolbar { Button(L10n.text("Refresh")) { Task { _ = await refresh() } }.disabled(working) }
            .confirmationDialog(
                L10n.text("Revoke and delete?"),
                isPresented: Binding(
                    get: { deleting != nil },
                    set: {
                        if !$0 {
                            deleting = nil
                        }
                    }
                ), titleVisibility: .visible
            ) {
                if let deleting {
                    Button(L10n.text("Revoke and delete"), role: .destructive) { revoke(deleting) }
                }
                Button(L10n.text("Cancel"), role: .cancel) { deleting = nil }
            } message: {
                Text(
                    L10n.text(
                        "This disables the link and removes its server files. Receive links stop accepting uploads. Copies already saved by anyone remain."
                    ))
            }
            .onAppear {
                visible = true
                if !deviceMode {
                    page.restoreCachedPage(
                        history: history, session: config.session, filter: filter)
                }
                page.resumeRoutine()
                if filter == .downloaded {
                    showDevice = true
                }
                history.reload()
                if deviceMode {
                    devicePage.resume(
                        history: history, guests: guests, session: config.session, filter: filter)
                }
            }
            .onDisappear {
                visible = false
                page.cancel()
            }
            .task(id: "\(visible)-\(scenePhase)-\(sessionGeneration)-\(showDevice)-\(page.scheduleID)") {
                guard visible, scenePhase == .active, config.isConfigured, !config.needsSignIn, !deviceMode else { return }
                while !Task.isCancelled, page.routineDelay() > 0 {
                    do {
                        try await Task.sleep(nanoseconds: UInt64(min(60, page.routineDelay()) * 1_000_000_000))
                    } catch { return }
                }
                guard !Task.isCancelled, visible, scenePhase == .active, !deviceMode,
                    !config.needsSignIn, config.isConfigured, let session = config.session, session.canTransfer
                else { return }
                // Routine sync must reach model coalescing even during rename/revoke.
                // The manual refresh helper keeps its separate disabled-state guard.
                page.setFilter(filter)
                _ = await page.refresh(history: history, session: session)
                // Completion changes scheduleID; the single view task reschedules
                // from that completion, canceling any previous routine sleep.
            }
            .onChange(of: scenePhase) { _, value in
                if value != .active { page.cancel() } else { page.resumeRoutine() }
            }
            .onReceive(NotificationCenter.default.publisher(for: .historyMutation)) { note in
                guard visible, scenePhase == .active, !deviceMode, let session = config.session,
                    note.object as? String == session.serverURL,
                    note.userInfo?["ownerID"] as? String == session.userID
                else { return }
                Task { _ = await page.refresh(history: history, session: session) }
            }
            .onChange(of: config.session) { _, _ in
                page.invalidate()
                devicePage.reset()
                reloadLocal()
                sessionGeneration = UUID()
                renaming = nil
                deleting = nil
                failedDeletion = nil
                error = nil
            }
            .onChange(of: filter) { _, value in
                if value == .downloaded {
                    showDevice = true
                }
                page.setFilter(value)
                reloadLocal()
                if !deviceMode {
                    Task { _ = await refresh() }
                }
            }
            .onChange(of: showDevice) { _, value in
                page.invalidate()
                if !value, filter == .downloaded {
                    filter = .all
                }
                reloadLocal()
            }
            .onChange(of: guests.revision) {
                _, _ in
                if deviceMode {
                    devicePage.refreshVisible(history: history, guests: guests)
                }
            }
            .onChange(of: history.revision) { _, _ in
                if deviceMode {
                    devicePage.refreshVisible(history: history, guests: guests)
                } else {
                    page.refreshLocal(history: history, session: config.session)
                }
            }
        }.modifier(PsstStyle())
    }

    private func downloadedTitle(_ record: GuestDownload) -> String {
        if let title = record.sharedTitle {
            return title
        }
        let name =
            record.files.first.map { GuestFiles.displayName($0.name) } ?? L10n.text("File transfer")
        return name
            + (record.files.count > 1
                ? L10n.text(L10n.format(" + %lld files", Int64(record.files.count - 1))) : "")
    }

    private func reloadLocal() {
        guard deviceMode else { return }
        devicePage.first(history: history, guests: guests, session: config.session, filter: filter)
    }

    private enum Navigation { case previous, next, first }
    private func navigate(_ direction: Navigation) {
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else {
            return
        }
        Task {
            switch direction {
            case .previous: await page.previous(history: history, session: session)
            case .next: await page.next(history: history, session: session)
            case .first: await page.first(history: history, session: session)
            }
        }
    }

    private func refresh() async -> Bool {
        if deviceMode {
            history.reload()
            reloadLocal()
            return true
        }
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else {
            return true
        }
        page.setFilter(filter)
        return await page.refresh(history: history, session: session)
    }

    private func revoke(_ record: TransferRecord) {
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else {
            return
        }
        busy = true
        error = nil
        deleting = nil
        Task {
            defer { busy = false }
            do {
                try await history.revoke(record, session: session)
                guard config.session == session, SecretStore.session == session else { return }
                failedDeletion = nil
                if !deviceMode {
                    _ = await page.refresh(history: history, session: session)
                }
            } catch {
                guard config.session == session, SecretStore.session == session else { return }
                failedDeletion = record
                self.error = L10n.message(
                    "Could not revoke this link. Its history entry has been kept. Reconnect or sign in again, then retry."
                )
            }
        }
    }
}

private struct HistoryDetail: View {
    @Environment(\.dismiss) private var dismiss
    var onSend: () -> Void = {}
    @Environment(TransferHistoryStore.self) private var history
    @Environment(ServerConfigManager.self) private var config
    @Environment(\.scenePhase) private var scenePhase
    let record: TransferRecord
    @State private var receive: ReceiveViewModel?
    @State private var refreshedDetail: TransferRecord?
    @State private var stale = false
    @State private var sessionGeneration = UUID()
    var current: TransferRecord {
        (try? history.record(record.localID)) ?? refreshedDetail ?? record
    }

    var body: some View {
        Group {
            if !record.canManage(
                as: config.session
                    ?? DeviceSession(
                        serverURL: "", userID: "", username: "", token: "", sessionID: "", expiresAt: ""))
            {
                ContentUnavailableView(
                    L10n.text("Account changed"), systemImage: "person.crop.circle",
                    description: Text(L10n.text("Return to History in the current account.")))
            } else if let receive {
                TransferDetailView(receiveViewModel: receive)
            } else {
                ScrollView {
                    VStack(spacing: 16) {
                        Text(verbatim: current.safeDisplayTitle).font(.headline)
                        Text(L10n.text(current.statusText))
                        if current.linkActive, let link = current.fullLink {
                            LinkCard(url: link)
                        } else if current.linkActive {
                            Text(
                                L10n.text(
                                    "This device does not have the encryption key. You can manage this item, but open its full link on the device that created it."
                                ))
                        }
                        if current.state == .exhausted {
                            Text(L10n.text("Choose the original files to create a new send link.")).font(
                                .footnote)
                            Button(L10n.text("Create replacement link")) {
                                dismiss()
                                onSend()
                            }.buttonStyle(PrimaryAction())
                        }
                        Text(L10n.text(current.summary))
                        if let limit = current.maxDownloads, limit > 0 {
                            Text(L10n.text(L10n.format("Up to %lld download attempts per file", Int64(limit))))
                                .font(.footnote)
                        }
                        DisclosureGroup(L10n.text("Technical details")) {
                            Text(L10n.text(current.id)).font(.caption).textSelection(.enabled)
                        }
                        if stale {
                            Text(L10n.text("Offline — showing last known status")).foregroundStyle(
                                PsstTheme.warning)
                        }
                    }.padding()
                }.modifier(PsstStyle())
            }
        }
        .onAppear {
            if current.isSlot == true, receive == nil {
                receive = ReceiveViewModel(serverConfig: config, historyStore: history, record: current)
            }
        }
        .onChange(of: config.session) { _, _ in
            sessionGeneration = UUID()
            stale = false
        }
        .task(id: "\(scenePhase)-\(sessionGeneration)") {
            guard scenePhase == .active, record.isSlot != true else { return }
            var delay: UInt64 = 5
            while !Task.isCancelled, !config.needsSignIn, let session = config.session,
                record.canManage(as: session), current.state != .revoked, !current.isExpired
            {
                do {
                    let refreshed = try await history.refreshSend(current, session: session)
                    guard config.session == session, SecretStore.session == session, !Task.isCancelled else {
                        return
                    }
                    refreshedDetail = refreshed
                    stale = false
                    delay = 5
                } catch {
                    guard config.session == session, SecretStore.session == session, !Task.isCancelled else {
                        return
                    }
                    stale = true
                    delay = min(30, delay * 2)
                }
                do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
            }
        }
        .onDisappear { receive?.cancelSaving() }
    }
}

/// Sanitize remotely supplied automatic names at presentation time, preserving local custom labels.
extension TransferRecord {
    var safeDisplayTitle: String {
        if let sharedTitle, !sharedTitle.isEmpty {
            return sharedTitle
        }
        if let customTitle, !customTitle.isEmpty {
            return customTitle
        }
        guard let title, !title.isEmpty else { return displayTitle }
        let name = GuestFiles.displayName(title)
        return fileCount > 1
            ? name + L10n.text(L10n.format(" + %lld files", Int64(fileCount - 1))) : name
    }
}

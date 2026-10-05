import SwiftUI

struct HistoryView: View {
    @Environment(TransferHistoryStore.self) private var history
    @Environment(ServerConfigManager.self) private var config
    @Environment(\.scenePhase) private var scenePhase
    @Environment(GuestDownloadStore.self) private var guests
    @Environment(GuestTransferModel.self) private var guestTransfer
    @Binding var filter: HistoryFilter
    @State private var removing: GuestDownload?
    @State private var deleting: TransferRecord?
    @State private var failedDeletion: TransferRecord?
    @State private var error: String?
    @State private var page = HistoryPageViewModel()
    @State private var devicePage = DeviceHistoryPageViewModel()
    @State private var guestPage = GuestHistoryPageViewModel()
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
        let owned = deviceMode ? (devicePage.session == config.session ? devicePage.records : []) : (page.loadedSession == config.session ? page.records : [])
        return HistoryEntry.combine(
            account: owned,
            downloads: deviceMode ? guestPage.records : [], session: config.session, filter: filter
        )
    }

    private var working: Bool {
        busy || page.loading
    }

    var body: some View {
        NavigationStack {
            List {
                if config.session?.canTransfer == true {
                    Picker("History source", selection: $showDevice) {
                        Text("Server").tag(false)
                        Text("On this device").tag(true)
                    }.pickerStyle(.segmented).disabled(working)
                }
                if deviceMode {
                    Text("Links and files remembered on this device. Open an item to check its current server status.").font(.footnote).foregroundStyle(PsstTheme.secondary)
                    if config.session?.canTransfer == true && filter != .downloaded {
                        HStack {
                            Text("Links page \(devicePage.number)")
                            Spacer()
                            Button("Previous") { navigateLocal(back: true) }.disabled(working || !devicePage.canGoBack)
                            Button("Next") { navigateLocal(back: false) }.disabled(working || devicePage.next == nil)
                        }
                        if devicePage.number > 1 {
                            Button("First links page") {
                                if let session = config.session {
                                    devicePage.first(history: history, session: session)
                                }
                            }.disabled(working)
                        }
                        if let error = devicePage.error {
                            Text(error).foregroundStyle(PsstTheme.error)
                        }
                    }
                    if filter == .all || filter == .downloaded {
                        HStack {
                            Text("Downloads page \(guestPage.number)")
                            Spacer()
                            Button("Previous") { guestPage.backward(store: guests) }.disabled(working || !guestPage.canGoBack)
                            Button("Next") { guestPage.forward(store: guests) }.disabled(working || guestPage.next == nil)
                        }
                        if guestPage.number > 1 {
                            Button("First downloads page") { guestPage.first(store: guests) }.disabled(working)
                        }
                        if let error = guestPage.error ?? guests.error {
                            Text(error).foregroundStyle(PsstTheme.error)
                        }
                        if guests.hasPendingReceipts {
                            Button("Retry delivery confirmations") { Task { await guests.flushReceipts() } }.disabled(working)
                        }
                    }
                } else {
                    HStack {
                        Text("Page \(page.window.number)")
                        Spacer()
                        if page.loading {
                            ProgressView()
                        }
                        Button("Previous") { navigate(.previous) }.disabled(working || !page.window.canGoBack)
                        Button("Next") { navigate(.next) }.disabled(working || page.window.nextCursor == nil)
                    }
                    if page.window.number > 1 {
                        Button("First page") { navigate(.first) }.disabled(working)
                    }
                    Text("Filters apply to this page.").font(.caption).foregroundStyle(PsstTheme.secondary)
                }
                Picker("History filter", selection: $filter) {
                    ForEach(HistoryFilter.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }.pickerStyle(.menu)
                if history.hasLegacyRecords {
                    Text("Pre-account history is preserved. Manage pre-account server resources from the administrator website.").font(.footnote).foregroundStyle(
                        PsstTheme.secondary
                    )
                }
                if page.stale, !deviceMode {
                    Label("Offline — showing last known status", systemImage: "wifi.slash").foregroundStyle(PsstTheme.warning)
                }
                if let lastUpdated = page.lastUpdated, !deviceMode {
                    HStack {
                        Text("Last updated")
                        Text(lastUpdated, style: .relative)
                    }.font(.caption)
                }
                if records.isEmpty {
                    ContentUnavailableView(
                        deviceMode ? "No transfers yet" : "No entries on this page", systemImage: "clock",
                        description: Text(
                            filter == .downloaded
                                ? "Files downloaded on this device will appear here, including while signed out." : "Choose another filter or page to see more entries."
                        )
                    )
                }
                ForEach(records) { entry in
                    switch entry {
                    case let .downloaded(record):
                        NavigationLink {
                            ScrollView { GuestDownloadDetail(record: record).padding() }.navigationTitle("Downloaded")
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                Label("Downloaded", systemImage: "arrow.down.doc").font(.caption)
                                Text((record.files.first.map { GuestFiles.displayName($0.name) } ?? "File transfer") + (record.files.count > 1 ? " + \(record.files.count - 1) files" : "")).font(
                                    .headline
                                )
                                .lineLimit(1).truncationMode(.middle)
                                Text("\(record.files.count) files · " + ByteCountFormatter.string(fromByteCount: record.files.reduce(Int64(0)) { $0 + $1.size }, countStyle: .file))
                                    .font(.caption)
                                Text(record.origin).font(.caption).lineLimit(2)
                                Text(record.createdAt.formatted(date: .abbreviated, time: .shortened)).font(.caption)
                                Text(record.complete ? "Saved on this device" : "Partially saved").font(.subheadline)
                            }
                        }.swipeActions {
                            Button("Remove from history", role: .destructive) { removing = record }.disabled(guestTransfer.active)
                        }
                    case let .account(record):
                        NavigationLink {
                            HistoryDetail(record: record)
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                if record.ownerID == nil {
                                    Text("Legacy item").font(.caption)
                                }
                                Label(record.isSlot == true ? "Receive link" : "Sent", systemImage: record.isSlot == true ? "tray.and.arrow.down" : "paperplane").font(.caption)
                                Text(record.safeDisplayTitle).font(.headline).lineLimit(1).truncationMode(.middle).accessibilityLabel(record.safeDisplayTitle)
                                Text(record.createdAt.formatted(date: .abbreviated, time: .shortened)).font(.caption)
                                Text(record.summary).font(.subheadline)
                                Text(record.statusText).font(.caption)
                                if let policy = record.linkPolicySummary {
                                    Text(policy).font(.caption).foregroundStyle(PsstTheme.secondary)
                                }
                                if let expiry = record.expiresAt {
                                    HStack {
                                        Text(LocalizedStringKey(record.isExpired ? "Expired" : "Expires"))
                                        Text(expiry, style: .relative)
                                    }.font(.caption)
                                }
                            }.padding(.vertical, 4)
                        }.swipeActions { Button("Revoke and delete", role: .destructive) { deleting = record }.disabled(working) }
                            .contextMenu {
                                Button("Rename on this device") {
                                    renameText = record.customTitle ?? ""
                                    renaming = record
                                }
                            }
                            .swipeActions(edge: .leading) {
                                Button("Rename") {
                                    renameText = record.customTitle ?? ""
                                    renaming = record
                                }
                            }
                    }
                }
                if let error {
                    Text(error).foregroundStyle(PsstTheme.error)
                    if let failedDeletion {
                        Button("Retry revocation") { revoke(failedDeletion) }.disabled(working)
                    }
                }
            }
            .navigationTitle("History")
            .alert(
                "Rename on this device",
                isPresented: Binding(
                    get: { renaming != nil },
                    set: {
                        if !$0 {
                            renaming = nil
                        }
                    }
                )
            ) {
                TextField("Name (optional)", text: $renameText)
                Button("Save") {
                    if let record = renaming, let session = config.session {
                        do { try history.rename(record, name: renameText, session: session) } catch { self.error = "Could not save the name. Check your account and retry." }
                    }
                    renaming = nil
                }
                Button("Cancel", role: .cancel) { renaming = nil }
            } message: {
                Text("Only this device uses this name. Leave it empty to restore the automatic title.")
            }
            .confirmationDialog(
                "Remove from history? Saved files remain and the sender’s link keeps working.",
                isPresented: Binding(
                    get: { removing != nil },
                    set: {
                        if !$0 {
                            removing = nil
                        }
                    }
                ), titleVisibility: .visible
            ) {
                Button("Remove from history", role: .destructive) {
                    if let removing {
                        do { try guests.remove(removing) } catch { self.error = "Could not update local history. Free storage and retry." }
                    }
                    removing = nil
                }
            }
            .refreshable { _ = await refresh() }
            .toolbar { Button("Refresh") { Task { _ = await refresh() } }.disabled(working) }
            .confirmationDialog(
                "Revoke and delete?",
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
                    Button("Revoke and delete", role: .destructive) { revoke(deleting) }
                }
                Button("Cancel", role: .cancel) { deleting = nil }
            } message: {
                Text("This disables the link and removes its server files. Receive links stop accepting uploads. Copies already saved by anyone remain.")
            }
            .onAppear {
                visible = true
                if filter == .downloaded {
                    showDevice = true
                }
                history.reload()
                reloadLocal()
            }
            .onDisappear { visible = false }
            .task(id: "\(visible)-\(scenePhase)-\(sessionGeneration)-\(showDevice)") {
                guard visible, scenePhase == .active, config.isConfigured, !deviceMode else { return }
                var delay: UInt64 = 5
                while !Task.isCancelled, !config.needsSignIn, config.isConfigured {
                    let success = await refresh()
                    delay = success ? 5 : min(30, delay * 2)
                    do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
                }
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
                reloadLocal()
            }
            .onChange(of: showDevice) { _, value in
                if !value, filter == .downloaded {
                    filter = .all
                }
                reloadLocal()
            }
            .onChange(of: guests.revision) {
                _, _ in if deviceMode {
                    guestPage.refresh(store: guests)
                }
            }
            .onChange(of: history.revision) { _, _ in
                if deviceMode {
                    reloadLocal()
                } else {
                    page.refreshLocal(history: history, session: config.session)
                }
            }
        }.modifier(PsstStyle())
    }

    private func reloadLocal() {
        guestPage.refresh(store: guests)
        devicePage.refresh(history: history, session: config.session, filter: filter)
    }

    private func navigateLocal(back: Bool) {
        guard !working, let session = config.session else { return }
        if back {
            devicePage.backward(history: history, session: session)
        } else {
            devicePage.forward(history: history, session: session)
        }
    }

    private enum Navigation { case previous, next, first }
    private func navigate(_ direction: Navigation) {
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else { return }
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
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else { return true }
        return await page.refresh(history: history, session: session)
    }

    private func revoke(_ record: TransferRecord) {
        guard !working, let session = config.session, session.canTransfer, !config.needsSignIn else { return }
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
                self.error = String(localized: "Could not revoke this link. Its history entry has been kept. Reconnect or sign in again, then retry.")
            }
        }
    }
}

private struct HistoryDetail: View {
    @Environment(TransferHistoryStore.self) private var history
    @Environment(ServerConfigManager.self) private var config
    @Environment(\.scenePhase) private var scenePhase
    let record: TransferRecord
    @State private var receive: ReceiveViewModel?
    @State private var stale = false
    @State private var sessionGeneration = UUID()
    var current: TransferRecord {
        (try? history.record(record.localID)) ?? record
    }

    var body: some View {
        Group {
            if !record.canManage(as: config.session ?? DeviceSession(serverURL: "", userID: "", username: "", token: "", sessionID: "", expiresAt: "")) {
                ContentUnavailableView("Account changed", systemImage: "person.crop.circle", description: Text("Return to History in the current account."))
            } else if let receive {
                TransferDetailView(receiveViewModel: receive)
            } else {
                ScrollView {
                    VStack(spacing: 16) {
                        Text(current.safeDisplayTitle).font(.headline)
                        Text(current.statusText)
                        if let link = current.fullLink {
                            LinkCard(url: link)
                        } else {
                            Text("This device does not have the encryption key. You can manage this item, but open its full link on the device that created it.")
                        }
                        Text(current.summary)
                        if let limit = current.maxDownloads, limit > 0 {
                            Text("Up to \(limit) download attempts per file").font(.footnote)
                        }
                        DisclosureGroup("Technical details") { Text(current.id).font(.caption).textSelection(.enabled) }
                        if stale {
                            Text("Offline — showing last known status").foregroundStyle(PsstTheme.warning)
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
                    try await history.refreshSend(record, session: session)
                    guard config.session == session, SecretStore.session == session, !Task.isCancelled else { return }
                    stale = false
                    delay = 5
                } catch {
                    guard config.session == session, SecretStore.session == session, !Task.isCancelled else { return }
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
        if let customTitle, !customTitle.isEmpty {
            return customTitle
        }
        guard let title, !title.isEmpty else { return displayTitle }
        let name = GuestFiles.displayName(title)
        return fileCount > 1 ? name + " + \(fileCount - 1) files" : name
    }
}

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
    @State private var stale = false
    @State private var lastUpdated: Date?
    @State private var visible = false
    @State private var busy = false
    @State private var renaming: TransferRecord?
    @State private var renameText = ""
    private var records: [HistoryEntry] {
        HistoryEntry.combine(account: history.visible(for: config.session), downloads: guests.records, session: config.session, filter: filter)
    }

    var body: some View {
        NavigationStack {
            List {
                Picker("History filter", selection: $filter) {
                    ForEach(HistoryFilter.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }.pickerStyle(.menu)
                if history.legacyCount > 0 {
                    Text("Pre-account history is preserved. Manage pre-account server resources from the administrator website.").font(.footnote).foregroundStyle(PsstTheme.secondary)
                }
                if stale {
                    Label("Offline — showing last known status", systemImage: "wifi.slash").foregroundStyle(PsstTheme.warning)
                }
                if let lastUpdated {
                    HStack { Text("Last updated")
                        Text(lastUpdated, style: .relative)
                    }.font(.caption)
                }
                if records.isEmpty {
                    ContentUnavailableView("No transfers yet", systemImage: "clock", description: Text(filter == .downloaded ? "Files downloaded on this device will appear here, including while signed out." : "No items in this filter yet."))
                }
                ForEach(records) { entry in
                    switch entry {
                    case let .downloaded(record):
                        NavigationLink {
                            ScrollView { GuestDownloadDetail(record: record).padding() }.navigationTitle("Downloaded")
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                Label("Downloaded", systemImage: "arrow.down.doc").font(.caption)
                                Text((record.files.first?.name ?? "File transfer") + (record.files.count > 1 ? " + \(record.files.count - 1) files" : "")).font(.headline).lineLimit(1).truncationMode(.middle)
                                Text("\(record.files.count) files · " + ByteCountFormatter.string(fromByteCount: record.files.reduce(Int64(0)) { $0 + $1.size }, countStyle: .file)).font(.caption)
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
                                Text(record.displayTitle).font(.headline).lineLimit(1).truncationMode(.middle).accessibilityLabel(record.displayTitle)
                                Text(record.createdAt.formatted(date: .abbreviated, time: .shortened)).font(.caption)
                                Text(record.summary).font(.subheadline)
                                Text(record.statusText).font(.caption)
                                if let expiry = record.expiresAt {
                                    HStack { Text(LocalizedStringKey(record.isExpired ? "Expired" : "Expires"))
                                        Text(expiry, style: .relative)
                                    }.font(.caption)
                                }
                            }.padding(.vertical, 4)
                        }.swipeActions { Button("Revoke and delete", role: .destructive) { deleting = record }.disabled(busy) }
                            .contextMenu {
                                Button("Rename on this device") { renameText = record.customTitle ?? ""; renaming = record }
                            }
                            .swipeActions(edge: .leading) { Button("Rename") { renameText = record.customTitle ?? ""; renaming = record } }
                    }
                }
                if let error {
                    Text(error).foregroundStyle(PsstTheme.error)
                    if let failedDeletion {
                        Button("Retry revocation") { revoke(failedDeletion) }.disabled(busy)
                    }
                }
            }
            .navigationTitle("History")
            .alert("Rename on this device", isPresented: Binding(get: { renaming != nil }, set: {
                if !$0 {
                    renaming = nil
                }
            })) {
                TextField("Name (optional)", text: $renameText)
                Button("Save") {
                    if let record = renaming, let session = config.session {
                        do { try history.rename(record, name: renameText, session: session) }
                        catch { self.error = "Could not save the name. Check your account and retry." }
                    }
                    renaming = nil
                }
                Button("Cancel", role: .cancel) { renaming = nil }
            } message: { Text("Only this device uses this name. Leave it empty to restore the automatic title.") }
            .confirmationDialog("Remove from history? Saved files remain and the sender’s link keeps working.", isPresented: Binding(get: { removing != nil }, set: {
                if !$0 {
                    removing = nil
                }
            }), titleVisibility: .visible) {
                Button("Remove from history", role: .destructive) {
                    if let removing {
                        do { try guests.remove(removing) } catch { self.error = "Could not update local history. Free storage and retry." }
                    }
                    removing = nil
                }
            }
            .refreshable { _ = await refresh() }
            .toolbar { Button("Refresh") { Task { _ = await refresh() } }.disabled(busy) }
            .confirmationDialog("Revoke and delete?", isPresented: Binding(get: { deleting != nil }, set: {
                if !$0 {
                    deleting = nil
                }
            }), titleVisibility: .visible) {
                if let deleting {
                    Button("Revoke and delete", role: .destructive) { revoke(deleting) }
                }
                Button("Cancel", role: .cancel) { deleting = nil }
            } message: { Text("This disables the link and removes its server files. Receive links stop accepting uploads. Copies already saved by anyone remain.") }
            .onAppear { visible = true
                history.reload()
            }
            .onDisappear { visible = false }
            .task(id: "\(visible)-\(scenePhase)-\(config.accountID ?? "")") {
                guard visible, scenePhase == .active, config.isConfigured else { return }
                var delay: UInt64 = 5
                while !Task.isCancelled, !config.needsSignIn, config.isConfigured {
                    let success = await refresh()
                    delay = success ? 5 : min(30, delay * 2)
                    do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
                }
            }
            .onChange(of: config.accountID) { _, _ in renaming = nil
                deleting = nil
                failedDeletion = nil
                error = nil
                lastUpdated = nil
                stale = false
            }
        }.modifier(PsstStyle())
    }

    private func refresh() async -> Bool {
        guard !busy, let session = config.session, session.canTransfer, !config.needsSignIn else { return true }
        busy = true
        defer { busy = false }
        do { try await history.refresh(session: session)
            stale = false
            lastUpdated = Date()
            return true
        } catch { stale = true
            return false
        }
    }

    private func revoke(_ record: TransferRecord) {
        guard !busy, let session = config.session, session.canTransfer, !config.needsSignIn else { return }
        busy = true
        error = nil
        deleting = nil
        Task {
            defer { busy = false }
            do { try await history.revoke(record, session: session)
                failedDeletion = nil
            } catch { failedDeletion = record
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
    var current: TransferRecord {
        history.visible(for: config.session).first { $0.localID == record.localID } ?? record
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
                        Text(current.displayTitle).font(.headline)
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
        .task(id: scenePhase) {
            guard scenePhase == .active, record.isSlot != true else { return }
            var delay: UInt64 = 5
            while !Task.isCancelled, !config.needsSignIn, let session = config.session,
                  record.canManage(as: session), current.state != .revoked, !current.isExpired
            {
                do { try await history.refresh(session: session)
                    stale = false
                    delay = 5
                } catch { stale = true
                    delay = min(30, delay * 2)
                }
                do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
            }
        }
        .onDisappear { receive?.cancelSaving() }
    }
}

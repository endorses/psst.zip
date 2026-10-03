import SwiftUI

struct HistoryView: View {
    @Environment(TransferHistoryStore.self) private var history
    @Environment(ServerConfigManager.self) private var config
    @Environment(\.scenePhase) private var scenePhase
    @State private var localReceived = false
    @State private var receiveOnly = false
    @State private var deleting: TransferRecord?
    @State private var failedDeletion: TransferRecord?
    @State private var error: String?
    @State private var stale = false
    @State private var lastUpdated: Date?
    @State private var visible = false
    @State private var busy = false
    private var records: [TransferRecord] {
        history.visible(for: config.session).filter { ($0.isSlot == true) == receiveOnly }
    }

    var body: some View {
        NavigationStack {
            List {
                Button { localReceived = true } label: { Label("Received on this device", systemImage: "arrow.down.doc") }
                Picker("History filter", selection: $receiveOnly) {
                    Text("Sent").tag(false)
                    Text("Receive links").tag(true)
                }.pickerStyle(.segmented)
                if !config.isConfigured || config.needsSignIn {
                    LoginFields()
                }
                if history.legacyCount > 0 {
                    Text("Pre-account history is preserved. Only administrators signed in to its original server can manage it.").font(.footnote).foregroundStyle(PsstTheme.secondary)
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
                    ContentUnavailableView("No transfers yet", systemImage: "clock", description: Text("Your account’s links will appear here."))
                }
                ForEach(records) { record in
                    NavigationLink {
                        HistoryDetail(record: record)
                    } label: {
                        VStack(alignment: .leading, spacing: 6) {
                            if record.ownerID == nil {
                                Text("Legacy item").font(.caption)
                            }
                            Text(record.displayTitle).font(.headline).lineLimit(2)
                            Text(record.summary).font(.subheadline)
                            Text(record.statusText).font(.caption)
                            if let expiry = record.expiresAt {
                                HStack { Text(LocalizedStringKey(record.isExpired ? "Expired" : "Expires"))
                                    Text(expiry, style: .relative)
                                }.font(.caption)
                            }
                        }.padding(.vertical, 4)
                    }.swipeActions { Button("Revoke and delete", role: .destructive) { deleting = record }.disabled(busy) }
                }
                if let error {
                    Text(error).foregroundStyle(PsstTheme.error)
                    if let failedDeletion {
                        Button("Retry revocation") { revoke(failedDeletion) }.disabled(busy)
                    }
                }
            }
            .navigationTitle("History")
            .sheet(isPresented: $localReceived) { ScanReceiveView(historyOnly: true).modifier(PsstAppearance()) }
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
                while !Task.isCancelled {
                    let success = await refresh()
                    delay = success ? 5 : min(30, delay * 2)
                    do { try await Task.sleep(nanoseconds: delay * 1_000_000_000) } catch { return }
                }
            }
            .onChange(of: config.accountID) { _, _ in deleting = nil
                failedDeletion = nil
                error = nil
                lastUpdated = nil
            }
        }.modifier(PsstStyle())
    }

    private func refresh() async -> Bool {
        guard !busy, let session = config.session else { return true }
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
        guard !busy, let session = config.session else { return }
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
        history.visible(for: config.session).first { $0.id == record.id } ?? record
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
                        if stale {
                            Text("Offline — showing last known status").foregroundStyle(PsstTheme.warning)
                        }
                    }.padding()
                }.modifier(PsstStyle())
            }
        }
        .onAppear {
            if current.isSlot == true, current.fullLink != nil, receive == nil {
                receive = ReceiveViewModel(serverConfig: config, historyStore: history, record: current)
            }
        }
        .task(id: scenePhase) {
            guard scenePhase == .active, record.isSlot != true else { return }
            var delay: UInt64 = 5
            while !Task.isCancelled, let session = config.session, record.canManage(as: session) {
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

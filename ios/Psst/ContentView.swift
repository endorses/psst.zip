import SwiftUI

struct ContentView: View {
    @Environment(ServerConfigManager.self) private var config
    @Environment(TransferHistoryStore.self) private var history
    @Environment(\.scenePhase) private var scenePhase
    @Environment(GuestDownloadStore.self) private var guests
    @Environment(GuestTransferModel.self) private var guestTransfer
    @State private var settings = false
    @State private var selectedTab = 0
    @State private var historyFilter = HistoryFilter.all
    var body: some View {
        Group {
            if history.isReady, guests.isReady {
                workspace
            } else {
                VStack(spacing: 16) {
                    Text(L10n.text("Preparing local history")).font(.title2)
                    if let error = history.migrationError ?? guests.error {
                        Text(L10n.text(error))
                        Button(L10n.text("Retry")) { Task { await prepareHistory() } }
                    } else {
                        ProgressView()
                        Text(L10n.text(L10n.format("Imported %lld records. Your original history is preserved.", Int64(history.importedRecords + guests.importedRecords)))).font(.footnote)
                    }
                }.padding()
            }
        }.modifier(PsstStyle()).task { await prepareHistory() }
    }

    private func prepareHistory() async {
        await history.finishMigration()
        await guests.finishMigration()
    }

    private var workspace: some View {
        TabView(selection: $selectedTab) {
            HomeView(receiving: false).tabItem { Label(L10n.text("Home"), systemImage: "house") }.tag(0)
            ScanReceiveView(isSelected: selectedTab == 1 && !settings) {
                historyFilter = .downloaded
                selectedTab = 3
            }
            .tabItem { Label(L10n.text("Scan"), systemImage: "qrcode.viewfinder") }.tag(1)
            HomeView(receiving: true).tabItem { Label(L10n.text("Receive"), systemImage: "square.and.arrow.down") }.tag(2)
            HistoryView(filter: $historyFilter, onSend: { selectedTab = 0 }).tabItem { Label(L10n.text("History"), systemImage: "clock") }.tag(3)
        }
        .modifier(PsstStyle())
        .safeAreaInset(edge: .top) {
            HStack {
                Text(L10n.text(config.indicator)).font(.caption).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button {
                    settings = true
                } label: {
                    Image(systemName: "gearshape").frame(minWidth: 44, minHeight: 44)
                }.accessibilityLabel(L10n.text("Settings"))
            }.padding(.horizontal).background(PsstTheme.surface)
        }
        .safeAreaInset(edge: .bottom) {
            let status = DeviceRetryStatus.shared
            if status.importing || status.receiptError != nil || status.cleanupError != nil {
                VStack(alignment: .leading, spacing: 8) {
                    if status.importing {
                        ProgressView(L10n.text("Preparing saved confirmations and upload cleanup…"))
                    }
                    if let error = status.receiptError {
                        Text(L10n.text(error))
                    }
                    if let error = status.cleanupError {
                        Text(L10n.text(error))
                    }
                    Button(L10n.text("Retry pending work")) {
                        Task {
                            await DownloadAcknowledgements.shared.flush()
                            await GuestUploadCleanup.flush()
                        }
                    }.disabled(status.importing)
                }.font(.footnote).padding().frame(maxWidth: .infinity, alignment: .leading).background(PsstTheme.surface)
            }
        }
        .sheet(isPresented: $settings) { NavigationStack { ServerConfigView() } }
        .onChange(of: scenePhase) { _, next in
            if next == .background {
                guestTransfer.cancel()
            }
        }
        .task(id: scenePhase) {
            if scenePhase == .active {
                config.reload()
                await config.refreshAccount()
                history.reload()
                await DownloadAcknowledgements.shared.flush()
                await guests.reconcilePending()
                await guests.flushReceipts()
                if !guestTransfer.active {
                    await GuestUploadCleanup.flush()
                }
            }
        }
    }
}

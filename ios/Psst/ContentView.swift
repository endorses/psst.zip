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
            if history.isReady && guests.isReady {
                workspace
            } else {
                VStack(spacing: 16) {
                    Text("Preparing local history").font(.title2)
                    if let error = history.migrationError ?? guests.error {
                        Text(error)
                        Button("Retry") { Task { await prepareHistory() } }
                    } else {
                        ProgressView()
                        Text("Imported \(history.importedRecords + guests.importedRecords) records. Your original history is preserved.").font(.footnote)
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
            HomeView(receiving: false).tabItem { Label("Home", systemImage: "house") }.tag(0)
            ScanReceiveView(isSelected: selectedTab == 1 && !settings) {
                historyFilter = .downloaded
                selectedTab = 3
            }
            .tabItem { Label("Scan", systemImage: "qrcode.viewfinder") }.tag(1)
            HomeView(receiving: true).tabItem { Label("Receive", systemImage: "square.and.arrow.down") }.tag(2)
            HistoryView(filter: $historyFilter, onSend: { selectedTab = 0 }).tabItem { Label("History", systemImage: "clock") }.tag(3)
        }
        .modifier(PsstStyle())
        .safeAreaInset(edge: .top) {
            HStack {
                Text(config.indicator).font(.caption).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button {
                    settings = true
                } label: {
                    Image(systemName: "gearshape").frame(minWidth: 44, minHeight: 44)
                }.accessibilityLabel("Settings")
            }.padding(.horizontal).background(PsstTheme.surface)
        }
        .safeAreaInset(edge: .bottom) {
            let status = DeviceRetryStatus.shared
            if status.importing || status.receiptError != nil || status.cleanupError != nil {
                VStack(alignment: .leading, spacing: 8) {
                    if status.importing { ProgressView("Preparing saved confirmations and upload cleanup…") }
                    if let error = status.receiptError { Text(error) }
                    if let error = status.cleanupError { Text(error) }
                    Button("Retry pending work") {
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

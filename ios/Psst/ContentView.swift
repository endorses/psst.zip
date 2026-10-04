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
        TabView(selection: $selectedTab) {
            HomeView(receiving: false).tabItem { Label("Home", systemImage: "house") }.tag(0)
            ScanReceiveView(isSelected: selectedTab == 1 && !settings) { historyFilter = .downloaded; selectedTab = 3 }
                .tabItem { Label("Scan", systemImage: "qrcode.viewfinder") }.tag(1)
            HomeView(receiving: true).tabItem { Label("Receive", systemImage: "square.and.arrow.down") }.tag(2)
            HistoryView(filter: $historyFilter).tabItem { Label("History", systemImage: "clock") }.tag(3)
        }
        .modifier(PsstStyle())
        .safeAreaInset(edge: .top) {
            HStack {
                Text(config.indicator).font(.caption).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button { settings = true } label: { Image(systemName: "gearshape").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Settings")
            }.padding(.horizontal).background(PsstTheme.surface)
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
                await guests.flushReceipts()
                if !guestTransfer.active {
                    await GuestUploadCleanup.flush()
                }
            }
        }
    }
}

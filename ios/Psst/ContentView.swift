import SwiftUI

struct ContentView: View {
    @Environment(ServerConfigManager.self) private var config
    @Environment(TransferHistoryStore.self) private var history
    @Environment(\.scenePhase) private var scenePhase
    @Environment(GuestDownloadStore.self) private var guests
    @Environment(GuestTransferModel.self) private var guestTransfer
    @State private var settings = false
    var body: some View {
        TabView {
            ScanReceiveView().tabItem { Label("Scan", systemImage: "qrcode.viewfinder") }
            HomeView(receiving: false).tabItem { Label("Send", systemImage: "square.and.arrow.up") }
            HomeView(receiving: true).tabItem { Label("Receive", systemImage: "square.and.arrow.down") }
            HistoryView().tabItem { Label("History", systemImage: "clock") }
        }
        .modifier(PsstStyle())
        .safeAreaInset(edge: .top) {
            HStack {
                Text(config.indicator).font(.caption).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button { settings = true } label: { Image(systemName: "gearshape").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Settings")
            }.padding(.horizontal).background(PsstTheme.surface)
        }
        .onChange(of: config.needsSignIn) {
            _, required in if required {
                settings = true
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

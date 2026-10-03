import SwiftUI

struct ContentView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        Group {
            if serverConfig.isConfigured {
                TabView {
                    HomeView().tabItem { Label("Home", systemImage: "house") }
                    HistoryView().tabItem { Label("History", systemImage: "clock") }
                    ServerConfigView().tabItem { Label("Settings", systemImage: "gear") }
                }
            } else {
                NavigationStack {
                    ServerConfigView(isInitialSetup: true)
                }
            }
        }
        .task(id: scenePhase) {
            if scenePhase == .active {
                await DownloadAcknowledgements.shared.flush()
            }
        }
    }
}

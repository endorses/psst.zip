import SwiftUI

struct ContentView: View {
    @Environment(ServerConfigManager.self) private var serverConfig

    var body: some View {
        if serverConfig.isConfigured {
            TabView {
                Tab("Home", systemImage: "house") {
                    HomeView()
                }
                Tab("History", systemImage: "clock") {
                    HistoryView()
                }
                Tab("Settings", systemImage: "gear") {
                    ServerConfigView()
                }
            }
        } else {
            NavigationStack {
                ServerConfigView(isInitialSetup: true)
            }
        }
    }
}

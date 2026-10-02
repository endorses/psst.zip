import SwiftUI

struct ContentView: View {
    @Environment(ServerConfigManager.self) private var serverConfig

    var body: some View {
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
}

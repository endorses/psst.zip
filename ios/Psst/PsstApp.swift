import SwiftUI

@main
@MainActor
struct PsstApp: App {
    @State private var serverConfig = ServerConfigManager()
    @State private var historyStore = TransferHistoryStore()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(serverConfig)
                .environment(historyStore)
        }
    }
}

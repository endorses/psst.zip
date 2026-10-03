import SwiftUI

@main
@MainActor
struct PsstApp: App {
    @State private var serverConfig = ServerConfigManager()
    @State private var historyStore = TransferHistoryStore()
    @State private var guestStore: GuestDownloadStore
    @State private var guestTransfer: GuestTransferModel

    init() {
        let store = GuestDownloadStore()
        _guestStore = State(initialValue: store)
        _guestTransfer = State(initialValue: GuestTransferModel(store: store))
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(serverConfig)
                .environment(historyStore)
                .environment(guestStore)
                .environment(guestTransfer)
                .modifier(PsstAppearance())
        }
    }
}

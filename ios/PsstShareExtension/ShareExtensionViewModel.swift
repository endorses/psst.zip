import Foundation

@Observable
@MainActor
final class ShareExtensionViewModel {
    let config = ServerConfigManager()
    let history = TransferHistoryStore()
    var send: SendViewModel?
    var files: [URL] = []
    var error: String?
    var cancelRequested = false
    var fileCount: Int {
        files.count
    }

    var totalSize: Int64 {
        (try? BufferedUpload.sizes(files, limit: 10 * 1024 * 1024).reduce(0, +)) ?? 0
    }

    var active: Bool {
        send?.active == true
    }

    private var originAccount: String?
    func prepare(_ urls: [URL]) {
        do { _ = try BufferedUpload.sizes(urls, limit: 10 * 1024 * 1024)
            files = urls
        } catch { error = String(localized: "The share extension accepts files up to 10 MiB each because its memory is limited. Use the app for files up to 25 MiB.") }
    }

    func start() {
        guard config.isConfigured, !files.isEmpty else { return }
        if let originAccount, originAccount != config.accountID {
            cancel()
            error = String(localized: "The account changed. Share the files again to choose this account.")
            return
        }
        originAccount = config.accountID
        if send == nil {
            send = SendViewModel(fileURLs: files, serverConfig: config, historyStore: history, limit: 10 * 1024 * 1024)
        }
        send?.start()
    }

    func accountChanged() {
        guard let originAccount, originAccount != config.accountID else { return }
        send?.clearForAccountChange()
        send = nil
        files = []
        error = String(localized: "The account changed. Share the files again to choose this account.")
    }

    func cancel() {
        send?.stop()
    }
}

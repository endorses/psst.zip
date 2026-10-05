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
    var limitEnabled = false
    var limitValue = "1"
    var sharedTitle = ""
    var fileCount: Int {
        files.count
    }

    var totalSize: Int64 {
        (try? BufferedUpload.sizes(files, limit: BufferedUpload.maxFileBytes).reduce(0, +)) ?? 0
    }

    var active: Bool {
        send?.active == true
    }

    private var originAccount: String?
    func prepare(_ urls: [URL]) {
        do {
            _ = try BufferedUpload.sizes(urls, limit: BufferedUpload.maxFileBytes)
            files = urls
            originAccount = config.accountID
        } catch { self.error = String(localized: "Files are encrypted in chunks. The server sets the maximum file size.") }
    }

    func start() {
        guard config.isConfigured, !files.isEmpty, let maxDownloads = LinkLimit.parse(limitValue, enabled: limitEnabled) else { return }
        if let originAccount, originAccount != config.accountID {
            cancel()
            error = String(localized: "The account changed. Share the files again to choose this account.")
            return
        }
        do { _ = try SharedLinkTitle.normalize(sharedTitle) } catch {
            self.error = error.localizedDescription
            return
        }
        originAccount = config.accountID
        if send == nil {
            send = SendViewModel(
                fileURLs: files, serverConfig: config, historyStore: history, limit: BufferedUpload.maxFileBytes, maxDownloads: maxDownloads, sharedTitle: sharedTitle)
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

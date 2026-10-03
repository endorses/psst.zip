import Foundation
import Shared

enum SendState: Equatable {
    case idle, encrypting, uploading(progress: Double), complete, failed(String)
}

@Observable
@MainActor
final class SendViewModel {
    let fileURLs: [URL]
    private(set) var state: SendState = .idle
    private(set) var shareURL: String?
    private(set) var expiresAt: Date?
    private(set) var progress: UploadProgress?
    private(set) var currentFile = ""
    private(set) var record: TransferRecord?
    private(set) var job: Task<Void, Never>?
    var selectionSize: Int64 {
        (try? BufferedUpload.sizes(fileURLs, limit: limit).reduce(0, +)) ?? 0
    }

    var fileNames: [String] {
        fileURLs.map(\.lastPathComponent)
    }

    var active: Bool {
        if case .encrypting = state {
            return true
        }
        if case .uploading = state {
            return true
        }
        return false
    }

    let limit: Int
    private let serverConfig: ServerConfigManager
    private let historyStore: TransferHistoryStore
    private var client: ApiClient?
    private var origin: DeviceSession?
    private var runID = UUID()
    private var starting = false

    init(fileURLs: [URL], serverConfig: ServerConfigManager, historyStore: TransferHistoryStore,
         limit: Int = BufferedUpload.maxFileBytes)
    {
        self.fileURLs = fileURLs
        self.serverConfig = serverConfig
        self.historyStore = historyStore
        self.limit = limit
    }

    func start() {
        guard !active, !starting else { return }
        starting = true
        job = Task {
            defer { starting = false }
            await startUpload()
        }
    }

    func stop() {
        guard active else { return }
        if var record {
            record.state = .failed
            try? historyStore.update(record)
            self.record = record
        }
        job?.cancel()
        client?.close()
    }

    func clearForAccountChange() {
        stop()
        shareURL = nil
        record = nil
        state = .idle
    }

    func startUpload() async {
        guard !active else { return }
        state = .encrypting
        let run = UUID()
        runID = run
        do {
            let session = try serverConfig.requireSession()
            // A retry is bound to its first account; logging into another account never reuses files silently.
            if let origin, origin.accountID != session.accountID {
                throw AccountError.changed
            }
            origin = session
            let sizes = try BufferedUpload.sizes(fileURLs, limit: limit)
            if let previous = record {
                try await historyStore.revoke(previous, session: session)
                record = nil
            }
            let client = serverConfig.makeApiClient(session: session)
            self.client = client
            defer { client.close()
                self.client = nil
            }
            let key = try CryptoProvider.shared.generateKey()
            let transfer = try await client.transfers.create()
            guard UUID(uuidString: transfer.id) != nil else { throw AccountError.request }
            let url = UrlHelper.shared.buildDownloadUrl(baseUrl: session.serverURL, transferId: transfer.id, key: key)
            var entry = TransferRecord(id: transfer.id, direction: .sent, state: .inProgress,
                                       createdAt: Date(), expiresAt: ServerTimestamp.parse(transfer.expiresAt),
                                       fileCount: fileURLs.count, totalSize: sizes.reduce(0, +), shareURL: nil,
                                       serverURL: session.serverURL, ownerID: session.userID, title: fileNames.first, isSlot: false)
            try historyStore.add(entry)
            record = entry
            try entry.saveSecrets(link: url, deletionToken: transfer.deleteToken)
            // The allocation was persisted before checking cancellation, so it remains revocable.
            try serverConfig.check(session)
            let metadata = try await BufferedUpload.send(fileURLs: fileURLs, client: client, transferId: transfer.id,
                                                         key: key, limit: limit, check: { try self.serverConfig.check(session) },
                                                         preparing: { self.currentFile = $0
                                                             self.state = .encrypting
                                                         }, progress: { value in
                                                             guard self.runID == run, self.active else { return }
                                                             guard JobIdentity(session: session).accepts(SecretStore.session, cancelled: self.job?.isCancelled == true) else { self.client?.close()
                                                                 return
                                                             }
                                                             self.progress = value
                                                             self.state = .uploading(progress: value.fraction)
                                                         })
            let manifest = try ManifestSerializer.encode(manifest: Manifest(files: metadata))
            let nonce = try CryptoProvider.shared.generateNonce()
            let encrypted = try CryptoProvider.shared.encrypt(key: key, nonce: nonce, plaintext: Data(manifest.utf8).toKotlinByteArray())
            try serverConfig.check(session)
            try await client.transfers.uploadManifest(transferId: transfer.id, manifestBytes: (nonce.toData() + encrypted.toData()).toKotlinByteArray())
            try await client.transfers.complete(transferId: transfer.id)
            try serverConfig.check(session)
            entry.state = .complete
            try historyStore.update(entry)
            record = entry
            shareURL = url
            expiresAt = entry.expiresAt
            state = .complete
        } catch {
            // Confirm session validity after an authenticated upload fails without exposing server error bodies.
            if !Task.isCancelled, let origin, SecretStore.session == origin {
                _ = try? await AccountHTTP.request(server: origin.serverURL, path: "auth/me", token: origin.token)
            }
            if var record {
                record.state = .failed
                try? historyStore.update(record)
                self.record = record
            }
            if let record, let origin, Task.isCancelled || SecretStore.session != origin {
                let token = record.capabilities?.deletionToken ?? origin.token
                let server = origin.serverURL
                let resourceID = record.id
                // This short, independent cleanup is allowed to finish after the upload task is cancelled.
                // iOS may terminate an extension immediately; its persisted failed row remains actionable.
                let removed = await Task.detached {
                    do {
                        _ = try await AccountHTTP.request(server: server, path: "transfers/" + resourceID, method: "DELETE", token: token)
                        return true
                    } catch { return false }
                }.value
                if removed {
                    do { try historyStore.remove(record)
                        self.record = nil
                    } catch { /* Retry revocation removes the retained row after a 404. */ }
                    state = .failed(String(localized: "Upload stopped. The server files were removed."))
                    return
                }
            }
            state = .failed(record == nil ? String(localized: "Upload could not start. Sign in or check your connection, then retry.") : String(localized: "Upload stopped. Its server record remains in History; retry or revoke it there."))
        }
    }
}

import Foundation

enum TransferDirection: String, Codable { case sent, received }
enum TransferState: String, Codable { case inProgress, complete, expired, failed, downloaded, started, saved, revoked, exhausted }

/// Optional fields preserve decoding of installed users' original history.
struct TransferRecord: Identifiable, Codable {
    let id: String
    let direction: TransferDirection
    var state: TransferState
    let createdAt: Date
    var expiresAt: Date?
    var fileCount: Int
    var totalSize: Int64
    var shareURL: String?
    var serverURL: String? = nil
    var ownerID: String? = nil
    var title: String? = nil
    var customTitle: String? = nil
    var sharedTitle: String? = nil
    var isSlot: Bool? = nil
    // Legacy import fields only. Normal storage moves them to indexed checkpoints.
    var savedFiles: [String: String]? = nil
    var savedTransfers: [String]? = nil
    var checkpointVersion: Int? = nil
    var receiveProtocol: Int? = nil
    var maxFiles: Int? = nil
    var reservedFiles: Int64? = nil
    var maxDownloads: Int? = nil
    var serverSummaryKnown: Bool? = nil

    var canDecryptInbox: Bool {
        guard isSlot == true else { return false }
        if receiveProtocol == 2 || fullLink?.contains("#v2.") == true {
            return capabilities?.receivePrivateKey?.count == 32
        }
        return fullLink != nil
    }

    var statusText: String {
        if isExpired {
            return L10n.text("Expired")
        }
        switch state {
        case .inProgress: return isSlot == true ? L10n.text("Waiting for files") : L10n.text("Uploading")
        case .complete: return isSlot == true ? L10n.text("Files received") : L10n.text("Ready to download")
        case .downloaded: return L10n.text("Downloaded")
        case .started: return L10n.text("Download started")
        case .saved: return L10n.text("Files saved locally")
        case .expired: return L10n.text("Expired")
        case .revoked: return L10n.text("Revoked")
        case .exhausted: return L10n.text("Download limit reached")
        case .failed: return L10n.text("Upload stopped")
        }
    }

    var summary: String {
        if serverSummaryKnown == false {
            return L10n.text("File count updating")
        }
        if serverSummaryKnown != nil, totalSize == 0 {
            return L10n.text(L10n.format("%lld files", Int64(fileCount)))
        }
        return L10n.text(L10n.format("%lld files · %@", Int64(fileCount), L10n.bytes(totalSize)))
    }

    var linkPolicySummary: String? {
        if isSlot == true {
            guard let maxFiles else { return nil }
            if maxFiles > 0 {
                guard let reservedFiles else {
                    return L10n.text(L10n.format("Maximum %lld files · Allowance use updating", Int64(maxFiles)))
                }
                return L10n.text(L10n.format("%lld of %lld file allowances used", reservedFiles, Int64(maxFiles)))
            }
            if let reservedFiles {
                return L10n.text(L10n.format("%lld file allowances used · No optional file-count limit", reservedFiles))
            }
            return L10n.text("No optional file-count limit")
        }
        guard let maxDownloads else { return nil }
        return maxDownloads > 0
            ? L10n.text(L10n.format("%lld download attempts per file", Int64(maxDownloads)))
            : L10n.text("No optional download limit")
    }

    var linkActive: Bool {
        !isExpired && state != .revoked && state != .exhausted
    }

    var displayTitle: String {
        if let sharedTitle, !sharedTitle.isEmpty {
            return sharedTitle
        }
        if let name = customTitle, !name.isEmpty {
            return name
        }
        if let title, !title.isEmpty {
            return fileCount > 1 ? title + L10n.text(L10n.format(" + %lld files", Int64(fileCount - 1))) : title
        }
        return L10n.text(isSlot == true ? "Receive link" : "Sent files")
    }

    var isExpired: Bool {
        expiresAt.map { $0 < Date() } ?? (state == .expired)
    }

    func preservingLocalName(from existing: TransferRecord?) -> TransferRecord {
        var refreshed = self
        if let existing, existing.localID == localID {
            refreshed.customTitle = existing.customTitle
            refreshed.title = refreshed.title ?? existing.title
        }
        return refreshed
    }

    var localID: String {
        vaultID + (isSlot == true ? "|slot" : "|transfer")
    }

    var vaultID: String {
        "resource|" + (serverURL ?? "legacy") + "|" + (ownerID ?? "legacy") + "|" + id
    }

    var capabilities: ResourceSecrets? {
        guard let data = SecretStore.read(vaultID) else { return nil }
        return try? JSONDecoder().decode(ResourceSecrets.self, from: data)
    }

    var fullLink: String? {
        capabilities?.link ?? shareURL
    }

    func saveSecrets(link: String?, deletionToken: String?, receivePrivateKey: Data? = nil) throws {
        try SecretStore.write(
            JSONEncoder().encode(ResourceSecrets(link: link, deletionToken: deletionToken, receivePrivateKey: receivePrivateKey ?? capabilities?.receivePrivateKey)), name: vaultID
        )
    }

    func belongs(to session: DeviceSession) -> Bool {
        serverURL == session.serverURL && ownerID == session.userID
    }

    func canManage(as session: DeviceSession) -> Bool {
        session.canTransfer && belongs(to: session)
    }
}

struct ResourceSecrets: Codable {
    let link: String?
    let deletionToken: String?
    var receivePrivateKey: Data? = nil
}

import Foundation

enum TransferDirection: String, Codable { case sent, received }
enum TransferState: String, Codable { case inProgress, complete, expired, failed, downloaded, started, saved, revoked }

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
    var isSlot: Bool? = nil
    var savedFiles: [String: String]? = nil
    var savedTransfers: [String]? = nil
    var statusText: String {
        if isExpired {
            return String(localized: "Expired")
        }
        switch state {
        case .inProgress: return isSlot == true ? String(localized: "Waiting for files") : String(localized: "Uploading")
        case .complete: return isSlot == true ? String(localized: "Files received") : String(localized: "Ready to download")
        case .downloaded: return String(localized: "Downloaded")
        case .started: return String(localized: "Download started")
        case .saved: return String(localized: "Files saved locally")
        case .expired: return String(localized: "Expired")
        case .revoked: return String(localized: "Revoked")
        case .failed: return String(localized: "Upload stopped")
        }
    }

    var summary: String {
        String(format: String(localized: "%lld files · %@"), Int64(fileCount), ByteCountFormatter.string(fromByteCount: totalSize, countStyle: .file))
    }

    var displayTitle: String {
        title ?? (isSlot == true ? String(localized: "Receive link") : String(localized: "Sent files"))
    }

    var isExpired: Bool {
        expiresAt.map { $0 < Date() } ?? (state == .expired)
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

    func saveSecrets(link: String?, deletionToken: String?) throws {
        try SecretStore.write(JSONEncoder().encode(ResourceSecrets(link: link, deletionToken: deletionToken)), name: vaultID)
    }

    func belongs(to session: DeviceSession) -> Bool {
        serverURL == session.serverURL && ownerID == session.userID
    }

    func canManage(as session: DeviceSession) -> Bool {
        belongs(to: session) || (ownerID == nil && session.role == "admin" && serverURL == session.serverURL)
    }
}

struct ResourceSecrets: Codable { let link: String?
    let deletionToken: String?
}

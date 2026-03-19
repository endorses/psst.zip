import Foundation

/// The direction of a transfer from the user's perspective.
enum TransferDirection: String, Codable {
    case sent
    case received
}

/// The current state of a transfer record.
enum TransferState: String, Codable {
    case inProgress
    case complete
    case expired
    case failed
}

/// A locally-stored record of a transfer or drop slot for history display.
struct TransferRecord: Identifiable, Codable {
    let id: String
    let direction: TransferDirection
    var state: TransferState
    let createdAt: Date
    var expiresAt: Date?
    var fileCount: Int
    var totalSize: Int64
    var shareURL: String?

    /// Human-readable summary of file count and size.
    var summary: String {
        let sizeString = ByteCountFormatter.string(fromByteCount: totalSize, countStyle: .file)
        if fileCount == 1 {
            return "1 file (\(sizeString))"
        }
        return "\(fileCount) files (\(sizeString))"
    }

    var isExpired: Bool {
        if let expiresAt {
            return expiresAt < Date()
        }
        return state == .expired
    }
}

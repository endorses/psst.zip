import Foundation

/// Locally validated upload facts, never server-controlled error descriptions.
enum LocalUploadFailure: Error, Equatable {
    static let maximumFileBytes: Int64 = 1024 * 1024 * 1024 * 1024
    case invalidSelection
    case fileTooLarge(limitBytes: Int64)

    static func validateSize(_ size: Int64, limitBytes: Int64) throws {
        guard size >= 0, limitBytes > 0, limitBytes <= maximumFileBytes else {
            throw LocalUploadFailure.invalidSelection
        }
        guard size <= limitBytes else {
            throw LocalUploadFailure.fileTooLarge(limitBytes: limitBytes)
        }
    }
}

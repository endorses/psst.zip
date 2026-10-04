import Foundation

enum GuestUploadSelectionError: LocalizedError {
    case invalidFiles, changedFiles, unavailable, exhausted, tooMany, tooLarge

    var errorDescription: String? {
        switch self {
        case .invalidFiles: "Choose up to 100 readable files within this server’s per-file limit. Your previous selection is unchanged."
        case .changedFiles: "A selected file changed. Refresh the selection before sending again."
        case .unavailable: "Receive capacity could not be checked. Refresh and try again; your files are still selected."
        case .exhausted: "This receive link cannot accept these files right now. Remove files or refresh and try again."
        case .tooMany: "Too many files for the remaining receive capacity. Remove files or refresh and try again."
        case .tooLarge: "The selected files exceed the remaining receive capacity. Remove files or refresh and try again."
        }
    }
}

/// Small, portable checks shared by the picker and streaming uploader. Wire sizes come from
/// the shared cryptographic implementation, including the authenticated empty-file frame.
enum GuestUploadSelection {
    static func totalWireBytes(_ wireSizes: [Int64]) throws -> Int64 {
        guard wireSizes.count <= 100 else { throw GuestUploadSelectionError.invalidFiles }
        return try wireSizes.reduce(0) { total, size in
            guard size >= 60 else { throw GuestUploadSelectionError.invalidFiles }
            let (next, overflow) = total.addingReportingOverflow(size)
            guard !overflow else { throw GuestUploadSelectionError.tooLarge }
            return next
        }
    }

    static func requireUnchanged(_ sizes: [Int64], expected: [Int64]?) throws {
        if let expected, sizes != expected { throw GuestUploadSelectionError.changedFiles }
    }

    static func requireManifestCapacity(plainBytes: Int, reserve: Int64) throws {
        // PSSTRCV2 magic + wrapped submission key + manifest nonce and GCM tag.
        guard plainBytes >= 0, reserve >= 116, reserve <= 1_048_576,
            Int64(plainBytes) <= reserve - 116
        else { throw GuestUploadSelectionError.tooLarge }
    }
}

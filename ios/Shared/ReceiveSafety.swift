import Foundation

/// Device policy applies even when a scanned server advertises no upload limit.
enum ReceiveSafety {
    static let automaticBytes: Int64 = 100 * 1024 * 1024
    static let reserveBytes: Int64 = 256 * 1024 * 1024
    static let maximumTotalBytes: Int64 = 1024 * 1024 * 1024 * 1024

    static func total(_ sizes: [Int64]) throws -> Int64 {
        var result: Int64 = 0
        for size in sizes {
            guard size >= 0, size <= maximumTotalBytes - result else { throw ReceiveSafetyError.tooLarge }
            result += size
        }
        return result
    }

    static func checkCapacity(available: Int64, additional: Int64) throws {
        guard additional >= 0, available >= reserveBytes, additional <= available - reserveBytes else {
            throw ReceiveSafetyError.storage
        }
    }

    static func checkSpace(at directory: URL, additional: Int64) throws {
        let values = try FileManager.default.attributesOfFileSystem(forPath: directory.path)
        guard let free = values[.systemFreeSize] as? NSNumber else { throw ReceiveSafetyError.storage }
        try checkCapacity(available: free.int64Value, additional: additional)
    }
}

enum ReceiveSafetyError: LocalizedError {
    case tooLarge, storage, changed
    var errorDescription: String? {
        switch self {
        case .tooLarge: "This transfer exceeds the supported total size. Ask for smaller transfers."
        case .storage: "Not enough free space to receive these files while keeping 256 MiB available. Free some storage and retry."
        case .changed: "The file list changed since this transfer was inspected. Already saved files are safe. Ask the sender for a new link."
        }
    }
}

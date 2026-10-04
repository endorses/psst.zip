import Foundation

/// Empty means unlimited. Reject fractional, negative, overflow and non-ASCII input.
enum LinkLimit {
    static func parse(_ text: String, enabled: Bool) -> Int32? {
        guard enabled else { return 0 }
        guard !text.isEmpty, text.utf8.allSatisfy({ $0 >= 48 && $0 <= 57 }),
              let value = Int32(text), value > 0 else { return nil }
        return value
    }
}

enum LinkLimitError: LocalizedError {
    case unsupportedServer
    var errorDescription: String? {
        "This server does not support the requested private receive protocol or link limit. Ask its administrator to update it before creating this link."
    }
}

import Foundation

/// Go emits RFC3339 timestamps with optional fractional seconds, including nanosecond precision.
/// Keep parsing consistent for resource creation, expiry, and refreshed account history.
enum ServerTimestamp {
    static func parse(_ raw: String?) -> Date? {
        guard let raw else { return nil }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let value = formatter.date(from: raw) {
            return value
        }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: raw)
    }
}

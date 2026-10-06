import Foundation

// Only platform/account boundaries are stubbed; store/checkpoint logic is copied verbatim.
extension String { init(localized value: String) { self = value } }
enum FileProtectionType { static let complete = "portable" }
extension FileAttributeKey { static let protectionKey = FileAttributeKey(rawValue: "NSFileProtectionKey") }
extension Data.WritingOptions { static let completeFileProtection: Self = [] }
extension FileManager { func containerURL(forSecurityApplicationGroupIdentifier: String) -> URL? { nil } }
final class NSFileCoordinator {
    struct WritingOptions: OptionSet {
        let rawValue: Int
        static let forReplacing = Self(rawValue: 1)
    }
    func coordinate(writingItemAt url: URL, options: WritingOptions, error: inout NSError?, byAccessor: (URL) -> Void) {
        byAccessor(url)
    }
}
struct DeviceSession: Equatable {
    var serverURL: String
    var userID: String
    var username: String
    var token: String
    var sessionID: String
    var expiresAt: String
    var accountID: String { serverURL + "|" + userID }
    var canTransfer = true
}
enum AccountError: Error { case storage, changed, request, unavailable }
enum HistoryFilter { case all, sent, receive }
enum AccountHTTP {
    static func origin(_ value: String) throws -> String { value }
    static func request(
        server: String, path: String, method: String = "GET", token: String, body: [String: String]? = nil,
        maximumBytes: Int = 1_048_576, timeout: TimeInterval = 15
    )
        async throws -> Data
    { throw AccountError.storage }
}
enum AppConstants {
    static let sharedDefaults = UserDefaults.standard
    static let appGroupIdentifier = "portable"
    static let transferHistoryKey = "transferHistory"
    static let serverURLKey = "server"
}
enum SecretStore {
    static var session: DeviceSession?
    private static var values: [String: Data] = [:]
    static func read(_ key: String) -> Data? { values[key] }
    static func write(_ value: Data, name: String) throws { values[name] = value }
    static func remove(_ key: String) { values.removeValue(forKey: key) }
}

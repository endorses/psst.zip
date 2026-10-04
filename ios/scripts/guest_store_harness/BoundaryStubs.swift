import CryptoKit
import Foundation

// Apple protection and keychain are deliberately absent on Linux.
enum FileProtectionType { static let complete = "portable-no-protection" }
extension FileAttributeKey { static let protectionKey = FileAttributeKey(rawValue: "NSFileProtectionKey") }
extension Data.WritingOptions { static let completeFileProtection: Self = [] }
extension Data {
    func toKotlinByteArray() -> Data { self }
    func toData() -> Data { self }
}
enum AccountError: Error { case storage }
@MainActor enum SecretStore {
    private static var values: [String: Data] = [:]
    static var readFails = false
    static func read(_ name: String) -> Data? { values[name] }
    static func readStrict(_ name: String) throws -> Data? {
        if readFails { throw AccountError.storage }
        return values[name]
    }
    static func write(_ data: Data, name: String) throws { values[name] = data }
    static func remove(_ name: String) { values.removeValue(forKey: name) }
}
enum StreamedFiles {
    private static let lock = NSLock()
    private static var hook: (() throws -> Void)?
    static func setHook(_ value: (() throws -> Void)?) {
        lock.lock()
        defer { lock.unlock() }
        hook = value
    }
    static func digest(_ url: URL) throws -> String {
        lock.lock()
        let callback = hook
        lock.unlock()
        try callback?()
        return SHA256.hash(data: try Data(contentsOf: url)).map { String(format: "%02x", $0) }.joined()
    }
}
enum GuestNetwork {
    static func client(_ origin: String) throws -> Client { throw AccountError.storage }
    final class Client {
        let transfers = Transfers()
        func close() {}
    }
    final class Transfers {
        func acknowledgeDownload(transferId: String) async throws { throw AccountError.storage }
    }
}

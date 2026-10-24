import Foundation

/// Only account/Keychain and provider-selection boundaries are absent from this
/// Foundation/crypto/presentation target. Their platform implementations are not tested here.
struct DeviceSession: Equatable {
    var serverURL: String
    var userID: String
    var canTransfer = true
}

enum SecretStore {
    static func read(_: String) -> Data? {
        nil
    }

    static func write(_: Data, name _: String) throws {}
}

enum AccountError: LocalizedError {
    case request
    var errorDescription: String? {
        nil
    }
}

enum ShareSelectionError: Error {
    case tooLarge
    var message: String {
        ""
    }
}

import Foundation

#if os(Linux)
    extension String { init(localized value: String) {
        self = value
    } }
#endif
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

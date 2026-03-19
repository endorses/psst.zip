import Foundation

enum AppConstants {
    static let appGroupIdentifier = "group.zip.psst.ios"
    static let serverURLKey = "serverURL"
    static let transferHistoryKey = "transferHistory"
    static let defaultServerURL = "https://drop.example.com"

    /// UserDefaults backed by the App Group, shared between main app and share extension.
    static var sharedDefaults: UserDefaults {
        UserDefaults(suiteName: appGroupIdentifier) ?? .standard
    }
}

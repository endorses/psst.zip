import Foundation

/// A deliberately invalid control. This must fail under strict Swift 6 concurrency.
@MainActor final class UnsafeOwner {
    private var token: NSObjectProtocol?
    deinit {
        if let token {
            NotificationCenter.default.removeObserver(token)
        }
    }
}

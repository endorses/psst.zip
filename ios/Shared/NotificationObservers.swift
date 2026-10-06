import Foundation

/// Own registrations separately from UI actors so their cleanup can run on any executor.
/// Registration stays confined to the actor that owns this collection.
final class NotificationObservers {
    private let center: NotificationCenter
    private var tokens: [NSObjectProtocol] = []

    init(center: NotificationCenter = .default) {
        self.center = center
    }

    func observe(
        name: Notification.Name,
        object: Any? = nil,
        queue: OperationQueue? = nil,
        using block: @escaping @Sendable (Notification) -> Void
    ) {
        tokens.append(center.addObserver(forName: name, object: object, queue: queue, using: block))
    }

    deinit {
        tokens.forEach { center.removeObserver($0) }
    }
}

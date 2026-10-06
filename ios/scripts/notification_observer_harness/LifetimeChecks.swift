import Foundation
import Synchronization

private final class Counter: Sendable {
    private let value = Mutex(0)
    var count: Int { value.withLock { $0 } }
    func increment() { value.withLock { $0 += 1 } }
}

/// Mirrors actor-owned storage in the configuration manager and camera controller.
/// The callbacks intentionally do not capture this owner: cleanup must remove registrations.
@MainActor
private final class ObserverOwner {
    private let observers: NotificationObservers

    init(center: NotificationCenter, first: Counter, second: Counter) {
        observers = NotificationObservers(center: center)
        observers.observe(name: Notification.Name("first")) { _ in
            first.increment()
        }
        observers.observe(name: Notification.Name("second")) { _ in
            second.increment()
        }
    }
}

@main
private struct LifetimeChecks {
    @MainActor
    static func main() async {
        let center = NotificationCenter()
        let first = Counter()
        let second = Counter()
        let unrelated = Counter()
        let sibling = NotificationObservers(center: center)
        sibling.observe(name: Notification.Name("first")) { _ in
            unrelated.increment()
        }

        let mainLease = makeOwner(center: center, first: first, second: second)
        postBoth(center)
        precondition(first.count == 1 && second.count == 1)
        mainLease.release()
        postBoth(center)
        precondition(
            first.count == 1 && second.count == 1,
            "Main actor owner release must remove both registrations")
        precondition(
            unrelated.count == 2,
            "Releasing one owner must preserve another owner's registration")

        let backgroundLease = makeOwner(center: center, first: first, second: second)
        postBoth(center)
        precondition(first.count == 2 && second.count == 2)
        // Retain exactly once beyond the factory's scope, then drop the final reference
        // away from its UI actor. Only the actor-independent registration owner deinitializes.
        await Task.detached { backgroundLease.release() }.value
        postBoth(center)
        precondition(
            first.count == 2 && second.count == 2,
            "Background final release must remove both registrations")
        precondition(unrelated.count == 4)
        withExtendedLifetime(sibling) {}
        print("4 observer lifetime checks passed under Swift 6 strict concurrency")
    }

    @MainActor
    private static func makeOwner(center: NotificationCenter, first: Counter, second: Counter) -> Unmanaged<
        ObserverOwner
    > {
        Unmanaged.passRetained(ObserverOwner(center: center, first: first, second: second))
    }

    private static func postBoth(_ center: NotificationCenter) {
        center.post(name: Notification.Name("first"), object: nil)
        center.post(name: Notification.Name("second"), object: nil)
    }
}

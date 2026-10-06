import Foundation

// Only the exported error shapes needed by the production presenters. This fake
// module makes canImport(Shared) true; it does not validate Kotlin/Native bridging
// or reproduce the Kotlin classifier. Tests supply its classification result.
public class KotlinThrowable: NSObject {
    fileprivate let fixtureDescription: FailureDescription?

    public init(fixtureDescription: FailureDescription? = nil) {
        self.fixtureDescription = fixtureDescription
    }
}

public final class FailureDescription {
    public let code: String
    public let arguments: [String: String]

    public init(code: String, arguments: [String: String] = [:]) {
        self.code = code
        self.arguments = arguments
    }
}

public final class FailureDescriptions {
    public static let shared = FailureDescriptions()

    public func describe(error: KotlinThrowable) -> FailureDescription? {
        error.fixtureDescription
    }
}

extension NSError {
    // Kotlin/Native's exported property is Any?, not KotlinThrowable?. Keeping
    // that signature reproduces the Swift type error observed in native CI.
    public var kotlinException: Any? { userInfo["HarnessKotlinException"] }
}

public final class PublicTransfersPausedException: KotlinThrowable {}
public final class ResourceRevokedException: KotlinThrowable {}
public final class TrafficAccountingUnavailableException: KotlinThrowable {}
public final class TrafficPolicyChangedException: KotlinThrowable {}
public final class TrafficBudgetExhaustedException: KotlinThrowable {
    public let retryAt: String?

    public init(retryAt: String?) {
        self.retryAt = retryAt
        super.init()
    }
}

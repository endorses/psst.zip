import Foundation
import Shared
import XCTest

@testable import LocalizationHarness

/// Exercise the real presenter against a fake exported boundary, not native bridging.
final class SharedFailureBridgeTests: XCTestCase {
    private let fallback = "Private fallback"

    private func bridgedError(_ exception: Any) -> NSError {
        NSError(
            domain: "Harness", code: 1,
            userInfo: [
                "HarnessKotlinException": exception,
                NSLocalizedDescriptionKey: "Private server prose",
            ])
    }

    func testClassifiedThrowableUsesItsCode() {
        let exception = KotlinThrowable(fixtureDescription: FailureDescription(code: "invalid_credentials"))
        XCTAssertEqual(
            L10n.failure(bridgedError(exception), fallback: fallback),
            "Check your username and password, then try again.")
    }

    func testUnclassifiedThrowableKeepsFallback() {
        XCTAssertEqual(L10n.failure(bridgedError(KotlinThrowable()), fallback: fallback), fallback)
    }

    func testForeignOrAbsentExceptionKeepsFallback() {
        XCTAssertEqual(L10n.failure(bridgedError("Private foreign value"), fallback: fallback), fallback)
        XCTAssertEqual(L10n.failure(NSError(domain: "Harness", code: 1), fallback: fallback), fallback)
    }

    func testKnownIncidentTakesPriorityOverDescription() {
        let exception = PublicTransfersPausedException(
            fixtureDescription: FailureDescription(code: "invalid_credentials"))
        XCTAssertEqual(
            L10n.failure(bridgedError(exception), fallback: fallback), TransferIncident.paused.errorDescription)
    }
}

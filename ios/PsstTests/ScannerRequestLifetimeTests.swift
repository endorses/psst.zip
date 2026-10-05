import Foundation
@testable import Psst
import XCTest

@MainActor
final class ScannerRequestLifetimeTests: XCTestCase {
    func testBackLeavesWhileUnreachableServerIgnoresCancellationAndLateReplyCannotReopen() async {
        let lifetime = ScannerRequestLifetime()
        let request = lifetime.begin()
        var reply: CheckedContinuation<String, Never>?
        var uploadPresented = true
        var checking = true
        var transportClosed = false
        var lateResultApplied = false
        let task = Task {
            let response = await withCheckedContinuation { reply = $0 }
            guard lifetime.isCurrent(request) else { return }
            lateResultApplied = true
            uploadPresented = response == "ready"
            checking = false
            lifetime.finish(request)
        }
        lifetime.attach(task, request: request)
        lifetime.attachTransport(request: request) { transportClosed = true }
        while reply == nil {
            await Task.yield()
        }

        // The view's Back action performs these synchronous operations, without awaiting the task.
        lifetime.cancel()
        checking = false
        uploadPresented = false
        XCTAssertFalse(uploadPresented)
        XCTAssertFalse(checking)
        XCTAssertTrue(transportClosed)
        XCTAssertTrue(task.isCancelled)
        XCTAssertFalse(lifetime.isCurrent(request))

        reply?.resume(returning: "ready")
        await task.value
        XCTAssertFalse(lateResultApplied)
        XCTAssertFalse(uploadPresented)
    }

    func testNewScanIgnoresPreviousFailureAndPreviousCompletionCannotFinishNewRequest() async {
        let lifetime = ScannerRequestLifetime()
        let old = lifetime.begin()
        var reply: CheckedContinuation<Void, Never>?
        var visibleError: String?
        let task = Task {
            await withCheckedContinuation { reply = $0 }
            if lifetime.isCurrent(old) {
                visibleError = "unreachable"
            }
            lifetime.finish(old)
        }
        lifetime.attach(task, request: old)
        while reply == nil {
            await Task.yield()
        }
        lifetime.cancel()
        let next = lifetime.begin()
        reply?.resume()
        await task.value
        XCTAssertNil(visibleError)
        XCTAssertTrue(lifetime.isCurrent(next))
        lifetime.finish(next)
        XCTAssertFalse(lifetime.isCurrent(next))
    }

    func testTransportAttachedAfterDepartureClosesImmediately() async {
        let lifetime = ScannerRequestLifetime()
        let request = lifetime.begin()
        lifetime.cancel()
        await Task.yield()
        var closed = false
        lifetime.attachTransport(request: request) { closed = true }
        XCTAssertTrue(closed)
        XCTAssertFalse(lifetime.isCurrent(request))
    }
}

import Foundation
import XCTest

@testable import Psst

final class InboxPageWindowTests: XCTestCase {
    func testEmptyPagesStillAdvanceAndFailedNavigationPreservesCurrentPage() throws {
        var current = InboxPageWindow()
        try current.accept(next: "opaque-1")
        let pending = try current.forward()
        XCTAssertNil(current.cursor)
        XCTAssertEqual(current.number, 1)
        XCTAssertEqual(pending.cursor, "opaque-1")
        XCTAssertFalse(pending.loaded)
        var accepted = pending
        try accepted.accept(next: "opaque-2")
        current = accepted
        XCTAssertEqual(current.number, 2)
        var previous = try current.backward()
        XCTAssertNil(previous.cursor)
        XCTAssertEqual(previous.number, 1)
        try previous.accept(next: "opaque-1")
        XCTAssertTrue(previous.loaded)
    }

    func testTrailStaysBoundedWithoutPreventingFurtherPages() throws {
        var current = InboxPageWindow()
        for index in 1...5000 {
            try current.accept(next: "cursor-\(index)")
            current = try current.forward()
            XCTAssertLessThanOrEqual(current.previous.count, 100)
        }
        XCTAssertEqual(current.number, 5001)
        for _ in 0..<100 { current = try current.backward() }
        XCTAssertFalse(current.canGoBack)
        XCTAssertEqual(current.number, 4901)
        current = InboxPageWindow()
        XCTAssertNil(current.cursor)
        XCTAssertEqual(current.number, 1)
    }

    func testMalformedAndRepeatedCursorsDoNotReplaceLoadedState() throws {
        var current = InboxPageWindow()
        try current.accept(next: "first")
        current = try current.forward()
        try current.accept(next: "second")
        for bad in ["", "first", String(repeating: "x", count: 513)] {
            let before = current
            XCTAssertThrowsError(try current.accept(next: bad))
            XCTAssertEqual(current, before)
        }
        current = try current.forward()
        XCTAssertThrowsError(try current.accept(next: "first"))
        try current.accept(next: nil)
        XCTAssertThrowsError(try current.forward())
    }

    func testApprovalBindsPageAndExactSubmissionCohort() {
        let page = UUID()
        let scope = InboxSaveScope(pageID: page, cursor: "page", transferIDs: ["a", "b"])
        XCTAssertTrue(scope.accepts(pageID: page, cursor: "page", available: ["a", "b", "new-arrival"]))
        XCTAssertEqual(scope.transferIDs, ["a", "b"])
        XCTAssertFalse(scope.accepts(pageID: UUID(), cursor: "page", available: ["a", "b"]))
        XCTAssertFalse(scope.accepts(pageID: page, cursor: nil, available: ["a", "b"]))
        XCTAssertFalse(scope.accepts(pageID: page, cursor: "page", available: ["a"]))
        XCTAssertFalse(InboxSaveScope(pageID: page, cursor: nil, transferIDs: []).accepts(pageID: page, cursor: nil, available: []))
        let oversized = Set((0...50).map(String.init))
        XCTAssertFalse(InboxSaveScope(pageID: page, cursor: nil, transferIDs: oversized).accepts(pageID: page, cursor: nil, available: oversized))
    }

    func testPartialUnknownAndFilteredPagesNeverMeanWholeInboxSaved() {
        XCTAssertTrue(InboxSaveScope.coversInbox(cursor: nil, next: nil, total: 3, visible: ["a": 1, "b": 2], saved: ["a", "b"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: nil, next: "more", total: 3, visible: ["a": 1, "b": 2], saved: ["a", "b"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: "older", next: nil, total: 3, visible: ["a": 1, "b": 2], saved: ["a", "b"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: nil, next: nil, total: nil, visible: ["a": 1], saved: ["a"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: nil, next: nil, total: 5, visible: ["a": 1, "b": 2], saved: ["a", "b"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: nil, next: nil, total: 3, visible: ["a": 1, "b": 2], saved: ["a"]))
        XCTAssertFalse(InboxSaveScope.coversInbox(cursor: nil, next: nil, total: 0, visible: [:], saved: []))
    }
}

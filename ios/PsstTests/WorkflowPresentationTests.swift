import Foundation
import XCTest

@testable import Psst

final class WorkflowPresentationTests: XCTestCase {
    func testExplicitTitleValidationUsesScalarsAndPreservesUnicode() throws {
        XCTAssertNil(try SharedLinkTitle.normalize(nil))
        XCTAssertNil(try SharedLinkTitle.normalize(" \u{3000}"))
        XCTAssertEqual(try SharedLinkTitle.normalize("\u{00a0}Family 👩‍👩‍👧‍👦\u{2003}"), "Family 👩‍👩‍👧‍👦")
        XCTAssertEqual(try SharedLinkTitle.normalize(String(repeating: "😀", count: 200))?.utf8.count, 800)
        XCTAssertThrowsError(try SharedLinkTitle.normalize(String(repeating: "😀", count: 201)))
        XCTAssertThrowsError(try SharedLinkTitle.normalize(String(repeating: "e\u{301}", count: 101)))
        for value in ["Title\n", "\tTitle", "A\u{7f}B", "A\u{85}B", "A\u{9f}B"] {
            XCTAssertThrowsError(try SharedLinkTitle.normalize(value))
        }
    }

    func testSharedTitlePrefersExplicitMetadataWithoutPublishingLocalFilename() throws {
        var record = TransferRecord(
            id: "id", direction: .sent, state: .complete, createdAt: Date(), fileCount: 2, totalSize: 0, shareURL: "local-key-link", title: "private.txt",
            customTitle: "Local label")
        XCTAssertNil(record.sharedTitle)
        XCTAssertEqual(record.displayTitle, "Local label")
        record.sharedTitle = "Shared title"
        XCTAssertEqual(record.displayTitle, "Shared title")
        let restored = try JSONDecoder().decode(TransferRecord.self, from: JSONEncoder().encode(record))
        XCTAssertEqual(restored.sharedTitle, "Shared title")
        record.sharedTitle = nil
        XCTAssertEqual(record.displayTitle, "Local label")
        record.state = .exhausted
        XCTAssertFalse(record.linkActive)
        XCTAssertEqual(record.statusText, "Download limit reached")
        XCTAssertEqual(record.shareURL, "local-key-link")  // Keep local data; suppress link actions.
    }

    func testMergedStreamsHaveOneChronologicalWindowWithBoundedBuffers() throws {
        var merge = BoundedHistoryMerge<Int, Int>()
        let left = Array(stride(from: 399, through: 1, by: -2))
        let right = Array(stride(from: 398, through: 0, by: -2))
        var leftCalls = 0
        var rightCalls = 0
        func page(_ items: [Int], _ cursor: Int?) -> ([Int], Int?) {
            let start = cursor ?? 0
            let end = min(start + 50, items.count)
            return (Array(items[start..<end]), end < items.count ? end : nil)
        }
        var all: [Int] = []
        while merge.hasMore {
            try merge.load(
                left: {
                    leftCalls += 1
                    return page(left, $0)
                },
                right: {
                    rightCalls += 1
                    return page(right, $0)
                }, precedes: >)
            all += merge.visible.suffix(50)
            XCTAssertLessThanOrEqual(merge.visible.count, 100)
            XCTAssertLessThanOrEqual(merge.left.items.count, 50)
            XCTAssertLessThanOrEqual(merge.right.items.count, 50)
        }
        XCTAssertEqual(all, Array((0..<400).reversed()))
        XCTAssertEqual(leftCalls, 4)
        XCTAssertEqual(rightCalls, 4)
        XCTAssertTrue(merge.trimmed)
    }

    func testFailedReadPreservesCommittedWindowAndCursorAndDeletedRowsDisappear() throws {
        enum Failure: Error { case unavailable }
        var committed = BoundedHistoryMerge<Int, Int>()
        try committed.load(left: { _ in (Array((50..<100).reversed()), 1) }, right: { _ in ([], nil) }, precedes: >)
        var pending = committed
        XCTAssertThrowsError(try pending.load(left: { _ in throw Failure.unavailable }, right: { _ in ([], nil) }, precedes: >))
        XCTAssertEqual(committed.visible, Array((50..<100).reversed()))
        XCTAssertEqual(committed.left.next, 1)
        committed.updateVisible { $0 == 80 ? nil : $0 }
        XCTAssertFalse(committed.visible.contains(80))
        try committed.load(
            left: { cursor in
                XCTAssertEqual(cursor, 1)
                return (Array((0..<50).reversed()), nil)
            },
            right: { _ in
                XCTFail("Ended source queried")
                return ([], nil)
            }, precedes: >)
        XCTAssertEqual(committed.visible.count, 99)
        XCTAssertFalse(committed.hasMore)
    }

    func testEmptySourceAndMalformedContinuationAreBounded() throws {
        var empty = BoundedHistoryMerge<Int, Int>()
        try empty.load(left: { _ in ([], nil) }, right: { _ in ([], nil) }, precedes: >)
        XCTAssertFalse(empty.hasMore)
        var invalid = BoundedHistoryMerge<Int, Int>()
        XCTAssertThrowsError(try invalid.load(left: { _ in ([], 1) }, right: { _ in ([], nil) }, precedes: >))
    }

    func testHistoryFilteringIsInRequestAndWrongKindIsRejected() async throws {
        let empty = Data(#"{"paginated":true,"transfers":[],"slots":[],"next_cursor":"next"}"#.utf8)
        let page = try await HistorySnapshot.load(kind: "slot") { path in
            XCTAssertEqual(path, "auth/resources?limit=50&kind=slot")
            return empty
        }
        XCTAssertEqual(page.next_cursor, "next")
        let slot = Data(
            #"{"paginated":true,"transfers":[],"slots":[{"id":"01234567-89ab-cdef-0123-456789abcdef","title":"Travel","status":"waiting","file_count":0,"completed_files":0,"total_size":0,"summary":{"state":"ready","file_count":0,"completed_files":0,"total_size":0}}],"next_cursor":null}"#
                .utf8)
        do {
            _ = try await HistorySnapshot.load(kind: "transfer") { _ in slot }
            XCTFail("Accepted receive link in sent-only page")
        } catch {}
        let received = try await HistorySnapshot.load(kind: "slot") { _ in slot }
        XCTAssertEqual(received.slots.first?.title, "Travel")
    }
}

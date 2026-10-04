import Foundation
@testable import Psst
import XCTest

final class HistorySnapshotTests: XCTestCase {
    func testAllPagesAreRequiredBeforeReturningHistory() async throws {
        let list = try await HistorySnapshot.load { path in
            if path.contains("after=") {
                XCTAssertTrue(path.contains("opaque%2Bcursor/"))
                return Data(#"{"transfers":[],"slots":[{"id":"01234567-89ab-cdef-0123-456789abcdef","status":"has_uploads","file_count":130,"completed_files":125,"total_size":999}],"next_cursor":null}"#.utf8)
            }
            return Data(#"{"transfers":[],"slots":[],"next_cursor":"opaque+cursor/="}"#.utf8)
        }
        XCTAssertEqual(list.slots.first?.completed_files, 125)
        XCTAssertNil(list.next_cursor)
    }

    func testRepeatedCursorFailsWithoutReturningPartialSnapshot() async {
        do {
            _ = try await HistorySnapshot.load { _ in
                Data(#"{"transfers":[],"slots":[],"next_cursor":"repeated"}"#.utf8)
            }
            XCTFail("Repeated cursor was accepted")
        } catch HistorySnapshot.Failure.repeatedCursor {} catch { XCTFail("Unexpected error: \(error)") }
    }

    func testFailedSecondPageAndOversizedResponseFailClosed() async {
        for oversized in [false, true] {
            do {
                _ = try await HistorySnapshot.load { path in
                    if oversized {
                        return Data(repeating: 0, count: 1_048_577)
                    }
                    if path.contains("after=") {
                        throw URLError(.notConnectedToInternet)
                    }
                    return Data(#"{"transfers":[],"slots":[],"next_cursor":"next"}"#.utf8)
                }
                XCTFail("Incomplete snapshot was accepted")
            } catch {}
        }
    }

    func testPageCeilingStopsAnUnendingServer() async {
        actor Pages {
            var count = 0
            func next() -> Data {
                count += 1
                return Data("{\"transfers\":[],\"slots\":[],\"next_cursor\":\"page-\(count)\"}".utf8)
            }
        }
        let pages = Pages()
        do {
            _ = try await HistorySnapshot.load { _ in await pages.next() }
            XCTFail("Unending history was accepted")
        } catch HistorySnapshot.Failure.tooLarge {} catch { XCTFail("Unexpected error: \(error)") }
        let count = await pages.count
        XCTAssertEqual(count, 100)
    }
}

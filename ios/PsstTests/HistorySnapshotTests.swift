import Foundation
import XCTest

@testable import Psst

final class HistorySnapshotTests: XCTestCase {
    private let slot =
        #"{"id":"01234567-89ab-cdef-0123-456789abcdef","status":"has_uploads","file_count":130,"completed_files":125,"total_size":999,"summary":{"state":"ready","file_count":130,"completed_files":125,"total_size":999}}"#
    private func page(_ slots: String = "", next: String = "null") -> Data {
        Data("{\"paginated\":true,\"transfers\":[],\"slots\":[\(slots)],\"next_cursor\":\(next)}".utf8)
    }

    func testFetchesOnlyRequestedPageIncludingEmptyContinuation() async throws {
        actor Calls {
            var count = 0
            func fetch(_ path: String) -> Data {
                count += 1
                XCTAssertEqual(path, "auth/resources?limit=50&after=page-two")
                return Data(#"{"paginated":true,"transfers":[],"slots":[],"next_cursor":"page-three"}"#.utf8)
            }
        }
        let calls = Calls()
        let list = try await HistorySnapshot.load(after: "page-two") { await calls.fetch($0) }
        XCTAssertTrue(list.identities.isEmpty)
        XCTAssertEqual(list.next_cursor, "page-three")
        let count = await calls.count
        XCTAssertEqual(count, 1)
    }

    func testKnownLargeCountsAndUnknownAreDifferent() async throws {
        let readyData = page(slot)
        let ready = try await HistorySnapshot.load { _ in readyData }
        XCTAssertEqual(ready.slots.first?.completed_files, 125)
        let unknownData = page(
            slot.replacingOccurrences(of: "130", with: "null").replacingOccurrences(of: "125", with: "null").replacingOccurrences(of: "999", with: "null").replacingOccurrences(
                of: "ready", with: "updating"))
        let unknown = try await HistorySnapshot.load { _ in unknownData }
        XCTAssertNil(unknown.slots.first?.completed_files)
        XCTAssertEqual(unknown.slots.first?.summary.state, "updating")
    }

    func testRejectsMissingFieldsContradictoryCountersAndDuplicateIdentities() async {
        let valid = String(data: page(slot), encoding: .utf8)!
        let invalid = [
            valid.replacingOccurrences(of: "\"paginated\":true,", with: ""),
            valid.replacingOccurrences(of: ",\"next_cursor\":null", with: ""),
            valid.replacingOccurrences(of: "\"total_size\":999,\"summary\"", with: "\"summary\""),
            valid.replacingOccurrences(of: "\"state\":\"ready\"", with: "\"state\":\"updating\""),
            valid.replacingOccurrences(of: "\"file_count\":130", with: "\"file_count\":100"),
            valid.replacingOccurrences(of: "\"status\":\"has_uploads\"", with: "\"status\":\"unknown\""),
            String(data: page(slot + "," + slot), encoding: .utf8)!,
        ]
        for json in invalid {
            do {
                _ = try await HistorySnapshot.load { _ in Data(json.utf8) }
                XCTFail("Accepted malformed page")
            } catch {}
        }
    }

    func testLimitsAndCursorErrorsNeverDrainOrReturnPartialResults() async {
        let cases = [
            page(next: "\"same\""),
            page(next: "\"not+a/base64url=cursor\""),
            page(next: "\"" + String(repeating: "a", count: 513) + "\""),
            Data(repeating: 0, count: HistorySnapshot.maximumBytes + 1),
            page(Array(repeating: slot, count: 51).joined(separator: ",")),
        ]
        for data in cases {
            do {
                _ = try await HistorySnapshot.load(after: "same") { _ in data }
                XCTFail("Accepted invalid page")
            } catch {}
        }
        do {
            _ = try await HistorySnapshot.load(after: "bad/cursor") { _ in
                XCTFail("Invalid cursor requested")
                return Data()
            }
            XCTFail("Invalid cursor accepted")
        } catch {}
    }

    func testFailedPageIsNotTurnedIntoEmptyHistory() async {
        do {
            _ = try await HistorySnapshot.load(after: "older") { _ in throw URLError(.notConnectedToInternet) }
            XCTFail("Failed request returned history")
        } catch let error as URLError { XCTAssertEqual(error.code, .notConnectedToInternet) } catch { XCTFail("Unexpected error: \(error)") }
    }
}

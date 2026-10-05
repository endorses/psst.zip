import Foundation
import XCTest

@testable import Psst

final class HistorySyncTests: XCTestCase {
    private let generation = "01234567-89ab-cdef-0123-456789abcdef"
    private let id = "11111111-1111-4111-8111-111111111111"
    private func batch(_ changes: String, cursor: String = "two", more: Bool = false) -> Data {
        Data(
            "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[\(changes)],\"next_cursor\":\"\(cursor)\",\"has_more\":\(more)}"
                .utf8)
    }
    func testFrozenBackendContractFixtures() throws {
        #if os(Linux)
            let bundle = Bundle.module
        #else
            let bundle = Bundle(for: HistorySyncTests.self)
        #endif
        let url = try XCTUnwrap(bundle.url(forResource: "history-sync-v1", withExtension: "json"))
        let fixture = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
        let snapshotData = try JSONSerialization.data(withJSONObject: fixture["snapshot"]!)
        let page = try JSONDecoder().decode(ResourceList.self, from: snapshotData)
        XCTAssertEqual(page.transfers.count, 1)
        XCTAssertEqual(page.slots.count, 1)
        for name in ["empty", "upsert", "remove", "unknown_summary"] {
            let data = try JSONSerialization.data(withJSONObject: fixture[name]!)
            let batch = try JSONDecoder().decode(HistorySync.Batch.self, from: data)
            try batch.validate(
                cursor: try XCTUnwrap(page.sync_cursor), generation: try XCTUnwrap(page.generation),
                limit: 50)
        }
    }

    func testEmptyFeedAndRemovalOnlyContainScopedMetadata() async throws {
        let data = batch("")
        let empty = try await HistorySync.load(cursor: "one", generation: generation) { path in
            XCTAssertEqual(path, "auth/history/changes?cursor=one&limit=50")
            return data
        }
        XCTAssertTrue(empty.changes.isEmpty)
        let removed = try JSONDecoder().decode(
            HistorySync.Batch.self,
            from: batch("{\"kind\":\"transfer\",\"id\":\"\(id)\",\"revision\":2,\"action\":\"remove\"}"))
        try removed.validate(cursor: "one", generation: generation, limit: 50)
        XCTAssertNil(removed.changes[0].transfer)
    }
    func testRejectsMismatchedRevisionIdentityGenerationAndCursorLoops() throws {
        let valid =
            "{\"kind\":\"transfer\",\"id\":\"\(id)\",\"revision\":2,\"action\":\"upsert\",\"resource\":{\"id\":\"\(id)\",\"revision\":2,\"status\":\"complete\",\"file_count\":1,\"total_size\":100,\"summary\":{\"state\":\"ready\",\"file_count\":1,\"completed_files\":1,\"total_size\":100}}}"
        let decoded = try JSONDecoder().decode(HistorySync.Batch.self, from: batch(valid))
        try decoded.validate(cursor: "one", generation: generation, limit: 50)
        XCTAssertThrowsError(
            try decoded.validate(cursor: "one", generation: UUID().uuidString, limit: 50))
        XCTAssertThrowsError(
            try JSONDecoder().decode(
                HistorySync.Batch.self,
                from: batch(
                    valid.replacingOccurrences(
                        of: "\"resource\":{\"id\":\"\(id)\",\"revision\":2",
                        with: "\"resource\":{\"id\":\"\(id)\",\"revision\":1"))))
        let loop = try JSONDecoder().decode(
            HistorySync.Batch.self, from: batch("", cursor: "one", more: true))
        XCTAssertThrowsError(try loop.validate(cursor: "one", generation: generation, limit: 50))
        let duplicate = try JSONDecoder().decode(
            HistorySync.Batch.self, from: batch(valid + "," + valid))
        XCTAssertThrowsError(try duplicate.validate(cursor: "one", generation: generation, limit: 50))
    }
    func testCachedNullableSummariesRoundTripWithoutBecomingMissingFields() throws {
        let raw = Data(
            #"{"id":"44444444-4444-4444-8444-444444444444","status":"waiting","file_count":null,"completed_files":null,"total_size":null,"summary":{"state":"updating","file_count":null,"completed_files":null,"total_size":null}}"#
                .utf8)
        let row = try JSONDecoder().decode(ResourceList.Slot.self, from: raw)
        let encoded = try JSONEncoder().encode(row)
        let decoded = try JSONDecoder().decode(ResourceList.Slot.self, from: encoded)
        XCTAssertEqual(decoded.summary.state, "updating")
        XCTAssertNil(decoded.completed_files)
        let page = ResourceList(transfers: [], slots: [row])
        let roundTrip = try JSONDecoder().decode(ResourceList.self, from: JSONEncoder().encode(page))
        XCTAssertNil(roundTrip.next_cursor)
        XCTAssertEqual(roundTrip.slots.count, 1)
    }

    func testFeedDeadlineCancelsItsUnderlyingRequest() async {
        do {
            _ = try await HistorySync.load(
                cursor: "one", generation: generation, timeoutNanoseconds: 20_000_000
            ) { _ in
                try await Task.sleep(nanoseconds: 60_000_000_000)
                return Data()
            }
            XCTFail("Unbounded request escaped its deadline")
        } catch HistorySnapshot.Failure.timedOut {} catch { XCTFail("Wrong deadline error: \(error)") }
    }

    func testRetryAfterSupportsSecondsAndHTTPDateWithoutLocaleDependence() {
        let now = Date(timeIntervalSince1970: 1_600_000_000)
        XCTAssertEqual(HistorySync.retryDelay("120", now: now), 120)
        XCTAssertEqual(HistorySync.retryDelay("Sun, 13 Sep 2020 12:28:40 GMT", now: now), 120)
        XCTAssertNil(HistorySync.retryDelay("-1", now: now))
        XCTAssertNil(HistorySync.retryDelay("NaN", now: now))
        XCTAssertNil(HistorySync.retryDelay("Sun, 13 Sep 2020 12:00:00 GMT", now: now))
        XCTAssertNil(HistorySync.retryDelay("not a date", now: now))
    }

    func testCapabilitiesDoNotConvertArbitraryFailuresIntoLegacySupport() throws {
        XCTAssertTrue(try HistorySync.supports(Data(#"{"history_sync_version":1}"#.utf8)))
        XCTAssertFalse(try HistorySync.supports(Data(#"{}"#.utf8)))
        XCTAssertThrowsError(try HistorySync.supports(Data("offline".utf8)))
    }
    func testOversizedBatchNeverDecodesOrAdvancesCursor() async {
        do {
            _ = try await HistorySync.load(cursor: "one", generation: generation) { _ in
                Data(repeating: 32, count: HistorySync.maximumBytes + 1)
            }
            XCTFail("Oversized batch accepted")
        } catch {}
    }
}

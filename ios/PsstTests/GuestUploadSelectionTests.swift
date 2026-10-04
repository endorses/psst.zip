import XCTest

@testable import Psst

final class GuestUploadSelectionTests: XCTestCase {
    func testManifestEnvelopeFitsReservedSpace() throws {
        try GuestUploadSelection.requireManifestCapacity(plainBytes: 1000, reserve: 1116)
        XCTAssertThrowsError(try GuestUploadSelection.requireManifestCapacity(plainBytes: 1001, reserve: 1116))
        XCTAssertThrowsError(try GuestUploadSelection.requireManifestCapacity(plainBytes: 0, reserve: 115))
        XCTAssertThrowsError(try GuestUploadSelection.requireManifestCapacity(plainBytes: Int.max, reserve: 1_048_576))
        XCTAssertThrowsError(try GuestUploadSelection.requireManifestCapacity(plainBytes: -1, reserve: 1_048_576))
    }

    func testEmptyFileStillConsumesCapacity() throws {
        XCTAssertEqual(try GuestUploadSelection.totalWireBytes([60, 60]), 120)
        XCTAssertEqual(try GuestUploadSelection.totalWireBytes([]), 0)
        XCTAssertThrowsError(try GuestUploadSelection.totalWireBytes([0]))
    }

    func testWholeBatchAndOverflowAreChecked() throws {
        XCTAssertEqual(try GuestUploadSelection.totalWireBytes([4_194_364, 120]), 4_194_484)
        XCTAssertEqual(try GuestUploadSelection.totalWireBytes(Array(repeating: 60, count: 100)), 6000)
        XCTAssertThrowsError(try GuestUploadSelection.totalWireBytes(Array(repeating: 60, count: 101)))
        XCTAssertThrowsError(try GuestUploadSelection.totalWireBytes([Int64.max, 60]))
        XCTAssertThrowsError(try GuestUploadSelection.totalWireBytes([-1]))
    }

    func testChangedSourceCannotIncreaseAllocationAfterPreflight() throws {
        try GuestUploadSelection.requireUnchanged([0, 12], expected: [0, 12])
        XCTAssertThrowsError(try GuestUploadSelection.requireUnchanged([0, 13], expected: [0, 12]))
        XCTAssertThrowsError(try GuestUploadSelection.requireUnchanged([12, 0], expected: [0, 12]))
        XCTAssertThrowsError(try GuestUploadSelection.requireUnchanged([0], expected: [0, 12]))
        try GuestUploadSelection.requireUnchanged([12], expected: nil)
    }
}

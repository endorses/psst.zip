import Foundation
@testable import Psst
import XCTest

final class ReceiveSafetyTests: XCTestCase {
    func testAggregateCannotOverflowOrExceedDevicePolicy() throws {
        XCTAssertEqual(try ReceiveSafety.total([0, 1, 2]), 3)
        XCTAssertThrowsError(try ReceiveSafety.total([-1]))
        XCTAssertThrowsError(try ReceiveSafety.total([Int64.max, 1]))
        XCTAssertThrowsError(try ReceiveSafety.total([ReceiveSafety.maximumTotalBytes, 1]))
        XCTAssertEqual(try ReceiveSafety.total([ReceiveSafety.maximumTotalBytes]), ReceiveSafety.maximumTotalBytes)
    }

    func testDiskReserveIsKeptForOtherApplications() {
        XCTAssertNoThrow(try ReceiveSafety.checkCapacity(available: ReceiveSafety.reserveBytes + 10, additional: 10))
        XCTAssertThrowsError(try ReceiveSafety.checkCapacity(available: ReceiveSafety.reserveBytes + 9, additional: 10))
        XCTAssertThrowsError(try ReceiveSafety.checkCapacity(available: 0, additional: 0))
        XCTAssertThrowsError(try ReceiveSafety.checkCapacity(available: Int64.max, additional: Int64.max))
    }
}

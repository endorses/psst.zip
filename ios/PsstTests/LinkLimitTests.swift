@testable import Psst
import XCTest

final class LinkLimitTests: XCTestCase {
    func testOptionalLimitAndExactIntegerRange() {
        XCTAssertEqual(LinkLimit.parse("", enabled: false), 0)
        XCTAssertEqual(LinkLimit.parse("1", enabled: true), 1)
        XCTAssertEqual(LinkLimit.parse("2147483647", enabled: true), Int32.max)
        for invalid in ["", "0", "-1", "1.5", "1e2", " 2", "2 ", "٢", "+2", "2147483648"] {
            XCTAssertNil(LinkLimit.parse(invalid, enabled: true), invalid)
        }
    }
}

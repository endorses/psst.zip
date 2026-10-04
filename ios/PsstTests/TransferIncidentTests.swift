import Foundation
@testable import Psst
import XCTest

final class TransferIncidentTests: XCTestCase {
    func testOnlyExactStatusAndCodePairsAreRecognized() {
        let pause = Data(#"{"code":"public_transfers_paused","error":"secret response"}"#.utf8)
        let revoke = Data(#"{"code":"resource_revoked"}"#.utf8)
        XCTAssertEqual(TransferIncident.response(status: 503, body: pause), .paused)
        XCTAssertEqual(TransferIncident.response(status: 410, body: revoke), .revoked)
        for status in [200, 401, 403, 404, 429, 500] {
            XCTAssertNil(TransferIncident.response(status: status, body: pause))
            XCTAssertNil(TransferIncident.response(status: status, body: revoke))
        }
        XCTAssertNil(TransferIncident.response(status: 410, body: pause))
        XCTAssertNil(TransferIncident.response(status: 503, body: revoke))
        XCTAssertEqual(TransferIncident.response(status: 503, body: Data(), codeHeader: "public_transfers_paused"), .paused)
        XCTAssertNil(TransferIncident.response(status: 503, body: pause, codeHeader: "unrecognized"))
    }

    func testMalformedAndUnboundedBodiesDoNotBecomeIncidentMessages() {
        for body in [Data(), Data("not JSON".utf8), Data(#"{"code":23}"#.utf8), Data(repeating: 65, count: 4097)] {
            XCTAssertNil(TransferIncident.response(status: 503, body: body))
        }
        XCTAssertEqual(TransferIncident.from(TransferIncident.paused), .paused)
        XCTAssertEqual(TransferIncident.from(TransferIncident.revoked), .revoked)
        XCTAssertNil(TransferIncident.from(NSError(domain: "public_transfers_paused", code: 503,
                                                   userInfo: [NSLocalizedDescriptionKey: "resource_revoked"])))
        XCTAssertFalse(TransferIncident.paused.localizedDescription.contains("secret response"))
        XCTAssertTrue(TransferIncident.paused.localizedDescription.contains("saved files"))
        XCTAssertTrue(TransferIncident.revoked.localizedDescription.contains("already saved"))
    }

    func testTrafficBudgetRequiresMatchingStatusAndUsesOnlyValidatedRetryDate() throws {
        let valid = "2026-11-01T00:00:00Z"
        let expected = TransferIncident.retryDate(valid)
        XCTAssertNotNil(expected)
        let body = Data(#"{"code":"traffic_budget_exhausted","retry_at":"2026-11-01T00:00:00Z"}"#.utf8)
        XCTAssertEqual(TransferIncident.response(status: 429, body: body), .budget(retryAt: expected))
        XCTAssertNil(TransferIncident.response(status: 503, body: body))
        XCTAssertEqual(TransferIncident.response(status: 429, body: Data(),
                                                 codeHeader: "traffic_budget_exhausted", retryHeader: valid), .budget(retryAt: expected))
        let malformed = Data(#"{"code":"traffic_budget_exhausted","retry_at":23}"#.utf8)
        XCTAssertEqual(TransferIncident.response(status: 429, body: malformed), .budget(retryAt: nil))
        let hostile = Data(#"{"code":"traffic_budget_exhausted","retry_at":"https://example.invalid/#secret"}"#.utf8)
        let safe = TransferIncident.response(status: 429, body: hostile)
        XCTAssertEqual(safe, .budget(retryAt: nil))
        XCTAssertFalse(try XCTUnwrap(safe?.localizedDescription.contains("secret")))
        for invalid in ["", "tomorrow", "2026-11-01", "2026-11-01T00:00:00", String(repeating: "1", count: 65)] {
            XCTAssertNil(TransferIncident.retryDate(invalid))
        }
        XCTAssertNotNil(TransferIncident.retryDate("2026-11-01T00:00:00.123456789Z"))
    }

    func testAccountingFailureIsDistinctFromLoginFailureAndBudgetReset() {
        let body = Data(#"{"code":"traffic_accounting_unavailable","retry_at":"2026-11-01T00:00:00Z"}"#.utf8)
        XCTAssertEqual(TransferIncident.response(status: 503, body: body), .accountingUnavailable)
        for status in [401, 403, 429, 200] {
            XCTAssertNil(TransferIncident.response(status: status, body: body))
        }
        XCTAssertTrue(TransferIncident.accountingUnavailable.localizedDescription.contains("saved files"))
        XCTAssertEqual(TransferIncident.from(TransferIncident.accountingUnavailable), .accountingUnavailable)
        let changed = Data(#"{"code":"traffic_policy_changed"}"#.utf8)
        XCTAssertEqual(TransferIncident.response(status: 409, body: changed), .policyChanged)
        XCTAssertNil(TransferIncident.response(status: 503, body: changed))
        XCTAssertNil(TransferIncident.response(status: 429, body: changed))
    }

    func testTrafficStatusIsOnlyASmallSafeDiagnosis() throws {
        XCTAssertEqual(TransferIncident.trafficStatus(Data(#"{"state":"exhausted","retry_at":"2026-11-01T00:00:00Z"}"#.utf8)),
                       .budget(retryAt: TransferIncident.retryDate("2026-11-01T00:00:00Z")))
        XCTAssertEqual(TransferIncident.trafficStatus(Data(#"{"state":"paused"}"#.utf8)), .paused)
        XCTAssertEqual(TransferIncident.trafficStatus(Data(#"{"state":"unavailable"}"#.utf8)), .accountingUnavailable)
        XCTAssertEqual(TransferIncident.trafficStatus(Data(#"{"state":"revoked"}"#.utf8)), .revoked)
        for state in ["ready", "unexpected server text", "<script>"] {
            let body = try JSONSerialization.data(withJSONObject: ["state": state])
            XCTAssertNil(TransferIncident.trafficStatus(body))
        }
        XCTAssertNil(TransferIncident.trafficStatus(Data(repeating: 65, count: 4097)))
        XCTAssertNil(TransferIncident.trafficStatus(Data(#"{"state":5}"#.utf8)))
    }
}

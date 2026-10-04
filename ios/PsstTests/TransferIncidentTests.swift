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
}

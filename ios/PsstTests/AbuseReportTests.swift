import Foundation
@testable import Psst
import XCTest

final class AbuseReportTests: XCTestCase {
    private var priorLanguage = AppLanguage.system
    override func setUp() {
        super.setUp()
        priorLanguage = LanguageSettings.shared.preference
        LanguageSettings.shared.preference = .en
    }

    override func tearDown() {
        LanguageSettings.shared.preference = priorLanguage
        super.tearDown()
    }

    private let identifier = "12345678-1234-1234-1234-123456789ABC"
    func testContactAcceptsOrdinaryASCIIAndRejectsInjectionAndLimits() {
        XCTAssertEqual(
            AbuseContact.validated("Abuse+reports%tag@EXAMPLE.org"), "Abuse+reports%tag@example.org"
        )
        for invalid in [
            "", "a@localhost", "a..b@example.org", ".a@example.org", "a.@example.org",
            "a b@example.org",
            "a@example..org", "a@-example.org", "a@example-.org",
            "a@example.org?bcc=other@example.org",
            "a@example.org\r\nBcc:x@y.org", "ä@example.org", "a@éxample.org", " a@example.org",
            "a@example.org ", "a@b@c.org", String(repeating: "a", count: 65) + "@example.org",
            "a@" + String(repeating: "b", count: 64) + ".org",
            "a@" + String(repeating: "long.", count: 70) + "org",
        ] {
            XCTAssertNil(AbuseContact.validated(invalid), invalid)
        }
    }

    func testContextContainsOnlyValidatedOriginTypeAndID() throws {
        let context = try XCTUnwrap(
            AbuseReportContext(
                origin: "HTTPS://EXAMPLE.org:443/", resourceType: "slot", resourceID: identifier
            )
        )
        XCTAssertEqual(context.origin, "https://example.org")
        XCTAssertEqual(
            context.text,
            "Instance: https://example.org\nResource type: slot\nResource ID: "
                + identifier.lowercased()
        )
        XCTAssertNotNil(AbuseReportContext(origin: "http://192.168.1.2:8080"))
        XCTAssertNotNil(AbuseReportContext(origin: "https://[::1]:8443"))
        for origin in [
            "https://example.org/d/id#KEY", "https://example.org#KEY",
            "https://example.org?token=KEY",
            "https://user:password@example.org", "https://example.org/path",
            "https://example.org\nInjected", "https://example.org%0A", "https://example.org:65536",
            "ftp://example.org", "https://-bad.example", "https://bad-.example",
            "https://bad..example", "https://256.1.1.1", "https://01.2.3.4", "https://123",
            "https://[1::2::3]", "https://" + String(repeating: "a", count: 64) + ".org",
        ] {
            XCTAssertNil(
                AbuseReportContext(origin: origin, resourceType: "transfer", resourceID: identifier)
            )
        }
        XCTAssertNil(
            AbuseReportContext(
                origin: "https://example.org", resourceType: "user", resourceID: identifier
            )
        )
        XCTAssertNil(
            AbuseReportContext(
                origin: "https://example.org", resourceType: "slot", resourceID: identifier + "#KEY"
            )
        )
        XCTAssertNil(AbuseReportContext(origin: "https://example.org", resourceID: identifier))
    }

    func testInvalidKeyStillProducesOnlySafeReportMetadata() throws {
        for route in ["d", "u"] {
            let context = try XCTUnwrap(
                AbuseReportContext.fromLink(
                    "https://example.org/\(route)/\(identifier)?token=DO_NOT_SEND#INVALID_KEY"
                )
            )
            XCTAssertEqual(context.resourceType, route == "d" ? "transfer" : "slot")
            XCTAssertEqual(context.resourceID, identifier.lowercased())
            XCTAssertFalse(context.text.contains("DO_NOT_SEND"))
            XCTAssertFalse(context.text.contains("INVALID_KEY"))
            XCTAssertFalse(
                try XCTUnwrap(context.mailURL(contact: "a@example.org")?.absoluteString.contains("INVALID_KEY"))
            )
        }
        for raw in [
            "https://example.org/pair#CODE", "https://example.org/d/invalid#SECRET",
            "https://name:pass@example.org/d/\(identifier)#KEY",
            "https://example.org/d/\(identifier)/extra#KEY",
            "https://example.org/d/" + String(repeating: "a", count: 8192),
        ] {
            XCTAssertNil(AbuseReportContext.fromLink(raw))
        }
    }

    func testExplicitMailDraftEncodesContactAndOnlySafeContext() throws {
        let context = try XCTUnwrap(
            AbuseReportContext(
                origin: "https://example.org:8443", resourceType: "transfer", resourceID: identifier
            )
        )
        let url = try XCTUnwrap(context.mailURL(contact: "Abuse+reports%tag@example.org"))
        let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
        XCTAssertEqual(components.scheme, "mailto")
        XCTAssertEqual(components.path, "Abuse+reports%tag@example.org")
        XCTAssertEqual(components.queryItems?.map(\.name), ["subject", "body"])
        XCTAssertTrue(components.queryItems?.last?.value?.hasPrefix(context.text) == true)
        XCTAssertNil(components.fragment)
        XCTAssertTrue(url.absoluteString.contains("%2B"))
        XCTAssertTrue(url.absoluteString.contains("%25"))
        XCTAssertNil(context.mailURL(contact: "a@example.org?bcc=x@y.org"))
    }
}

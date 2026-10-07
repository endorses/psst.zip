import Foundation
import XCTest

@testable import Psst

final class ReleaseSourceTests: XCTestCase {
  func testConfiguredOriginRejectsCapabilityAndCredentialURLs() {
    XCTAssertEqual(
      ReleaseSource.metadataURL(server: "https://operator.example:8443/")?.absoluteString,
      "https://operator.example:8443/licenses/release.json")
    for url in [
      "http://example.org", "https://user:secret@example.org", "https://example.org/?secret=x",
      "https://example.org/#secret", "https://example.org/path", "javascript:alert(1)",
    ] {
      XCTAssertNil(ReleaseSource.metadataURL(server: url))
    }
  }
  func testExactSourceRequiresFullCommitAndBoundedSafeMetadata() {
    let revision = String(repeating: "a", count: 40)
    let json =
      #"{"name":"psst.zip","license":"AGPL-3.0-only","version":"v1.2.3","revision":"REV","source":"https://example.org/fork","source_archive":"https://example.org/source/a.tar.gz"}"#
      .replacingOccurrences(of: "REV", with: revision)
    XCTAssertEqual(ReleaseSource.parse(Data(json.utf8))?.revision, revision)
    XCTAssertNil(
      ReleaseSource.parse(Data(json.replacingOccurrences(of: revision, with: "main").utf8)))
    XCTAssertNil(ReleaseSource.parse(Data(String(repeating: " ", count: 16 * 1024 + 1).utf8)))
    XCTAssertNil(
      ReleaseSource.parse(
        Data(
          json.replacingOccurrences(
            of: "https://example.org/source/a.tar.gz", with: "javascript:alert(1)"
          ).utf8)))
  }
  #if canImport(UIKit)
    func testAppAndEmbeddedShareExtensionPackageLegalResources() throws {
      let app = Bundle.main
      let plugins = try XCTUnwrap(app.builtInPlugInsURL)
      let extensionBundle = try XCTUnwrap(
        Bundle(url: plugins.appendingPathComponent("PsstShareExtension.appex")))
      for bundle in [app, extensionBundle] {
        let licenseURL = try XCTUnwrap(
          bundle.url(forResource: "AGPL-3.0-only", withExtension: "txt"))
        let license = try String(contentsOf: licenseURL, encoding: .utf8)
        XCTAssertTrue(license.contains("GNU AFFERO GENERAL PUBLIC LICENSE"))
        let noticesURL = try XCTUnwrap(
          bundle.url(forResource: "THIRD_PARTY_NOTICES", withExtension: "txt"))
        XCTAssertTrue(try String(contentsOf: noticesURL, encoding: .utf8).contains("Kotlin/Native"))
        let inventoryURL = try XCTUnwrap(
          bundle.url(forResource: "dependency-inventory", withExtension: "json"))
        let inventory = try XCTUnwrap(
          JSONSerialization.jsonObject(with: Data(contentsOf: inventoryURL)) as? [String: Any])
        XCTAssertEqual(inventory["platform"] as? String, "ios")
        XCTAssertEqual(inventory["notices_complete_for_scope"] as? Bool, true)
        XCTAssertFalse(try XCTUnwrap(inventory["components"] as? [[String: Any]]).isEmpty)
        let sourceURL = try XCTUnwrap(
          bundle.url(forResource: "ClientSource", withExtension: "json"))
        let source = try XCTUnwrap(
          JSONSerialization.jsonObject(with: Data(contentsOf: sourceURL)) as? [String: String])
        let revision = try XCTUnwrap(source["revision"])
        XCTAssertTrue(
          revision.isEmpty
            || (revision.count == 40
              && revision.range(of: #"^[a-f0-9]{40}$"#, options: .regularExpression) != nil)
        )
      }
    }
  #endif

}

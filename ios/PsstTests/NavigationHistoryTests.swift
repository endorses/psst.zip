import Foundation
@testable import Psst
import UIKit
import Vision
import XCTest

@MainActor
final class NavigationHistoryTests: XCTestCase {
    private let session = DeviceSession(serverURL: "https://one.example", userID: "user", username: "name", token: "token", sessionID: "session", expiresAt: "later")
    private func owned(_ id: String, slot: Bool = false, date: TimeInterval = 10) -> TransferRecord {
        TransferRecord(id: id, direction: slot ? .received : .sent, state: .complete, createdAt: Date(timeIntervalSince1970: date), fileCount: 1, totalSize: 10, serverURL: session.serverURL, ownerID: session.userID, isSlot: slot)
    }

    private func local(_ origin: String = "https://one.example", date: TimeInterval = 20) -> GuestDownload {
        GuestDownload(id: GuestDownload.identity(origin: origin, transferID: "same"), origin: origin, transferID: "same", createdAt: Date(timeIntervalSince1970: date))
    }

    func testMixedHistoryOrdersFiltersAndPreservesBothMeanings() {
        let account = [owned("same"), owned("slot", slot: true, date: 30)]
        let downloads = [local()]
        let all = HistoryEntry.combine(account: account, downloads: downloads, session: session, filter: .all)
        XCTAssertEqual(all.count, 3)
        XCTAssertEqual(all.map(\.date), [30, 20, 10].map { Date(timeIntervalSince1970: $0) })
        XCTAssertEqual(Set(all.map(\.id)).count, 3)
        for filter in [HistoryFilter.sent, .receive, .downloaded] {
            XCTAssertEqual(HistoryEntry.combine(account: account, downloads: downloads, session: session, filter: filter).count, 1)
        }
    }

    func testSignedOutAndChangedAccountKeepLocalHistoryOnly() {
        let downloads = [local()]
        XCTAssertEqual(HistoryEntry.combine(account: [owned("same")], downloads: downloads, session: nil, filter: .all).count, 1)
        let other = DeviceSession(serverURL: "https://two.example", userID: "other", username: "other", token: "other", sessionID: "other", expiresAt: "later")
        XCTAssertEqual(HistoryEntry.combine(account: [owned("same")], downloads: downloads, session: other, filter: .all).count, 1)
    }

    func testEqualIDsAcrossOriginsAndTypesDoNotCollideAndSortStably() {
        let downloads = [local(), local("https://two.example")]
        let first = HistoryEntry.combine(account: [owned("same"), owned("same", slot: true)], downloads: downloads, session: session, filter: .all)
        let second = HistoryEntry.combine(account: [owned("same", slot: true), owned("same")], downloads: downloads.reversed(), session: session, filter: .all)
        XCTAssertEqual(Set(first.map(\.id)).count, 4)
        XCTAssertEqual(first.map(\.id), second.map(\.id))
    }

    func testCompletedSendCannotRestartOnBackForegroundOrRepeatStart() {
        XCTAssertFalse(SendState.complete.permitsStart)
        XCTAssertFalse(SendState.encrypting.permitsStart)
        XCTAssertFalse(SendState.uploading(progress: 0.5).permitsStart)
        XCTAssertTrue(SendState.idle.permitsStart)
        XCTAssertTrue(SendState.failed("Retry").permitsStart)
    }

    func testBrandedQRDecodesDownloadUploadAndPairingPayloads() throws {
        XCTAssertNotNil(UIImage(named: "BrandSymbol"))
        let key = String(repeating: "A", count: 43)
        let origin = "https://a-long-self-hosted-transfer-server.example:8443"
        let values = [
            "\(origin)/d/123e4567-e89b-12d3-a456-426614174000#\(key)",
            "\(origin)/u/123e4567-e89b-12d3-a456-426614174000#\(key)",
            "{\"type\":\"psst-pairing\",\"version\":1,\"server_url\":\"\(origin)\",\"code\":\"\(key)\"}",
        ]
        for value in values {
            let image = try XCTUnwrap(QRCodeGenerator.generate(from: value, size: 640)?.cgImage)
            let request = VNDetectBarcodesRequest()
            request.symbologies = [.qr]
            try VNImageRequestHandler(cgImage: image).perform([request])
            XCTAssertEqual(request.results?.compactMap(\.payloadStringValue), [value])
        }
    }
}

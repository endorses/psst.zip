import AVFoundation
import Foundation
import UIKit
import Vision
import XCTest

@testable import Psst

@MainActor
final class NavigationHistoryTests: XCTestCase {
    private let session = DeviceSession(serverURL: "https://one.example", userID: "user", username: "name", token: "token", sessionID: "session", expiresAt: "later")
    private func owned(_ id: String, slot: Bool = false, date: TimeInterval = 10) -> TransferRecord {
        TransferRecord(
            id: id, direction: slot ? .received : .sent, state: .complete, createdAt: Date(timeIntervalSince1970: date), fileCount: 1, totalSize: 10, serverURL: session.serverURL,
            ownerID: session.userID, isSlot: slot)
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

    func testHistoryTitlesKeepUnicodeFilenameAndAdditionalCount() throws {
        var record = owned("sent")
        record.title = "夏の写真📷.jpeg"
        record.fileCount = 4
        XCTAssertEqual(record.displayTitle, "夏の写真📷.jpeg + 3 files")
        record.customTitle = "Wedding photos"
        XCTAssertEqual(record.displayTitle, "Wedding photos")
        let restored = try JSONDecoder().decode(TransferRecord.self, from: JSONEncoder().encode(record))
        XCTAssertEqual(restored.displayTitle, "Wedding photos")
        record.customTitle = nil
        XCTAssertEqual(record.displayTitle, "夏の写真📷.jpeg + 3 files")
    }

    func testPollingAndStaleCheckpointPreserveRenameAndClearByTypedAccountIdentity() {
        var refreshed = owned("same")
        refreshed.title = "notes.pdf"
        var named = refreshed
        named.customTitle = "My documents"
        XCTAssertEqual(refreshed.preservingLocalName(from: named).displayTitle, "My documents")
        var stale = named
        stale.customTitle = "Old name"
        named.customTitle = nil
        XCTAssertNil(stale.preservingLocalName(from: named).customTitle)
        var other = named
        other.customTitle = "Private"
        other.ownerID = "another user"
        XCTAssertNil(refreshed.preservingLocalName(from: other).customTitle)
        other = owned("same", slot: true)
        other.customTitle = "A slot with the same ID"
        XCTAssertNotEqual(other.localID, refreshed.localID)
        XCTAssertNil(refreshed.preservingLocalName(from: other).customTitle)
    }

    func testHistoryMigrationPersistenceAndRefreshKeepLocalNameWithoutTypeCollision() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let suiteName = "psst-history-tests-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer {
            defaults.removePersistentDomain(forName: suiteName)
            try? FileManager.default.removeItem(at: directory)
        }
        var original = owned("shared-id")
        original.title = "report.pdf"
        original.customTitle = "Private document"
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        try defaults.set(encoder.encode([original]), forKey: AppConstants.transferHistoryKey)
        let file = directory.appendingPathComponent("history.json")
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertEqual(store.visible(for: session).first?.displayTitle, "Private document")
        try store.update(owned("shared-id"))
        try store.add(owned("shared-id", slot: true))
        let relaunched = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertEqual(relaunched.visible(for: session).count, 2)
        XCTAssertEqual(relaunched.records.first(where: { $0.isSlot != true })?.customTitle, "Private document")
        XCTAssertNil(relaunched.records.first(where: { $0.isSlot == true })?.customTitle)
        XCTAssertEqual(relaunched.records.first(where: { $0.isSlot != true })?.title, "report.pdf")
        XCTAssertNil(defaults.data(forKey: AppConstants.transferHistoryKey))
        let another = DeviceSession(serverURL: session.serverURL, userID: "other", username: "other", token: "other", sessionID: "other", expiresAt: "later")
        XCTAssertTrue(relaunched.visible(for: another).isEmpty)
    }

    func testBatchSnapshotKeepsOtherRecordsAndConcurrentLocalNames() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let suite = "history-batch-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let file = directory.appendingPathComponent("history.json")
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        var named = owned("refreshed")
        named.customTitle = "Keep this local name"
        try store.add(named)
        try store.add(owned("another"))
        var incoming = owned("refreshed")
        incoming.fileCount = 250
        try store.applySnapshot([incoming])
        let restored = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertEqual(restored.records.count, 2)
        XCTAssertEqual(restored.records.first(where: { $0.id == "refreshed" })?.fileCount, 250)
        XCTAssertEqual(restored.records.first(where: { $0.id == "refreshed" })?.customTitle, "Keep this local name")
        XCTAssertNotNil(restored.records.first(where: { $0.id == "another" }))
    }

    func testHistoryPageMergePreservesUnloadedRecordsAndConcurrentCheckpoints() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let suite = "history-page-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let file = directory.appendingPathComponent("history.json")
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        let id = "01234567-89ab-cdef-0123-456789abcdef"
        var local = owned(id)
        local.isSlot = true
        local.fileCount = 3
        local.shareURL = "https://one.example/u/" + id + "#local-key"
        try store.add(local)
        try store.add(owned("unloaded"))
        let secondWriter = TransferHistoryStore(defaults: defaults, fileURL: file)
        var updated = local
        updated.savedFiles = ["child|file": "Received/file"]
        updated.savedTransfers = ["child"]
        updated.customTitle = "Concurrent local name"
        try secondWriter.update(updated)
        let unknown =
            #"{"paginated":true,"transfers":[],"slots":[{"id":"01234567-89ab-cdef-0123-456789abcdef","status":"has_uploads","file_count":null,"completed_files":null,"total_size":null,"summary":{"state":"updating","file_count":null,"completed_files":null,"total_size":null}}],"next_cursor":"more"}"#
        try store.mergeResourcePage(JSONDecoder().decode(ResourceList.self, from: Data(unknown.utf8)), session: session)
        let merged = try XCTUnwrap(store.records.first { $0.id == id })
        XCTAssertEqual(merged.fileCount, 3)
        XCTAssertEqual(merged.serverSummaryKnown, false)
        XCTAssertEqual(merged.shareURL, local.shareURL)
        XCTAssertEqual(merged.savedFiles, updated.savedFiles)
        XCTAssertEqual(merged.savedTransfers, updated.savedTransfers)
        XCTAssertEqual(merged.customTitle, updated.customTitle)
        XCTAssertNotEqual(store.records.first { $0.id == "unloaded" }?.state, .revoked)
        let ready = unknown.replacingOccurrences(of: "null", with: "1").replacingOccurrences(of: "updating", with: "ready")
        try store.mergeResourcePage(JSONDecoder().decode(ResourceList.self, from: Data(ready.utf8)), session: session)
        XCTAssertEqual(store.records.first { $0.id == id }?.fileCount, 1)
        XCTAssertEqual(store.records.first { $0.id == id }?.savedFiles, updated.savedFiles)
    }

    func testAdminAndRestrictedSessionsExposeOnlyDeviceDownloads() {
        for state in ["admin", "restricted"] {
            var blocked = session
            blocked.role = state == "admin" ? "admin" : "user"
            blocked.mustChangePassword = state == "restricted"
            XCTAssertFalse(blocked.canTransfer)
            XCTAssertEqual(HistoryEntry.combine(account: [owned("same")], downloads: [local()], session: blocked, filter: .all).count, 1)
        }
    }

    func testOldDeviceSessionRemainsDecodableWithoutMandatoryChangeFlag() throws {
        let json = #"{"serverURL":"https://one.example","userID":"one","username":"name","token":"test","sessionID":"session","expiresAt":"later","role":"user"}"#
        let stored = try JSONDecoder().decode(DeviceSession.self, from: Data(json.utf8))
        XCTAssertNil(stored.mustChangePassword)
        XCTAssertTrue(stored.canTransfer)
    }

    func testPasswordConfirmationAndBytePolicy() {
        XCTAssertTrue(PasswordReplacementPolicy.valid(current: "temporary password", replacement: "new secure password", confirmation: "new secure password"))
        XCTAssertFalse(PasswordReplacementPolicy.valid(current: "temporary password", replacement: "temporary password", confirmation: "temporary password"))
        XCTAssertFalse(PasswordReplacementPolicy.valid(current: "temporary password", replacement: "new secure password", confirmation: "new secure password "))
        XCTAssertFalse(PasswordReplacementPolicy.valid(current: "temporary password", replacement: "short", confirmation: "short"))
        let long = String(repeating: "🔐", count: 19)
        XCTAssertFalse(PasswordReplacementPolicy.valid(current: "temporary password", replacement: long, confirmation: long))
    }

    func testCameraSelectionPrefersRearAndKeepsFrontOnlyAndNoCameraCases() {
        let positions: [AVCaptureDevice.Position] = [.front, .back, .unspecified]
        XCTAssertEqual(ScannerCameraSelection.ordered(positions, position: { $0 }), [.back, .front, .unspecified])
        XCTAssertEqual(ScannerCameraSelection.ordered([AVCaptureDevice.Position.front], position: { $0 }), [.front])
        XCTAssertTrue(ScannerCameraSelection.ordered([AVCaptureDevice.Position](), position: { $0 }).isEmpty)
    }

    func testBrandedQRDecodesDownloadUploadAndPairingPayloads() throws {
        XCTAssertNotNil(UIImage(named: "BrandQrIcon"))
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

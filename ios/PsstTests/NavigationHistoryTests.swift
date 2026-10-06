import AVFoundation
import CoreImage
import CoreImage.CIFilterBuiltins
import Foundation
import UIKit
import Vision
import XCTest

@testable import Psst

@MainActor
final class NavigationHistoryTests: XCTestCase {
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

    private let session = DeviceSession(
        serverURL: "https://one.example", userID: "user", username: "name", token: "token", sessionID: "session",
        expiresAt: "later")
    private func owned(_ id: String, slot: Bool = false, date: TimeInterval = 10) -> TransferRecord {
        TransferRecord(
            id: id, direction: slot ? .received : .sent, state: .complete, createdAt: Date(timeIntervalSince1970: date),
            fileCount: 1, totalSize: 10, serverURL: session.serverURL,
            ownerID: session.userID, isSlot: slot
        )
    }

    private func local(_ origin: String = "https://one.example", date: TimeInterval = 20) -> GuestDownload {
        GuestDownload(
            id: GuestDownload.identity(origin: origin, transferID: "same"), origin: origin, transferID: "same",
            createdAt: Date(timeIntervalSince1970: date))
    }

    func testMixedHistoryOrdersFiltersAndPreservesBothMeanings() {
        let account = [owned("same"), owned("slot", slot: true, date: 30)]
        let downloads = [local()]
        let all = HistoryEntry.combine(account: account, downloads: downloads, session: session, filter: .all)
        XCTAssertEqual(all.count, 3)
        XCTAssertEqual(all.map(\.date), [30, 20, 10].map { Date(timeIntervalSince1970: $0) })
        XCTAssertEqual(Set(all.map(\.id)).count, 3)
        for filter in [HistoryFilter.sent, .receive, .downloaded] {
            XCTAssertEqual(
                HistoryEntry.combine(account: account, downloads: downloads, session: session, filter: filter).count, 1)
        }
    }

    func testSignedOutAndChangedAccountKeepLocalHistoryOnly() {
        let downloads = [local()]
        XCTAssertEqual(
            HistoryEntry.combine(account: [owned("same")], downloads: downloads, session: nil, filter: .all).count, 1)
        let other = DeviceSession(
            serverURL: "https://two.example", userID: "other", username: "other", token: "other", sessionID: "other",
            expiresAt: "later")
        XCTAssertEqual(
            HistoryEntry.combine(account: [owned("same")], downloads: downloads, session: other, filter: .all).count, 1)
    }

    func testEqualIDsAcrossOriginsAndTypesDoNotCollideAndSortStably() {
        let downloads = [local(), local("https://two.example")]
        let first = HistoryEntry.combine(
            account: [owned("same"), owned("same", slot: true)], downloads: downloads, session: session, filter: .all)
        let second = HistoryEntry.combine(
            account: [owned("same", slot: true), owned("same")], downloads: downloads.reversed(), session: session,
            filter: .all)
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

    func testLegacyReceivedTitleIsSanitizedOnlyForDisplayAndCustomLabelIsPreserved() {
        var record = owned("received", slot: true)
        record.title = "photo\u{061C}\u{200E}\u{200F}\u{202E}jpg.exe"
        record.fileCount = 2
        let original = record.title
        XCTAssertEqual(record.safeDisplayTitle, "photo____jpg.exe + 1 file")
        XCTAssertEqual(record.title, original)
        record.customTitle = "My personal label"
        XCTAssertEqual(record.safeDisplayTitle, "My personal label")
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
        XCTAssertEqual(
            relaunched.visible(for: session).first(where: { $0.isSlot != true })?.customTitle, "Private document")
        XCTAssertNil(relaunched.visible(for: session).first(where: { $0.isSlot == true })?.customTitle)
        XCTAssertEqual(relaunched.visible(for: session).first(where: { $0.isSlot != true })?.title, "report.pdf")
        XCTAssertNotNil(defaults.data(forKey: AppConstants.transferHistoryKey))  // Migration retains its original source.
        let another = DeviceSession(
            serverURL: session.serverURL, userID: "other", username: "other", token: "other", sessionID: "other",
            expiresAt: "later")
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
        XCTAssertEqual(restored.visible(for: session).count, 2)
        XCTAssertEqual(restored.visible(for: session).first(where: { $0.id == "refreshed" })?.fileCount, 250)
        XCTAssertEqual(
            restored.visible(for: session).first(where: { $0.id == "refreshed" })?.customTitle, "Keep this local name")
        XCTAssertNotNil(restored.visible(for: session).first(where: { $0.id == "another" }))
    }

    func testOversizedBatchMutationRollsBackEveryChangedRecord() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let suite = "history-byte-budget-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = TransferHistoryStore(defaults: defaults, fileURL: directory.appendingPathComponent("history.json"))
        let first = owned("first")
        let second = owned("second")
        try store.add(first)
        try store.add(second)
        let largePath = String(repeating: "a", count: 9 * 1024 * 1024)
        XCTAssertThrowsError(
            try store.mutate(ids: [first.localID, second.localID]) { records in
                for index in records.indices {
                    records[index].customTitle = largePath
                }
            }
        )
        XCTAssertNil(try store.record(first.localID)?.customTitle)
        XCTAssertNil(try store.record(second.localID)?.customTitle)
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
        let updated = local
        _ = try secondWriter.saveReceivedFile(
            parent: updated, transferID: "child", blobID: "file", path: "Received/file", size: 3, title: "file")
        try secondWriter.completeReceivedTransfer(
            parent: updated, transferID: "child", blobIDs: ["file"], fileExists: { _, _ in true })
        // Incoming status updates preserve persisted local names. Write the local
        // edit through the coordinated mutation used for explicit device edits.
        try secondWriter.mutate(ids: [updated.localID]) { records in
            records[0].customTitle = "Concurrent local name"
        }
        try secondWriter.update(updated)
        XCTAssertEqual(try secondWriter.record(updated.localID)?.customTitle, "Concurrent local name")
        let unknown =
            #"{"paginated":true,"transfers":[],"slots":[{"id":"01234567-89ab-cdef-0123-456789abcdef","status":"has_uploads","file_count":null,"completed_files":null,"total_size":null,"summary":{"state":"updating","file_count":null,"completed_files":null,"total_size":null}}],"next_cursor":"more"}"#
        try store.mergeResourcePage(JSONDecoder().decode(ResourceList.self, from: Data(unknown.utf8)), session: session)
        let merged = try XCTUnwrap(store.visible(for: session).first { $0.id == id })
        XCTAssertEqual(merged.fileCount, 3)
        XCTAssertEqual(merged.serverSummaryKnown, false)
        XCTAssertEqual(merged.shareURL, local.shareURL)
        XCTAssertNil(merged.savedFiles)
        XCTAssertNil(merged.savedTransfers)
        XCTAssertTrue(
            try store.receiveCheckpoints(parent: merged, transferIDs: ["child"], fileExists: { _, _ in true })["child"]?
                .isSaved(fileCount: 1) == true)
        XCTAssertEqual(merged.customTitle, "Concurrent local name")
        XCTAssertEqual(store.visible(for: session).first { $0.id == "unloaded" }?.state, .complete)
        let ready = unknown.replacingOccurrences(of: "null", with: "1").replacingOccurrences(
            of: "updating", with: "ready")
        try store.mergeResourcePage(JSONDecoder().decode(ResourceList.self, from: Data(ready.utf8)), session: session)
        XCTAssertEqual(store.visible(for: session).first { $0.id == id }?.fileCount, 1)
        XCTAssertEqual(store.visible(for: session).first { $0.id == id }?.customTitle, "Concurrent local name")
        XCTAssertTrue(
            try store.receiveCheckpoints(parent: merged, transferIDs: ["child"], fileExists: { _, _ in true })["child"]?
                .isSaved(fileCount: 1) == true)
    }

    func testIndexedHistoryImportAndPagesPreserveEveryRecord() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let suite = "history-indexed-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let file = directory.appendingPathComponent("history.json")
        let original = (0..<130).map { owned("old-" + String($0)) }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let originalData = try encoder.encode(original)
        try originalData.write(to: file)
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertFalse(store.isReady)
        XCTAssertThrowsError(try store.add(owned("new-before-import")))
        await store.finishMigration()
        XCTAssertTrue(store.isReady)
        var next: HistoryRecordDatabase.Cursor?
        var ids = Set<String>()
        repeat {
            let page = try store.page(session: session, after: next)
            XCTAssertLessThanOrEqual(page.records.count, 50)
            for record in page.records {
                XCTAssertTrue(ids.insert(record.id).inserted)
            }
            next = page.next
        } while next != nil
        XCTAssertEqual(ids, Set(original.map(\.id)))
        XCTAssertEqual(try Data(contentsOf: file), originalData)
        let reopened = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertTrue(reopened.isReady)
        XCTAssertEqual(try reopened.record(original[100].localID)?.id, original[100].id)
    }

    func testAdminAndRestrictedSessionsExposeOnlyDeviceDownloads() {
        for state in ["admin", "restricted"] {
            var blocked = session
            blocked.role = state == "admin" ? "admin" : "user"
            blocked.mustChangePassword = state == "restricted"
            XCTAssertFalse(blocked.canTransfer)
            XCTAssertEqual(
                HistoryEntry.combine(account: [owned("same")], downloads: [local()], session: blocked, filter: .all)
                    .count, 1)
        }
    }

    func testOldDeviceSessionRemainsDecodableWithoutMandatoryChangeFlag() throws {
        let json =
            #"{"serverURL":"https://one.example","userID":"one","username":"name","token":"test","sessionID":"session","expiresAt":"later","role":"user"}"#
        let stored = try JSONDecoder().decode(DeviceSession.self, from: Data(json.utf8))
        XCTAssertNil(stored.mustChangePassword)
        XCTAssertTrue(stored.canTransfer)
    }

    func testPasswordConfirmationAndBytePolicy() {
        XCTAssertTrue(
            PasswordReplacementPolicy.valid(
                current: "temporary password", replacement: "new secure password", confirmation: "new secure password"))
        XCTAssertFalse(
            PasswordReplacementPolicy.valid(
                current: "temporary password", replacement: "temporary password", confirmation: "temporary password"))
        XCTAssertFalse(
            PasswordReplacementPolicy.valid(
                current: "temporary password", replacement: "new secure password", confirmation: "new secure password ")
        )
        XCTAssertFalse(
            PasswordReplacementPolicy.valid(current: "temporary password", replacement: "short", confirmation: "short"))
        let long = String(repeating: "🔐", count: 19)
        XCTAssertFalse(
            PasswordReplacementPolicy.valid(current: "temporary password", replacement: long, confirmation: long))
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
            let decoded = request.results?.compactMap(\.payloadStringValue)
            if decoded != [value] {
                // Keep the production Vision requirement, but retain enough native evidence to
                // distinguish renderer damage from a decoder or pixel-format limitation.
                let filter = CIFilter.qrCodeGenerator()
                filter.message = Data(value.utf8)
                filter.correctionLevel = "H"
                let output = try XCTUnwrap(filter.outputImage)
                let quietBounds = output.extent.insetBy(dx: -4, dy: -4)
                let background = CIImage(color: .white).cropped(to: quietBounds)
                let plain = output.composited(over: background).transformed(by: CGAffineTransform(scaleX: 8, y: 8))
                let context = CIContext()
                let plainImage = try XCTUnwrap(context.createCGImage(plain, from: plain.extent))
                let plainRequest = VNDetectBarcodesRequest()
                plainRequest.symbologies = [.qr]
                try VNImageRequestHandler(cgImage: plainImage).perform([plainRequest])
                let detector = try XCTUnwrap(
                    CIDetector(
                        ofType: CIDetectorTypeQRCode, context: context,
                        options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]))
                let coreImageDecoded = detector.features(in: CIImage(cgImage: image)).compactMap {
                    ($0 as? CIQRCodeFeature)?.messageString
                }
                let evidence =
                    "payloadBytes=\(value.utf8.count), branded=\(image.width)x\(image.height), bitsPerComponent=\(image.bitsPerComponent), bitsPerPixel=\(image.bitsPerPixel), bitmapInfo=\(image.bitmapInfo.rawValue), colorSpace=\(String(describing: image.colorSpace?.name)), VisionRevision=\(request.revision), plainVision=\(String(describing: plainRequest.results?.compactMap(\.payloadStringValue))), brandedCoreImage=\(coreImageDecoded)"
                print("QR decoding diagnostic: \(evidence)")
                for (name, diagnosticImage) in [("branded", image), ("plain-core-image", plainImage)] {
                    let attachment = XCTAttachment(image: UIImage(cgImage: diagnosticImage))
                    attachment.name = "QR \(name) \(value.utf8.count) bytes"
                    attachment.lifetime = .keepAlways
                    add(attachment)
                }
                XCTAssertEqual(decoded, [value], evidence)
            } else {
                XCTAssertEqual(decoded, [value])
            }
        }
    }
}

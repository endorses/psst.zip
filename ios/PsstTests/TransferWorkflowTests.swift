import CoreImage
@testable import Psst
import XCTest

final class TransferWorkflowTests: XCTestCase {
    private func record() -> TransferRecord {
        TransferRecord(id: "original-slot", direction: .received, state: .complete, createdAt: Date(),
                       fileCount: 2, totalSize: 0, shareURL: nil, serverURL: "https://one.example", ownerID: "alice", isSlot: true)
    }

    func testPartialSaveRetryKeepsOriginalSlotAndSkipsSuccessfulFile() throws {
        var checkpoint = ReceiveCheckpoint(record: record(), fileExists: { $0 == "saved-first" })
        checkpoint.saved(transferID: "child", blobID: "first", path: "saved-first", size: 10, title: "notes.txt")
        let persisted = try JSONEncoder().encode(checkpoint.record)
        let reopened = try JSONDecoder().decode(TransferRecord.self, from: persisted)
        let retry = ReceiveCheckpoint(record: reopened, fileExists: { $0 == "saved-first" })
        XCTAssertEqual(retry.slotID, "original-slot")
        XCTAssertFalse(retry.needsFile(transferID: "child", blobID: "first"))
        XCTAssertTrue(retry.needsFile(transferID: "child", blobID: "second"))
        XCTAssertFalse(retry.readyToAcknowledge(transferID: "child", blobIDs: ["first", "second"]))
    }

    func testAcknowledgementOnlyAfterAllSavedAndCountsDoNotDoubleOnRetry() {
        var checkpoint = ReceiveCheckpoint(record: record(), fileExists: { _ in true })
        XCTAssertFalse(checkpoint.completed(transferID: "child", blobIDs: []))
        checkpoint.saved(transferID: "child", blobID: "first", path: "first", size: 10, title: "notes.txt")
        XCTAssertFalse(checkpoint.completed(transferID: "child", blobIDs: ["first", "second"]))
        checkpoint.saved(transferID: "child", blobID: "second", path: "second", size: 20, title: "other.txt")
        checkpoint.saved(transferID: "child", blobID: "first", path: "first", size: 10, title: "notes.txt")
        XCTAssertTrue(checkpoint.completed(transferID: "child", blobIDs: ["first", "second"]))
        XCTAssertTrue(checkpoint.completed(transferID: "child", blobIDs: ["first", "second"]))
        XCTAssertEqual(checkpoint.record.totalSize, 30)
        XCTAssertEqual(checkpoint.record.savedTransfers, ["child"])
    }

    func testMissingLocalFileIsSavedAgain() {
        var checkpoint = ReceiveCheckpoint(record: record(), fileExists: { _ in false })
        checkpoint.saved(transferID: "child", blobID: "first", path: "deleted", size: 10, title: "notes.txt")
        XCTAssertTrue(checkpoint.needsFile(transferID: "child", blobID: "first"))
        XCTAssertFalse(checkpoint.readyToAcknowledge(transferID: "child", blobIDs: ["first"]))
    }

    func testOldHistoryDecodesWithoutAssigningAnotherAccountsOwnership() throws {
        let json = #"{"id":"old","direction":"sent","state":"complete","createdAt":0,"fileCount":1,"totalSize":12,"shareURL":"https://old.example/d/old#secret"}"#
        let old = try JSONDecoder().decode(TransferRecord.self, from: Data(json.utf8))
        XCTAssertNil(old.ownerID)
        XCTAssertNil(old.savedFiles)
        XCTAssertFalse(old.belongs(to: session()))
        XCTAssertEqual(old.shareURL, "https://old.example/d/old#secret")
    }

    private func session(user: String = "alice", host: String = "https://one.example", token: String = "first") -> DeviceSession {
        DeviceSession(serverURL: host, userID: user, username: user, token: token, sessionID: token, expiresAt: "2099-01-01T00:00:00Z")
    }

    func testLateCallbacksRejectLogoutAccountServerAndSessionChanges() {
        let identity = JobIdentity(session: session())
        XCTAssertTrue(identity.accepts(session(), cancelled: false))
        XCTAssertFalse(identity.accepts(nil, cancelled: false))
        XCTAssertFalse(identity.accepts(session(user: "bob"), cancelled: false))
        XCTAssertFalse(identity.accepts(session(host: "https://two.example"), cancelled: false))
        XCTAssertFalse(identity.accepts(session(token: "renewed"), cancelled: false))
        XCTAssertFalse(identity.accepts(session(), cancelled: true))
        XCTAssertTrue(identity.mayResume(as: session(token: "renewed")))
        XCTAssertFalse(identity.mayResume(as: session(user: "bob")))
    }

    func testLegacyVisibilityRequiresAdminOnOriginalServer() {
        var old = record()
        old.ownerID = nil
        XCTAssertFalse(old.canManage(as: session()))
        var administrator = session()
        administrator.role = "admin"
        XCTAssertTrue(old.canManage(as: administrator))
        var elsewhere = session(host: "https://two.example")
        elsewhere.role = "admin"
        XCTAssertFalse(old.canManage(as: elsewhere))
    }

    func testProgressIsWeightedByBytesNotFileCount() {
        XCTAssertEqual(UploadProgress(name: "big", sent: 25, total: 100).fraction, 0.25)
        XCTAssertEqual(UploadProgress(name: "empty", sent: 0, total: 0).fraction, 0)
    }

    func testCredentialOriginsRejectPathsAndEmbeddedCredentials() throws {
        XCTAssertEqual(try AccountHTTP.origin("https://EXAMPLE.com/"), "https://example.com")
        for origin in ["https://user:pass@example.com", "https://example.com/path", "https://example.com#key", "file:///tmp/file"] {
            XCTAssertThrowsError(try AccountHTTP.origin(origin))
        }
    }

    func testServerTimestampAcceptsWholeAndFractionalSeconds() throws {
        let whole = try XCTUnwrap(ServerTimestamp.parse("2026-10-03T17:05:31Z"))
        let fractional = try XCTUnwrap(ServerTimestamp.parse("2026-10-03T17:05:31.123456789Z"))
        let offset = try XCTUnwrap(ServerTimestamp.parse("2026-10-03T19:05:31.123456789+02:00"))
        XCTAssertEqual(fractional.timeIntervalSince(whole), 0.123456789, accuracy: 0.001)
        XCTAssertEqual(offset.timeIntervalSince(fractional), 0, accuracy: 0.001)
        XCTAssertNil(ServerTimestamp.parse(nil))
        XCTAssertNil(ServerTimestamp.parse("not-a-timestamp"))
    }

    @MainActor
    func testMixedValidAndOversizedShareSelectionRejectsAllAndCleansCopies() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let first = directory.appendingPathComponent("first.txt")
        try Data("first".utf8).write(to: first)
        do {
            _ = try await ShareSelection.loadAll([0, 1], load: { index in
                if index == 0 {
                    return first
                }
                throw ShareSelectionError.tooLarge
            })
            XCTFail("An oversized attachment must reject the entire selection")
        } catch {
            XCTAssertEqual(error as? ShareSelectionError, .tooLarge)
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.path))
    }

    @MainActor
    func testUnknownProviderFailureDoesNotPrepareValidSubset() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let first = directory.appendingPathComponent("first.txt")
        try Data("first".utf8).write(to: first)
        do {
            _ = try await ShareSelection.loadAll([0, 1], load: { index in
                if index == 0 {
                    return first
                }
                throw NSError(domain: "provider", code: 1)
            })
            XCTFail("An unreadable attachment must reject the entire selection")
        } catch {
            XCTAssertEqual(error as? ShareSelectionError, .unreadable)
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.path))
    }

    func testProviderCopyRejectsElevenMiBBeforeCopying() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let source = directory.appendingPathComponent("large.bin")
        XCTAssertTrue(FileManager.default.createFile(atPath: source.path, contents: Data()))
        let handle = try FileHandle(forWritingTo: source)
        defer { try? handle.close() }
        try handle.truncate(atOffset: 11 * 1024 * 1024)
        XCTAssertThrowsError(try ShareSelection.copyProviderFile(source)) { error in
            XCTAssertEqual(error as? ShareSelectionError, .tooLarge)
        }
    }

    func testQRCodeKeepsFourModuleQuietZoneAndDecodesAfterIntegerScaling() throws {
        let link = "https://files.example/d/transfer#abcdefghijklmnopqrstuvwxyz0123456789"
        let native = try XCTUnwrap(QRCodeGenerator.generate(from: link, size: 1)?.cgImage)
        XCTAssertEqual(native.bitsPerPixel, 8)
        let pixels = try XCTUnwrap(native.dataProvider?.data) as Data
        var darkColumns = Set<Int>()
        var darkRows = Set<Int>()
        for y in 0 ..< native.height {
            for x in 0 ..< native.width where pixels[y * native.bytesPerRow + x] < 128 {
                darkColumns.insert(x)
                darkRows.insert(y)
            }
        }
        XCTAssertEqual(darkColumns.min(), 4)
        XCTAssertEqual(darkRows.min(), 4)
        XCTAssertEqual(darkColumns.max(), native.width - 5)
        XCTAssertEqual(darkRows.max(), native.height - 5)

        let scaled = try XCTUnwrap(QRCodeGenerator.generate(from: link, size: 1080)?.cgImage)
        XCTAssertEqual(scaled.width, native.width * (1080 / native.width))
        XCTAssertEqual(scaled.width, scaled.height)
        let detector = try XCTUnwrap(CIDetector(ofType: CIDetectorTypeQRCode, context: CIContext(),
                                                options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]))
        let feature = try XCTUnwrap(detector.features(in: CIImage(cgImage: scaled)).first as? CIQRCodeFeature)
        XCTAssertEqual(feature.messageString, link)
    }

    func testKeyRequiresExactly32Bytes() {
        XCTAssertNil(ReceiveViewModel.decodeKey("invalid"))
        XCTAssertEqual(ReceiveViewModel.decodeKey(Data(repeating: 0, count: 32).base64EncodedString())?.count, 32)
    }
}

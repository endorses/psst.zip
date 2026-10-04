import CoreImage
import XCTest

@testable import Psst

final class TransferWorkflowTests: XCTestCase {
    private func record() -> TransferRecord {
        TransferRecord(
            id: "original-slot", direction: .received, state: .complete, createdAt: Date(),
            fileCount: 2, totalSize: 0, shareURL: nil, serverURL: "https://one.example", ownerID: "alice", isSlot: true)
    }

    func testPrivateInboxSecretIsScopedAndNotSerializedIntoHistory() throws {
        var entry = record()
        entry.receiveProtocol = 2
        let keys = ReceiveCrypto.generateKeyPair()
        let link = "https://one.example/u/" + entry.id + "#v2.public"
        defer { SecretStore.remove(entry.vaultID) }
        try entry.saveSecrets(link: link, deletionToken: "delete", receivePrivateKey: keys.privateKey)
        XCTAssertTrue(entry.canDecryptInbox)
        XCTAssertEqual(entry.capabilities?.receivePrivateKey, keys.privateKey)
        // Updating the public link/deletion capability must not discard the private key.
        try entry.saveSecrets(link: link, deletionToken: "new-delete")
        XCTAssertEqual(entry.capabilities?.receivePrivateKey, keys.privateKey)
        let history = try JSONEncoder().encode(entry)
        XCTAssertFalse(String(decoding: history, as: UTF8.self).contains(keys.privateKey.base64EncodedString()))
        var secondAccount = entry
        secondAccount.ownerID = "bob"
        XCTAssertFalse(secondAccount.canDecryptInbox)
        var secondServer = entry
        secondServer.serverURL = "https://two.example"
        XCTAssertFalse(secondServer.canDecryptInbox)
    }

    func testExistingResourceSecretDecodesWithoutPrivateInboxAuthority() throws {
        let bytes = try JSONSerialization.data(withJSONObject: ["link": "https://one.example/u/id#old", "deletionToken": "delete"])
        let value = try JSONDecoder().decode(ResourceSecrets.self, from: bytes)
        XCTAssertNil(value.receivePrivateKey)
        XCTAssertEqual(value.deletionToken, "delete")
    }

    func testPartialSaveRetryKeepsOriginalSlotAndSkipsSuccessfulFile() throws {
        let retry = ReceiveCheckpoint(
            slotID: "original-slot", transferID: "child", paths: ["first": "saved-first"], complete: false, fileExists: { path, _ in path == "saved-first" })
        XCTAssertEqual(retry.slotID, "original-slot")
        XCTAssertFalse(retry.needsFile(blobID: "first"))
        XCTAssertTrue(retry.needsFile(blobID: "second"))
        XCTAssertFalse(retry.readyToAcknowledge(blobIDs: ["first", "second"]))
    }

    func testAcknowledgementOnlyAfterAllSavedAndCountsDoNotDoubleOnRetry() {
        let checkpoint = ReceiveCheckpoint(
            slotID: "original-slot", transferID: "child", paths: ["first": "first", "second": "second"], complete: true, fileExists: { _, _ in true })
        XCTAssertFalse(checkpoint.readyToAcknowledge(blobIDs: []))
        XCTAssertFalse(checkpoint.readyToAcknowledge(blobIDs: ["first", "first"]))
        XCTAssertTrue(checkpoint.readyToAcknowledge(blobIDs: ["first", "second"]))
        XCTAssertTrue(checkpoint.isSaved(fileCount: 2))
        XCTAssertFalse(checkpoint.isSaved(fileCount: 3))
    }

    func testMissingLocalFileIsSavedAgain() {
        let checkpoint = ReceiveCheckpoint(slotID: "original-slot", transferID: "child", paths: ["first": "deleted"], complete: true, fileExists: { _, _ in false })
        XCTAssertTrue(checkpoint.needsFile(blobID: "first"))
        XCTAssertFalse(checkpoint.readyToAcknowledge(blobIDs: ["first"]))
        XCTAssertFalse(checkpoint.isSaved(fileCount: 1))
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

    func testLegacyHistoryRemainsHiddenFromMobileAccounts() {
        var old = record()
        old.ownerID = nil
        XCTAssertFalse(old.canManage(as: session()))
        var administrator = session()
        administrator.role = "admin"
        XCTAssertFalse(old.canManage(as: administrator))
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
            _ = try await ShareSelection.loadAll(
                [0, 1],
                load: { index in
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
            _ = try await ShareSelection.loadAll(
                [0, 1],
                load: { index in
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

    func testProviderCopyRejectsBeyondStreamingCeilingBeforeCopying() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let source = directory.appendingPathComponent("large.bin")
        XCTAssertTrue(FileManager.default.createFile(atPath: source.path, contents: Data()))
        let handle = try FileHandle(forWritingTo: source)
        defer { try? handle.close() }
        try handle.truncate(atOffset: UInt64(BufferedUpload.maxFileBytes) + 1)
        XCTAssertThrowsError(try ShareSelection.copyProviderFile(source)) { error in
            XCTAssertEqual(error as? ShareSelectionError, .tooLarge)
        }
    }

    func testQRCodeKeepsFourModuleQuietZoneAndDecodesAfterIntegerScaling() throws {
        let link = "https://files.example/d/transfer#abcdefghijklmnopqrstuvwxyz0123456789"
        let native = try XCTUnwrap(QRCodeGenerator.generate(from: link, size: 1)?.cgImage)
        var pixels = [UInt8](repeating: 255, count: native.width * native.height)
        try pixels.withUnsafeMutableBytes { bytes in
            let canvas = try XCTUnwrap(
                CGContext(
                    data: bytes.baseAddress, width: native.width, height: native.height, bitsPerComponent: 8, bytesPerRow: native.width, space: CGColorSpaceCreateDeviceGray(),
                    bitmapInfo: CGImageAlphaInfo.none.rawValue))
            canvas.draw(native, in: CGRect(x: 0, y: 0, width: native.width, height: native.height))
        }
        var darkColumns = Set<Int>()
        var darkRows = Set<Int>()
        for y in 0..<native.height {
            for x in 0..<native.width where pixels[y * native.width + x] < 128 {
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
        let detector = try XCTUnwrap(
            CIDetector(
                ofType: CIDetectorTypeQRCode, context: CIContext(),
                options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]))
        let feature = try XCTUnwrap(detector.features(in: CIImage(cgImage: scaled)).first as? CIQRCodeFeature)
        XCTAssertEqual(feature.messageString, link)
    }

    func testKeyRequiresExactly32Bytes() {
        XCTAssertNil(ReceiveViewModel.decodeKey("invalid"))
        XCTAssertEqual(ReceiveViewModel.decodeKey(Data(repeating: 0, count: 32).base64EncodedString())?.count, 32)
    }
}

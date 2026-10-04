import Foundation
@testable import Psst
import Shared
import XCTest

@MainActor
final class GuestDownloadTests: XCTestCase {
    private func directory() throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return root
    }

    private func record() -> GuestDownload {
        GuestDownload(id: GuestDownload.identity(origin: "https://one.example", transferID: "transfer"), origin: "https://one.example", transferID: "transfer", files: [
            GuestFile(id: "one", name: "same.txt", size: 3, mime: "text/plain"),
            GuestFile(id: "two", name: "same.txt", size: 3, mime: "text/plain"),
        ])
    }

    func testDownloadCountersKeepUnknownDistinctFromExhaustedAndSavedCopyOpenable() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.remainingDownloads = ["one": 0]
        try store.save(Data("abc".utf8), index: 0, record: &entry)
        let reopened = GuestDownloadStore(root: root)
        let restored = try XCTUnwrap(reopened.records.first)
        XCTAssertEqual(restored.remainingDownloads?["one"], 0)
        XCTAssertNil(restored.remainingDownloads?["two"])
        XCTAssertNotNil(reopened.url(restored.files[0]))
        XCTAssertFalse(restored.complete)
        XCTAssertFalse(restored.receiptPending)
    }

    func testOriginIsPartOfLocalIdentity() {
        XCTAssertNotEqual(GuestDownload.identity(origin: "http://one.example", transferID: "same"), GuestDownload.identity(origin: "http://two.example", transferID: "same"))
        XCTAssertEqual(GuestDownload.identity(origin: "http://one.example", transferID: "same"), GuestDownload.identity(origin: "http://one.example", transferID: "same"))
    }

    func testDuplicateNamesSaveSeparatelyAndPartialCheckpointSurvivesRestart() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        try store.update(entry)
        try store.save(Data("one".utf8), index: 0, record: &entry)
        let reopened = GuestDownloadStore(root: root)
        var resumed = try XCTUnwrap(reopened.records.first)
        XCTAssertNotNil(reopened.url(resumed.files[0]))
        XCTAssertNil(reopened.url(resumed.files[1]))
        XCTAssertFalse(resumed.complete)
        XCTAssertFalse(resumed.receiptPending)
        try reopened.save(Data("two".utf8), index: 1, record: &resumed)
        XCTAssertNotEqual(resumed.files[0].relativePath, resumed.files[1].relativePath)
        XCTAssertEqual(try Data(contentsOf: XCTUnwrap(reopened.url(resumed.files[0]))), Data("one".utf8))
        XCTAssertEqual(try Data(contentsOf: XCTUnwrap(reopened.url(resumed.files[1]))), Data("two".utf8))
    }

    func testKillAfterPublicationReconcilesIntentBeforeReceipt() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files = [entry.files[0]]
        let path = "Received/reconciled.txt"
        let data = Data("one".utf8)
        entry.files[0].relativePath = path
        entry.files[0].digest = GuestFiles.digest(data)
        try store.update(entry)
        try FileManager.default.createDirectory(at: root.appendingPathComponent("Received"), withIntermediateDirectories: true)
        try data.write(to: root.appendingPathComponent(path), options: .atomic)
        let reopened = GuestDownloadStore(root: root)
        let saved = try XCTUnwrap(reopened.records.first)
        XCTAssertTrue(saved.files[0].saved)
        XCTAssertTrue(saved.complete)
        XCTAssertTrue(saved.receiptPending)
        XCTAssertNotNil(reopened.url(saved.files[0]))
    }

    func testIncompleteWriteIsRemovedAndNeverReceipted() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files[0].relativePath = "partial.txt"
        entry.files[0].digest = GuestFiles.digest(Data("one".utf8))
        try store.update(entry)
        let pending = root.appendingPathComponent("partial.txt.pending")
        try Data("o".utf8).write(to: pending)
        let reopened = GuestDownloadStore(root: root)
        XCTAssertFalse(FileManager.default.fileExists(atPath: pending.path))
        XCTAssertFalse(try XCTUnwrap(reopened.records.first).receiptPending)
        XCTAssertNil(reopened.url(entry.files[0]))
    }

    func testRemovalKeepsSavedFilesAndReceiptJournal() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files = [entry.files[0]]
        try store.save(Data("one".utf8), index: 0, record: &entry)
        entry.complete = true; entry.receiptPending = true
        try store.update(entry)
        let saved = try XCTUnwrap(store.url(entry.files[0]))
        try store.remove(entry)
        XCTAssertTrue(store.records.isEmpty)
        XCTAssertEqual(try Data(contentsOf: saved), Data("one".utf8))
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: root.appendingPathComponent("guest-downloads.json"))) as? [String: Any])
        XCTAssertEqual((json["receipts"] as? [[String: String]])?.count, 1)
    }

    func testMissingOrTruncatedOutputIsNotASuccessfulCheckpoint() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        try store.save(Data("one".utf8), index: 0, record: &entry)
        let path = try XCTUnwrap(store.url(entry.files[0]))
        try Data("o".utf8).write(to: path)
        XCTAssertNil(store.url(entry.files[0]))
        try FileManager.default.removeItem(at: path)
        XCTAssertNil(store.url(entry.files[0]))
    }

    func testUnreadableHistoryNeverOverwritesExistingBytes() throws {
        let root = try directory()
        let path = root.appendingPathComponent("guest-downloads.json")
        let damaged = Data("not valid history".utf8)
        try damaged.write(to: path)
        let store = GuestDownloadStore(root: root)
        XCTAssertNotNil(store.error)
        XCTAssertThrowsError(try store.update(record()))
        XCTAssertEqual(try Data(contentsOf: path), damaged)
    }

    func testUnavailableDestinationRetainsHistoryAndDoesNotPublish() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        try store.update(entry)
        try Data("blocking file".utf8).write(to: root.appendingPathComponent("Received"))
        XCTAssertThrowsError(try store.save(Data("one".utf8), index: 0, record: &entry))
        XCTAssertFalse(try XCTUnwrap(store.records.first).files[0].saved)
        XCTAssertFalse(try XCTUnwrap(store.records.first).receiptPending)
    }

    func testTraversalRejectedAndPortableNamesSanitized() throws {
        for name in ["../secret", "folder/file", "folder\\file", ".", ".."] {
            XCTAssertThrowsError(try GuestFiles.filename(name))
        }
        XCTAssertEqual(try GuestFiles.filename("safe:name.txt"), "safe_name.txt")
        XCTAssertLessThanOrEqual(try GuestFiles.filename(String(repeating: "é", count: 300) + ".txt").utf8.count, 200)
    }

    func testDeclaredLengthMismatchNeverPublishes() throws {
        let store = try GuestDownloadStore(root: directory())
        var entry = record()
        try store.update(entry)
        XCTAssertThrowsError(try store.save(Data("wrong length".utf8), index: 0, record: &entry))
        XCTAssertNil(store.records.first?.files[0].relativePath)
    }

    func testCancelledSaveRemovesTemporaryFileAndRetainsCheckpoint() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let job = Task { @MainActor in
            var entry = self.record()
            try store.update(entry)
            try store.save(Data("one".utf8), index: 0, record: &entry)
        }
        job.cancel()
        do { try await job.value; XCTFail("Expected cancellation") } catch is CancellationError {} catch { XCTFail("Unexpected error: \(error)") }
        let row = try XCTUnwrap(store.records.first)
        XCTAssertFalse(row.files[0].saved)
        XCTAssertNil(store.url(row.files[0]))
        let enumerator = try XCTUnwrap(FileManager.default.enumerator(at: root, includingPropertiesForKeys: nil))
        XCTAssertFalse(enumerator.allObjects.compactMap { $0 as? URL }.contains { $0.pathExtension == "pending" })
    }

    func testReceiptFailureKeepsSavedStateAndRetriesWithoutFileWorkAfterRemoval() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files = [entry.files[0]]
        try store.save(Data("one".utf8), index: 0, record: &entry)
        entry.complete = true; entry.receiptPending = true
        try store.update(entry)
        await store.flushReceipts { _, _ in throw AccountError.request }
        XCTAssertTrue(try XCTUnwrap(store.records.first).complete)
        XCTAssertTrue(try XCTUnwrap(store.records.first).receiptPending)
        try store.remove(entry)
        let reopened = GuestDownloadStore(root: root)
        var delivered = 0
        await reopened.flushReceipts { origin, id in
            XCTAssertEqual(origin, entry.origin)
            XCTAssertEqual(id, entry.transferID)
            delivered += 1
        }
        XCTAssertEqual(delivered, 1)
        await reopened.flushReceipts { _, _ in XCTFail("Receipt must not be repeated after persisted success") }
        XCTAssertEqual(try Data(contentsOf: XCTUnwrap(store.url(entry.files[0]))), Data("one".utf8))
    }

    func testEarlierGuestArrayDecodesWithoutTouchingAccountHistory() throws {
        let root = try directory()
        let entry = record()
        try JSONEncoder().encode([entry]).write(to: root.appendingPathComponent("guest-downloads.json"))
        let account = root.appendingPathComponent("transfer-history.json")
        let accountBytes = Data("existing account history".utf8)
        try accountBytes.write(to: account)
        let store = GuestDownloadStore(root: root)
        XCTAssertEqual(store.records, [entry])
        try store.update(entry)
        XCTAssertEqual(try Data(contentsOf: account), accountBytes)
    }

    func testAuthenticatedDecryptionRejectsWrongKeyAndTampering() throws {
        let key = try CryptoProvider.shared.generateKeyData()
        let nonce = try CryptoProvider.shared.generateNonceData()
        let plain = Data("received bytes".utf8)
        let encrypted = try CryptoProvider.shared.encrypt(key: key, nonce: nonce, plaintext: plain)
        let blob = nonce + encrypted
        XCTAssertEqual(try GuestFiles.decrypt(blob, key: key), plain)
        XCTAssertThrowsError(try GuestFiles.decrypt(blob, key: CryptoProvider.shared.generateKeyData()))
        var damaged = blob
        damaged[damaged.count - 1] ^= 1
        XCTAssertThrowsError(try GuestFiles.decrypt(damaged, key: key))
        XCTAssertThrowsError(try GuestFiles.decrypt(Data(repeating: 0, count: 27), key: key))
    }

    func testPartialTransferMissingPublishedFileRequiresConsentBeforeResume() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        try store.save(Data("one".utf8), index: 0, record: &entry)
        XCTAssertFalse(entry.complete)
        XCTAssertFalse(entry.files[1].saved)
        XCTAssertFalse(store.requiresRedownloadConsent(entry))
        let published = try XCTUnwrap(store.url(entry.files[0]))
        try FileManager.default.removeItem(at: published)
        XCTAssertTrue(store.requiresRedownloadConsent(entry))
        let model = GuestTransferModel(store: store)
        model.resume(entry)
        XCTAssertFalse(model.active, "No receive job may start before redownload consent")
        XCTAssertEqual(model.error, GuestError.redownloadConsent.localizedDescription)
        await Task.yield()
        XCTAssertFalse(model.active)
        XCTAssertEqual(store.records.first, entry)
        model.resume(entry, allowRedownload: true)
        XCTAssertTrue(model.active, "Explicit consent permits retrying missing output")
        model.cancel()
        await Task.yield()
    }

    func testUnpublishedPartialFilesDoNotRequireRedownloadConsent() throws {
        let store = try GuestDownloadStore(root: directory())
        var entry = record()
        entry.files[0].relativePath = "Received/unfinished.txt"
        entry.files[0].digest = GuestFiles.digest(Data("one".utf8))
        XCTAssertFalse(entry.files[0].saved)
        XCTAssertFalse(store.requiresRedownloadConsent(entry), "A failed publication intent is ordinary retry work")
    }
}

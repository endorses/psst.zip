import Foundation
import Shared
import XCTest

@testable import Psst

@MainActor
final class GuestDownloadTests: XCTestCase {
    private func directory() throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return root
    }

    private func record() -> GuestDownload {
        GuestDownload(
            id: GuestDownload.identity(origin: "https://one.example", transferID: "transfer"), origin: "https://one.example", transferID: "transfer",
            files: [
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
        let restored = try XCTUnwrap(reopened.find(record().id))
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
        var resumed = try XCTUnwrap(reopened.find(record().id))
        XCTAssertNotNil(reopened.url(resumed.files[0]))
        XCTAssertNil(reopened.url(resumed.files[1]))
        XCTAssertFalse(resumed.complete)
        XCTAssertFalse(resumed.receiptPending)
        try reopened.save(Data("two".utf8), index: 1, record: &resumed)
        XCTAssertNotEqual(resumed.files[0].relativePath, resumed.files[1].relativePath)
        XCTAssertEqual(try Data(contentsOf: XCTUnwrap(reopened.url(resumed.files[0]))), Data("one".utf8))
        XCTAssertEqual(try Data(contentsOf: XCTUnwrap(reopened.url(resumed.files[1]))), Data("two".utf8))
    }

    func testKillAfterPublicationReconcilesIntentBeforeReceipt() async throws {
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
        try await reopened.reconcile(entry.id)
        let saved = try XCTUnwrap(reopened.find(record().id))
        XCTAssertTrue(saved.files[0].saved)
        XCTAssertTrue(saved.complete)
        XCTAssertTrue(saved.receiptPending)
        XCTAssertNotNil(reopened.url(saved.files[0]))
    }

    func testIncompleteWriteIsRemovedAndNeverReceipted() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files[0].relativePath = "partial.txt"
        entry.files[0].digest = GuestFiles.digest(Data("one".utf8))
        try store.update(entry)
        let pending = root.appendingPathComponent("partial.txt.pending")
        try Data("o".utf8).write(to: pending)
        let reopened = GuestDownloadStore(root: root)
        try await reopened.reconcile(entry.id)
        XCTAssertFalse(FileManager.default.fileExists(atPath: pending.path))
        XCTAssertFalse(try XCTUnwrap(reopened.find(record().id)).receiptPending)
        XCTAssertNil(reopened.url(entry.files[0]))
    }

    func testRemovalKeepsSavedFilesAndReceiptJournal() throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        var entry = record()
        entry.files = [entry.files[0]]
        try store.save(Data("one".utf8), index: 0, record: &entry)
        entry.complete = true
        entry.receiptPending = true
        try store.update(entry)
        let saved = try XCTUnwrap(store.url(entry.files[0]))
        try store.remove(entry)
        XCTAssertTrue(try store.page().records.isEmpty)
        XCTAssertEqual(try Data(contentsOf: saved), Data("one".utf8))
        XCTAssertTrue(store.hasPendingReceipts)
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
        XCTAssertFalse(try XCTUnwrap(store.find(record().id)).files[0].saved)
        XCTAssertFalse(try XCTUnwrap(store.find(record().id)).receiptPending)
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
        XCTAssertNil(try store.find(record().id)?.files[0].relativePath)
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
        do {
            try await job.value
            XCTFail("Expected cancellation")
        } catch is CancellationError {} catch { XCTFail("Unexpected error: \(error)") }
        let row = try XCTUnwrap(store.find(record().id))
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
        entry.complete = true
        entry.receiptPending = true
        try store.update(entry)
        await store.flushReceipts { _, _ in throw AccountError.request }
        XCTAssertTrue(try XCTUnwrap(store.find(record().id)).complete)
        XCTAssertTrue(try XCTUnwrap(store.find(record().id)).receiptPending)
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
        XCTAssertEqual(try store.page().records, [entry])
        try store.update(entry)
        XCTAssertEqual(try Data(contentsOf: account), accountBytes)
    }

    func testLargeSnapshotMigrationPagesAndRetainsOriginalAndIndependentReceipt() async throws {
        let root = try directory()
        let rows = (0..<130).map { index in
            let transfer = "transfer-" + String(index)
            return GuestDownload(
                id: GuestDownload.identity(origin: "https://one.example", transferID: transfer), origin: "https://one.example", transferID: transfer,
                createdAt: Date(timeIntervalSince1970: Double(index)))
        }
        struct LegacyReceipt: Encodable {
            let origin: String
            let transferID: String
        }
        struct LegacySnapshot: Encodable {
            let records: [GuestDownload]
            let receipts: [LegacyReceipt]
        }
        let original = try JSONEncoder().encode(LegacySnapshot(records: rows, receipts: [LegacyReceipt(origin: "https://one.example", transferID: "removed")]))
        let file = root.appendingPathComponent("guest-downloads.json")
        try original.write(to: file)
        let interrupted = GuestDownloadStore(root: root)
        XCTAssertFalse(interrupted.isReady)
        XCTAssertThrowsError(try interrupted.update(record()))
        let restored = GuestDownloadStore(root: root)
        await restored.finishMigration()
        XCTAssertTrue(restored.isReady)
        var cursor: HistoryRecordDatabase.Cursor?
        var ids: [String] = []
        repeat {
            let page = try restored.page(after: cursor)
            XCTAssertLessThanOrEqual(page.records.count, 50)
            ids += page.records.map(\.id)
            cursor = page.next
        } while cursor != nil
        XCTAssertEqual(ids.count, 130)
        XCTAssertEqual(Set(ids), Set(rows.map(\.id)))
        XCTAssertEqual(try restored.find(rows[0].id), rows[0])
        XCTAssertEqual(try Data(contentsOf: file), original)
        var receipts = 0
        await restored.flushReceipts { _, transfer in
            XCTAssertEqual(transfer, "removed")
            receipts += 1
        }
        XCTAssertEqual(receipts, 1)
        try restored.remove(rows[0])
        let reopened = GuestDownloadStore(root: root)
        XCTAssertTrue(reopened.isReady)
        XCTAssertNil(try reopened.find(rows[0].id))
        XCTAssertFalse(reopened.hasPendingReceipts)
        XCTAssertEqual(try Data(contentsOf: file), original)
    }

    func testReceiptRetriesAreBoundedFairAndSuccessSurvivesStaleCheckpoint() async throws {
        let store = try GuestDownloadStore(root: directory())
        var entries: [GuestDownload] = []
        for index in 0..<9 {
            let transfer = "queued-" + String(index)
            var entry = GuestDownload(id: GuestDownload.identity(origin: "https://one.example", transferID: transfer), origin: "https://one.example", transferID: transfer)
            entry.receiptPending = true
            try store.update(entry)
            entries.append(entry)
        }
        var attempted: [String] = []
        await store.flushReceipts { _, transfer in
            attempted.append(transfer)
            throw AccountError.request
        }
        XCTAssertEqual(attempted.count, 4)
        let first = Set(attempted)
        attempted = []
        await store.flushReceipts { _, transfer in
            attempted.append(transfer)
            throw AccountError.request
        }
        XCTAssertEqual(attempted.count, 4)
        XCTAssertTrue(first.isDisjoint(with: attempted))
        var delivered: String?
        await store.flushReceipts { _, transfer in delivered = transfer }
        let stale = try XCTUnwrap(entries.first { $0.transferID == delivered })
        try store.update(stale)
        XCTAssertTrue(try XCTUnwrap(store.find(stale.id)).receiptDelivered)
        XCTAssertFalse(try XCTUnwrap(store.find(stale.id)).receiptPending)
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
        XCTAssertEqual(try store.find(record().id), entry)
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

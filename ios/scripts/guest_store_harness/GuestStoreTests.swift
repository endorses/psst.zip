import Foundation
import XCTest

@testable import GuestStoreHarness

@MainActor
final class GuestStoreTests: XCTestCase {
    private func directory() throws -> URL {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock {
            StreamedFiles.setHook(nil)
            try? FileManager.default.removeItem(at: root)
        }
        return root
    }
    private func entry(_ transfer: String, origin: String = "https://guest.example") -> GuestDownload {
        GuestDownload(id: GuestDownload.identity(origin: origin, transferID: transfer), origin: origin, transferID: transfer)
    }
    private func database(_ root: URL) throws -> HistoryRecordDatabase {
        try HistoryRecordDatabase(url: root.appendingPathComponent("guest-history.store/records.sqlite3"))
    }
    private func intent(_ root: URL, _ store: GuestDownloadStore, name: String = "transfer") throws -> GuestDownload {
        var record = entry(name)
        let bytes = Data("abc".utf8)
        record.files = [GuestFile(id: "file", name: "file.txt", size: 3, mime: "text/plain", relativePath: name + ".txt", digest: GuestFiles.digest(bytes))]
        try bytes.write(to: root.appendingPathComponent(name + ".txt"))
        try store.update(record)
        return record
    }

    func testSnapshotMigrationResumesThenPagesOnlyGuestRows() async throws {
        struct Receipt: Codable {
            let origin: String
            let transferID: String
        }
        struct Snapshot: Codable {
            let records: [GuestDownload]
            let receipts: [Receipt]
        }
        let root = try directory()
        var records = (0..<131).map { entry("transfer-\($0)") }
        for index in records.indices { records[index].createdAt = Date(timeIntervalSince1970: Double(index)) }
        let snapshot = Snapshot(records: records, receipts: [Receipt(origin: records[0].origin, transferID: records[0].transferID)])
        let source = root.appendingPathComponent("guest-downloads.json")
        let bytes = try JSONEncoder().encode(snapshot)
        try bytes.write(to: source)
        let first = GuestDownloadStore(root: root)
        XCTAssertFalse(first.isReady)
        XCTAssertEqual(first.importedRecords, 25)
        XCTAssertThrowsError(try first.update(entry("early")))
        let resumed = GuestDownloadStore(root: root)
        await resumed.finishMigration()
        XCTAssertTrue(resumed.isReady)
        XCTAssertEqual(resumed.importedRecords, 132)
        let db = try database(root)
        try db.write(.init(id: "foreign", scope: "another-device", kind: "guest", created: 999, body: Data("bad".utf8)))
        var cursor: HistoryRecordDatabase.Cursor?
        var ids: [String] = []
        repeat {
            let page = try resumed.page(after: cursor)
            XCTAssertLessThanOrEqual(page.records.count, 50)
            ids += page.records.map(\.id)
            cursor = page.next
        } while cursor != nil
        XCTAssertEqual(ids, records.reversed().map(\.id))
        XCTAssertTrue(resumed.hasPendingReceipts)
        XCTAssertEqual(try Data(contentsOf: source), bytes)
        try FileManager.default.removeItem(at: source)
        XCTAssertTrue(GuestDownloadStore(root: root).isReady)
    }

    func testRootArrayMigrationDerivesReceiptThatSurvivesRemoval() async throws {
        let root = try directory()
        var record = entry("legacy")
        record.receiptPending = true
        try JSONEncoder().encode([record]).write(to: root.appendingPathComponent("guest-downloads.json"))
        let store = GuestDownloadStore(root: root)
        XCTAssertTrue(store.isReady)
        XCTAssertTrue(store.hasPendingReceipts)
        try store.remove(record)
        XCTAssertNil(try store.find(record.id))
        XCTAssertTrue(store.hasPendingReceipts)
        var delivered: [String] = []
        await store.flushReceipts { _, id in delivered.append(id) }
        XCTAssertEqual(delivered, [record.transferID])
        XCTAssertFalse(store.hasPendingReceipts)
        XCTAssertNil(try store.find(record.id))
    }

    func testLocalNavigationPreservesPageOnFailureAndReachesEverySavedTransfer() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        for index in 0..<125 {
            var record = entry("paged-\(index)")
            record.createdAt = Date(timeIntervalSince1970: Double(index))
            try store.update(record)
        }
        let model = GuestHistoryPageViewModel()
        model.refresh(store: store)
        let first = model.records
        XCTAssertEqual(first.count, 50)
        model.forward(store: store)
        let second = model.records
        XCTAssertEqual(model.number, 2)
        let brokenRoot = try directory()
        try Data("damaged legacy history".utf8).write(to: brokenRoot.appendingPathComponent("guest-downloads.json"))
        model.forward(store: GuestDownloadStore(root: brokenRoot))
        XCTAssertEqual(model.number, 2)
        XCTAssertEqual(model.records, second)
        XCTAssertNotNil(model.error)
        model.forward(store: store)
        XCTAssertEqual(model.number, 3)
        XCTAssertEqual(model.records.count, 25)
        XCTAssertEqual(Set((first + second + model.records).map(\.id)).count, 125)
        XCTAssertNil(model.next)
        model.backward(store: store)
        XCTAssertEqual(model.records, second)
        model.first(store: store)
        XCTAssertEqual(model.records, first)
        XCTAssertEqual(model.number, 1)
    }

    func testReceiptFailuresRotatePastOldestBatch() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        for index in 0..<9 {
            var record = entry("receipt-\(index)")
            record.receiptPending = true
            try store.update(record)
        }
        var attempted = Set<String>()
        for _ in 0..<3 {
            await store.flushReceipts { _, id in
                attempted.insert(id)
                throw AccountError.storage
            }
        }
        XCTAssertEqual(attempted.count, 9)
        XCTAssertTrue(store.hasPendingReceipts)
        for _ in 0..<3 { await store.flushReceipts { _, _ in } }
        XCTAssertFalse(store.hasPendingReceipts)
    }

    func testPublishedIntentReconcilesToReceiptAndRetainsOutputAfterRemoval() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let record = try intent(root, store)
        await store.reconcilePending()
        let saved = try XCTUnwrap(store.find(record.id))
        XCTAssertTrue(saved.files[0].saved)
        XCTAssertTrue(saved.complete)
        XCTAssertTrue(saved.receiptPending)
        XCTAssertNotNil(store.url(saved.files[0]))
        try store.remove(saved)
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appendingPathComponent("transfer.txt").path))
        XCTAssertTrue(store.hasPendingReceipts)
    }

    func testReconcileCannotRestoreDeletionWhileDigestIsSuspended() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let record = try intent(root, store)
        let started = expectation(description: "digest started")
        let gate = DispatchSemaphore(value: 0)
        StreamedFiles.setHook {
            started.fulfill()
            _ = gate.wait(timeout: .now() + 5)
        }
        let task = Task { try await store.reconcile(record.id) }
        await fulfillment(of: [started], timeout: 3)
        try store.remove(record)
        gate.signal()
        try await task.value
        XCTAssertNil(try store.find(record.id))
        XCTAssertFalse(store.hasPendingReceipts)
    }

    func testReconcileDoesNotOverwriteNewerPublicationCheckpoint() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let record = try intent(root, store)
        let started = expectation(description: "digest started")
        let gate = DispatchSemaphore(value: 0)
        StreamedFiles.setHook {
            started.fulfill()
            _ = gate.wait(timeout: .now() + 5)
        }
        let task = Task { try await store.reconcile(record.id) }
        await fulfillment(of: [started], timeout: 3)
        var newer = record
        newer.files[0].relativePath = "newer.txt"
        newer.files[0].digest = GuestFiles.digest(Data("new".utf8))
        try store.update(newer)
        gate.signal()
        try await task.value
        let current = try XCTUnwrap(store.find(record.id))
        XCTAssertEqual(current.files[0].relativePath, "newer.txt")
        XCTAssertFalse(current.files[0].saved)
        XCTAssertFalse(current.complete)
        XCTAssertFalse(store.hasPendingReceipts)
    }

    func testCorruptReceiptRemainsVisibleWithoutStarvingHealthyJobs() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let db = try database(root)
        try db.write(.init(id: "receipt|corrupt", scope: "guest-device", kind: "receipt", created: 0, body: Data("invalid".utf8)))
        for index in 0..<7 {
            var record = entry("healthy-\(index)")
            record.receiptPending = true
            try store.update(record)
        }
        var delivered = Set<String>()
        for _ in 0..<3 { await store.flushReceipts { _, id in delivered.insert(id) } }
        XCTAssertEqual(delivered.count, 7)
        XCTAssertTrue(store.hasPendingReceipts)
        XCTAssertNotNil(try db.read("receipt|corrupt"))
        XCTAssertNotNil(store.error)
    }

    func testTransientDigestReadFailureRetainsReconciliationForRetry() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let record = try intent(root, store)
        StreamedFiles.setHook { throw AccountError.storage }
        await store.reconcilePending()
        XCTAssertFalse(try XCTUnwrap(store.find(record.id)).files[0].saved)
        XCTAssertTrue(try database(root).hasAny(scopes: ["guest-device"], kinds: ["reconcile"]))
        XCTAssertNotNil(store.error)
        StreamedFiles.setHook(nil)
        await store.reconcilePending()
        XCTAssertTrue(try XCTUnwrap(store.find(record.id)).files[0].saved)
        XCTAssertFalse(try database(root).hasAny(scopes: ["guest-device"], kinds: ["reconcile"]))
    }

    func testStaleMetadataDoesNotRequeueDeliveredReceiptOrUndoSavedCheckpoint() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let stale = try intent(root, store)
        await store.reconcilePending()
        await store.flushReceipts { _, _ in }
        try store.update(stale)
        let current = try XCTUnwrap(store.find(stale.id))
        XCTAssertTrue(current.files[0].saved)
        XCTAssertTrue(current.receiptDelivered)
        XCTAssertFalse(current.receiptPending)
        XCTAssertFalse(store.hasPendingReceipts)
    }

    func testUnavailableKeychainReadDoesNotReplaceStoredKeyOrMetadata() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let old = try store.prepare(origin: "https://key.example", transferID: "key", key: Data("old".utf8))
        SecretStore.readFails = true
        defer { SecretStore.readFails = false }
        XCTAssertThrowsError(try store.prepare(origin: old.origin, transferID: old.transferID, key: Data("new".utf8)))
        XCTAssertEqual(SecretStore.read(old.keyReference), Data("old".utf8))
        XCTAssertEqual(try store.find(old.id), old)
    }

    func testCancellationDuringReconcileKeepsPendingIntent() async throws {
        let root = try directory()
        let store = GuestDownloadStore(root: root)
        let record = try intent(root, store)
        let started = expectation(description: "digest started")
        let gate = DispatchSemaphore(value: 0)
        StreamedFiles.setHook {
            started.fulfill()
            _ = gate.wait(timeout: .now() + 5)
        }
        let task = Task { try await store.reconcile(record.id) }
        await fulfillment(of: [started], timeout: 3)
        task.cancel()
        gate.signal()
        do {
            try await task.value
            XCTFail("expected cancellation")
        } catch is CancellationError {}
        XCTAssertFalse(try XCTUnwrap(store.find(record.id)).files[0].saved)
        XCTAssertTrue(try database(root).hasAny(scopes: ["guest-device"], kinds: ["reconcile"]))
    }

    func testMissingIncompleteMigrationSourceStaysBlocked() async throws {
        let root = try directory()
        let source = root.appendingPathComponent("guest-downloads.json")
        try JSONEncoder().encode((0..<30).map { entry("item-\($0)") }).write(to: source)
        let store = GuestDownloadStore(root: root)
        XCTAssertFalse(store.isReady)
        try FileManager.default.removeItem(at: source)
        await store.finishMigration()
        XCTAssertFalse(store.isReady)
        XCTAssertNotNil(store.error)
        XCTAssertThrowsError(try store.page())
    }
}

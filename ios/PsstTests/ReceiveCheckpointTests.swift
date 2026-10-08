import Foundation
import SQLite3
import XCTest

@testable import Psst

@MainActor final class ReceiveCheckpointTests: XCTestCase {
    private func setup() throws -> (URL, UserDefaults) {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let suite = "receive-checkpoints-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        addTeardownBlock {
            defaults.removePersistentDomain(forName: suite)
            try? FileManager.default.removeItem(at: directory)
        }
        return (directory.appendingPathComponent("history.json"), defaults)
    }
    private func parent(_ id: String = "inbox", owner: String = "alice", origin: String = "https://one.example")
        -> TransferRecord
    {
        TransferRecord(
            id: id, direction: .received, state: .complete, createdAt: Date(), fileCount: 0, totalSize: 0,
            shareURL: nil, serverURL: origin, ownerID: owner, isSlot: true)
    }
    private func database(_ source: URL) throws -> HistoryRecordDatabase {
        try HistoryRecordDatabase(
            url: source.deletingLastPathComponent().appendingPathComponent(
                source.lastPathComponent + ".store/records.sqlite3"))
    }
    private func encode(_ record: TransferRecord) throws -> Data {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return try encoder.encode(record)
    }

    func testThousandChildrenKeepParentBoundedAndMetadataRefreshPreservesPaths() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertTrue(store.isReady)
        let original = parent()
        try store.add(original)
        let initialBytes = try XCTUnwrap(database(file).read(original.localID)).body.count
        for index in 0..<1000 {
            _ = try store.saveReceivedFile(
                parent: original, transferID: "child-\(index)", blobID: "file", path: "Received/\(index)", size: 2,
                title: "file")
            try store.completeReceivedTransfer(
                parent: original, transferID: "child-\(index)", blobIDs: ["file"], fileExists: { _, _ in true })
        }
        try store.update(original)
        let current = try XCTUnwrap(store.record(original.localID))
        XCTAssertEqual(current.totalSize, 2000)
        XCTAssertNil(current.savedFiles)
        XCTAssertNil(current.savedTransfers)
        XCTAssertLessThan(try XCTUnwrap(database(file).read(original.localID)).body.count, initialBytes + 100)
        let visible = try store.receiveCheckpoints(
            parent: current, transferIDs: (900..<950).map { "child-\($0)" }, fileExists: { _, _ in true })
        XCTAssertEqual(visible.count, 50)
        XCTAssertTrue(visible.values.allSatisfy { $0.isSaved(fileCount: 1) })
        XCTAssertThrowsError(
            try store.receiveCheckpoints(
                parent: current, transferIDs: (0..<51).map { "child-\($0)" }, fileExists: { _, _ in true }))
    }

    func testPerChildCapUnsafePathAndIncompleteAcknowledgementFailAtomically() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        let original = parent()
        try store.add(original)
        for index in 0..<100 {
            try store.saveReceivedFile(
                parent: original, transferID: "child", blobID: "file-\(index)", path: "Received/\(index)", size: 1,
                title: "file")
        }
        XCTAssertThrowsError(
            try store.saveReceivedFile(
                parent: original, transferID: "child", blobID: "extra", path: "Received/extra", size: 1, title: "file"))
        for path in ["../escape", "/absolute", "a/../escape", "a//b", "a/./b"] {
            XCTAssertThrowsError(
                try store.saveReceivedFile(
                    parent: original, transferID: "other", blobID: "file", path: path, size: 1, title: "file"))
        }
        XCTAssertThrowsError(
            try store.completeReceivedTransfer(
                parent: original, transferID: "child", blobIDs: ["missing"], fileExists: { _, _ in true }))
        XCTAssertThrowsError(
            try store.completeReceivedTransfer(
                parent: original, transferID: "child", blobIDs: ["file-0", "file-0"], fileExists: { _, _ in true }))
        XCTAssertEqual(try store.record(original.localID)?.totalSize, 100)
        XCTAssertFalse(
            try XCTUnwrap(
                store.receiveCheckpoints(parent: original, transferIDs: ["child"], fileExists: { _, _ in true })[
                    "child"]
            ).complete)
    }

    func testTwoConnectionsMergeSavesWithoutDoubleCountingAndKeepAccountOriginIsolation() async throws {
        let (file, defaults) = try setup()
        let first = TransferHistoryStore(defaults: defaults, fileURL: file)
        let second = TransferHistoryStore(defaults: defaults, fileURL: file)
        let original = parent()
        let otherAccount = parent(owner: "bob")
        let otherOrigin = parent(origin: "https://two.example")
        for record in [original, otherAccount, otherOrigin] { try first.add(record) }
        try first.saveReceivedFile(
            parent: original, transferID: "child", blobID: "first", path: "Received/first", size: 10, title: "first")
        try second.saveReceivedFile(
            parent: original, transferID: "child", blobID: "second", path: "Received/second", size: 20, title: "second")
        try first.saveReceivedFile(
            parent: original, transferID: "child", blobID: "first", path: "Received/again", size: 10, title: "first")
        try second.update(original)
        XCTAssertEqual(try first.record(original.localID)?.totalSize, 30)
        for record in [otherAccount, otherOrigin] {
            XCTAssertTrue(
                try XCTUnwrap(
                    first.receiveCheckpoints(parent: record, transferIDs: ["child"], fileExists: { _, _ in true })[
                        "child"]
                ).paths.isEmpty)
        }
        let paths = try XCTUnwrap(
            first.receiveCheckpoints(parent: original, transferIDs: ["child"], fileExists: { _, _ in true })["child"]
        ).paths
        XCTAssertEqual(paths, ["first": "Received/again", "second": "Received/second"])
    }

    func testLegacyMapsResumeIntoIndexedRowsAndPreserveImmutableSource() async throws {
        let (file, defaults) = try setup()
        var old = parent()
        old.savedFiles = Dictionary(uniqueKeysWithValues: (0..<130).map { ("child-\($0)/file", "Received/\($0)") })
        old.savedTransfers = (0..<130).map { "child-\($0)" }
        old.totalSize = 999
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let original = try encoder.encode([old])
        try original.write(to: file)
        let first = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertFalse(first.isReady)
        XCTAssertThrowsError(try first.record(old.localID))
        let resumed = TransferHistoryStore(defaults: defaults, fileURL: file)
        await resumed.finishMigration()
        XCTAssertTrue(resumed.isReady, resumed.migrationError ?? "")
        XCTAssertEqual(try resumed.record(old.localID)?.totalSize, 999)
        XCTAssertNil(try resumed.record(old.localID)?.savedFiles)
        for base in stride(from: 0, to: 130, by: 50) {
            let ids = (base..<min(base + 50, 130)).map { "child-\($0)" }
            let restored = try resumed.receiveCheckpoints(parent: old, transferIDs: ids, fileExists: { _, _ in true })
            XCTAssertTrue(restored.values.allSatisfy { $0.isSaved(fileCount: 1) })
        }
        XCTAssertEqual(try Data(contentsOf: file), original)
        let sources = try database(file).page(scopes: ["receive-checkpoint-migration"], kinds: ["source"], limit: 50)
        XCTAssertFalse(sources.records.isEmpty)
        XCTAssertTrue(sources.records.contains { String(decoding: $0.body, as: UTF8.self).contains("savedFiles") })
        let checkpoint = try XCTUnwrap(
            resumed.receiveCheckpoints(parent: old, transferIDs: ["child-129"], fileExists: { _, _ in false })[
                "child-129"])
        XCTAssertTrue(checkpoint.needsFile(blobID: "file"))
    }

    func testAlreadyIndexedLegacyParentWithoutJSONIsDiscoveredAndNormalized() async throws {
        let (file, defaults) = try setup()
        var old = parent()
        old.savedFiles = ["child/file": "Received/file"]
        old.savedTransfers = ["child"]
        let db = try database(file)
        try db.write(
            .init(id: old.localID, scope: "https://one.example|alice", kind: "slot", created: 1, body: encode(old)))
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        await store.finishMigration()
        XCTAssertTrue(store.isReady, store.migrationError ?? "")
        XCTAssertTrue(
            try XCTUnwrap(
                store.receiveCheckpoints(parent: old, transferIDs: ["child"], fileExists: { _, _ in true })["child"]
            ).isSaved(fileCount: 1))
        XCTAssertNil(try store.record(old.localID)?.savedFiles)
    }

    func testMalformedLegacyPathRemainsPreservedAndUnavailable() async throws {
        let (file, defaults) = try setup()
        var old = parent()
        old.savedFiles = ["child/file": "../outside"]
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let original = try encoder.encode([old])
        try original.write(to: file)
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        await store.finishMigration()
        XCTAssertFalse(store.isReady)
        XCTAssertNotNil(store.migrationError)
        XCTAssertThrowsError(
            try store.receiveCheckpoints(parent: old, transferIDs: ["child"], fileExists: { _, _ in true }))
        XCTAssertEqual(try Data(contentsOf: file), original)
    }

    func testKnownSavedLengthDetectsTruncationWhileLegacyUnknownLengthRemainsExplicit() async throws {
        let checkpoint = ReceiveCheckpoint(
            slotID: "slot", transferID: "child", paths: ["known": "known", "legacy": "legacy"], sizes: ["known": 10],
            complete: true,
            fileExists: { _, expected in expected == nil || expected == 3 })
        XCTAssertTrue(checkpoint.needsFile(blobID: "known"))
        XCTAssertFalse(checkpoint.needsFile(blobID: "legacy"))
        XCTAssertFalse(checkpoint.isSaved(fileCount: 2))
    }

    func testStaleParentMapsCannotReappearAfterNormalization() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        var old = parent()
        try store.add(old)
        old.savedFiles = ["child/file": "Received/stale"]
        old.savedTransfers = ["child"]
        XCTAssertThrowsError(try store.update(old))
        XCTAssertNil(try store.record(old.localID)?.savedFiles)
        XCTAssertNil(try store.record(old.localID)?.savedTransfers)
        XCTAssertTrue(
            try XCTUnwrap(
                store.receiveCheckpoints(parent: old, transferIDs: ["child"], fileExists: { _, _ in true })["child"]
            ).paths.isEmpty)
    }

    func testDeviceHistoryPagesUseTheNormalizedStore() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        for index in 0..<125 { try store.add(parent("slot-\(index)")) }
        let session = DeviceSession(
            serverURL: "https://one.example", userID: "alice", username: "alice", token: "token", sessionID: "session",
            expiresAt: "2099-01-01T00:00:00Z")
        let model = DeviceHistoryPageViewModel()
        model.refresh(history: store, session: session, filter: .receive)
        XCTAssertEqual(model.records.count, 50)
        model.forward(history: store, session: session)
        XCTAssertEqual(model.records.count, 50)
        model.forward(history: store, session: session)
        XCTAssertEqual(model.records.count, 25)
        XCTAssertNil(model.next)
        model.backward(history: store, session: session)
        XCTAssertEqual(model.records.count, 50)
        XCTAssertTrue(model.records.allSatisfy { $0.savedFiles == nil && $0.savedTransfers == nil })
    }

    func testLegacyNormalizationKeepsNewerCheckpointsAndDeletedChildTombstones() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        var old = parent()
        try store.add(old)
        for child in ["newer", "deleted"] {
            try store.saveReceivedFile(
                parent: old, transferID: child, blobID: "file", path: "Received/" + child, size: 1, title: "file")
        }
        let db = try database(file)
        var cursor: String?
        var remove: [String] = []
        repeat {
            let page = try db.migrationPage(afterID: cursor)
            for entry in page.entries where entry.kind.hasPrefix("receive-") {
                let body = try XCTUnwrap(db.read(entry.id)).body
                if let value = try JSONSerialization.jsonObject(with: body) as? [String: Any],
                    value["transferID"] as? String == "deleted"
                {
                    remove.append(entry.id)
                }
            }
            cursor = page.nextID
        } while cursor != nil
        XCTAssertEqual(remove.count, 2)
        for id in remove { try db.remove(id) }
        old.totalSize = 2
        old.savedFiles = ["newer/file": "Received/old", "deleted/file": "Received/deleted"]
        old.savedTransfers = ["newer", "deleted"]
        let body = try encode(old)
        try db.transaction { db in
            let original = HistoryRecordDatabase.Record(
                id: old.localID, scope: "https://one.example|alice", kind: "slot", created: 1, body: body)
            let stripped = try ReceiveCheckpointStorage.stage(db, record: old, original: original)
            try db.write(
                .init(id: old.localID, scope: original.scope, kind: "slot", created: 1, body: encode(stripped)))
        }
        let resumed = TransferHistoryStore(defaults: defaults, fileURL: file)
        await resumed.finishMigration()
        XCTAssertTrue(resumed.isReady, resumed.migrationError ?? "")
        let values = try resumed.receiveCheckpoints(
            parent: old, transferIDs: ["newer", "deleted"], fileExists: { _, _ in true })
        XCTAssertEqual(values["newer"]?.paths["file"], "Received/newer")
        XCTAssertFalse(values["newer"]?.complete ?? true)
        XCTAssertTrue(values["deleted"]?.paths.isEmpty == true)
        XCTAssertFalse(values["deleted"]?.complete ?? true)
    }

    func testMissingParentCannotBeResurrectedByCheckpointWrite() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        let old = parent()
        try store.add(old)
        try store.saveReceivedFile(
            parent: old, transferID: "child", blobID: "file", path: "Received/file", size: 1, title: "file")
        try store.remove(old)
        XCTAssertThrowsError(
            try store.saveReceivedFile(
                parent: old, transferID: "child", blobID: "late", path: "Received/late", size: 1, title: "late"))
        XCTAssertNil(try store.record(old.localID))
    }
}

extension ReceiveCheckpointTests {
    func testOversizedInboxStreamsBeforeLateIdentityAndResumesWithinObject() async throws {
        let (file, defaults) = try setup()
        var old = parent("large-inbox")
        old.totalSize = 987_654_321
        defer { SecretStore.remove(old.vaultID) }
        let scalar = try encode(old)
        let tail = scalar.dropFirst()
        let output = try FileHandle(
            forWritingTo: {
                _ = FileManager.default.createFile(atPath: file.path, contents: nil)
                return file
            }())
        try output.write(contentsOf: Data("[{\"savedFiles\":{".utf8))
        // Exceed the 16 MiB record cap with fewer rows to stage and promote.
        // Paths remain below the production 4096-character path limit.
        let suffix = String(repeating: "x", count: 4000)
        for index in 0..<4200 {
            if index > 0 { try output.write(contentsOf: Data([44])) }
            try output.write(
                contentsOf: Data("\"child-\(index)/file\":\"Received/\(index)-\(suffix)\"".utf8))
        }
        try output.write(contentsOf: Data("},\"savedTransfers\":[\"child-4199\"],".utf8))
        try output.write(contentsOf: tail)
        try output.write(contentsOf: Data([93]))
        try output.close()
        let bytes = try FileManager.default.attributesOfItem(atPath: file.path)[.size] as! NSNumber
        XCTAssertGreaterThan(bytes.intValue, HistoryRecordDatabase.maximumRecordBytes)
        try old.saveSecrets(
            link: "private-link", deletionToken: "delete",
            receivePrivateKey: Data(repeating: 7, count: 32))
        let first = TransferHistoryStore(defaults: defaults, fileURL: file)
        XCTAssertFalse(first.isReady)
        let db = try database(file)
        XCTAssertEqual(try db.migrationProgress(key: "account-history-v2")?.processed, 0)
        XCTAssertNil(try db.read(old.localID))
        let staged = try db.page(
            scopes: ["receive-stage|account-history-v2|1"], kinds: ["stage-entry"], limit: 50)
        XCTAssertEqual(staged.records.count, 32)
        let resumed = TransferHistoryStore(defaults: defaults, fileURL: file)
        await resumed.finishMigration()
        XCTAssertTrue(resumed.isReady, resumed.migrationError ?? "")
        let current = try XCTUnwrap(resumed.record(old.localID))
        XCTAssertEqual(current.totalSize, old.totalSize)
        XCTAssertEqual(current.vaultID, "resource|https://one.example|alice|large-inbox")
        XCTAssertEqual(current.capabilities?.receivePrivateKey, Data(repeating: 7, count: 32))
        XCTAssertNil(current.savedFiles)
        let value = try XCTUnwrap(
            resumed.receiveCheckpoints(
                parent: current, transferIDs: ["child-4199"], fileExists: { _, _ in true })[
                    "child-4199"])
        XCTAssertEqual(value.paths["file"], "Received/4199-" + suffix)
        XCTAssertTrue(value.complete)
        XCTAssertEqual(
            (try FileManager.default.attributesOfItem(atPath: file.path)[.size] as! NSNumber).intValue,
            bytes.intValue)
        // Installed SQLite histories can already contain an oversized parent.
        // Fixture insertion bypasses only the normal 16 MiB writer cap.
        let (sqliteSource, sqliteDefaults) = try setup()
        let legacyDB = try database(sqliteSource)
        let sqlitePath = sqliteSource.deletingLastPathComponent().appendingPathComponent(
            sqliteSource.lastPathComponent + ".store/records.sqlite3"
        ).path
        var handle: OpaquePointer?
        XCTAssertEqual(sqlite3_open(sqlitePath, &handle), SQLITE_OK)
        defer { sqlite3_close(handle) }
        var statement: OpaquePointer?
        XCTAssertEqual(
            sqlite3_prepare_v2(
                handle,
                "INSERT INTO records(id,scope,kind,sort_created,body_bytes,body) VALUES(?,'https://one.example|alice','slot',-1,?,?)",
                -1, &statement, nil),
            SQLITE_OK)
        let original = try Data(contentsOf: file).dropFirst().dropLast()
        let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)
        XCTAssertEqual(sqlite3_bind_text(statement, 1, old.localID, -1, transient), SQLITE_OK)
        XCTAssertEqual(sqlite3_bind_int64(statement, 2, Int64(original.count)), SQLITE_OK)
        original.withUnsafeBytes {
            XCTAssertEqual(
                sqlite3_bind_blob(statement, 3, $0.baseAddress, Int32(original.count), transient), SQLITE_OK
            )
        }
        XCTAssertEqual(sqlite3_step(statement), SQLITE_DONE)
        sqlite3_finalize(statement)
        XCTAssertThrowsError(try legacyDB.read(old.localID))
        let sqliteStore = TransferHistoryStore(defaults: sqliteDefaults, fileURL: sqliteSource)
        await sqliteStore.finishMigration()
        XCTAssertTrue(sqliteStore.isReady, sqliteStore.migrationError ?? "")
        XCTAssertEqual(try sqliteStore.record(old.localID)?.totalSize, old.totalSize)
        let restored = try XCTUnwrap(
            sqliteStore.receiveCheckpoints(
                parent: old, transferIDs: ["child-4199"], fileExists: { _, _ in true })[
                    "child-4199"])
        XCTAssertEqual(restored.paths, value.paths)
        XCTAssertTrue(restored.complete)
    }

    func testStreamingStateRowsRollbackAndChangedSourceCannotResume() async throws {
        let (file, _) = try setup()
        var old = parent()
        old.savedFiles = Dictionary(uniqueKeysWithValues: (0..<70).map { ("child-\($0)/file", "Received/\($0)") })
        let source = Data([91]) + (try encode(old)) + Data([93])
        try source.write(to: file)
        let db = try database(file)
        enum Injected: Error { case fail }
        XCTAssertThrowsError(
            try db.transaction { db in
                _ = try db.migrateReceiveJSONBatch(
                    source: file, key: "atomic", decode: { _ in throw Injected.fail },
                    validateEntry: ReceiveCheckpointStorage.validateEntry, promote: { _, _ in })
                throw Injected.fail
            })
        XCTAssertNil(try db.migrationProgress(key: "atomic"))
        XCTAssertTrue(
            try db.page(scopes: ["receive-stage|atomic|1"], kinds: ["stage-entry"], limit: 50).records.isEmpty)
        _ = try db.migrateReceiveJSONBatch(
            source: file, key: "atomic", decode: { _ in throw Injected.fail },
            validateEntry: ReceiveCheckpointStorage.validateEntry, promote: { _, _ in })
        XCTAssertEqual(
            try db.page(scopes: ["receive-stage|atomic|1"], kinds: ["stage-entry"], limit: 50).records.count, 32)
        try (source + Data([32])).write(to: file)
        XCTAssertThrowsError(
            try db.migrateReceiveJSONBatch(
                source: file, key: "atomic", decode: { _ in throw Injected.fail },
                validateEntry: ReceiveCheckpointStorage.validateEntry, promote: { _, _ in }))
        XCTAssertEqual(try db.migrationProgress(key: "atomic")?.processed, 0)
    }

    func testStreamingMalformedAndOversizedIndividualValuesGiveSpecificFeedback() async throws {
        for fragment in [
            "\"child/file\":\"../bad\"", "\"child/file\":\"" + String(repeating: "x", count: 66000) + "\"",
            "\"child/file\":\"ok\",",
        ] {
            let (file, defaults) = try setup()
            let source = Data(("[{\"savedFiles\":{" + fragment + "}}]").utf8)
            try source.write(to: file)
            let store = TransferHistoryStore(defaults: defaults, fileURL: file)
            await store.finishMigration()
            XCTAssertFalse(store.isReady)
            XCTAssertTrue(store.migrationError?.contains("recovery") == true)
            XCTAssertEqual(try Data(contentsOf: file), source)
        }
    }

    func testCompletionArrayBeyondRecordCapUsesBoundedTokenReadsAndState() async throws {
        let (file, _) = try setup()
        let entry = String(repeating: "a", count: 128)
        _ = FileManager.default.createFile(atPath: file.path, contents: nil)
        let handle = try FileHandle(forWritingTo: file)
        try handle.write(contentsOf: Data("[{\"savedTransfers\":[".utf8))
        for index in 0..<130000 {
            try handle.write(contentsOf: Data(((index == 0 ? "" : ",") + "\"" + entry + "\"").utf8))
        }
        try handle.write(contentsOf: Data("],\"id\":\"after-array\"}]".utf8))
        try handle.close()
        let source = try HistoryJSONStream(url: file, maximumObjectBytes: HistoryRecordDatabase.maximumRecordBytes)
        defer { source.close() }
        var state = ReceiveHistoryStream.State()
        var count = 0
        var largestRead = 0
        repeat {
            let parser = ReceiveHistoryStream(
                state: state,
                read: { offset, bytes in
                    largestRead = max(largestRead, bytes)
                    return try source.readRange(at: offset, count: bytes)
                })
            for _ in 0..<32 {
                guard let value = try parser.next() else { break }
                XCTAssertEqual(value.value, entry)
                count += 1
            }
            state = try JSONDecoder().decode(ReceiveHistoryStream.State.self, from: JSONEncoder().encode(parser.state))
        } while state.phase != "ready"
        XCTAssertEqual(count, 130000)
        XCTAssertEqual(largestRead, 16384)
        XCTAssertGreaterThan(state.offset, Int64(HistoryRecordDatabase.maximumRecordBytes))
        XCTAssertEqual(state.fields["id"], Data("\"after-array\"".utf8))
    }
}

extension ReceiveCheckpointTests {
    func testOldPartialJobRecoversCompletionBeforeMapsWithoutAdoptingNewerFiles() async throws {
        let (file, defaults) = try setup()
        let store = TransferHistoryStore(defaults: defaults, fileURL: file)
        var old = parent("upgrade")
        try store.add(old)
        let db = try database(file)
        old.savedFiles = ["child/file": "Received/old", "newer/file": "Received/old"]
        var body = Data("{\"savedTransfers\":[\"child\",\"newer\"],".utf8)
        body.append(try encode(old).dropFirst())
        try db.transaction { db in
            let original = HistoryRecordDatabase.Record(
                id: old.localID, scope: "https://one.example|alice", kind: "slot", created: 1, body: body)
            let stripped = try ReceiveCheckpointStorage.stage(db, record: old, original: original)
            try db.write(
                .init(
                    id: original.id, scope: original.scope, kind: original.kind, created: original.created,
                    body: encode(stripped)))
            try ReceiveCheckpointStorage.save(
                db, parent: old, file: .init(transferID: "child", blobID: "file", path: "Received/old", size: nil),
                importing: true)
            try ReceiveCheckpointStorage.save(
                db, parent: old, file: .init(transferID: "newer", blobID: "file", path: "Received/new", size: 12))
            var cursor: String?
            repeat {
                let page = try db.migrationPage(afterID: cursor)
                for entry in page.entries where entry.kind == "receive-child" || entry.kind == "job" {
                    let row = try XCTUnwrap(db.read(entry.id))
                    var value = try JSONSerialization.jsonObject(with: row.body) as! [String: Any]
                    if entry.kind == "receive-child" { value.removeValue(forKey: "imported") }
                    if entry.kind == "job" { value["afterFile"] = "newer/file" }
                    try db.write(
                        .init(
                            id: row.id, scope: row.scope, kind: row.kind, created: row.created,
                            body: JSONSerialization.data(withJSONObject: value)))
                }
                cursor = page.nextID
            } while cursor != nil
        }
        let resumed = TransferHistoryStore(defaults: defaults, fileURL: file)
        await resumed.finishMigration()
        XCTAssertTrue(resumed.isReady, resumed.migrationError ?? "")
        let values = try resumed.receiveCheckpoints(
            parent: old, transferIDs: ["child", "newer"], fileExists: { _, _ in true })
        XCTAssertTrue(values["child"]?.complete == true)
        XCTAssertFalse(values["newer"]?.complete ?? true)
        XCTAssertEqual(values["newer"]?.paths["file"], "Received/new")
    }

    func testPaddedMetadataYieldsAtBoundedMemberBoundary() async throws {
        var text = "[{"
        for index in 0..<100 {
            if index > 0 { text += "," }
            text += "\"unknown-\(index)\":0" + String(repeating: " ", count: 7000)
        }
        text += "}]"
        let source = Data(text.utf8)
        var state = ReceiveHistoryStream.State()
        var batches = 0
        repeat {
            let start = state.offset
            let parser = ReceiveHistoryStream(
                state: state,
                read: { offset, count in
                    let start = Int(offset)
                    return source.subdata(in: start..<min(start + count, source.count))
                })
            XCTAssertNil(try parser.next())
            state = parser.state
            XCTAssertLessThanOrEqual(state.offset - start, 262144 + 16384)
            batches += 1
        } while state.phase != "ready"
        XCTAssertGreaterThan(batches, 1)
    }
}

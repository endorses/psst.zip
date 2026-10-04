import Foundation
import SQLite3
import XCTest

@testable import Psst

final class HistoryRecordDatabaseTests: XCTestCase {
    private typealias Database = HistoryRecordDatabase
    private struct Legacy: Codable {
        let id: String
        let note: String
    }
    private func directory() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }
    private func record(_ id: String, scope: String = "account", kind: String = "transfer", created: Double = 10, body: Data = Data("value".utf8)) -> Database.Record {
        Database.Record(id: id, scope: scope, kind: kind, created: created, body: body)
    }
    private func decode(_ data: Data) throws -> Database.Record {
        let old = try JSONDecoder().decode(Legacy.self, from: data)
        return record(old.id, body: data)
    }
    private func legacy(_ count: Int, at source: URL) throws {
        try JSONEncoder().encode((0..<count).map { Legacy(id: "item-\($0)", note: "original") }).write(to: source)
    }
    private func rawSQL(_ url: URL, _ sql: String) throws {
        var db: OpaquePointer?
        XCTAssertEqual(sqlite3_open(url.path, &db), SQLITE_OK)
        defer { sqlite3_close_v2(db) }
        let code = sqlite3_exec(db, sql, nil, nil, nil)
        if code != SQLITE_OK { throw Database.Failure.sqlite(code) }
    }

    func testSnapshotResumesAcrossBothArraysAndPreservesNewerRowsAndDeletes() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("guest.json")
        let url = folder.appendingPathComponent("records.sqlite")
        let snapshot = [
            "records": (0..<103).map { Legacy(id: "record-\($0)", note: "old") },
            "receipts": (0..<104).map { Legacy(id: "receipt-\($0)", note: "old") },
        ]
        try JSONEncoder().encode(snapshot).write(to: source)
        let original = try Data(contentsOf: source)
        let db = try Database(url: url)
        try db.write(record("record-102", body: Data("new".utf8)))
        try db.remove("receipt-103")
        var calls = 0
        func convert(_ name: String, _ data: Data) throws -> Database.Record {
            calls += 1
            let old = try JSONDecoder().decode(Legacy.self, from: data)
            return record(old.id, scope: "guest", kind: name, body: data)
        }
        let first = try db.migrateJSONSnapshotBatch(source: source, key: "guest", decode: convert)
        XCTAssertEqual(first.processed, 100)
        XCTAssertEqual(calls, 100)
        XCTAssertFalse(first.complete)
        let reopened = try Database(url: url)
        let second = try reopened.migrateJSONSnapshotBatch(source: source, key: "guest", decode: convert)
        XCTAssertEqual(second.processed, 200)
        XCTAssertEqual(calls, 200)
        let last = try db.migrateJSONSnapshotBatch(source: source, key: "guest", decode: convert)
        XCTAssertEqual(last.processed, 207)
        XCTAssertEqual(last.inserted, 205)
        XCTAssertTrue(last.complete)
        XCTAssertEqual(try db.read("record-102")?.body, Data("new".utf8))
        XCTAssertNil(try db.read("receipt-103"))
        XCTAssertEqual(try db.read("receipt-0")?.kind, "receipts")
        XCTAssertEqual(try Data(contentsOf: source), original)
        try FileManager.default.removeItem(at: source)
        XCTAssertEqual(try db.migrateJSONSnapshotBatch(source: source, key: "guest", decode: convert), last)
        XCTAssertEqual(calls, 207)
    }

    func testSnapshotBoundaryEmptyArraysAndRootArrayCompatibility() throws {
        let folder = try directory()
        let fixtures = [
            (#"{"records":[{"id":"a","note":"x"}],"receipts":[{"id":"b","note":"x"}]}"#, ["records", "receipts"]),
            (#"{"receipts":[],"records":[{"id":"a","note":"x"}]}"#, ["records"]),
            (#"{"records":[],"receipts":[]}"#, []),
            (#"{}"#, []),
            (#"[{"id":"a","note":"x"}]"#, ["records"]),
        ]
        for (index, fixture) in fixtures.enumerated() {
            let source = folder.appendingPathComponent("source-\(index).json")
            try Data(fixture.0.utf8).write(to: source)
            let db = try Database(url: folder.appendingPathComponent("db-\(index).sqlite"))
            var arrays: [String] = []
            var progress: Database.MigrationProgress
            repeat {
                progress = try db.migrateJSONSnapshotBatch(source: source, key: "guest", limit: 1) { name, data in
                    arrays.append(name)
                    return try self.decode(data)
                }
            } while !progress.complete
            XCTAssertEqual(arrays, fixture.1)
        }
    }

    func testSnapshotMalformedStructureAndDerivedImportRollback() throws {
        let folder = try directory()
        let fixtures = [
            #"{"records":[],"records":[]}"#, #"{"unknown":[]}"#,
            #"{"records":[],}"#, #"{"receipts":[{}]} trailing"#,
            #"{"records":[{"id":"a","note":"x"}],"receipts":["#,
            #"{"records":null}"#, #"{"records":[{},]}"#,
        ]
        for (index, json) in fixtures.enumerated() {
            let source = folder.appendingPathComponent("bad-\(index).json")
            try Data(json.utf8).write(to: source)
            let db = try Database(url: folder.appendingPathComponent("bad-\(index).sqlite"))
            XCTAssertThrowsError(try db.migrateJSONSnapshotBatch(source: source, key: "guest") { _, _ in self.record("bad") })
            XCTAssertNil(try db.migrationProgress(key: "guest"))
            XCTAssertNil(try db.read("bad"))
            XCTAssertEqual(try String(contentsOf: source), json)
        }
        let source = folder.appendingPathComponent("derived.json")
        try legacy(2, at: source)
        let db = try Database(url: folder.appendingPathComponent("derived.sqlite"))
        enum Expected: Error { case stop }
        XCTAssertThrowsError(
            try db.migrateJSONSnapshotBatch(source: source, key: "guest") { _, data in
                try db.importIfAbsent(self.record("derived"))
                if try JSONDecoder().decode(Legacy.self, from: data).id == "item-1" { throw Expected.stop }
                return try self.decode(data)
            })
        XCTAssertNil(try db.read("derived"))
        XCTAssertNil(try db.read("item-0"))
        XCTAssertNil(try db.migrationProgress(key: "guest"))
        try db.write(record("derived", body: Data("new".utf8)))
        try db.remove("deleted")
        _ = try db.migrateJSONSnapshotBatch(source: source, key: "guest") { _, data in
            XCTAssertFalse(try db.importIfAbsent(self.record("derived")))
            XCTAssertFalse(try db.importIfAbsent(self.record("deleted")))
            return try self.decode(data)
        }
        XCTAssertEqual(try db.read("derived")?.body, Data("new".utf8))
        XCTAssertNil(try db.read("deleted"))
    }

    func testSnapshotSourceChangeAndMigrationModeMismatchFailClosed() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("guest.json")
        let original = #"{"records":[{"id":"a","note":"x"},{"id":"b","note":"x"}],"receipts":[]}"#
        try Data(original.utf8).write(to: source)
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        _ = try db.migrateJSONSnapshotBatch(source: source, key: "guest", limit: 1) { _, data in try self.decode(data) }
        XCTAssertThrowsError(try db.migrateJSONBatch(source: source, key: "guest", decode: decode))
        try Data(original.replacingOccurrences(of: "note", with: "nope").utf8).write(to: source)
        XCTAssertThrowsError(try db.migrateJSONSnapshotBatch(source: source, key: "guest") { _, data in try self.decode(data) })
        XCTAssertEqual(try db.migrationProgress(key: "guest")?.processed, 1)
        XCTAssertNil(try db.read("b"))
    }

    func testVersionOneDatabaseUpgradePreservesRootArrayMigrationProgress() throws {
        let folder = try directory()
        let url = folder.appendingPathComponent("records.sqlite")
        let source = folder.appendingPathComponent("legacy.json")
        try legacy(2, at: source)
        do {
            let db = try Database(url: url)
            _ = try db.migrateJSONBatch(source: source, key: "legacy", limit: 1, decode: decode)
        }
        try rawSQL(url, "ALTER TABLE migrations DROP COLUMN stream_state; PRAGMA user_version=1;")
        let db = try Database(url: url)
        let progress = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(progress.processed, 2)
        XCTAssertTrue(progress.complete)
        XCTAssertNotNil(try db.read("item-0"))
        XCTAssertNotNil(try db.read("item-1"))
    }

    func testMigrationDiscoveryUsesBoundedPrimaryKeyPagesAcrossScopes() throws {
        let url = try directory().appendingPathComponent("records.sqlite")
        let db = try Database(url: url)
        try db.transaction { db in
            for index in 0..<251 { try db.write(record(String(format: "id-%04d", index), scope: "scope-\(index)", kind: index % 2 == 0 ? "slot" : "transfer")) }
        }
        // Discovery reads metadata only, including a row whose body cannot decode.
        try rawSQL(url, "UPDATE records SET body='invalid', body_bytes=-1 WHERE id='id-0000'")
        var cursor: String?
        var ids: [String] = []
        repeat {
            let page = try db.migrationPage(afterID: cursor, limit: 25)
            XCTAssertLessThanOrEqual(page.entries.count, 25)
            ids += page.entries.map(\.id)
            cursor = page.nextID
        } while cursor != nil
        XCTAssertEqual(ids.count, 251)
        XCTAssertEqual(Set(ids).count, 251)
        XCTAssertEqual(ids, ids.sorted())
        XCTAssertThrowsError(try db.migrationPage(limit: 101))
        XCTAssertThrowsError(try db.migrationPage(afterID: ""))
    }

    func testWritesReadsUpdatesRemovalsAndReopenedConnection() throws {
        let url = try directory().appendingPathComponent("records.sqlite")
        let db = try Database(url: url)
        let first = record("one")
        try db.write(first)
        XCTAssertEqual(try db.read("one"), first)
        let reopened = try Database(url: url)
        let changed = record("one", scope: "guest", kind: "download", body: Data())
        try reopened.write(changed)
        XCTAssertEqual(try db.read("one"), changed)
        XCTAssertFalse(try db.hasAny(scopes: ["account"], kinds: ["transfer"]))
        XCTAssertTrue(try db.hasAny(scopes: ["guest"], kinds: ["download"]))
        try db.remove("one")
        XCTAssertNil(try reopened.read("one"))
        try db.write(first)
        XCTAssertEqual(try reopened.read("one"), first)
    }

    func testAtomicReadModifyWriteAndNestedRollback() throws {
        enum Failed: Error { case expected }
        let db = try Database(url: directory().appendingPathComponent("records.sqlite"))
        try db.write(record("one"))
        XCTAssertThrowsError(
            try db.transaction { tx in
                try tx.write(record("one", body: Data("changed".utf8)))
                try tx.remove("missing")
                throw Failed.expected
            })
        XCTAssertEqual(try db.read("one")?.body, Data("value".utf8))
        try db.transaction { tx in
            try tx.write(record("outer"))
            do {
                try tx.transaction { inner in
                    try inner.remove("one")
                    throw Failed.expected
                }
            } catch Failed.expected {}
            XCTAssertNotNil(try tx.read("one"))
        }
        XCTAssertNotNil(try db.read("outer"))
        XCTAssertNotNil(try db.read("one"))
    }

    func testConcurrentConnectionsDoNotLoseReadModifyWriteUpdates() throws {
        let url = try directory().appendingPathComponent("records.sqlite")
        let first = try Database(url: url)
        let second = try Database(url: url)
        try first.write(record("counter", body: Data("0".utf8)))
        let failures = Errors()
        let group = DispatchGroup()
        for database in [first, second] {
            group.enter()
            DispatchQueue.global().async {
                defer { group.leave() }
                do {
                    for _ in 0..<50 {
                        try database.transaction { tx in
                            let old = try XCTUnwrap(tx.read("counter"))
                            let count = Int(String(decoding: old.body, as: UTF8.self))!
                            try tx.write(Database.Record(id: old.id, scope: old.scope, kind: old.kind, created: old.created, body: Data(String(count + 1).utf8)))
                        }
                    }
                } catch { failures.append(error) }
            }
        }
        XCTAssertEqual(group.wait(timeout: .now() + 10), .success)
        XCTAssertEqual(failures.count, 0)
        XCTAssertEqual(try first.read("counter")?.body, Data("100".utf8))
    }

    func testWriterBusyTimeoutLeavesOtherTransactionIntact() throws {
        let url = try directory().appendingPathComponent("records.sqlite")
        let first = try Database(url: url)
        let second = try Database(url: url)
        let entered = DispatchSemaphore(value: 0)
        let release = DispatchSemaphore(value: 0)
        let finished = DispatchSemaphore(value: 0)
        let failures = Errors()
        DispatchQueue.global().async {
            defer { finished.signal() }
            do {
                try first.transaction { tx in
                    try tx.write(Database.Record(id: "held", scope: "account", kind: "transfer", created: 1, body: Data()))
                    entered.signal()
                    _ = release.wait(timeout: .now() + 10)
                }
            } catch {
                failures.append(error)
                entered.signal()
            }
        }
        XCTAssertEqual(entered.wait(timeout: .now() + 3), .success)
        let start = Date()
        XCTAssertThrowsError(try second.write(record("blocked")))
        XCTAssertLessThan(Date().timeIntervalSince(start), 5)
        release.signal()
        XCTAssertEqual(finished.wait(timeout: .now() + 3), .success)
        XCTAssertEqual(failures.count, 0)
        XCTAssertNil(try second.read("blocked"))
        XCTAssertNotNil(try second.read("held"))
    }

    func testPagesUseStableTieOrderAndScopeBoundCursors() throws {
        let db = try Database(url: directory().appendingPathComponent("records.sqlite"))
        for index in 0..<130 {
            try db.write(
                record(String(format: "%03d", index), scope: index % 2 == 0 ? "account" : "legacy", kind: index % 3 == 0 ? "slot" : "transfer", created: Double(index / 10)))
        }
        try db.write(record("private", scope: "another", created: 100))
        var after: Database.Cursor?
        var seen: [Database.Record] = []
        repeat {
            let page = try db.page(scopes: ["legacy", "account"], kinds: ["slot", "transfer"], after: after, limit: 17)
            XCTAssertLessThanOrEqual(page.records.count, 17)
            seen += page.records
            after = page.next
            if let cursor = after {
                let decoded = try JSONDecoder().decode(Database.Cursor.self, from: JSONEncoder().encode(cursor))
                XCTAssertEqual(cursor, decoded)
                XCTAssertThrowsError(try db.page(scopes: ["another"], kinds: ["slot", "transfer"], after: cursor))
            }
        } while after != nil
        XCTAssertEqual(seen.count, 130)
        XCTAssertEqual(Set(seen.map(\.id)).count, 130)
        for (left, right) in zip(seen, seen.dropFirst()) {
            XCTAssertTrue(left.created > right.created || (left.created == right.created && left.id < right.id))
        }
        XCTAssertThrowsError(try db.page(scopes: ["account"], kinds: ["transfer"], limit: 0))
        XCTAssertThrowsError(try db.page(scopes: ["account"], kinds: ["transfer"], limit: 101))
    }

    func testPageBodyBudgetKeepsAUsableContinuation() throws {
        let db = try Database(url: directory().appendingPathComponent("records.sqlite"))
        let body = Data(repeating: 65, count: 9 * 1024 * 1024)
        try db.write(record("a", body: body))
        try db.write(record("b", body: body))
        let first = try db.page(scopes: ["account"], kinds: ["transfer"])
        XCTAssertEqual(first.records.map(\.id), ["a"])
        XCTAssertNotNil(first.next)
        let second = try db.page(scopes: ["account"], kinds: ["transfer"], after: first.next)
        XCTAssertEqual(second.records.map(\.id), ["b"])
        XCTAssertNil(second.next)
    }

    func testInvalidAndOversizedRecordsDoNotEraseExistingData() throws {
        let db = try Database(url: directory().appendingPathComponent("records.sqlite"))
        try db.write(record("one"))
        for bad in [
            record("one", created: .infinity), record("one", scope: ""), record("one\0suffix"), record("one", body: Data(repeating: 1, count: Database.maximumRecordBytes + 1)),
        ] {
            XCTAssertThrowsError(try db.write(bad))
            XCTAssertEqual(try db.read("one")?.body, Data("value".utf8))
        }
    }

    func testMigrationResumesWithoutOverwritingNewerRowsOrResurrectingDeletes() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        let url = folder.appendingPathComponent("records.sqlite")
        try legacy(205, at: source)
        let original = try Data(contentsOf: source)
        let db = try Database(url: url)
        let first = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(first, Database.MigrationProgress(processed: 100, inserted: 100, complete: false))
        let second = try Database(url: url)
        try second.write(record("item-101", body: Data("new checkpoint".utf8)))
        try second.remove("item-150")
        var calls = 0
        let middle = try second.migrateJSONBatch(source: source, key: "legacy") { data in
            calls += 1
            return try self.decode(data)
        }
        XCTAssertEqual(calls, 100)
        XCTAssertEqual(middle.processed, 200)
        XCTAssertFalse(middle.complete)
        let last = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(last, Database.MigrationProgress(processed: 205, inserted: 203, complete: true))
        XCTAssertEqual(try db.read("item-101")?.body, Data("new checkpoint".utf8))
        XCTAssertNil(try db.read("item-150"))
        XCTAssertEqual(try Data(contentsOf: source), original)
        let repeated = try second.migrateJSONBatch(source: source, key: "legacy") { _ in
            XCTFail("Completed migration decoded again")
            return self.record("bad")
        }
        XCTAssertEqual(repeated, last)
    }

    func testMigrationFailedBatchRollsBackRowsAndProgress() throws {
        enum Failed: Error { case expected }
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        try legacy(4, at: source)
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        var calls = 0
        XCTAssertThrowsError(
            try db.migrateJSONBatch(source: source, key: "legacy") { data in
                calls += 1
                if calls == 3 { throw Failed.expected }
                return try self.decode(data)
            })
        XCTAssertFalse(try db.hasAny(scopes: ["account"], kinds: ["transfer"]))
        let progress = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(progress.processed, 4)
        XCTAssertEqual(progress.inserted, 4)
        XCTAssertTrue(progress.complete)
    }

    func testMigrationDetectsChangedSourceBetweenAndDuringBatches() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        try legacy(3, at: source)
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        _ = try db.migrateJSONBatch(source: source, key: "legacy", limit: 1, decode: decode)
        try legacy(4, at: source)
        XCTAssertThrowsError(try db.migrateJSONBatch(source: source, key: "legacy", decode: decode))
        XCTAssertNil(try db.read("item-1"))
        let during = folder.appendingPathComponent("during.json")
        try legacy(3, at: during)
        var changed = false
        XCTAssertThrowsError(
            try db.migrateJSONBatch(source: during, key: "during") { data in
                if !changed {
                    changed = true
                    try Data("[]".utf8).write(to: during, options: .atomic)
                }
                return try self.decode(data)
            })
        XCTAssertNil(try db.read("item-2"))
        XCTAssertEqual(try Data(contentsOf: during), Data("[]".utf8))
    }

    func testStreamingParserHandlesEscapesNestedValuesAndRejectsTruncation() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        let good = #"[{"id":"a","note":"escaped \\"}, {"id":"b","note":"quotes \" } ] { ","nested":[{"more":true}]}]"#
        try Data(good.utf8).write(to: source)
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        let progress = try db.migrateJSONBatch(source: source, key: "good", decode: decode)
        XCTAssertTrue(progress.complete)
        XCTAssertEqual(progress.processed, 2)
        for (index, invalid) in ["{}", "[{\"id\":\"a\",\"note\":\"x\"},]", "[{\"id\":\"a\",\"note\":\"x\"}", "[] trailing", "[1]"].enumerated() {
            let bad = folder.appendingPathComponent("bad-\(index).json")
            try Data(invalid.utf8).write(to: bad)
            XCTAssertThrowsError(try db.migrateJSONBatch(source: bad, key: "bad-\(index)", decode: decode))
            XCTAssertEqual(try String(contentsOf: bad), invalid)
        }
    }

    func testOversizedStreamingObjectPreservesOriginalAndEmptyDatabase() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("large.json")
        let bytes = Data(("[{\"id\":\"large\",\"note\":\"" + String(repeating: "x", count: Database.maximumRecordBytes) + "\"}]").utf8)
        try bytes.write(to: source)
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        XCTAssertThrowsError(try db.migrateJSONBatch(source: source, key: "large", decode: decode))
        XCTAssertEqual(try Data(contentsOf: source), bytes)
        XCTAssertFalse(try db.hasAny(scopes: ["account"], kinds: ["transfer"]))
    }

    func testMigrationSQLiteFailureDoesNotCommitItsMarker() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        let url = folder.appendingPathComponent("records.sqlite")
        try legacy(3, at: source)
        let db = try Database(url: url)
        try rawSQL(url, "CREATE TRIGGER fail_second BEFORE INSERT ON records WHEN NEW.id='item-1' BEGIN SELECT RAISE(ABORT,'expected'); END;")
        XCTAssertThrowsError(try db.migrateJSONBatch(source: source, key: "legacy", decode: decode))
        XCTAssertNil(try db.read("item-0"))
        try rawSQL(url, "DROP TRIGGER fail_second")
        let progress = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(progress.processed, 3)
        XCTAssertEqual(progress.inserted, 3)
        XCTAssertTrue(progress.complete)
    }

    func testCompletedMigrationSurvivesReplacedOrMissingLegacySource() throws {
        let folder = try directory()
        let source = folder.appendingPathComponent("legacy.json")
        let db = try Database(url: folder.appendingPathComponent("records.sqlite"))
        XCTAssertNil(try db.migrationProgress(key: "legacy"))
        try legacy(1, at: source)
        let complete = try db.migrateJSONBatch(source: source, key: "legacy", decode: decode)
        XCTAssertEqual(try db.migrationProgress(key: "legacy"), complete)
        try Data("invalid replacement".utf8).write(to: source, options: .atomic)
        XCTAssertEqual(try db.migrateJSONBatch(source: source, key: "legacy", decode: decode), complete)
        try FileManager.default.removeItem(at: source)
        XCTAssertEqual(try db.migrateJSONBatch(source: source, key: "legacy", decode: decode), complete)
        try legacy(3, at: source)
        _ = try db.migrateJSONBatch(source: source, key: "incomplete", limit: 1, decode: decode)
        XCTAssertEqual(try db.migrationProgress(key: "incomplete")?.complete, false)
        try FileManager.default.removeItem(at: source)
        XCTAssertThrowsError(try db.migrateJSONBatch(source: source, key: "incomplete", decode: decode))
        XCTAssertEqual(try db.migrationProgress(key: "incomplete")?.complete, false)
    }

    func testMetadataPageAndExistenceQueriesUseCoveringIndexSeeks() throws {
        let url = try directory().appendingPathComponent("records.sqlite")
        let database = try Database(url: url)
        try database.transaction { tx in
            for index in 0..<2_000 { try tx.write(record("row-\(index)", created: Double(index))) }
        }
        var db: OpaquePointer?
        XCTAssertEqual(sqlite3_open(url.path, &db), SQLITE_OK)
        defer { sqlite3_close_v2(db) }
        for query in [
            "SELECT id,sort_created,body_bytes FROM records INDEXED BY records_page WHERE scope='account' AND kind='transfer' AND (sort_created,id)>(-1000,'row-1000') ORDER BY sort_created,id LIMIT 101",
            "SELECT EXISTS(SELECT 1 FROM records INDEXED BY records_page WHERE scope='legacy' AND kind='slot')",
        ] {
            var statement: OpaquePointer?
            XCTAssertEqual(sqlite3_prepare_v2(db, "EXPLAIN QUERY PLAN " + query, -1, &statement, nil), SQLITE_OK)
            defer { sqlite3_finalize(statement) }
            var plan = ""
            while sqlite3_step(statement) == SQLITE_ROW {
                if let detail = sqlite3_column_text(statement, 3) { plan += String(cString: detail) }
            }
            XCTAssertTrue(plan.contains("SEARCH records USING COVERING INDEX records_page"), plan)
            XCTAssertFalse(plan.contains("TEMP B-TREE"), plan)
        }
    }

    func testUnicodeTieOrderMatchesSQLiteBinarySeekOrder() throws {
        let db = try Database(url: directory().appendingPathComponent("records.sqlite"))
        let ids = ["é", "e\u{301}", "z"]
        for id in ids { try db.write(record(id)) }
        var after: Database.Cursor?
        var actual: [Data] = []
        repeat {
            let page = try db.page(scopes: ["account"], kinds: ["transfer"], after: after, limit: 1)
            actual += page.records.map { Data($0.id.utf8) }
            after = page.next
        } while after != nil
        XCTAssertEqual(actual, ids.sorted { $0.utf8.lexicographicallyPrecedes($1.utf8) }.map { Data($0.utf8) })
    }

    private final class Errors: @unchecked Sendable {
        private let lock = NSLock()
        private var errors: [Error] = []
        var count: Int {
            lock.lock()
            defer { lock.unlock() }
            return errors.count
        }
        func append(_ error: Error) {
            lock.lock()
            defer { lock.unlock() }
            errors.append(error)
        }
    }
}

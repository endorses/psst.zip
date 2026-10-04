import Foundation
import XCTest

@testable import Psst

final class DeviceRetryQueueTests: XCTestCase {
    private func root() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }
    private func job(_ id: String, owner: String? = nil, origin: String = "https://one.example") -> DeviceRetryQueue.Job {
        .init(origin: origin, transferID: id, ownerID: owner)
    }
    func testExactWritesScopedEligibleBatchesAndFailureRotationSurviveRestart() throws {
        let file = try root().appendingPathComponent("queue.sqlite3")
        let queue = try DeviceRetryQueue(url: file, kind: "receipt")
        for n in 0..<1000 { try queue.enqueue(job("foreign-\(n)", owner: "other"), now: Double(n)) }
        for n in 0..<9 { try queue.enqueue(job("mine-\(n)", owner: "me"), now: Double(n)) }
        try queue.enqueue(job("anonymous"), now: 0)
        let scope = DeviceRetryQueue.identity(["https://one.example", "me"])
        let batch = try queue.batch(scopes: ["anonymous", scope]).jobs
        XCTAssertEqual(batch.count, 4)
        XCTAssertTrue(batch.allSatisfy { $0.ownerID == "me" || $0.ownerID == nil })
        for row in batch { try queue.rotate(row, now: 10000) }
        let next = try queue.batch(scopes: ["anonymous", scope]).jobs
        XCTAssertTrue(next.allSatisfy { !batch.contains($0) })
        try queue.remove(next[0])
        let reopened = try DeviceRetryQueue(url: file, kind: "receipt")
        XCTAssertNil(try reopened.find(next[0].id))
        XCTAssertEqual(try reopened.find(job("foreign-999", owner: "other").id), job("foreign-999", owner: "other"))
    }
    func testMalformedOldestJobIsPreservedAndRotatedWithoutStarvingHealthyTail() throws {
        let file = try root().appendingPathComponent("queue.sqlite3")
        let queue = try DeviceRetryQueue(url: file, kind: "receipt")
        let raw = try HistoryRecordDatabase(url: file)
        let damaged = Data("broken metadata".utf8)
        try raw.write(.init(id: "damaged", scope: "anonymous", kind: "receipt", created: 0, body: damaged))
        for n in 0..<8 { try queue.enqueue(job("valid-\(n)"), now: Double(n + 1)) }
        let first = try queue.batch(scopes: ["anonymous"])
        XCTAssertTrue(first.hadInvalidJobs)
        XCTAssertEqual(first.jobs.count, 3)
        XCTAssertEqual(try raw.read("damaged")?.body, damaged)
        for row in first.jobs { try queue.remove(row) }
        let second = try queue.batch(scopes: ["anonymous"])
        XCTAssertFalse(second.hadInvalidJobs)
        XCTAssertEqual(second.jobs.count, 4)
        XCTAssertTrue(second.jobs.allSatisfy { !first.jobs.contains($0) })
        try queue.remove(job("again"))
        try queue.enqueue(job("again"))
        XCTAssertEqual(try queue.find(job("again").id), job("again"), "Explicit new receipt may reactivate an identity; import may not")
    }
    func testMigrationBatchesRetainCurrentRecordsAndRetiredJobsAndCompletionSkipsLegacyRead() throws {
        let file = try root().appendingPathComponent("queue.sqlite3")
        let source = try JSONEncoder().encode((0..<120).map { job("legacy-\($0)") })
        var queue = try DeviceRetryQueue(url: file, kind: "receipt")
        let current = job("legacy-60")
        try queue.enqueue(current, now: 9000)
        try queue.remove(job("legacy-70"))
        var prepared = 0
        let read = { DeviceRetryQueue.LegacySource(data: source, fingerprint: "same") }
        let decode = { try JSONDecoder().decode(DeviceRetryQueue.Job.self, from: $0) }
        XCTAssertFalse(try queue.migrate(source: read, decode: decode, prepare: { _, _ in prepared += 1 }))
        XCTAssertEqual(prepared, 25)
        queue = try DeviceRetryQueue(url: file, kind: "receipt")
        while try !queue.migrate(source: read, decode: decode, prepare: { _, _ in prepared += 1 }) {}
        XCTAssertEqual(prepared, 118)
        XCTAssertNil(try queue.find(job("legacy-70").id))
        XCTAssertEqual(try queue.find(current.id), current)
        XCTAssertTrue(
            try queue.migrate(
                source: {
                    XCTFail("Completed migration must not read Keychain/defaults")
                    return nil
                }, decode: decode))
        XCTAssertEqual(try JSONDecoder().decode([DeviceRetryQueue.Job].self, from: source), (0..<120).map { job("legacy-\($0)") })
    }
    func testUnavailableSourceNeverBecomesEmptyAndPartialMigrationRejectsChangedOrMissingSource() throws {
        let queue = try DeviceRetryQueue(url: root().appendingPathComponent("queue.sqlite3"), kind: "cleanup")
        enum Locked: Error { case keychain }
        let decode = { try JSONDecoder().decode(DeviceRetryQueue.Job.self, from: $0) }
        XCTAssertThrowsError(try queue.migrate(source: { throw Locked.keychain }, decode: decode))
        XCTAssertFalse(try queue.migrationComplete)
        let data = try JSONEncoder().encode((0..<30).map { job("old-\($0)") })
        XCTAssertFalse(try queue.migrate(source: { .init(data: data, fingerprint: "original") }, decode: decode))
        XCTAssertThrowsError(try queue.migrate(source: { nil }, decode: decode))
        XCTAssertThrowsError(try queue.migrate(source: { .init(data: data, fingerprint: "changed") }, decode: decode))
        XCTAssertTrue(try queue.migrate(source: { .init(data: data, fingerprint: "original") }, decode: decode))
    }
    func testSecretsPrepareBeforeIndexAndFailureRollsBackWholeBatch() throws {
        struct Legacy: Codable {
            let origin: String
            let transferID: String
            let capability: String
        }
        let queue = try DeviceRetryQueue(url: root().appendingPathComponent("queue.sqlite3"), kind: "cleanup")
        let input = [
            Legacy(origin: "https://one.example", transferID: "one", capability: "secret-one"), Legacy(origin: "https://one.example", transferID: "two", capability: "secret-two"),
        ]
        let data = try JSONEncoder().encode(input)
        var secrets: [String: String] = [:]
        let decode: (Data) throws -> DeviceRetryQueue.Job = { data in
            let old = try JSONDecoder().decode(Legacy.self, from: data)
            return .init(origin: old.origin, transferID: old.transferID, keyReference: "ref-" + old.transferID)
        }
        let read = { DeviceRetryQueue.LegacySource(data: data, fingerprint: "same") }
        XCTAssertThrowsError(
            try queue.migrate(
                source: read, decode: decode,
                prepare: { body, row in
                    if row.transferID == "two" { throw DeviceRetryQueue.Failure.invalid }
                    secrets[row.keyReference!] = try JSONDecoder().decode(Legacy.self, from: body).capability
                }))
        XCTAssertNil(try queue.find(job("one").id))
        XCTAssertFalse(try queue.migrationComplete)
        XCTAssertTrue(
            try queue.migrate(
                source: read, decode: decode,
                prepare: { body, row in
                    secrets[row.keyReference!] = try JSONDecoder().decode(Legacy.self, from: body).capability
                }))
        let row = try XCTUnwrap(queue.find(job("one").id))
        XCTAssertFalse(String(decoding: try JSONEncoder().encode(row), as: UTF8.self).contains("secret-one"))
        XCTAssertEqual(secrets[row.keyReference!], "secret-one")
    }
    func testMalformedOversizedAndEscapedLegacyEntriesPreserveSourceAndRejectInvalidBounds() throws {
        let queue = try DeviceRetryQueue(url: root().appendingPathComponent("queue.sqlite3"), kind: "receipt")
        let decode = { try JSONDecoder().decode(DeviceRetryQueue.Job.self, from: $0) }
        for text in ["[{},]", "[", "[] trailing", "[" + String(repeating: " ", count: 1_048_578) + "]", "[{\"x\":\"" + String(repeating: "x", count: 16_385) + "\"}]"] {
            let data = Data(text.utf8)
            XCTAssertThrowsError(try queue.migrate(source: { .init(data: data, fingerprint: "same") }, decode: decode))
            XCTAssertFalse(try queue.migrationComplete)
            XCTAssertEqual(data, Data(text.utf8))
        }
        let escaped = job("quoted-\"-\\-é")
        let data = try JSONEncoder().encode([escaped])
        XCTAssertTrue(try queue.migrate(source: { .init(data: data, fingerprint: "valid") }, decode: decode))
        XCTAssertEqual(try queue.find(escaped.id), escaped)
        XCTAssertThrowsError(try queue.batch(scopes: ["anonymous"], limit: 6))
    }
}

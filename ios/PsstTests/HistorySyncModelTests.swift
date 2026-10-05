import Foundation
import XCTest

@testable import Psst

// Portable harness supplies network, Keychain and private-record persistence boundaries;
// production snapshot/cache reconciliation, SQLite metadata and HistoryPageViewModel execute unchanged.
#if os(Linux)
    @MainActor
    final class HistorySyncModelTests: XCTestCase {
        private let generation = "01234567-89ab-cdef-0123-456789abcdef"
        private let id = "22222222-2222-4222-8222-222222222222"
        private var row: String {
            "{\"id\":\"\(id)\",\"revision\":1,\"title\":\"Original\",\"created_at\":\"2026-10-05T10:00:00Z\",\"status\":\"complete\",\"file_count\":1,\"total_size\":100,\"summary\":{\"state\":\"ready\",\"file_count\":1,\"completed_files\":1,\"total_size\":100}}"
        }
        private func store() throws -> TransferHistoryStore {
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent(
                UUID().uuidString)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
            return try TransferHistoryStore(url: directory.appendingPathComponent("cache.sqlite"))
        }
        private func snapshot() throws -> ResourceList {
            try JSONDecoder().decode(
                ResourceList.self,
                from: Data(
                    "{\"paginated\":true,\"transfers\":[\(row)],\"slots\":[],\"next_cursor\":null,\"sync_cursor\":\"one\",\"generation\":\"\(generation)\"}"
                        .utf8))
        }
        private func retain(_ history: TransferHistoryStore, session: DeviceSession) throws {
            var record = try XCTUnwrap(history.projectedResourcePage(snapshot(), session: session, local: []).first)
            record.shareURL = session.serverURL + "/d/" + id + "#private-key"
            record.customTitle = "Retained device label"
            try history.mutate(ids: [record.localID]) { $0 = [record] }
        }
        func testCachedPageAppearsBeforePendingNetworkAndCancellationRejectsLateReply() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try history.cacheServerPage(
                snapshot(), session: session, kind: nil, after: nil, expected: nil)
            actor Gate {
                var continuation: CheckedContinuation<Data, Error>?
                func wait() async throws -> Data {
                    try await withCheckedThrowingContinuation { continuation = $0 }
                }
                func ready() -> Bool { continuation != nil }
                func finish() {
                    continuation?.resume(returning: Data(#"{"history_sync_version":1}"#.utf8))
                    continuation = nil
                }
            }
            let gate = Gate()
            AccountHTTP.handler = { _ in try await gate.wait() }
            let model = HistoryPageViewModel()
            let task = Task { await model.refresh(history: history, session: session) }
            while !(await gate.ready()) { await Task.yield() }
            XCTAssertEqual(model.records.first?.sharedTitle, "Original")
            XCTAssertTrue(model.loading)
            model.cancel()
            await gate.finish()
            let success = await task.value
            XCTAssertFalse(success)
            XCTAssertFalse(model.loading)
            XCTAssertEqual(try history.historySyncState(session: session)?.cursor, "one")
        }
        func testQuietRefreshUsesOnlyFeedAndMutationRefreshesCoalesce() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try history.cacheServerPage(
                snapshot(), session: session, kind: nil, after: nil, expected: nil)
            let generation = self.generation
            actor Requests {
                var paths: [String] = []
                var continuation: CheckedContinuation<Data, Error>?
                let reply: Data
                init(generation: String) {
                    reply = Data(
                        "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[],\"next_cursor\":\"one\",\"has_more\":false}"
                            .utf8)
                }
                func fetch(_ path: String) async throws -> Data {
                    paths.append(path)
                    if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                    if paths.count == 2 {
                        return try await withCheckedThrowingContinuation { continuation = $0 }
                    }
                    return reply
                }
                func ready() -> Bool { continuation != nil }
                func finish() {
                    continuation?.resume(returning: reply)
                    continuation = nil
                }
                func observed() -> [String] { paths }
            }
            let requests = Requests(generation: generation)
            AccountHTTP.handler = { path in try await requests.fetch(path) }
            let model = HistoryPageViewModel()
            let running = Task { await model.refresh(history: history, session: session) }
            while !(await requests.ready()) { await Task.yield() }
            _ = await model.refresh(history: history, session: session)
            _ = await model.refresh(history: history, session: session)
            await requests.finish()
            let success = await running.value
            XCTAssertTrue(success)
            let paths = await requests.observed()
            XCTAssertEqual(
                paths,
                [
                    "config", "auth/history/changes?cursor=one&limit=50",
                    "auth/history/changes?cursor=one&limit=50",
                ])
            XCTAssertEqual(model.records.first?.sharedTitle, "Original")
            XCTAssertFalse(model.loading)
        }

        func testLegacyFallbackFetchesOnlyOneBoundedPagePerRefresh() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            let legacy = ResourceList(transfers: try snapshot().transfers, slots: [])
            let data = try JSONEncoder().encode(legacy)
            actor Requests {
                var paths: [String] = []
                let page: Data
                init(page: Data) { self.page = page }
                func fetch(_ path: String) -> Data {
                    paths.append(path)
                    return path == "config" ? Data(#"{}"#.utf8) : page
                }
                func observed() -> [String] { paths }
            }
            let requests = Requests(page: data)
            AccountHTTP.handler = { path in await requests.fetch(path) }
            let model = HistoryPageViewModel()
            let first = await model.refresh(history: history, session: session)
            let second = await model.refresh(history: history, session: session)
            XCTAssertTrue(first)
            XCTAssertTrue(second)
            let paths = await requests.observed()
            XCTAssertEqual(paths, ["config", "auth/resources?limit=50", "auth/resources?limit=50"])
            XCTAssertEqual(model.records.count, 1)
        }

        func testLargeArrivalsUseServerRowAnchorAndResetPreservesOlderWindow() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            let originalGeneration = generation
            let restartedGeneration = "33333333-3333-4333-8333-333333333333"
            let template = try JSONSerialization.jsonObject(with: Data(row.utf8)) as! [String: Any]
            func resource(_ number: Int) -> [String: Any] {
                var result = template
                result["id"] = String(format: "00000000-0000-4000-8000-%012x", number)
                result["revision"] = number + 1
                result["title"] = "File \(number)"
                result["created_at"] = ISO8601DateFormatter().string(
                    from: Date(timeIntervalSince1970: 1_759_658_400 + Double(number)))
                result["history_after"] = "anchor-all-\(number)"
                result["history_after_kind"] = "anchor-kind-\(number)"
                return result
            }
            func page(_ numbers: [Int], next: String?, generation: String, cursor: String) throws -> Data {
                try JSONSerialization.data(withJSONObject: [
                    "paginated": true, "transfers": numbers.map(resource), "slots": [],
                    "next_cursor": next as Any? ?? NSNull(), "sync_cursor": cursor, "generation": generation,
                ])
            }
            let original = try JSONDecoder().decode(
                ResourceList.self,
                from: page(
                    Array((0..<50).reversed()), next: nil, generation: originalGeneration, cursor: "one"))
            try history.cacheServerPage(original, session: session, kind: nil, after: nil, expected: nil)
            var state = HistoryRecordDatabase.SyncState(generation: originalGeneration, cursor: "one")
            for first in [50, 100] {
                let changes: [[String: Any]] = (first..<(first + 50)).map { number in
                    let row = resource(number)
                    return [
                        "kind": "transfer", "id": row["id"]!, "revision": number + 1, "action": "upsert",
                        "resource": row,
                    ]
                }
                let cursor = "cursor-\(first)"
                let data = try JSONSerialization.data(withJSONObject: [
                    "version": 1, "generation": originalGeneration, "changes": changes, "next_cursor": cursor,
                    "has_more": false,
                ])
                try history.applyHistoryChanges(
                    JSONDecoder().decode(HistorySync.Batch.self, from: data), session: session,
                    expected: state)
                state = .init(generation: originalGeneration, cursor: cursor)
            }
            let newest = try XCTUnwrap(history.cachedServerPage(session: session, kind: nil, after: nil))
            XCTAssertEqual(newest.1.count, 50)
            XCTAssertEqual(newest.0.next, "anchor-all-100")
            XCTAssertFalse(newest.0.navigationStale == true)
            let older = try page(
                Array((50..<100).reversed()), next: "anchor-all-50", generation: originalGeneration,
                cursor: state.cursor)
            let restartNewest = try page(
                Array((100..<150).reversed()), next: "anchor-all-100", generation: restartedGeneration,
                cursor: "restart")
            let restartOlder = try page(
                Array((50..<100).reversed()), next: "anchor-all-50", generation: restartedGeneration,
                cursor: "restart")
            let oldest = try page(
                Array((0..<50).reversed()), next: nil, generation: restartedGeneration, cursor: "restart")
            actor Requests {
                var restart = false
                let originalGeneration: String
                let older: Data, newest: Data, restartOlder: Data, oldest: Data
                var paths: [String] = []
                init(generation: String, older: Data, newest: Data, restartOlder: Data, oldest: Data) {
                    originalGeneration = generation
                    self.older = older
                    self.newest = newest
                    self.restartOlder = restartOlder
                    self.oldest = oldest
                }
                func setRestart() { restart = true }
                func fetch(_ path: String) throws -> Data {
                    paths.append(path)
                    if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                    if path.hasPrefix("auth/history") {
                        if restart { throw HistorySync.Failure.resetRequired }
                        return Data(
                            "{\"version\":1,\"generation\":\"\(originalGeneration)\",\"changes\":[],\"next_cursor\":\"cursor-100\",\"has_more\":false}"
                                .utf8)
                    }
                    if path.contains("after=anchor-all-100") { return restart ? restartOlder : older }
                    if path.contains("after=anchor-all-50") { return oldest }
                    return newest
                }
            }
            let requests = Requests(
                generation: originalGeneration, older: older, newest: restartNewest,
                restartOlder: restartOlder, oldest: oldest)
            AccountHTTP.handler = { path in try await requests.fetch(path) }
            let model = HistoryPageViewModel()
            _ = await model.refresh(history: history, session: session)
            await model.next(history: history, session: session)
            XCTAssertEqual(model.records.first?.sharedTitle, "File 99")
            XCTAssertEqual(model.window.cursor, "anchor-all-100")
            await requests.setRestart()
            let recovered = await model.refresh(history: history, session: session)
            XCTAssertTrue(recovered)
            XCTAssertEqual(
                model.records.first?.sharedTitle, "File 99",
                "Restart must not jump older-page reader to newest rows")
            XCTAssertEqual(model.window.cursor, "anchor-all-100")
            await model.next(history: history, session: session)
            XCTAssertEqual(model.records.first?.sharedTitle, "File 49")
            XCTAssertEqual(model.records.last?.sharedTitle, "File 0")
        }

        func testCancelStopsUnderlyingHTTPAndScopesRemainIsolated() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try history.cacheServerPage(
                snapshot(), session: session, kind: nil, after: nil, expected: nil)
            actor Request {
                var waiting = false
                func fetch(_ path: String) async throws -> Data {
                    waiting = true
                    try await Task.sleep(nanoseconds: 60_000_000_000)
                    return Data()
                }
                func ready() -> Bool { waiting }
            }
            let request = Request()
            AccountHTTP.handler = { path in try await request.fetch(path) }
            let model = HistoryPageViewModel()
            let running = Task { await model.refresh(history: history, session: session) }
            while !(await request.ready()) { await Task.yield() }
            model.cancel()
            let result = await running.value
            XCTAssertFalse(result)
            XCTAssertFalse(model.loading)
            var other = session
            other.serverURL = "https://other.example"
            XCTAssertNil(try history.cachedServerPage(session: other, kind: nil, after: nil))
            XCTAssertNil(try history.historySyncState(session: other))
            XCTAssertNotNil(try history.cachedServerPage(session: session, kind: nil, after: nil))
        }

        func testCorruptDisposableSummaryRebootstrapsWithoutRemovingPrivateRecord() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try retain(history, session: session)
            let original = try snapshot()
            try history.cacheServerPage(original, session: session, kind: nil, after: nil, expected: nil)
            let state = HistoryRecordDatabase.SyncState(generation: generation, cursor: "one")
            try history.readyDatabase().applyServerFacts(
                scope: session.accountID, expected: state, state: state,
                facts: [
                    .init(
                        identity: id + "|transfer", kind: "transfer", revision: 2, created: 100,
                        body: Data("corrupt".utf8))
                ])
            let bootstrap = try JSONEncoder().encode(original)
            let generation = self.generation
            AccountHTTP.handler = { path in
                if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                if path.hasPrefix("auth/history") {
                    return Data(
                        "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[],\"next_cursor\":\"one\",\"has_more\":false}"
                            .utf8)
                }
                return bootstrap
            }
            let model = HistoryPageViewModel()
            let recovered = await model.refresh(history: history, session: session)
            XCTAssertTrue(recovered)
            XCTAssertEqual(model.records.first?.sharedTitle, "Original")
            XCTAssertEqual(
                try history.records(
                    ids: ["resource|" + session.serverURL + "|" + session.userID + "|" + id + "|transfer"],
                    session: session
                ).count, 1)
        }

        func testCachedExpiryDisablesAvailabilityEvenWithoutHTTP() async throws {
            let history = try store()
            let session = DeviceSession()
            var object = try JSONSerialization.jsonObject(with: Data(row.utf8)) as! [String: Any]
            object["expires_at"] = "2000-01-01T00:00:00Z"
            let expired = try JSONDecoder().decode(
                ResourceList.Transfer.self, from: JSONSerialization.data(withJSONObject: object))
            let page = ResourceList(transfers: [expired], slots: [], sync: "one", generation: generation)
            try history.cacheServerPage(page, session: session, kind: nil, after: nil, expected: nil)
            let cached = try XCTUnwrap(
                history.cachedServerPage(session: session, kind: nil, after: nil)?.1.first)
            XCTAssertTrue(cached.isExpired)
            XCTAssertFalse(cached.linkActive)
        }

        func testLaterNewestSnapshotCannotSkipUnseenOlderChanges() async throws {
            let history = try store()
            let session = DeviceSession()
            try history.cacheServerPage(
                snapshot(), session: session, kind: nil, after: nil, expected: nil)
            let before = HistoryRecordDatabase.SyncState(generation: generation, cursor: "one")
            let newer = ResourceList(
                transfers: try snapshot().transfers, slots: [], sync: "three", generation: generation)
            try history.cacheServerPage(newer, session: session, kind: nil, after: nil, expected: before)
            XCTAssertEqual(
                try history.historySyncState(session: session), before,
                "Only bootstrap/reset may seed the feed cursor; page navigation must not skip changes to cached older rows"
            )
        }

        func testFeedUpdatesOlderMetadataThenRemovalKeepsDeviceRecord() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try retain(history, session: session)
            try history.cacheServerPage(
                snapshot(), session: session, kind: nil, after: nil, expected: nil)
            let changed = row.replacingOccurrences(of: "Original", with: "Updated").replacingOccurrences(
                of: "\"revision\":1", with: "\"revision\":2")
            let update = Data(
                "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[{\"kind\":\"transfer\",\"id\":\"\(id)\",\"revision\":2,\"action\":\"upsert\",\"resource\":\(changed)}],\"next_cursor\":\"two\",\"has_more\":false}"
                    .utf8)
            let batch = try JSONDecoder().decode(HistorySync.Batch.self, from: update)
            try history.applyHistoryChanges(
                batch, session: session, expected: .init(generation: generation, cursor: "one"))
            XCTAssertEqual(
                try history.cachedServerPage(session: session, kind: nil, after: nil)?.1.first?.sharedTitle,
                "Updated")
            let removal = try JSONDecoder().decode(
                HistorySync.Batch.self,
                from: Data(
                    "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[{\"kind\":\"transfer\",\"id\":\"\(id)\",\"revision\":3,\"action\":\"remove\"}],\"next_cursor\":\"three\",\"has_more\":false}"
                        .utf8))
            try history.applyHistoryChanges(
                removal, session: session, expected: .init(generation: generation, cursor: "two"))
            XCTAssertTrue(
                try history.cachedServerPage(session: session, kind: nil, after: nil)!.1.isEmpty)
            XCTAssertEqual(
                try history.records(
                    ids: ["resource|" + session.serverURL + "|" + session.userID + "|" + id + "|transfer"],
                    session: session
                ).first?.state, .revoked)
            // A stale snapshot cannot overwrite the newer removal or cursor.
            try history.cacheServerPage(
                snapshot(), session: session, kind: "transfer", after: nil,
                expected: .init(generation: generation, cursor: "three"))
            XCTAssertTrue(
                try history.cachedServerPage(session: session, kind: "transfer", after: nil)!.1.isEmpty)
            // A contradictory equal-revision page cannot revive either facts or private status.
            let sameRevision = try JSONDecoder().decode(
                ResourceList.Transfer.self, from: Data(row.replacingOccurrences(of: "\"revision\":1", with: "\"revision\":3").utf8))
            try history.cacheServerPage(
                ResourceList(transfers: [sameRevision], slots: [], sync: "three", generation: generation), session: session,
                kind: "transfer", after: nil, expected: .init(generation: generation, cursor: "three"))
            let retained = try history.records(
                ids: ["resource|" + session.serverURL + "|" + session.userID + "|" + id + "|transfer"], session: session)
            XCTAssertEqual(retained.first?.state, .revoked)
        }

        func testServerOnlyRowsNeverAccumulateInPrivateRecordsAndEvictionInvalidatesCheckpoint() async throws {
            let history = try store()
            let session = DeviceSession()
            try retain(history, session: session)
            let template = try JSONSerialization.jsonObject(with: Data(row.utf8)) as! [String: Any]
            for batch in 0..<21 {
                let resources: [[String: Any]] = (0..<100).map { offset in
                    var resource = template
                    let number = batch * 100 + offset
                    resource["id"] = String(format: "00000000-0000-4000-8000-%012x", number)
                    resource["created_at"] = ISO8601DateFormatter().string(
                        from: Date(timeIntervalSince1970: 1_759_658_400 + Double(number)))
                    return resource
                }
                let data = try JSONSerialization.data(withJSONObject: [
                    "paginated": true, "transfers": resources, "slots": [], "next_cursor": NSNull(),
                    "sync_cursor": "one", "generation": generation,
                ])
                let page = try JSONDecoder().decode(ResourceList.self, from: data)
                try history.cacheServerPage(
                    page, session: session, kind: nil, after: nil, expected: history.historySyncState(session: session))
            }
            let database = try history.readyDatabase()
            XCTAssertNil(try history.historySyncState(session: session))
            XCTAssertNil(try history.cachedServerPage(session: session, kind: nil, after: nil))
            let retained = try database.page(scopes: [session.accountID], kinds: ["transfer", "slot"], limit: 50)
            XCTAssertEqual(
                retained.records.count, 1, "Only explicit private records may be retained; 2100 server-only summaries remain disposable")
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            let record = try decoder.decode(TransferRecord.self, from: retained.records[0].body)
            XCTAssertEqual(record.customTitle, "Retained device label")
            XCTAssertTrue(record.shareURL?.hasSuffix("#private-key") == true)
            try history.clearServerMetadata(session: session)
            XCTAssertNotNil(try database.read(record.localID))
        }

        func testEnvelopeRevisionMismatchTriggersDisposableCacheRecovery() async throws {
            let history = try store()
            let session = DeviceSession()
            try history.cacheServerPage(snapshot(), session: session, kind: nil, after: nil, expected: nil)
            let state = try XCTUnwrap(history.historySyncState(session: session))
            try history.readyDatabase().applyServerFacts(
                scope: session.accountID, expected: state, state: state,
                facts: [
                    .init(
                        identity: id + "|transfer", kind: "transfer", revision: 999, created: 100,
                        body: JSONEncoder().encode(snapshot().transfers[0]))
                ])
            XCTAssertThrowsError(try history.cachedServerPage(session: session, kind: nil, after: nil)) { error in
                guard case HistorySync.Failure.invalidCache = error else { return XCTFail("Expected typed corrupt-cache recovery") }
            }
            XCTAssertThrowsError(try history.cacheServerPage(snapshot(), session: session, kind: nil, after: nil, expected: state))
            XCTAssertEqual(try history.historySyncState(session: session), state)
        }

        func testMetadataOnlyDetailsRefreshWithoutRetainingServerMirror() async throws {
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            let record = try XCTUnwrap(history.projectedResourcePage(snapshot(), session: session, local: []).first)
            let id = self.id
            AccountHTTP.handler = { _ in
                Data(
                    "{\"id\":\"\(id)\",\"status\":\"complete\",\"title\":\"Fresh detail\",\"file_count\":1,\"download_count\":1,\"downloaded_at\":\"2026-10-05T10:01:00Z\",\"max_downloads\":0}"
                        .utf8)
            }
            let refreshed = try await history.refreshSend(record, session: session)
            XCTAssertEqual(refreshed.sharedTitle, "Fresh detail")
            XCTAssertEqual(refreshed.state, .downloaded)
            XCTAssertNil(try history.record(record.localID))
        }

        func testCompletedRetryAfterCannotBeBypassedByManualMutationOrRebuiltView() async throws {
            let history = try store()
            var session = DeviceSession()
            session.userID = "66666666-6666-4666-8666-666666666666"
            SecretStore.session = session
            try history.cacheServerPage(snapshot(), session: session, kind: nil, after: nil, expected: nil)
            actor Requests {
                var paths: [String] = []
                func fetch(_ path: String) throws -> Data {
                    paths.append(path)
                    if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                    throw HistorySync.Failure.retryAfter(120)
                }
                func count() -> Int { paths.count }
            }
            let requests = Requests()
            AccountHTTP.handler = { path in try await requests.fetch(path) }
            let model = HistoryPageViewModel()
            let first = await model.refresh(history: history, session: session)
            XCTAssertFalse(first)
            XCTAssertGreaterThan(model.retryAfter, 119)
            _ = await model.refresh(history: history, session: session)
            model.cancel()
            _ = await model.refresh(history: history, session: session)
            let rebuilt = HistoryPageViewModel()
            _ = await rebuilt.refresh(history: history, session: session)
            let count = await requests.count()
            XCTAssertEqual(count, 2, "Only original configuration/feed requests may run during server cooldown")
            XCTAssertEqual(rebuilt.records.first?.sharedTitle, "Original")
            SecretStore.session = nil
            _ = await rebuilt.refresh(history: history, session: session)
            XCTAssertTrue(rebuilt.records.isEmpty, "A retained cooldown must not expose rows after session rejection")
        }

        func testRoutineDeadlineMovesAfterManualMutationAndFilterRefreshCompletion() async throws {
            final class Clock { var date = Date(timeIntervalSince1970: 1000) }
            let clock = Clock()
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            let page = try snapshot()
            let data = try JSONEncoder().encode(page)
            let generation = self.generation
            try history.cacheServerPage(page, session: session, kind: nil, after: nil, expected: nil)
            AccountHTTP.handler = { path in
                if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                if path.hasPrefix("auth/resources") { return data }
                return Data(
                    "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[],\"next_cursor\":\"one\",\"has_more\":false}".utf8)
            }
            let model = HistoryPageViewModel(now: { clock.date })
            _ = await model.refresh(history: history, session: session)
            XCTAssertEqual(model.routineDelay(), 10, accuracy: 0.001)
            let originalSchedule = model.scheduleID
            // Manual refresh finishes eight seconds into the old routine sleep.
            clock.date = Date(timeIntervalSince1970: 1008)
            _ = await model.refresh(history: history, session: session)
            XCTAssertNotEqual(model.scheduleID, originalSchedule)
            clock.date = Date(timeIntervalSince1970: 1010)
            XCTAssertEqual(model.routineDelay(), 8, accuracy: 0.001)
            // The mutation callback uses the same coalesced entry point.
            clock.date = Date(timeIntervalSince1970: 1016)
            _ = await model.refresh(history: history, session: session)
            XCTAssertEqual(model.nextRefreshAt, Date(timeIntervalSince1970: 1026))
            clock.date = Date(timeIntervalSince1970: 1024)
            model.setFilter(.sent)
            _ = await model.refresh(history: history, session: session)
            XCTAssertEqual(model.nextRefreshAt, Date(timeIntervalSince1970: 1034))
            model.cancel()
            XCTAssertFalse(model.loading)
        }

        func testFailedMutationSpanningRoutineDeadlineDoesNotStopPolling() async throws {
            final class Clock { var date = Date(timeIntervalSince1970: 1000) }
            actor FailedMutation {
                var continuation: CheckedContinuation<Void, Error>?
                func run() async throws {
                    try await withCheckedThrowingContinuation { continuation = $0 }
                }
                func pending() -> Bool { continuation != nil }
                func fail() {
                    continuation?.resume(throwing: URLError(.cannotConnectToHost))
                    continuation = nil
                }
            }
            actor Requests {
                var feedCount = 0
                let reply: Data
                init(generation: String) {
                    reply = Data(
                        "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[],\"next_cursor\":\"one\",\"has_more\":false}".utf8)
                }
                func fetch(_ path: String) -> Data {
                    if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                    feedCount += 1
                    return reply
                }
                func count() -> Int { feedCount }
            }
            let clock = Clock()
            let history = try store()
            let session = DeviceSession()
            SecretStore.session = session
            try history.cacheServerPage(snapshot(), session: session, kind: nil, after: nil, expected: nil)
            let requests = Requests(generation: generation)
            AccountHTTP.handler = { path in await requests.fetch(path) }
            let model = HistoryPageViewModel(now: { clock.date })
            _ = await model.refresh(history: history, session: session)
            clock.date = Date(timeIntervalSince1970: 1008)
            let mutation = FailedMutation()
            let operation = Task { try await mutation.run() }
            while !(await mutation.pending()) { await Task.yield() }
            // The production view routine goes directly to this coalescing entry
            // point despite its rename/revoke busy state; source gates verify wiring.
            clock.date = Date(timeIntervalSince1970: 1010)
            XCTAssertEqual(model.routineDelay(), 0)
            _ = await model.refresh(history: history, session: session)
            XCTAssertEqual(model.nextRefreshAt, Date(timeIntervalSince1970: 1020))
            let scheduled = model.scheduleID
            await mutation.fail()
            do {
                try await operation.value
                XCTFail("Mutation should fail without publishing a successful-mutation refresh")
            } catch {}
            XCTAssertEqual(model.scheduleID, scheduled)
            clock.date = Date(timeIntervalSince1970: 1020)
            XCTAssertEqual(model.routineDelay(), 0)
            _ = await model.refresh(history: history, session: session)
            let count = await requests.count()
            XCTAssertEqual(count, 3)
            XCTAssertEqual(model.nextRefreshAt, Date(timeIntervalSince1970: 1030))
        }

        func testCooldownOverflowIsFiniteAndPreservesRestrictionsAfterReconstruction() async {
            let now = Date(timeIntervalSince1970: 1000)
            var cooldowns = HistoryCooldowns(capacity: 2)
            for (scope, seconds) in [("A", 120.0), ("B", 180.0), ("C", 240.0), ("D", 300.0)] {
                cooldowns.record(scope: scope, until: now.addingTimeInterval(seconds), now: now)
            }
            XCTAssertEqual(cooldowns.deadlines.count, 2)
            XCTAssertEqual(cooldowns.deadline(for: "A", now: now), now.addingTimeInterval(120))
            XCTAssertEqual(cooldowns.deadline(for: "B", now: now), now.addingTimeInterval(180))
            var reconstructed = cooldowns
            XCTAssertEqual(reconstructed.deadline(for: "C", now: now), now.addingTimeInterval(300))
            XCTAssertEqual(reconstructed.deadline(for: "D", now: now), now.addingTimeInterval(300))
            XCTAssertLessThanOrEqual(reconstructed.deadlines.count, 2)
            XCTAssertNil(reconstructed.deadline(for: "C", now: now.addingTimeInterval(301)))
        }

        func testEqualTimestampMixedKindArrivalsPreserveDescendingOrderAcrossNextPage() async throws {
            let history = try store()
            let session = DeviceSession()
            let generation = self.generation
            SecretStore.session = session
            let template = try JSONSerialization.jsonObject(with: Data(row.utf8)) as! [String: Any]
            func transfer(_ number: Int) -> [String: Any] {
                var row = template
                row["id"] = String(format: "00000000-0000-4000-8000-%012x", number)
                row["history_after"] = "anchor-\(number)-transfer"
                row["history_after_kind"] = "sent-\(number)"
                return row
            }
            func slot(_ number: Int) -> [String: Any] {
                [
                    "id": String(format: "00000000-0000-4000-8000-%012x", number), "revision": 1,
                    "status": "waiting", "created_at": "2026-10-05T10:00:00Z", "receive_protocol": 2,
                    "recipient_public_key": String(repeating: "A", count: 43), "file_count": 0, "completed_files": 0, "total_size": 0,
                    "max_files": 0, "reserved_files": 0, "remaining_files": NSNull(),
                    "history_after": "anchor-\(number)-slot", "history_after_kind": "received-\(number)",
                    "summary": ["state": "ready", "file_count": 0, "completed_files": 0, "total_size": 0],
                ]
            }
            let initial = try JSONDecoder().decode(
                ResourceList.self,
                from: JSONSerialization.data(withJSONObject: [
                    "paginated": true, "transfers": (1...20).map(transfer), "slots": (1...20).map(slot),
                    "next_cursor": NSNull(), "sync_cursor": "one", "generation": generation,
                ]))
            try history.cacheServerPage(initial, session: session, kind: nil, after: nil, expected: nil)
            let changes: [[String: Any]] = (21...31).map { number in
                let row = transfer(number)
                return ["kind": "transfer", "id": row["id"]!, "revision": 1, "action": "upsert", "resource": row]
            }
            let data = try JSONSerialization.data(withJSONObject: [
                "version": 1, "generation": generation,
                "changes": changes, "next_cursor": "two", "has_more": false,
            ])
            try history.applyHistoryChanges(
                JSONDecoder().decode(HistorySync.Batch.self, from: data), session: session,
                expected: .init(generation: generation, cursor: "one"))
            let newest = try XCTUnwrap(history.cachedServerPage(session: session, kind: nil, after: nil))
            XCTAssertEqual(newest.1.count, 50)
            XCTAssertEqual(newest.1.first?.id, transfer(31)["id"] as? String)
            XCTAssertEqual(newest.1.last?.id, transfer(1)["id"] as? String)
            XCTAssertFalse(newest.1.last?.isSlot == true)
            XCTAssertEqual(newest.0.next, "anchor-1-transfer")
            let lastPage = try JSONSerialization.data(withJSONObject: [
                "paginated": true, "transfers": [], "slots": [slot(1)],
                "next_cursor": NSNull(), "sync_cursor": "two", "generation": generation,
            ])
            AccountHTTP.handler = { path in
                if path == "config" { return Data(#"{"history_sync_version":1}"#.utf8) }
                if path.hasPrefix("auth/resources") {
                    guard path.contains("after=anchor-1-transfer") else { throw AccountError.request }
                    return lastPage
                }
                return Data(
                    "{\"version\":1,\"generation\":\"\(generation)\",\"changes\":[],\"next_cursor\":\"two\",\"has_more\":false}".utf8)
            }
            let model = HistoryPageViewModel()
            _ = await model.refresh(history: history, session: session)
            let firstIDs = Set(model.records.map { $0.id + ($0.isSlot == true ? "|slot" : "|transfer") })
            await model.next(history: history, session: session)
            XCTAssertEqual(model.records.count, 1)
            XCTAssertTrue(model.records.first?.isSlot == true)
            let remaining = Set(model.records.map { $0.id + ($0.isSlot == true ? "|slot" : "|transfer") })
            XCTAssertTrue(firstIDs.isDisjoint(with: remaining))
            XCTAssertEqual(firstIDs.union(remaining).count, 51)
        }
    }
#endif

import Foundation

@MainActor
extension TransferHistoryStore {
    struct Coverage: Codable {
        let identities: [String]
        let next: String?
        let generation: String?
        var navigationStale: Bool? = nil
    }

    func historySyncState(session: DeviceSession) throws -> HistoryRecordDatabase.SyncState? {
        let state = try readyDatabase().syncState(scope: session.accountID)
        if let state {
            guard UUID(uuidString: state.generation) != nil, HistorySnapshot.validCursor(state.cursor)
            else { throw HistorySync.Failure.invalidCache }
        }
        return state
    }

    func clearServerMetadata(session: DeviceSession) throws {
        try readyDatabase().clearServerMetadata(scope: session.accountID)
        advanceRevision()
    }

    private func coverageKey(kind: String?, after: String?) -> String {
        (kind ?? "all") + "|" + (after ?? "first")
    }

    func cachedServerPage(session: DeviceSession, kind: String?, after: String?) throws -> (
        Coverage, [TransferRecord]
    )? {
        let database = try readyDatabase()
        return try database.transaction { database in
            guard
                let data = try database.serverCoverage(
                    scope: session.accountID, page: coverageKey(kind: kind, after: after)
                )
            else { return nil }
            let coverage: Coverage
            do { coverage = try JSONDecoder().decode(Coverage.self, from: data) } catch {
                throw HistorySync.Failure.invalidCache
            }
            guard coverage.identities.count <= 100,
                  Set(coverage.identities).count == coverage.identities.count,
                  HistorySnapshot.validCursor(coverage.next),
                  coverage.generation == nil || UUID(uuidString: coverage.generation!) != nil
            else { throw HistorySync.Failure.invalidCache }
            var facts: [HistoryRecordDatabase.ServerFact] = []
            for identity in coverage.identities {
                guard let fact = try database.serverFact(scope: session.accountID, identity: identity) else {
                    // Eviction must not turn an incomplete page into an empty authoritative one.
                    return nil
                }
                facts.append(fact)
            }
            let page = try decodedServerFacts(facts)
            let prefix = "resource|" + session.serverURL + "|" + session.userID + "|"
            let local = try records(ids: page.identities.map { prefix + $0 }, session: session)
            let rows = projectedResourcePage(page, session: session, local: local).sorted {
                $0.createdAt == $1.createdAt ? $0.localID > $1.localID : $0.createdAt > $1.createdAt
            }
            return (coverage, rows)
        }
    }

    func cacheServerPage(
        _ page: ResourceList, session: DeviceSession, kind: String?, after: String?,
        expected: HistoryRecordDatabase.SyncState?, reset: Bool = false
    ) throws {
        let database = try readyDatabase()
        let state = page.generation.flatMap { generation in
            page.sync_cursor.map { HistoryRecordDatabase.SyncState(generation: generation, cursor: $0) }
        }
        guard
            page.generation == nil
            || (page.transfers.allSatisfy { $0.revision != nil }
                && page.slots.allSatisfy { $0.revision != nil })
        else { throw HistorySync.Failure.invalidBatch }
        if let expected, let generation = page.generation, expected.generation != generation, !reset {
            throw HistorySync.Failure.resetRequired
        }
        // An older page contributes rows/coverage, never replaces the account's newer feed cursor.
        let nextState = expected == nil || reset ? state : expected
        let facts = try serverFacts(page)
        let accepted = try facts.filter { fact in
            guard !reset,
                  let prior = try database.serverFact(scope: session.accountID, identity: fact.identity)
            else { return true }
            _ = try decodedServerFacts([prior])
            return prior.revision < fact.revision || (prior.revision == fact.revision && prior.body != nil)
        }
        let acceptedIDs = Set(accepted.map(\.identity))
        let safe = ResourceList(
            transfers: page.transfers.filter { acceptedIDs.contains($0.id + "|transfer") },
            slots: page.slots.filter { acceptedIDs.contains($0.id + "|slot") }
        )
        let coverage = Coverage(
            identities: page.transfers.map { $0.id + "|transfer" } + page.slots.map { $0.id + "|slot" },
            next: page.next_cursor, generation: page.generation
        )
        try database.applyServerFacts(
            scope: session.accountID, expected: expected, state: nextState, facts: facts,
            page: coverageKey(kind: kind, after: after), coverage: JSONEncoder().encode(coverage),
            reset: reset
        ) {
            try self.mergeResourcePage(safe, session: session)
        }
        advanceRevision()
    }

    func applyHistoryChanges(
        _ batch: HistorySync.Batch, session: DeviceSession, expected: HistoryRecordDatabase.SyncState
    ) throws {
        try batch.validate(cursor: expected.cursor, generation: expected.generation, limit: 100)
        let database = try readyDatabase()
        for change in batch.changes {
            if let prior = try database.serverFact(scope: session.accountID, identity: change.id + "|" + change.kind) {
                _ = try decodedServerFacts([prior])
            }
        }
        let page = ResourceList(
            transfers: batch.changes.compactMap(\.transfer), slots: batch.changes.compactMap(\.slot)
        )
        var facts = try serverFacts(page)
        for change in batch.changes where change.action == "remove" {
            let identity = change.id + "|" + change.kind
            let created =
                try database.serverFact(scope: session.accountID, identity: identity)?.created
                    ?? Date().timeIntervalSince1970
            facts.append(
                .init(
                    identity: identity, kind: change.kind, revision: change.revision, created: created,
                    body: nil
                )
            )
        }
        let next = HistoryRecordDatabase.SyncState(
            generation: batch.generation, cursor: batch.next_cursor
        )
        try database.applyServerFacts(
            scope: session.accountID, expected: expected, state: next, facts: facts
        ) {
            let accepted = try batch.changes.filter { change in
                guard let fact = try database.serverFact(scope: session.accountID, identity: change.id + "|" + change.kind) else {
                    return false
                }
                return fact.revision == change.revision && ((change.action == "remove") == (fact.body == nil))
            }
            try self.mergeResourcePage(
                ResourceList(
                    transfers: accepted.compactMap(\.transfer), slots: accepted.compactMap(\.slot)
                ),
                session: session
            )
            let removed = accepted.filter { $0.action == "remove" }.map {
                "resource|" + session.serverURL + "|" + session.userID + "|" + $0.id + "|" + $0.kind
            }
            if !removed.isEmpty {
                try self.mutate(ids: removed) { records in
                    for index in records.indices {
                        records[index].state = .revoked
                    }
                }
            }
            try self.refreshNewestCoverage(session: session)
        }
        advanceRevision()
    }

    /// A bounded newest window. If arrivals change its membership, navigation
    /// uses the server-issued row anchor (or revalidates old-server snapshots), avoiding gaps.
    func refreshNewestCoverage(session: DeviceSession) throws {
        let database = try readyDatabase()
        try database.transaction { database in
            let state = try database.syncState(scope: session.accountID)
            for kind in [nil, "transfer", "slot"] as [String?] {
                let key = coverageKey(kind: kind, after: nil)
                guard let data = try database.serverCoverage(scope: session.accountID, page: key) else {
                    continue
                }
                let old: Coverage
                do { old = try JSONDecoder().decode(Coverage.self, from: data) } catch {
                    throw HistorySync.Failure.invalidCache
                }
                let facts = try database.newestServerFacts(scope: session.accountID, kind: kind, limit: 51)
                let shown = Array(facts.prefix(50))
                let ids = shown.map(\.identity)
                guard Set(ids) != Set(old.identities) else { continue }
                var anchor: String?
                if let last = shown.last, let body = last.body, facts.count > 50 || old.next != nil {
                    _ = try decodedServerFacts([last])
                    if last.kind == "transfer" {
                        let row: ResourceList.Transfer
                        do { row = try JSONDecoder().decode(ResourceList.Transfer.self, from: body) } catch {
                            throw HistorySync.Failure.invalidCache
                        }
                        anchor = kind == nil ? row.history_after : row.history_after_kind
                    } else {
                        let row: ResourceList.Slot
                        do { row = try JSONDecoder().decode(ResourceList.Slot.self, from: body) } catch {
                            throw HistorySync.Failure.invalidCache
                        }
                        anchor = kind == nil ? row.history_after : row.history_after_kind
                    }
                }
                let needsNext = facts.count > 50 || old.next != nil
                let next = Coverage(
                    identities: ids, next: needsNext ? anchor ?? old.next : nil,
                    generation: old.generation, navigationStale: needsNext && anchor == nil
                )
                try database.applyServerFacts(
                    scope: session.accountID, expected: state, state: state, facts: [], page: key,
                    coverage: JSONEncoder().encode(next)
                )
            }
        }
    }

    private func serverFacts(_ page: ResourceList) throws -> [HistoryRecordDatabase.ServerFact] {
        try page.transfers.map {
            try .init(
                identity: $0.id + "|transfer", kind: "transfer", revision: $0.revision ?? 0,
                created: ServerTimestamp.parse($0.created_at)?.timeIntervalSince1970 ?? 0,
                body: JSONEncoder().encode($0)
            )
        }
            + page.slots.map {
                try .init(
                    identity: $0.id + "|slot", kind: "slot", revision: $0.revision ?? 0,
                    created: ServerTimestamp.parse($0.created_at)?.timeIntervalSince1970 ?? 0,
                    body: JSONEncoder().encode($0)
                )
            }
    }

    private func decodedServerFacts(_ facts: [HistoryRecordDatabase.ServerFact]) throws -> ResourceList {
        var transfers: [ResourceList.Transfer] = []
        var slots: [ResourceList.Slot] = []
        do {
            for fact in facts {
                guard ["transfer", "slot"].contains(fact.kind), (0 ... 9_007_199_254_740_991).contains(fact.revision), fact.created.isFinite
                else {
                    throw HistorySync.Failure.invalidCache
                }
                guard let body = fact.body else { continue }
                if fact.kind == "transfer" {
                    let row = try JSONDecoder().decode(ResourceList.Transfer.self, from: body)
                    guard row.id + "|transfer" == fact.identity, (row.revision ?? 0) == fact.revision else {
                        throw HistorySync.Failure.invalidCache
                    }
                    transfers.append(row)
                } else {
                    let row = try JSONDecoder().decode(ResourceList.Slot.self, from: body)
                    guard row.id + "|slot" == fact.identity, (row.revision ?? 0) == fact.revision else {
                        throw HistorySync.Failure.invalidCache
                    }
                    slots.append(row)
                }
            }
        } catch { throw HistorySync.Failure.invalidCache }
        return ResourceList(transfers: transfers, slots: slots)
    }
}

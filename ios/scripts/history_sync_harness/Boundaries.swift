import Foundation
import Observation

struct DeviceSession: Equatable, Sendable {
    var serverURL = "https://server.example"
    var userID = "11111111-1111-4111-8111-111111111111"
    var token = "private-token"
    var canTransfer = true
    var accountID: String { serverURL + "|" + userID }
}
enum SecretStore {
    static var session: DeviceSession?
    static func read(_ key: String) -> Data? { nil }
    static func write(_ data: Data, name: String) throws {}
}
enum AccountError: Error { case storage, request, changed, unavailable }
enum TransferIncident: Error {
    case revoked
    static func from(_ error: Error) -> Self? { nil }
}
enum AccountHTTP {
    static var handler: @Sendable (String) async throws -> Data = { _ in throw AccountError.request }
    static func request(
        server: String, path: String, token: String? = nil, maximumBytes: Int = 1_048_576,
        timeout: Double = 10
    ) async throws -> Data {
        try await handler(path)
    }
}
@MainActor
final class TransferHistoryStore {
    private let database: HistoryRecordDatabase
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    var revision = 0
    init(url: URL) throws {
        database = try HistoryRecordDatabase(url: url)
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601
    }
    func readyDatabase() throws -> HistoryRecordDatabase { database }
    func records(ids: [String], session: DeviceSession) throws -> [TransferRecord] {
        try ids.compactMap { id in
            guard let row = try database.read(id) else { return nil }
            let record = try decoder.decode(TransferRecord.self, from: row.body)
            return record.belongs(to: session) ? record : nil
        }
    }
    func record(_ id: String) throws -> TransferRecord? {
        try database.read(id).map { try decoder.decode(TransferRecord.self, from: $0.body) }
    }
    func mutate(ids: [String], _ change: (inout [TransferRecord]) -> Void) throws {
        try database.transaction { database in
            var records = try ids.compactMap { id in
                try database.read(id).map { try decoder.decode(TransferRecord.self, from: $0.body) }
            }
            change(&records)
            for record in records {
                try database.write(
                    .init(
                        id: record.localID, scope: record.serverURL! + "|" + record.ownerID!,
                        kind: record.isSlot == true ? "slot" : "transfer",
                        created: record.createdAt.timeIntervalSince1970, body: encoder.encode(record)))
            }
        }
    }
}

enum HistoryFilter { case all, sent, receive, downloaded }

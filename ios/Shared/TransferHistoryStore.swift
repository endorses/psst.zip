import Foundation

/// Each mutation rereads the shared file under a coordinator so the extension cannot overwrite main-app history.
@Observable
@MainActor
final class TransferHistoryStore {
    private(set) var records: [TransferRecord] = []
    private let defaults: UserDefaults
    private let fileURL: URL?
    init(defaults: UserDefaults = AppConstants.sharedDefaults,
         fileURL: URL? = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: AppConstants.appGroupIdentifier)?.appendingPathComponent("transferHistory-v2.json"))
    {
        self.defaults = defaults
        self.fileURL = fileURL
        // Preserve the pre-account server for old received records that never stored their own URL.
        if defaults.string(forKey: "legacyHistoryServerURL") == nil,
           defaults.data(forKey: AppConstants.transferHistoryKey) != nil,
           let original = defaults.string(forKey: AppConstants.serverURLKey)
        {
            defaults.set(original, forKey: "legacyHistoryServerURL")
        }
        reload()
    }

    private func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    private func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }

    private func read() throws -> [TransferRecord] {
        let data: Data? = if let fileURL, FileManager.default.fileExists(atPath: fileURL.path) {
            try Data(contentsOf: fileURL)
        } else {
            defaults.data(forKey: AppConstants.transferHistoryKey)
        }
        guard let data else { return [] }
        var values = try decoder().decode([TransferRecord].self, from: data)
        for index in values.indices where values[index].ownerID == nil && values[index].serverURL == nil {
            if var origin = URLComponents(string: values[index].shareURL ?? "") {
                origin.path = ""
                origin.query = nil
                origin.fragment = nil
                values[index].serverURL = try? AccountHTTP.origin(origin.string ?? "")
            }
            if values[index].serverURL == nil,
               let original = defaults.string(forKey: "legacyHistoryServerURL")
            {
                values[index].serverURL = try? AccountHTTP.origin(original)
            }
        }
        return values
    }

    func reload() {
        // Keep the last successful read during a transient filesystem failure; writes always throw.
        if let values = try? read() {
            records = values
        }
    }

    func visible(for session: DeviceSession?) -> [TransferRecord] {
        guard let session else { return [] }
        return records.filter { $0.canManage(as: session) }.sorted { $0.createdAt > $1.createdAt }
    }

    var legacyCount: Int {
        records.filter { $0.ownerID == nil }.count
    }

    func add(_ record: TransferRecord) throws {
        try update(record)
    }

    func update(_ record: TransferRecord) throws {
        try mutate { values in
            // A polling refresh or in-flight receive checkpoint must not erase a concurrent rename.
            let next = record.preservingLocalName(from: values.first(where: { $0.localID == record.localID }))
            values.removeAll { $0.localID == record.localID }
            values.insert(next, at: 0)
        }
    }

    func rename(_ record: TransferRecord, name: String, session: DeviceSession) throws {
        guard record.belongs(to: session), session.canTransfer, SecretStore.session?.accountID == session.accountID else { throw AccountError.changed }
        let normalized = name.trimmingCharacters(in: .whitespacesAndNewlines)
        try mutate { values in
            guard let index = values.firstIndex(where: { $0.localID == record.localID }) else { return }
            values[index].customTitle = normalized.isEmpty ? nil : String(normalized.prefix(200))
        }
    }

    func remove(_ record: TransferRecord) throws {
        try mutate { $0.removeAll { $0.localID == record.localID } }
        SecretStore.remove(record.vaultID)
    }

    private func mutate(_ change: (inout [TransferRecord]) -> Void) throws {
        guard let fileURL else { throw AccountError.storage }
        let coordinator = NSFileCoordinator()
        var coordinationError: NSError?
        var writeError: Error?
        coordinator.coordinate(writingItemAt: fileURL, options: .forMerging, error: &coordinationError) { url in
            do {
                var values = try read()
                change(&values)
                try encoder().encode(values).write(to: url, options: [.atomic, .completeFileProtection])
                records = values
                defaults.removeObject(forKey: AppConstants.transferHistoryKey)
            } catch { writeError = error }
        }
        if let coordinationError {
            throw coordinationError
        }
        if let writeError {
            throw writeError
        }
    }

    func revoke(_ record: TransferRecord, session: DeviceSession) async throws {
        guard record.canManage(as: session), SecretStore.session == session else { throw AccountError.changed }
        let path = (record.isSlot == true ? "slots/" : "transfers/") + record.id
        _ = try await AccountHTTP.request(server: session.serverURL, path: path, method: "DELETE",
                                          token: record.capabilities?.deletionToken ?? session.token)
        guard SecretStore.session == session else { throw AccountError.changed }
        try remove(record)
    }
}

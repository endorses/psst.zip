import Foundation

/// Persists transfer history in App Group UserDefaults.
@Observable
final class TransferHistoryStore {
    private(set) var records: [TransferRecord] = []

    init() {
        load()
    }

    func add(_ record: TransferRecord) {
        records.insert(record, at: 0)
        save()
    }

    func update(_ record: TransferRecord) {
        if let index = records.firstIndex(where: { $0.id == record.id }) {
            records[index] = record
            save()
        }
    }

    func remove(at offsets: IndexSet) {
        records.remove(atOffsets: offsets)
        save()
    }

    func removeAll() {
        records.removeAll()
        save()
    }

    // MARK: - Persistence

    private func save() {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        if let data = try? encoder.encode(records) {
            AppConstants.sharedDefaults.set(data, forKey: AppConstants.transferHistoryKey)
        }
    }

    private func load() {
        guard let data = AppConstants.sharedDefaults.data(forKey: AppConstants.transferHistoryKey) else {
            return
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        if let decoded = try? decoder.decode([TransferRecord].self, from: data) {
            records = decoded
        }
    }
}

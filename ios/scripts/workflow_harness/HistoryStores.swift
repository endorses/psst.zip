import Foundation

// Indexed storage boundaries only; compile the actual merge view model and row identities.
enum HistoryRecordDatabase { typealias Cursor = Int }
struct GuestDownload {
    let id: String
    let createdAt: Date
}
final class TransferHistoryStore {
    var rows: [TransferRecord] = []
    var calls: [[String]] = []
    var fail = false
    struct Page {
        let records: [TransferRecord]
        let next: Int?
    }
    func page(session: DeviceSession, kinds: [String], after: Int?) throws -> Page {
        calls.append(kinds)
        if fail { throw URLError(.cannotOpenFile) }
        let filtered = rows.filter { $0.belongs(to: session) && kinds.contains($0.isSlot == true ? "slot" : "transfer") }
            .sorted { $0.createdAt == $1.createdAt ? $0.localID < $1.localID : $0.createdAt > $1.createdAt }
        let start = after ?? 0
        let end = min(start + 50, filtered.count)
        return Page(records: Array(filtered[start..<end]), next: end < filtered.count ? end : nil)
    }
    func record(_ id: String) throws -> TransferRecord? {
        if fail { throw URLError(.cannotOpenFile) }
        return rows.first { $0.localID == id }
    }
}
final class GuestDownloadStore {
    var rows: [GuestDownload] = []
    var calls = 0
    var fail = false
    struct Page {
        let records: [GuestDownload]
        let next: Int?
    }
    func page(after: Int?) throws -> Page {
        calls += 1
        if fail { throw URLError(.cannotOpenFile) }
        let sorted = rows.sorted { $0.createdAt == $1.createdAt ? $0.id < $1.id : $0.createdAt > $1.createdAt }
        let start = after ?? 0
        let end = min(start + 50, sorted.count)
        return Page(records: Array(sorted[start..<end]), next: end < sorted.count ? end : nil)
    }
    func find(_ id: String) throws -> GuestDownload? {
        if fail { throw URLError(.cannotOpenFile) }
        return rows.first { $0.id == id }
    }
}

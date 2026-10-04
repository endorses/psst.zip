import Foundation

enum HistoryFilter: String, CaseIterable { case all = "All", sent = "Sent", receive = "Receive links", downloaded = "Downloaded" }

enum HistoryEntry: Identifiable {
    case account(TransferRecord), downloaded(GuestDownload)
    var id: String {
        switch self {
        case let .account(record): "account|\(record.isSlot == true ? "slot" : "send")|\(record.vaultID)"
        case let .downloaded(record): "download|\(record.id)"
        }
    }

    var date: Date {
        switch self { case let .account(record): record.createdAt; case let .downloaded(record): record.createdAt }
    }

    static func combine(account: [TransferRecord], downloads: [GuestDownload], session: DeviceSession?, filter: HistoryFilter) -> [HistoryEntry] {
        let owned = account.filter { record in session.map { record.canManage(as: $0) } ?? false }
        let entries = owned.map(Self.account) + downloads.map(Self.downloaded)
        return entries.filter {
            switch ($0, filter) {
            case (_, .all), (.downloaded, .downloaded): true
            case let (.account(record), .sent): record.isSlot != true
            case let (.account(record), .receive): record.isSlot == true
            default: false
            }
        }.sorted { $0.date == $1.date ? $0.id < $1.id : $0.date > $1.date }
    }
}

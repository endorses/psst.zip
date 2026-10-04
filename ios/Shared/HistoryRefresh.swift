import Foundation

@MainActor
extension TransferHistoryStore {
    func refresh(session: DeviceSession) async throws {
        let list = try await HistorySnapshot.load { path in
            try await AccountHTTP.request(server: session.serverURL, path: path, token: session.token)
        }
        guard SecretStore.session == session else { throw AccountError.changed }
        reload()
        let old = visible(for: session)
        let previous = Dictionary(old.map { ($0.localID, $0) }, uniquingKeysWith: { first, _ in first })
        var refreshed: [TransferRecord] = []
        func date(_ raw: String?) -> Date? {
            ServerTimestamp.parse(raw)
        }
        for transfer in list.transfers where UUID(uuidString: transfer.id) != nil {
            let localID = "resource|" + session.serverURL + "|" + session.userID + "|" + transfer.id + "|transfer"
            var record = previous[localID] ?? TransferRecord(id: transfer.id, direction: .sent, state: .inProgress,
                                                             createdAt: date(transfer.created_at) ?? Date(), expiresAt: date(transfer.expires_at), fileCount: 0, totalSize: 0, shareURL: nil,
                                                             serverURL: session.serverURL, ownerID: session.userID, isSlot: false)
            if transfer.status == "expired" {
                record.state = .expired
            } else if transfer.status == "revoked" {
                record.state = .revoked
            } else if transfer.downloaded_at != nil {
                record.state = .downloaded
            } else if (transfer.download_count ?? 0) > 0 {
                record.state = .started
            } else if transfer.status == "complete" {
                record.state = .complete
            }
            // An incomplete upload remains actionable after cancellation.
            record.fileCount = max(record.fileCount, transfer.file_count)
            record.totalSize = max(record.totalSize, transfer.total_size)
            record.maxDownloads = transfer.max_downloads
            refreshed.append(record)
        }
        for slot in list.slots where UUID(uuidString: slot.id) != nil {
            let localID = "resource|" + session.serverURL + "|" + session.userID + "|" + slot.id + "|slot"
            var record = previous[localID] ?? TransferRecord(id: slot.id, direction: .received, state: .inProgress,
                                                             createdAt: date(slot.created_at) ?? Date(), expiresAt: date(slot.expires_at), fileCount: 0, totalSize: 0, shareURL: nil,
                                                             serverURL: session.serverURL, ownerID: session.userID, isSlot: true)
            record.receiveProtocol = slot.receive_protocol
            record.maxFiles = slot.max_files
            record.reservedFiles = slot.reserved_files
            record.fileCount = slot.completed_files
            record.totalSize = slot.total_size
            // Compact summaries do not identify each saved child. Keep local paths,
            // but only detailed inbox refresh can confirm all current arrivals saved.
            record.state = slot.status == "expired" ? .expired : slot.status == "revoked" ? .revoked : slot.completed_files == 0 ? .inProgress : .complete
            refreshed.append(record)
        }
        let ids = Set(list.transfers.map { $0.id + "|transfer" } + list.slots.map { $0.id + "|slot" })
        for var record in old where record.ownerID != nil && !ids.contains(record.id + (record.isSlot == true ? "|slot" : "|transfer")) {
            record.state = record.isExpired ? .expired : .revoked
            refreshed.append(record)
        }
        try applySnapshot(refreshed)
    }
}

import Foundation

struct ResourceList: Decodable {
    struct Transfer: Decodable {
        let id: String
        let status: String
        let file_count: Int
        let total_size: Int64
        let expires_at: String?
        let created_at: String?
        let downloaded_at: String?
        let download_count: Int?
    }

    struct Slot: Decodable {
        struct Child: Decodable { let transfer_id: String
            let status: String
            let file_count: Int
        }

        let id: String
        let status: String
        let expires_at: String?
        let created_at: String?
        let transfers: [Child]
    }

    let transfers: [Transfer]
    let slots: [Slot]
}

@MainActor
extension TransferHistoryStore {
    func refresh(session: DeviceSession) async throws {
        let data = try await AccountHTTP.request(server: session.serverURL, path: "auth/resources", token: session.token)
        guard SecretStore.session == session else { throw AccountError.changed }
        let list = try JSONDecoder().decode(ResourceList.self, from: data)
        reload()
        let old = visible(for: session)
        func date(_ raw: String?) -> Date? {
            ServerTimestamp.parse(raw)
        }
        for transfer in list.transfers where UUID(uuidString: transfer.id) != nil {
            var record = old.first { $0.id == transfer.id } ?? TransferRecord(id: transfer.id, direction: .sent, state: .inProgress,
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
            try update(record)
        }
        for slot in list.slots where UUID(uuidString: slot.id) != nil {
            var record = old.first { $0.id == slot.id } ?? TransferRecord(id: slot.id, direction: .received, state: .inProgress,
                                                                          createdAt: date(slot.created_at) ?? Date(), expiresAt: date(slot.expires_at), fileCount: 0, totalSize: 0, shareURL: nil,
                                                                          serverURL: session.serverURL, ownerID: session.userID, isSlot: true)
            let complete = slot.transfers.filter { $0.status == "complete" }
            record.fileCount = complete.reduce(0) { $0 + $1.file_count }
            record.state = slot.status == "expired" ? .expired : slot.status == "revoked" ? .revoked : complete.isEmpty ? .inProgress :
                complete.allSatisfy { (record.savedTransfers ?? []).contains($0.transfer_id) } ? .saved : .complete
            try update(record)
        }
        let ids = Set(list.transfers.map(\.id) + list.slots.map(\.id))
        for var record in old where record.ownerID != nil && !ids.contains(record.id) {
            record.state = record.isExpired ? .expired : .revoked
            try update(record)
        }
    }
}

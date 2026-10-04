import Foundation

@MainActor
extension TransferHistoryStore {
    /// Apply only returned identities under the same coordinated write that reads
    /// current local checkpoints. Unloaded identities are never inferred revoked.
    func mergeResourcePage(_ list: ResourceList, session: DeviceSession) throws {
        try mutate { values in
            var positions = Dictionary(values.enumerated().map { ($0.element.localID, $0.offset) }, uniquingKeysWith: { first, _ in first })
            func base(_ id: String, slot: Bool, created: String?, expires: String?) -> TransferRecord {
                let key = "resource|" + session.serverURL + "|" + session.userID + "|" + id + (slot ? "|slot" : "|transfer")
                if let index = positions[key] { return values[index] }
                return TransferRecord(
                    id: id, direction: slot ? .received : .sent, state: .inProgress,
                    createdAt: ServerTimestamp.parse(created) ?? Date(), expiresAt: ServerTimestamp.parse(expires), fileCount: 0, totalSize: 0,
                    shareURL: nil, serverURL: session.serverURL, ownerID: session.userID, isSlot: slot)
            }
            func save(_ record: TransferRecord) {
                if let index = positions[record.localID] {
                    values[index] = record
                } else {
                    positions[record.localID] = values.count
                    values.append(record)
                }
            }
            for transfer in list.transfers {
                var record = base(transfer.id, slot: false, created: transfer.created_at, expires: transfer.expires_at)
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
                } else if record.state != .failed {
                    record.state = .inProgress
                }
                if let count = transfer.file_count { record.fileCount = Int(clamping: count) }
                // Server byte counts describe ciphertext, not local plaintext size.
                record.serverSummaryKnown = transfer.summary.state == "ready"
                record.expiresAt = ServerTimestamp.parse(transfer.expires_at) ?? record.expiresAt
                record.maxDownloads = transfer.max_downloads
                save(record)
            }
            for slot in list.slots {
                var record = base(slot.id, slot: true, created: slot.created_at, expires: slot.expires_at)
                record.receiveProtocol = slot.receive_protocol
                record.maxFiles = slot.max_files
                record.reservedFiles = slot.reserved_files
                if slot.status == "expired" {
                    record.state = .expired
                } else if slot.status == "revoked" {
                    record.state = .revoked
                } else if let count = slot.completed_files {
                    record.state = count == 0 ? .inProgress : .complete
                }
                // Only detailed inbox evidence can establish that every arrival is saved.
                if let count = slot.completed_files { record.fileCount = Int(clamping: count) }
                record.serverSummaryKnown = slot.summary.state == "ready"
                record.expiresAt = ServerTimestamp.parse(slot.expires_at) ?? record.expiresAt
                save(record)
            }
        }
    }

    /// Refresh one send detail, without enumerating account history or inboxes.
    func refreshSend(_ record: TransferRecord, session: DeviceSession) async throws {
        guard record.belongs(to: session), session.canTransfer, record.isSlot != true, UUID(uuidString: record.id) != nil else { throw AccountError.changed }
        do {
            let data = try await AccountHTTP.request(server: session.serverURL, path: "transfers/" + record.id, token: session.token, maximumBytes: 131_072, timeout: 10)
            struct Status: Decodable {
                let id: String
                let status: String
                let file_count: Int
                let download_count: Int
                let downloaded_at: String?
                let max_downloads: Int
            }
            let status = try JSONDecoder().decode(Status.self, from: data)
            guard status.id == record.id, status.file_count >= 0, status.file_count <= 100, status.download_count >= 0, status.max_downloads >= 0,
                ["pending", "complete", "expired", "revoked"].contains(status.status)
            else { throw AccountError.request }
            guard SecretStore.session == session, !Task.isCancelled else { throw AccountError.changed }
            try mutate { values in
                guard let index = values.firstIndex(where: { $0.localID == record.localID }) else { return }
                values[index].fileCount = status.file_count
                values[index].serverSummaryKnown = true
                values[index].maxDownloads = status.max_downloads
                if status.status == "revoked" {
                    values[index].state = .revoked
                } else if status.status == "expired" {
                    values[index].state = .expired
                } else if status.downloaded_at != nil {
                    values[index].state = .downloaded
                } else if status.download_count > 0 {
                    values[index].state = .started
                } else if status.status == "complete" {
                    values[index].state = .complete
                } else if values[index].state != .failed {
                    values[index].state = .inProgress
                }
            }
        } catch {
            guard SecretStore.session == session, !Task.isCancelled else { throw AccountError.changed }
            let unavailable: Bool
            if case AccountError.unavailable = error { unavailable = true } else { unavailable = TransferIncident.from(error) == .revoked }
            guard unavailable else { throw error }
            try mutate { values in
                guard let index = values.firstIndex(where: { $0.localID == record.localID }) else { return }
                values[index].state = values[index].isExpired ? .expired : .revoked
            }
        }
    }
}

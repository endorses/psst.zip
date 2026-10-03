import Foundation

/// Persistent per-file checkpoint. Successful writes are committed before advancing to another file.
/// A receipt is eligible only after every manifest file has been saved; retry never changes slot identity.
struct ReceiveCheckpoint {
    private(set) var record: TransferRecord
    let fileExists: (String) -> Bool
    var slotID: String {
        record.id
    }

    func needsFile(transferID: String, blobID: String) -> Bool {
        guard let path = record.savedFiles?[transferID + "/" + blobID] else { return true }
        return !fileExists(path)
    }

    mutating func saved(transferID: String, blobID: String, path: String, size: Int64, title: String) {
        let identity = transferID + "/" + blobID
        if record.savedFiles?[identity] == nil {
            record.totalSize += size
        }
        if record.savedFiles == nil {
            record.savedFiles = [:]
        }
        record.savedFiles?[identity] = path
        record.title = record.title ?? title
    }

    func readyToAcknowledge(transferID: String, blobIDs: [String]) -> Bool {
        !blobIDs.isEmpty && blobIDs.allSatisfy { !needsFile(transferID: transferID, blobID: $0) }
    }

    mutating func completed(transferID: String, blobIDs: [String]) -> Bool {
        guard readyToAcknowledge(transferID: transferID, blobIDs: blobIDs) else { return false }
        if !(record.savedTransfers ?? []).contains(transferID) {
            record.savedTransfers = (record.savedTransfers ?? []) + [transferID]
        }
        return true
    }
}

struct JobIdentity {
    let session: DeviceSession
    func accepts(_ current: DeviceSession?, cancelled: Bool) -> Bool {
        !cancelled && current == session
    }

    func mayResume(as current: DeviceSession?) -> Bool {
        current?.accountID == session.accountID
    }
}

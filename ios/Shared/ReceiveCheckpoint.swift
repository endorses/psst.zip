import Foundation

/// One child transfer, capped to the protocol's 100 files. The parent history
/// record never owns a growing collection of paths or completed child IDs.
struct ReceiveCheckpoint {
    let slotID: String
    let transferID: String
    private(set) var paths: [String: String]
    var sizes: [String: Int64] = [:]
    private(set) var complete: Bool
    let fileExists: (String, Int64?) -> Bool

    func needsFile(blobID: String) -> Bool {
        guard let path = paths[blobID] else { return true }
        return !fileExists(path, sizes[blobID])
    }
    func readyToAcknowledge(blobIDs: [String]) -> Bool {
        !blobIDs.isEmpty && blobIDs.count <= 100 && Set(blobIDs).count == blobIDs.count && blobIDs.allSatisfy { !needsFile(blobID: $0) }
    }
    var savedPaths: [String] { paths.compactMap { fileExists($0.value, sizes[$0.key]) ? $0.value : nil } }
    func isSaved(fileCount: Int) -> Bool {
        complete && fileCount > 0 && fileCount <= 100 && paths.count == fileCount && paths.allSatisfy { fileExists($0.value, sizes[$0.key]) }
    }
}

struct JobIdentity {
    let session: DeviceSession
    func accepts(_ current: DeviceSession?, cancelled: Bool) -> Bool { !cancelled && current == session }
    func mayResume(as current: DeviceSession?) -> Bool { current?.accountID == session.accountID }
}

import CryptoKit
import Foundation

@Observable
@MainActor
final class DeviceRetryStatus {
    static let shared = DeviceRetryStatus()
    var receiptError: String?
    var cleanupError: String?
    var importingReceipts = false
    var importingCleanup = false
    var importing: Bool { importingReceipts || importingCleanup }
}

/// Each queue has its own protected SQLite file; legacy sources remain untouched.
@MainActor
final class DeviceRetryStorage {
    private let kind: String
    private var opened: DeviceRetryQueue?
    private var legacyLoaded = false
    private var legacy: DeviceRetryQueue.LegacySource?
    init(kind: String) { self.kind = kind }
    func queue() throws -> DeviceRetryQueue {
        if let opened { return opened }
        let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("device-retry-queues", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.protectionKey: FileProtectionType.complete])
        let queue = try DeviceRetryQueue(url: directory.appendingPathComponent(kind + ".sqlite3"), kind: kind)
        opened = queue
        return queue
    }
    func migrate(
        read: () throws -> Data?, decode: (Data) throws -> DeviceRetryQueue.Job,
        prepare: (Data, DeviceRetryQueue.Job) throws -> Void = { _, _ in }
    ) throws -> Bool {
        let queue = try queue()
        let complete: Bool
        do {
            complete = try queue.migrate(
                source: {
                    if !legacyLoaded {
                        if let data = try read() {
                            legacy = .init(data: data, fingerprint: SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined())
                        }
                        legacyLoaded = true
                    }
                    return legacy
                }, decode: decode, prepare: prepare)
        } catch {
            legacy = nil
            legacyLoaded = false
            throw error
        }
        if complete {
            legacy = nil
            legacyLoaded = false
        }
        return complete
    }
}

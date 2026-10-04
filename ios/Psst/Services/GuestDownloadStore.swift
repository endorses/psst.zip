import CryptoKit
import Foundation
import Shared

/// Device-local downloads never acquire account ownership or deletion capabilities.
struct GuestFile: Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var size: Int64
    var mime: String
    var relativePath: String?
    var digest: String?
    var saved = false
    var encoding = "chunked-v1"
    var chunkSize: Int32 = 4_194_304
    var encryptionID = ""
}

/// Old local entries remain openable; absent frame metadata never enables legacy wire decoding.
extension GuestFile {
    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(String.self, forKey: .id)
        name = try values.decode(String.self, forKey: .name)
        size = try values.decode(Int64.self, forKey: .size)
        mime = try values.decode(String.self, forKey: .mime)
        relativePath = try values.decodeIfPresent(String.self, forKey: .relativePath)
        digest = try values.decodeIfPresent(String.self, forKey: .digest)
        saved = try values.decodeIfPresent(Bool.self, forKey: .saved) ?? false
        encoding = try values.decodeIfPresent(String.self, forKey: .encoding) ?? ""
        chunkSize = try values.decodeIfPresent(Int32.self, forKey: .chunkSize) ?? 0
        encryptionID = try values.decodeIfPresent(String.self, forKey: .encryptionID) ?? ""
    }
}

struct GuestDownload: Codable, Identifiable, Equatable {
    var id: String
    var origin: String
    var transferID: String
    var createdAt = Date()
    var files: [GuestFile] = []
    var complete = false
    var receiptPending = false
    var receiptDelivered = false
    var remainingDownloads: [String: Int64]? = nil
    var keyReference: String {
        "guest-download-" + id
    }

    static func identity(origin: String, transferID: String) -> String {
        SHA256.hash(data: Data((origin + "|download|" + transferID).utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

@Observable
@MainActor
final class GuestDownloadStore {
    private(set) var records: [GuestDownload] = []
    private(set) var error: String?
    private struct Receipt: Codable, Equatable { let origin: String; let transferID: String }
    private struct Snapshot: Codable { let records: [GuestDownload]; let receipts: [Receipt] }
    private var receipts: [Receipt] = []
    private let file: URL
    let documents: URL
    private var loaded = false
    private var flushing = false
    init(root: URL? = nil) {
        let manager = FileManager.default
        documents = root ?? manager.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let support = root ?? manager.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        file = support.appendingPathComponent("guest-downloads.json")
        do {
            if manager.fileExists(atPath: file.path) {
                let data = try Data(contentsOf: file)
                if let snapshot = try? JSONDecoder().decode(Snapshot.self, from: data) {
                    records = snapshot.records; receipts = snapshot.receipts
                } else {
                    records = try JSONDecoder().decode([GuestDownload].self, from: data)
                }
            }
            loaded = true
            try reconcile()
        } catch { self.error = "Local received history could not be read. Free storage or restore access, then restart. Existing history has been preserved." }
    }

    func update(_ record: GuestDownload) throws {
        var next = records.filter { $0.id != record.id }
        next.insert(record, at: 0)
        try persist(next)
    }

    private func persist(_ next: [GuestDownload], remainingReceipts: [Receipt]? = nil) throws {
        guard loaded else { throw AccountError.storage }
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        var queue = remainingReceipts ?? receipts
        for record in next where record.receiptPending {
            let receipt = Receipt(origin: record.origin, transferID: record.transferID)
            if !queue.contains(receipt) {
                queue.append(receipt)
            }
        }
        try JSONEncoder().encode(Snapshot(records: next, receipts: queue)).write(to: file, options: [.atomic, .completeFileProtection])
        records = next
        receipts = queue
    }

    func prepare(origin: String, transferID: String, key: Data) throws -> GuestDownload {
        guard loaded else { throw AccountError.storage }
        let id = GuestDownload.identity(origin: origin, transferID: transferID)
        let record = records.first { $0.id == id } ?? GuestDownload(id: id, origin: origin, transferID: transferID)
        if let existing = SecretStore.read(record.keyReference), existing != key, !record.files.isEmpty || record.complete || record.receiptPending || record.receiptDelivered {
            throw GuestError.conflictingKey
        }
        try SecretStore.write(key, name: record.keyReference)
        try update(record)
        return record
    }

    func remove(_ record: GuestDownload) throws {
        try persist(records.filter { $0.id != record.id })
        SecretStore.remove(record.keyReference)
        // Saved files intentionally remain. No server API is involved.
    }

    /// Missing previously published output needs consent even when the transfer is only partial.
    func requiresRedownloadConsent(_ record: GuestDownload) -> Bool {
        record.files.contains { $0.saved && url($0) == nil }
    }

    func url(_ file: GuestFile) -> URL? {
        guard file.saved, let path = file.relativePath, let url = safeURL(path),
              FileManager.default.isReadableFile(atPath: url.path),
              let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, Int64(size) == file.size else { return nil }
        return url
    }

    private func safeURL(_ path: String) -> URL? {
        guard !path.hasPrefix("/"), !path.split(separator: "/").contains("..") else { return nil }
        let result = documents.appendingPathComponent(path).standardizedFileURL
        guard result.path.hasPrefix(documents.standardizedFileURL.path + "/") else { return nil }
        return result
    }

    /// An intent is persisted before publication; its digest reconciles a kill between rename and checkpoint.
    func save(_ data: Data, index: Int, record: inout GuestDownload) throws {
        let temporary = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: temporary) }
        try data.write(to: temporary, options: [.atomic, .completeFileProtection])
        try publish(temporary, index: index, record: &record)
    }

    func publish(_ temporary: URL, index: Int, record: inout GuestDownload) throws {
        guard record.files.indices.contains(index),
              let size = try temporary.resourceValues(forKeys: [.fileSizeKey]).fileSize,
              Int64(size) == record.files[index].size else { throw GuestError.invalidManifest }
        let directory = "Received/Guest/" + record.id
        try FileManager.default.createDirectory(at: documents.appendingPathComponent(directory), withIntermediateDirectories: true)
        let safe = try GuestFiles.filename(record.files[index].name)
        let relative = directory + "/" + UUID().uuidString + "-" + safe
        let destination = documents.appendingPathComponent(relative)
        record.files[index].relativePath = relative
        record.files[index].digest = try StreamedFiles.digest(temporary)
        record.files[index].saved = false
        try update(record)
        try Task.checkCancellation()
        try FileManager.default.moveItem(at: temporary, to: destination)
        record.files[index].saved = true
        try update(record)
    }

    func reconcile() throws {
        var next = records
        for row in next.indices {
            for index in next[row].files.indices {
                let value = next[row].files[index]
                guard let path = value.relativePath, let destination = safeURL(path) else { continue }
                try? FileManager.default.removeItem(at: destination.appendingPathExtension("pending"))
                if !value.saved, let size = try? destination.resourceValues(forKeys: [.fileSizeKey]).fileSize, Int64(size) == value.size,
                   (try? StreamedFiles.digest(destination)) == value.digest
                {
                    next[row].files[index].saved = true
                }
            }
            if !next[row].files.isEmpty, next[row].files.allSatisfy({ url($0) != nil }) {
                next[row].complete = true
                next[row].receiptPending = !next[row].receiptDelivered
            }
        }
        if next != records {
            try persist(next)
        }
    }

    func flushReceipts(deliver: ((String, String) async throws -> Void)? = nil) async {
        guard !flushing, loaded else { return }
        flushing = true
        defer { flushing = false }
        for receipt in receipts.prefix(4) {
            do {
                if let deliver {
                    try await deliver(receipt.origin, receipt.transferID)
                } else {
                    let client = try GuestNetwork.client(receipt.origin)
                    defer { client.close() }
                    try await client.transfers.acknowledgeDownload(transferId: receipt.transferID)
                }
                var next = records
                if let index = next.firstIndex(where: { $0.origin == receipt.origin && $0.transferID == receipt.transferID }) {
                    next[index].receiptPending = false
                    next[index].receiptDelivered = true
                }
                try persist(next, remainingReceipts: receipts.filter { $0 != receipt })
            } catch {
                // Rotate unavailable hosts so a long queue does not starve later receipts.
                let queue = receipts.filter { $0 != receipt } + [receipt]
                try? persist(records, remainingReceipts: queue)
            }
        }
    }
}

enum GuestFiles {
    static func decrypt(_ data: Data, key: Data) throws -> Data {
        guard key.count == 32, data.count >= 28 else { throw GuestError.invalidManifest }
        return try CryptoProvider.shared.decrypt(key: key.toKotlinByteArray(), nonce: Data(data.prefix(12)).toKotlinByteArray(), ciphertext: Data(data.dropFirst(12)).toKotlinByteArray()).toData()
    }

    static func digest(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    static func filename(_ name: String) throws -> String {
        try ManifestValidator.shared.safeFilename(name: name)
    }
}

enum GuestError: LocalizedError {
    case input, conflictingKey, invalidManifest, missingKey, notReady, redownloadConsent, downloadLimit
    var errorDescription: String? {
        switch self {
        case .input: "This is not a supported psst.zip link or server login code. Scan again or paste a complete link."
        case .conflictingKey: "This transfer is already stored with a different key. Its existing files and key were preserved."
        case .invalidManifest: "The encrypted file list is invalid or exceeds this app’s limits."
        case .missingKey: "This transfer’s key is unavailable. Scan or paste the original link again."
        case .redownloadConsent: "A previously saved file is missing. Choose Download missing files to receive it again."
        case .downloadLimit: "Download limit reached. Already saved files remain available. Ask the sender for a new link for missing files."
        case .notReady: "This transfer is not ready or has expired. Ask the sender for an available download link."
        }
    }
}

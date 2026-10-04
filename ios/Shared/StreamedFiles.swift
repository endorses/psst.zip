import CryptoKit
import Foundation
import Shared

/// One authenticated frame in memory; incomplete plaintext stays in a private temporary file.
enum StreamedFiles {
    static let chunkBytes = 4 * 1024 * 1024
    static let frameBytes = chunkBytes + 60
    static func clearAbandonedReceives() {
        let directory = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("Receiving", isDirectory: true)
        // Called once at app launch, before any receive coordinator is installed.
        try? FileManager.default.removeItem(at: directory)
    }

    @MainActor
    static func receive(client: ApiClient, transferID: String, file: FileMetadata, key: KotlinByteArray,
                        progress: @escaping @MainActor (Int64) -> Void = { _ in }) async throws -> URL
    {
        guard file.encoding == "chunked-v1", file.chunkSize == Int32(chunkBytes), !file.encryptionId.isEmpty else { throw AccountError.request }
        let writer = try AuthenticatedFileWriter(file: file, key: key)
        do {
            try await client.transfers.downloadFileChunks(transferId: transferID, fileId: file.blobId,
                                                          wireSize: ChunkedFileCrypto.shared.wireSize(totalSize: file.size),
                                                          chunkBytes: Int32(frameBytes), onChunk: { frame in
                                                              let accepted = writer.accept(frame)
                                                              let count = writer.written
                                                              Task { @MainActor in progress(count) }
                                                              return KotlinBoolean(bool: accepted)
                                                          })
            try Task.checkCancellation()
            return try writer.finish()
        } catch {
            writer.discard()
            throw error
        }
    }

    static func digest(_ url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hash = SHA256()
        while let data = try handle.read(upToCount: 64 * 1024), !data.isEmpty {
            hash.update(data: data)
        }
        return hash.finalize().map { String(format: "%02x", $0) }.joined()
    }
}

/// Ktor invokes chunk callbacks serially, potentially outside the main actor.
final class AuthenticatedFileWriter {
    private let lock = NSLock()
    private let file: FileMetadata
    private let key: KotlinByteArray
    private let url: URL
    private let handle: FileHandle
    private var next: Int64 = 0
    private var count: Int64 = 0
    private var failure: Error?
    var written: Int64 {
        lock.withLock { count }
    }

    init(file: FileMetadata, key: KotlinByteArray) throws {
        self.file = file; self.key = key
        let directory = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("Receiving", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        url = directory.appendingPathComponent(UUID().uuidString + ".pending")
        guard FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.protectionKey: FileProtectionType.complete]) else { throw AccountError.request }
        handle = try FileHandle(forWritingTo: url)
    }

    func accept(_ frame: KotlinByteArray) -> Bool {
        lock.withLock {
            guard failure == nil else { return false }
            do {
                let plaintext = try ChunkedFileCrypto.shared.decrypt(key: key, id: file.encryptionId, totalSize: file.size, index: next, frame: frame).toData()
                try ReceiveSafety.checkSpace(at: url.deletingLastPathComponent(), additional: Int64(plaintext.count))
                try handle.write(contentsOf: plaintext)
                count += Int64(plaintext.count); next += 1
            } catch { failure = error; return false }
            return true
        }
    }

    func finish() throws -> URL {
        try lock.withLock {
            if let failure {
                throw failure
            }
            guard count == file.size, next == max(1, (file.size + Int64(StreamedFiles.chunkBytes) - 1) / Int64(StreamedFiles.chunkBytes)) else { throw AccountError.request }
            try handle.synchronize(); try handle.close()
            return url
        }
    }

    func discard() {
        lock.withLock { try? handle.close(); try? FileManager.default.removeItem(at: url) }
    }
}

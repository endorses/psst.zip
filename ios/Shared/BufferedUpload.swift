import Foundation
import Shared
import UniformTypeIdentifiers

struct UploadProgress {
    let name: String
    let sent: Int64
    let total: Int64
    var fraction: Double {
        total > 0 ? Double(sent) / Double(total) : 0
    }
}

enum BufferedUpload {
    static let maxFileBytes = Int(LocalUploadFailure.maximumFileBytes)
    static func mimeType(for url: URL) -> String {
        UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
    }

    static func sizes(_ urls: [URL], limit: Int) throws -> [Int64] {
        guard !urls.isEmpty, urls.count <= 100 else { throw AccountError.request }
        return try urls.map { url in
            let access = url.startAccessingSecurityScopedResource()
            defer {
                if access {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            guard let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize else { throw AccountError.request }
            try LocalUploadFailure.validateSize(Int64(size), limitBytes: Int64(limit))
            return Int64(size)
        }
    }

    @MainActor
    static func send(
        fileURLs: [URL], client: ApiClient, transferId: String, key: KotlinByteArray,
        limit: Int, expectedSizes: [Int64]? = nil, check: () throws -> Void,
        preparing: (String) -> Void, progress: @escaping @MainActor (UploadProgress) -> Void
    ) async throws -> [FileMetadata] {
        let sizes = try sizes(fileURLs, limit: limit)
        try GuestUploadSelection.requireUnchanged(sizes, expected: expectedSizes)
        let total = try GuestUploadSelection.totalWireBytes(sizes.map { try ChunkedFileCrypto.shared.wireSize(totalSize: $0) })
        var sent: Int64 = 0
        var files: [FileMetadata] = []
        for (index, url) in fileURLs.enumerated() {
            try check()
            preparing(url.lastPathComponent)
            await Task.yield()
            let accessing = url.startAccessingSecurityScopedResource()
            defer {
                if accessing {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            let handle = try FileHandle(forReadingFrom: url)
            defer { try? handle.close() }
            let size = sizes[index]
            let encryptionID = try ChunkedFileCrypto.shared.createId()
            let wireSize = try ChunkedFileCrypto.shared.wireSize(totalSize: size)
            let resourceURL = try await client.createFileUpload(transferId: transferId, wireSize: wireSize)
            var remaining = size
            var frameIndex: Int64 = 0
            var offset: Int64 = 0
            repeat {
                try check()
                let expected = Int(min(Int64(StreamedFiles.chunkBytes), remaining))
                var plain = Data()
                while plain.count < expected {
                    guard let part = try handle.read(upToCount: expected - plain.count), !part.isEmpty else { throw AccountError.request }
                    plain.append(part)
                }
                let frame = try ChunkedFileCrypto.shared.encrypt(key: key, id: encryptionID, totalSize: size, index: frameIndex, plaintext: plain.toKotlinByteArray())
                try await client.tus.uploadChunk(resourceUrl: resourceURL, data: frame, offset: offset)
                offset += Int64(frame.size)
                progress(UploadProgress(name: url.lastPathComponent, sent: sent + offset, total: total))
                remaining -= Int64(expected)
                frameIndex += 1
            } while remaining > 0
            guard try (handle.read(upToCount: 1) ?? Data()).isEmpty, offset == wireSize else {
                throw AccountError.request
            }
            try check()
            sent += wireSize
            files.append(
                FileMetadata(
                    name: url.lastPathComponent, size: size,
                    mimeType: mimeType(for: url),
                    blobId: URL(string: resourceURL)?.lastPathComponent ?? resourceURL,
                    encoding: "chunked-v1", chunkSize: Int32(StreamedFiles.chunkBytes), encryptionId: encryptionID
                )
            )
        }
        return files
    }
}

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
    static let maxFileBytes = 25 * 1024 * 1024
    static func sizes(_ urls: [URL], limit: Int) throws -> [Int64] {
        try urls.map { url in
            let access = url.startAccessingSecurityScopedResource()
            defer {
                if access {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            guard let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize else { throw AccountError.request }
            guard size <= limit else { throw NSError(domain: "Psst", code: 1, userInfo: [NSLocalizedDescriptionKey: String(format: String(localized: "Each file must be no larger than %lld MiB."), Int64(limit / 1024 / 1024))]) }
            return Int64(size)
        }
    }

    @MainActor
    static func send(fileURLs: [URL], client: ApiClient, transferId: String, key: KotlinByteArray,
                     limit: Int, check: () throws -> Void,
                     preparing: (String) -> Void, progress: @escaping @MainActor (UploadProgress) -> Void) async throws -> [FileMetadata]
    {
        let sizes = try sizes(fileURLs, limit: limit)
        let total = sizes.reduce(0, +) + Int64(fileURLs.count * 28)
        var sent: Int64 = 0
        var files: [FileMetadata] = []
        for (index, url) in fileURLs.enumerated() {
            try check()
            preparing(url.lastPathComponent)
            // Give SwiftUI a chance to draw the indeterminate preparation state.
            await Task.yield()
            let accessing = url.startAccessingSecurityScopedResource()
            defer {
                if accessing {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            let handle = try FileHandle(forReadingFrom: url)
            defer { try? handle.close() }
            var plaintext = Data()
            while plaintext.count <= limit {
                try check()
                let chunk = try handle.read(upToCount: min(64 * 1024, limit + 1 - plaintext.count)) ?? Data()
                if chunk.isEmpty {
                    break
                }
                plaintext.append(chunk)
            }
            guard plaintext.count <= limit, Int64(plaintext.count) == sizes[index] else { throw AccountError.request }
            let nonce = try CryptoProvider.shared.generateNonce()
            let encrypted = try CryptoProvider.shared.encrypt(key: key, nonce: nonce, plaintext: plaintext.toKotlinByteArray())
            let blob = nonce.toData() + encrypted.toData()
            let completedBytes = sent
            let resourceURL = try await client.uploadFile(transferId: transferId, data: blob.toKotlinByteArray(), metadata: [:], onProgress: { uploaded in
                let value = UploadProgress(name: url.lastPathComponent, sent: completedBytes + uploaded.int64Value, total: total)
                Task { @MainActor in progress(value) }
            })
            try check()
            sent += Int64(blob.count)
            files.append(FileMetadata(name: url.lastPathComponent, size: Int64(plaintext.count), mimeType: UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream", blobId: URL(string: resourceURL)?.lastPathComponent ?? resourceURL))
        }
        return files
    }
}

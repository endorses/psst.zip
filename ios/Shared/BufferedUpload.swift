import Foundation
import Shared
import UniformTypeIdentifiers

/// Whole-file AES-GCM is bounded until all clients support a streaming format.
enum BufferedUpload {
    static let maxFileBytes = 25 * 1024 * 1024

    @MainActor
    static func send(
        fileURLs: [URL], client: ApiClient, transferId: String, key: KotlinByteArray,
        limit: Int, progress: (Double) -> Void
    ) async throws -> [FileMetadata] {
        var files: [FileMetadata] = []
        for (index, url) in fileURLs.enumerated() {
            let accessing = url.startAccessingSecurityScopedResource()
            defer {
                if accessing {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            // Bound the read itself; metadata alone can be absent or stale.
            let handle = try FileHandle(forReadingFrom: url)
            defer { try? handle.close() }
            var plaintext = Data()
            while plaintext.count <= limit {
                let chunk = try handle.read(upToCount: min(64 * 1024, limit + 1 - plaintext.count)) ?? Data()
                if chunk.isEmpty {
                    break
                }
                plaintext.append(chunk)
            }
            guard plaintext.count <= limit else {
                throw NSError(domain: "Psst", code: 1, userInfo: [
                    NSLocalizedDescriptionKey: "Files must be no larger than \(limit / 1024 / 1024) MiB.",
                ])
            }
            let nonce = try CryptoProvider.shared.generateNonce()
            let encrypted = try CryptoProvider.shared.encrypt(
                key: key, nonce: nonce, plaintext: plaintext.toKotlinByteArray()
            )
            let blob = nonce.toData() + encrypted.toData()
            let resourceURL = try await client.uploadFile(
                transferId: transferId, data: blob.toKotlinByteArray(), metadata: [:],
                onProgress: { _ in }
            )
            let blobId = URL(string: resourceURL)?.lastPathComponent ?? resourceURL
            files.append(FileMetadata(
                name: url.lastPathComponent, size: Int64(plaintext.count),
                mimeType: UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream",
                blobId: blobId
            ))
            progress(Double(index + 1) / Double(fileURLs.count))
        }
        return files
    }
}

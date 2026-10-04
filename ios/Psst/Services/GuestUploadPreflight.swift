import Foundation
import Shared

enum GuestUploadPreflight {
    static func validate(_ availability: SlotAvailability, link: ParsedUrl, urls: [URL], sizes: [Int64]) throws {
        guard urls.count == sizes.count else { throw GuestUploadSelectionError.invalidFiles }
        do { try availability.validateInvitation(slotId: link.id, publicKey: link.key) } catch { throw GuestUploadSelectionError.unavailable }
        guard let capacity = availability.uploadCapacity, capacity.isFresh() else {
            throw GuestUploadSelectionError.unavailable
        }
        guard capacity.state != "unknown" else { throw GuestUploadSelectionError.unavailable }
        guard capacity.state == "ready" else { throw GuestUploadSelectionError.exhausted }
        if !sizes.isEmpty {
            // Placeholder identifiers have exactly the same encoded lengths as upload IDs.
            let selection = Manifest(
                files: zip(urls, sizes).map { url, size in
                    FileMetadata(
                        name: url.lastPathComponent, size: size, mimeType: BufferedUpload.mimeType(for: url),
                        blobId: UUID().uuidString, encoding: "chunked-v1", chunkSize: Int32(StreamedFiles.chunkBytes),
                        encryptionId: UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased())
                })
            _ = try ManifestValidator.shared.validate(manifest: selection)
            let encoded = try ManifestSerializer.encode(manifest: selection)
            try GuestUploadSelection.requireManifestCapacity(plainBytes: encoded.utf8.count, reserve: capacity.manifestReserveBytes)
        }
        let wireBytes = try GuestUploadSelection.totalWireBytes(
            sizes.map {
                try ChunkedFileCrypto.shared.wireSize(totalSize: $0)
            })
        guard let count = capacity.availableFiles, Int64(sizes.count) <= count.int64Value,
            availability.remainingFiles == nil || Int64(sizes.count) <= availability.remainingFiles!.int64Value
        else { throw GuestUploadSelectionError.tooMany }
        guard let bytes = capacity.availableWireBytes, wireBytes <= bytes.int64Value,
            wireBytes <= availability.remainingBytes
        else { throw GuestUploadSelectionError.tooLarge }
        do {
            if sizes.isEmpty {
                try capacity.validateSelection(fileCount: 0, totalWireBytes: 0)
            } else {
                try availability.validateForSubmission(
                    slotId: link.id, publicKey: link.key,
                    fileCount: Int32(sizes.count), totalWireBytes: wireBytes)
            }
        } catch { throw GuestUploadSelectionError.unavailable }
    }
}

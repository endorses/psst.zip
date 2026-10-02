import Foundation
import Shared

/// Canonical manifest wire format, shared with Kotlin and the web client.
enum ManifestSerializer {
    private struct WireManifest: Codable {
        struct File: Codable {
            let name: String
            let size: Int64
            let mime_type: String
            let blob_id: String
        }

        let files: [File]
    }

    static func encode(manifest: Manifest) throws -> String {
        let wire = WireManifest(files: manifest.files.map {
            WireManifest.File(name: $0.name, size: $0.size, mime_type: $0.mimeType, blob_id: $0.blobId)
        })
        return try String(decoding: JSONEncoder().encode(wire), as: UTF8.self)
    }

    static func decode(json: String) -> Manifest? {
        guard let wire = try? JSONDecoder().decode(WireManifest.self, from: Data(json.utf8)),
              wire.files.allSatisfy({ $0.size >= 0 && UUID(uuidString: $0.blob_id) != nil })
        else {
            return nil
        }
        return Manifest(files: wire.files.map {
            FileMetadata(name: $0.name, size: $0.size, mimeType: $0.mime_type, blobId: $0.blob_id)
        })
    }
}

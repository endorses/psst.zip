import CryptoKit
import Foundation
import Shared
import XCTest

@testable import Psst

final class StreamedFileTests: XCTestCase {
    private let key = Data(repeating: 7, count: 32).toKotlinByteArray()
    private let context = String(repeating: "a", count: 32)
    private func metadata(_ size: Int64) -> FileMetadata {
        FileMetadata(
            name: "large.bin", size: size, mimeType: "application/octet-stream", blobId: UUID().uuidString,
            encoding: "chunked-v1", chunkSize: Int32(StreamedFiles.chunkBytes), encryptionId: context)
    }

    private func frame(total: Int64, index: Int64) throws -> KotlinByteArray {
        let count = try Int(ChunkedFileCrypto.shared.plaintextSize(totalSize: total, index: index))
        // Kotlin arrays start zero-filled. Avoid per-byte Swift bridge calls just
        // to construct fixtures; encryption and the production writer stay real.
        return try ChunkedFileCrypto.shared.encrypt(
            key: key, id: context, totalSize: total, index: index,
            plaintext: KotlinByteArray(size: Int32(count)))
    }

    func testMoreThan100MiBWritesFramesWithoutWholeFileBuffer() throws {
        let total: Int64 = 101 * 1024 * 1024 + 17
        let writer = try AuthenticatedFileWriter(file: metadata(total), key: key)
        defer { writer.discard() }
        var expected = SHA256()
        let chunks = try ChunkedFileCrypto.shared.chunkCount(totalSize: total)
        for index in 0..<chunks {
            try autoreleasepool {
                let count = try Int(ChunkedFileCrypto.shared.plaintextSize(totalSize: total, index: index))
                expected.update(data: Data(repeating: 0, count: count))
                XCTAssertTrue(try writer.accept(frame(total: total, index: index)))
            }
        }
        let url = try writer.finish()
        XCTAssertEqual(writer.written, total)
        XCTAssertEqual(try StreamedFiles.digest(url), expected.finalize().map { String(format: "%02x", $0) }.joined())
    }

    func testTruncatedAndReorderedFilesNeverFinish() throws {
        let total = Int64(StreamedFiles.chunkBytes) + 1
        let writer = try AuthenticatedFileWriter(file: metadata(total), key: key)
        defer { writer.discard() }
        XCTAssertTrue(try writer.accept(frame(total: total, index: 0)))
        XCTAssertThrowsError(try writer.finish())
        XCTAssertFalse(try writer.accept(frame(total: total, index: 0)))
        XCTAssertThrowsError(try writer.finish())
    }

    func testTamperedFrameIsNotPublished() throws {
        let writer = try AuthenticatedFileWriter(file: metadata(8), key: key)
        defer { writer.discard() }
        var bytes = try frame(total: 8, index: 0).toData()
        bytes[bytes.count - 1] ^= 1
        XCTAssertFalse(writer.accept(bytes.toKotlinByteArray()))
        XCTAssertThrowsError(try writer.finish())
    }

    func testEmptyFileStillRequiresAuthenticatedFrame() throws {
        let writer = try AuthenticatedFileWriter(file: metadata(0), key: key)
        defer { writer.discard() }
        XCTAssertThrowsError(try writer.finish())
        XCTAssertTrue(try writer.accept(frame(total: 0, index: 0)))
        XCTAssertEqual(try writer.finish().resourceValues(forKeys: [.fileSizeKey]).fileSize, 0)
    }

    func testManifestRequiresAndPreservesChunkContract() throws {
        let original = metadata(101 * 1024 * 1024)
        let json = try ManifestSerializer.encode(manifest: Manifest(files: [original]))
        let result = try XCTUnwrap(ManifestSerializer.decode(json: json)?.files.first)
        XCTAssertEqual(result.encryptionId, context)
        XCTAssertEqual(result.chunkSize, Int32(StreamedFiles.chunkBytes))
        XCTAssertEqual(result.encoding, "chunked-v1")
        XCTAssertNil(
            ManifestSerializer.decode(
                json:
                    "{\"files\":[{\"name\":\"x\",\"size\":1,\"mime_type\":\"text/plain\",\"blob_id\":\"00000000-0000-0000-0000-000000000000\"}]}"
            ))
    }

    func testIndependentCrossPlatformFrameVector() throws {
        // Same public deterministic Node AES-GCM vector as docs/protocol/chunked-file-vector.json.
        let hex =
            "202122232425262728292a2bd22b844328cd7c7992e5e8750dc51a06d049ec9c878063ee6cf66c12498a5e1e05fd9670a05f5caa349e06e736623fae8b5298fbe859278064c1cb8958bad91aea26d71db5bbdf"
        let chars = Array(hex)
        let bytes = Data(stride(from: 0, to: chars.count, by: 2).map { UInt8(String(chars[$0...$0 + 1]), radix: 16)! })
        let key = Data((0..<32).map(UInt8.init)).toKotlinByteArray()
        let plain = try ChunkedFileCrypto.shared.decrypt(
            key: key, id: "00112233445566778899aabbccddeeff", totalSize: 23, index: 0, frame: bytes.toKotlinByteArray())
        XCTAssertEqual(plain.toData(), Data("psst.zip chunk fixture\n".utf8))
        XCTAssertThrowsError(
            try ChunkedFileCrypto.shared.decrypt(
                key: key, id: "00112233445566778899aabbccddeeff", totalSize: 24, index: 0,
                frame: bytes.toKotlinByteArray()))
    }

    func testRetryUsesRaisedServerLimitInsteadOfPreviousAttemptLimit() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("large.bin")
        XCTAssertTrue(FileManager.default.createFile(atPath: file.path, contents: nil))
        let handle = try FileHandle(forWritingTo: file)
        try handle.truncate(atOffset: 150 * 1024 * 1024)
        try handle.close()

        // SendViewModel and the extension use this policy on every attempt before selection validation.
        var policy = UploadSizePolicy(processingCeiling: BufferedUpload.maxFileBytes)
        try policy.refresh(serverMaximum: 25 * 1024 * 1024)
        XCTAssertThrowsError(try BufferedUpload.sizes([file], limit: policy.limit))
        try policy.refresh(serverMaximum: 200 * 1024 * 1024)
        XCTAssertEqual(try BufferedUpload.sizes([file], limit: policy.limit), [Int64(150 * 1024 * 1024)])
        try policy.refresh(serverMaximum: 100 * 1024 * 1024)
        XCTAssertThrowsError(try BufferedUpload.sizes([file], limit: policy.limit))

        var constrained = UploadSizePolicy(processingCeiling: 100 * 1024 * 1024)
        try constrained.refresh(serverMaximum: 200 * 1024 * 1024)
        XCTAssertThrowsError(try BufferedUpload.sizes([file], limit: constrained.limit))
    }
}

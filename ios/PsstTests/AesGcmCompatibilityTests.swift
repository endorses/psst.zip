import Foundation
import Shared
import XCTest

@testable import Psst

/// Exercise the Kotlin iOS provider through its real exported interface.
final class AesGcmCompatibilityTests: XCTestCase {
    // Public AES-256-GCM vectors: revised GCM specification, Appendix B cases 13/14.
    // These key/nonce values must never be used for real encryption.
    private let key = Data(repeating: 0, count: 32)
    private let nonce = Data(repeating: 0, count: 12)

    func testIndependentEmptyAndSingleBlockVectors() throws {
        for (plaintext, encoded) in [
            (Data(), "530f8afbc74536b9a963b4f1c4cb738b"),
            (
                Data(repeating: 0, count: 16),
                "cea7403d4d606b6e074ec5d3baf39d18d0d1c8a799996bf0265b98b5d48ab919"
            ),
        ] {
            let expected = try hex(encoded)
            XCTAssertEqual(try encrypt(key: key, nonce: nonce, plaintext: plaintext), expected)
            XCTAssertEqual(try decrypt(key: key, nonce: nonce, ciphertext: expected), plaintext)
        }
    }

    func testEmptyAndNonemptyMessagesRejectModifiedAuthenticationInputs() throws {
        for plaintext in [Data(), Data(repeating: 0, count: 16)] {
            let ciphertext = try encrypt(key: key, nonce: nonce, plaintext: plaintext)
            var modifiedTag = ciphertext
            modifiedTag[modifiedTag.count - 1] ^= 1
            XCTAssertThrowsError(try decrypt(key: key, nonce: nonce, ciphertext: modifiedTag))

            var wrongKey = key
            wrongKey[0] ^= 1
            XCTAssertThrowsError(try decrypt(key: wrongKey, nonce: nonce, ciphertext: ciphertext))

            var wrongNonce = nonce
            wrongNonce[0] ^= 1
            XCTAssertThrowsError(try decrypt(key: key, nonce: wrongNonce, ciphertext: ciphertext))

            if !plaintext.isEmpty {
                var modifiedCiphertext = ciphertext
                modifiedCiphertext[0] ^= 1
                XCTAssertThrowsError(try decrypt(key: key, nonce: nonce, ciphertext: modifiedCiphertext))
            }
        }
    }

    func testInvalidKeyNonceAndTruncatedTagAreRejected() throws {
        let tag = try encrypt(key: key, nonce: nonce, plaintext: Data())
        for size in [0, 16, 24, 31, 33] {
            let invalidKey = Data(repeating: 0, count: size)
            XCTAssertThrowsError(try encrypt(key: invalidKey, nonce: nonce, plaintext: Data()))
            XCTAssertThrowsError(try decrypt(key: invalidKey, nonce: nonce, ciphertext: tag))
        }
        for size in [0, 11, 13] {
            let invalidNonce = Data(repeating: 0, count: size)
            XCTAssertThrowsError(try encrypt(key: key, nonce: invalidNonce, plaintext: Data()))
            XCTAssertThrowsError(try decrypt(key: key, nonce: invalidNonce, ciphertext: tag))
        }
        for size in [0, 15] {
            XCTAssertThrowsError(
                try decrypt(key: key, nonce: nonce, ciphertext: Data(repeating: 0, count: size)))
        }
    }

    private func encrypt(key: Data, nonce: Data, plaintext: Data) throws -> Data {
        try CryptoProvider.shared.encrypt(
            key: key.toKotlinByteArray(), nonce: nonce.toKotlinByteArray(),
            plaintext: plaintext.toKotlinByteArray()
        ).toData()
    }

    private func decrypt(key: Data, nonce: Data, ciphertext: Data) throws -> Data {
        try CryptoProvider.shared.decrypt(
            key: key.toKotlinByteArray(), nonce: nonce.toKotlinByteArray(),
            ciphertext: ciphertext.toKotlinByteArray()
        ).toData()
    }

    private func hex(_ text: String) throws -> Data {
        try Data(
            stride(from: 0, to: text.count, by: 2).map { offset in
                let start = text.index(text.startIndex, offsetBy: offset)
                return try XCTUnwrap(UInt8(text[start..<text.index(start, offsetBy: 2)], radix: 16))
            })
    }
}

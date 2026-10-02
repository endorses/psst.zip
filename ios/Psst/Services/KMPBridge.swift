import Foundation
import Shared

// Convenience extensions for bridging between Swift Data types and Kotlin byte arrays.
// The KMP Shared framework (via SKIE) exposes Kotlin classes to Swift.

// MARK: - CryptoProvider convenience

extension CryptoProvider {
    /// Generate a key and return it as Swift Data.
    func generateKeyData() throws -> Data {
        try generateKey().toData()
    }

    /// Generate a nonce and return it as Swift Data.
    func generateNonceData() throws -> Data {
        try generateNonce().toData()
    }

    /// Encrypt with Swift Data types.
    func encrypt(key: Data, nonce: Data, plaintext: Data) throws -> Data {
        let result = try encrypt(
            key: key.toKotlinByteArray(),
            nonce: nonce.toKotlinByteArray(),
            plaintext: plaintext.toKotlinByteArray()
        )
        return result.toData()
    }

    /// Decrypt with Swift Data types.
    func decrypt(key: Data, nonce: Data, ciphertext: Data) throws -> Data {
        let result = try decrypt(
            key: key.toKotlinByteArray(),
            nonce: nonce.toKotlinByteArray(),
            ciphertext: ciphertext.toKotlinByteArray()
        )
        return result.toData()
    }
}

// MARK: - Encrypted blob format helpers

/// The encrypted blob format used throughout the app:
///   [12-byte nonce][ciphertext with GCM auth tag]
enum EncryptedBlob {
    static let nonceSize = 12

    /// Pack a nonce and ciphertext into blob format.
    static func pack(nonce: Data, ciphertext: Data) -> Data {
        nonce + ciphertext
    }

    /// Unpack a blob into nonce and ciphertext.
    static func unpack(_ blob: Data) -> (nonce: Data, ciphertext: Data)? {
        guard blob.count > nonceSize else { return nil }
        let nonce = blob.prefix(nonceSize)
        let ciphertext = blob.dropFirst(nonceSize)
        return (Data(nonce), Data(ciphertext))
    }
}

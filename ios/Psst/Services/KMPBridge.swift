import Foundation
import Shared

/// Convenience extensions for bridging between Swift Data types and Kotlin byte arrays.
/// The KMP Shared framework (via SKIE) exposes Kotlin classes to Swift.

// MARK: - CryptoProvider convenience

extension CryptoProvider {
    /// Generate a key and return it as Swift Data.
    func generateKeyData() -> Data {
        generateKey().toData()
    }

    /// Generate a nonce and return it as Swift Data.
    func generateNonceData() -> Data {
        generateNonce().toData()
    }

    /// Encrypt with Swift Data types.
    func encrypt(key: Data, nonce: Data, plaintext: Data) -> Data {
        let result = encrypt(
            key: key.toKotlinByteArray(),
            nonce: nonce.toKotlinByteArray(),
            plaintext: plaintext.toKotlinByteArray()
        )
        return Data(result)
    }

    /// Decrypt with Swift Data types.
    func decrypt(key: Data, nonce: Data, ciphertext: Data) -> Data {
        let result = decrypt(
            key: key.toKotlinByteArray(),
            nonce: nonce.toKotlinByteArray(),
            ciphertext: ciphertext.toKotlinByteArray()
        )
        return Data(result)
    }
}

// MARK: - Flow bridging

/// Helper to convert a Kotlin Flow to Swift AsyncSequence.
/// SKIE handles this automatically; this type alias documents the pattern.
///
/// Usage:
///   for try await event in client.slots.events(slotId: id).asAsyncSequence() {
///       // handle event
///   }
///
/// SKIE generates the `asAsyncSequence()` method on Kotlin Flow types.

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

#if canImport(CryptoKit)
    import CryptoKit
#else
    import Crypto
#endif
import Foundation

/// RFC 9180 Base mode. This adapter only wraps submission keys; existing file
/// and manifest encryption still uses a fresh AES-256 key per submission.
enum ReceiveCrypto {
    static let maximumEnvelopeBytes = 1_048_576
    static let magic = Data("PSSTRCV2".utf8)
    private static let suite = HPKE.Ciphersuite(kem: .Curve25519_HKDF_SHA256, kdf: .HKDF_SHA256, aead: .AES_GCM_256)

    struct KeyPair {
        let privateKey: Data
        let publicKey: Data
    }

    struct Envelope {
        let wrappedKey: Data
        let encryptedManifest: Data
    }

    static func generateKeyPair() -> KeyPair {
        let key = Curve25519.KeyAgreement.PrivateKey()
        return KeyPair(privateKey: key.rawRepresentation, publicKey: key.publicKey.rawRepresentation)
    }

    /// All context binding goes in HPKE info, with empty AAD for Tink interop.
    static func info(slotID: String, transferID: String, publicKey: Data) throws -> Data {
        guard publicKey.count == 32 else { throw ReceiveCryptoError.invalidKey }
        var context = Data("psst.zip/receive-key/v2\0".utf8)
        context.append(contentsOf: [0, 32, 0, 1, 0, 2])
        try context.append(uuidBytes(slotID))
        try context.append(uuidBytes(transferID))
        context.append(publicKey)
        return context
    }

    static func sealSubmissionKey(_ submissionKey: Data, publicKey: Data, slotID: String, transferID: String) throws -> Data {
        guard submissionKey.count == 32, publicKey.count == 32 else { throw ReceiveCryptoError.invalidKey }
        let recipient = try validatedPublicKey(publicKey)
        var sender = try HPKE.Sender(recipientKey: recipient, ciphersuite: suite, info: info(slotID: slotID, transferID: transferID, publicKey: publicKey))
        let ciphertext = try sender.seal(submissionKey)
        guard sender.encapsulatedKey.count == 32, ciphertext.count == 48 else { throw ReceiveCryptoError.invalidEnvelope }
        return sender.encapsulatedKey + ciphertext
    }

    static func openSubmissionKey(_ wrappedKey: Data, privateKey: Data, publicKey: Data, slotID: String, transferID: String) throws -> Data {
        guard wrappedKey.count == 80, privateKey.count == 32, publicKey.count == 32 else { throw ReceiveCryptoError.invalidKey }
        let key = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey)
        guard key.publicKey.rawRepresentation == publicKey else { throw ReceiveCryptoError.invalidKey }
        _ = try validatedPublicKey(Data(wrappedKey.prefix(32)))
        var recipient = try HPKE.Recipient(privateKey: key, ciphersuite: suite, info: info(slotID: slotID, transferID: transferID, publicKey: publicKey), encapsulatedKey: Data(wrappedKey.prefix(32)))
        let plaintext = try recipient.open(Data(wrappedKey.dropFirst(32)))
        guard plaintext.count == 32 else { throw ReceiveCryptoError.invalidKey }
        return plaintext
    }

    static func encodeEnvelope(wrappedKey: Data, encryptedManifest: Data) throws -> Data {
        guard wrappedKey.count == 80, encryptedManifest.count >= 28,
              encryptedManifest.count <= maximumEnvelopeBytes - 88 else { throw ReceiveCryptoError.invalidEnvelope }
        return magic + wrappedKey + encryptedManifest
    }

    static func decodeEnvelope(_ data: Data) throws -> Envelope {
        guard data.count >= 116, data.count <= maximumEnvelopeBytes, data.prefix(8) == magic else {
            throw ReceiveCryptoError.invalidEnvelope
        }
        return Envelope(wrappedKey: Data(data.dropFirst(8).prefix(80)), encryptedManifest: Data(data.dropFirst(88)))
    }

    /// RFC 9180 requires rejecting X25519 inputs that produce an all-zero DH result.
    /// Some provider versions do not enforce this inside HPKE. Validate with the
    /// provider's key-agreement API; never use this probe to derive application keys.
    private static func validatedPublicKey(_ bytes: Data) throws -> Curve25519.KeyAgreement.PublicKey {
        guard bytes.count == 32 else { throw ReceiveCryptoError.invalidKey }
        let publicKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: bytes)
        let probe = try Curve25519.KeyAgreement.PrivateKey().sharedSecretFromKeyAgreement(with: publicKey)
        guard probe.withUnsafeBytes({ $0.contains(where: { $0 != 0 }) }) else { throw ReceiveCryptoError.invalidKey }
        return publicKey
    }

    private static func uuidBytes(_ text: String) throws -> Data {
        guard text.utf8.count == 36, let value = UUID(uuidString: text), value.uuidString.lowercased() == text.lowercased() else {
            throw ReceiveCryptoError.invalidIdentity
        }
        var raw = value.uuid
        return withUnsafeBytes(of: &raw) { Data($0) }
    }
}

enum ReceiveCryptoError: LocalizedError {
    case invalidIdentity, invalidKey, invalidEnvelope
    var errorDescription: String? {
        switch self {
        case .invalidIdentity: "This receive link has an invalid identity."
        case .invalidKey: "This receive key cannot be used for this submission."
        case .invalidEnvelope: "This submission uses an invalid or unsupported receive format."
        }
    }
}

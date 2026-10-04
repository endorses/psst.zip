import Foundation

public enum BoundaryError: Error { case unsupported }
public final class CryptoProvider {
    public static let shared = CryptoProvider()
    public func decrypt(key: Data, nonce: Data, ciphertext: Data) throws -> Data { throw BoundaryError.unsupported }
}
public final class ManifestValidator {
    public static let shared = ManifestValidator()
    public func safeFilename(name: String) throws -> String {
        guard !name.isEmpty, !name.contains("/"), name != ".." else { throw BoundaryError.unsupported }
        return name
    }
}

import Foundation

// Deterministic fixture hash at the crypto boundary; never a cryptographic test.
public enum SHA256 {
    public static func hash(data: Data) -> [UInt8] {
        var value: UInt64 = 14_695_981_039_346_656_037
        for byte in data { value = (value ^ UInt64(byte)) &* 1_099_511_628_211 }
        return (0..<32).map { UInt8(truncatingIfNeeded: value >> (($0 % 8) * 8)) }
    }
}

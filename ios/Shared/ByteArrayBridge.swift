import Foundation
import Shared

// MARK: - Data / KotlinByteArray bridging

extension Data {
    func toKotlinByteArray() -> KotlinByteArray {
        let bytes = KotlinByteArray(size: Int32(count))
        withUnsafeBytes { rawBuffer in
            guard let baseAddress = rawBuffer.baseAddress else { return }
            let pointer = baseAddress.assumingMemoryBound(to: Int8.self)
            for i in 0 ..< count {
                bytes.set(index: Int32(i), value: pointer[i])
            }
        }
        return bytes
    }
}

extension KotlinByteArray {
    func toData() -> Data {
        var bytes = [UInt8](repeating: 0, count: Int(size))
        for i in 0 ..< Int(size) {
            bytes[i] = UInt8(bitPattern: get(index: Int32(i)))
        }
        return Data(bytes)
    }
}

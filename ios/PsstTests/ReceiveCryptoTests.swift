import Foundation
@testable import Psst
import XCTest

final class ReceiveCryptoTests: XCTestCase {
    private let slot = "00112233-4455-6677-8899-aabbccddeeff"
    private let transfer = "ffeeddcc-bbaa-9988-7766-554433221100"

    func testIndependentProviderFixtures() throws {
        #if SWIFT_PACKAGE
            let bundle = Bundle.module
        #else
            let bundle = Bundle(for: ReceiveCryptoTests.self)
        #endif
        let url = try XCTUnwrap(bundle.url(forResource: "hpke-receive-v2", withExtension: "json"))
        let fixture = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        func bytes(_ field: String) throws -> Data {
            let hex = try XCTUnwrap(fixture[field] as? String)
            guard hex.count % 2 == 0 else { throw ReceiveCryptoError.invalidEnvelope }
            return try Data(stride(from: 0, to: hex.count, by: 2).map { offset in
                let start = hex.index(hex.startIndex, offsetBy: offset)
                return try XCTUnwrap(UInt8(hex[start ..< hex.index(start, offsetBy: 2)], radix: 16))
            })
        }
        let slot = try XCTUnwrap(fixture["slot_id"] as? String)
        let transfer = try XCTUnwrap(fixture["transfer_id"] as? String)
        XCTAssertEqual(try ReceiveCrypto.info(slotID: slot, transferID: transfer, publicKey: bytes("public_key")), try bytes("context_info"))
        for field in ["wrapped_key", "tink_wrapped_key", "swift_wrapped_key"] {
            XCTAssertEqual(try ReceiveCrypto.openSubmissionKey(bytes(field), privateKey: bytes("private_key"), publicKey: bytes("public_key"), slotID: slot, transferID: transfer), try bytes("submission_key"))
        }
    }

    func testIndependentSubmissionKeysAndContextBinding() throws {
        let keys = ReceiveCrypto.generateKeyPair()
        let first = Data(repeating: 1, count: 32)
        let second = Data(repeating: 2, count: 32)
        let wrapped = try ReceiveCrypto.sealSubmissionKey(first, publicKey: keys.publicKey, slotID: slot, transferID: transfer)
        let other = try ReceiveCrypto.sealSubmissionKey(second, publicKey: keys.publicKey, slotID: slot, transferID: transfer)
        XCTAssertEqual(wrapped.count, 80)
        XCTAssertNotEqual(wrapped, other)
        XCTAssertEqual(try ReceiveCrypto.openSubmissionKey(wrapped, privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer), first)
        XCTAssertEqual(try ReceiveCrypto.openSubmissionKey(other, privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer), second)
        XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(wrapped, privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: transfer, transferID: slot))
        let wrong = ReceiveCrypto.generateKeyPair()
        XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(wrapped, privateKey: wrong.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer))
        XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(wrapped, privateKey: wrong.privateKey, publicKey: wrong.publicKey, slotID: slot, transferID: transfer))
        var changed = wrapped
        changed[79] ^= 1
        XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(changed, privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer))
        changed = wrapped
        changed[0] ^= 1
        XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(changed, privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer))
    }

    func testLowOrderRecipientAndEncapsulationAreRejected() throws {
        let keys = ReceiveCrypto.generateKeyPair()
        let lowOrderHex = [
            String(repeating: "00", count: 32), "01" + String(repeating: "00", count: 31),
            "e0eb7a7c3b41b8ae1656e3faf19fc46ada098deb9c32b1fd866205165f49b800",
            "5f9c95bca3508c24b1d0b1559c83ef5b04445cc4581c8e86d8224eddd09f1157",
            "ec" + String(repeating: "ff", count: 30) + "7f",
            "ed" + String(repeating: "ff", count: 30) + "7f",
            "ee" + String(repeating: "ff", count: 30) + "7f",
        ]
        for hex in lowOrderHex {
            let bytes = Data(stride(from: 0, to: 64, by: 2).map { offset in
                let start = hex.index(hex.startIndex, offsetBy: offset)
                return UInt8(hex[start ..< hex.index(start, offsetBy: 2)], radix: 16)!
            })
            for highBit in [UInt8(0), 0x80] {
                var point = bytes
                point[31] |= highBit
                XCTAssertThrowsError(try ReceiveCrypto.sealSubmissionKey(Data(repeating: 7, count: 32), publicKey: point, slotID: slot, transferID: transfer))
                XCTAssertThrowsError(try ReceiveCrypto.openSubmissionKey(point + Data(repeating: 0, count: 48), privateKey: keys.privateKey, publicKey: keys.publicKey, slotID: slot, transferID: transfer))
            }
        }
    }

    func testEnvelopeIsVersionedAndBounded() throws {
        let wrapped = Data(repeating: 3, count: 80), manifest = Data(repeating: 4, count: 28)
        let encoded = try ReceiveCrypto.encodeEnvelope(wrappedKey: wrapped, encryptedManifest: manifest)
        XCTAssertEqual(encoded.count, 116)
        XCTAssertEqual(try ReceiveCrypto.decodeEnvelope(encoded).wrappedKey, wrapped)
        XCTAssertEqual(try ReceiveCrypto.decodeEnvelope(encoded).encryptedManifest, manifest)
        XCTAssertThrowsError(try ReceiveCrypto.decodeEnvelope(Data(encoded.dropLast())))
        XCTAssertThrowsError(try ReceiveCrypto.decodeEnvelope(Data(repeating: 0, count: 116)))
        XCTAssertThrowsError(try ReceiveCrypto.encodeEnvelope(wrappedKey: wrapped, encryptedManifest: Data(repeating: 0, count: ReceiveCrypto.maximumEnvelopeBytes)))
        XCTAssertThrowsError(try ReceiveCrypto.info(slotID: "bad", transferID: transfer, publicKey: Data(repeating: 0, count: 32)))
        XCTAssertThrowsError(try ReceiveCrypto.sealSubmissionKey(Data(repeating: 0, count: 32), publicKey: Data(repeating: 0, count: 32), slotID: slot, transferID: transfer))
    }
}

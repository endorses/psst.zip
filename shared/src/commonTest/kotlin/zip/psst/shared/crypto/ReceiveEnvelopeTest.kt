package zip.psst.shared.crypto

import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class ReceiveEnvelopeTest {
    private val slot = "00112233-4455-6677-8899-aabbccddeeff"
    private val transfer = "ffeeddcc-bbaa-9988-7766-554433221100"

    @Test
    fun contextHasFrozenSuiteAndCanonicalUuidEncoding() {
        val key = ByteArray(32) { it.toByte() }
        val expected =
            "707373742e7a69702f726563656976652d6b65792f763200002000010002" +
                "00112233445566778899aabbccddeeffffeeddccbbaa99887766554433221100" +
                "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
        assertEquals(
            expected,
            ReceiveEnvelope.info(slot, transfer, key).joinToString("") {
                (it.toInt() and 255).toString(16).padStart(2, '0')
            },
        )
        for (invalid in listOf(slot.uppercase(), slot.replace("-", ""), "../$slot", "")) {
            assertFailsWith<IllegalArgumentException> {
                ReceiveEnvelope.info(invalid, transfer, key)
            }
            assertFailsWith<IllegalArgumentException> { ReceiveEnvelope.info(slot, invalid, key) }
        }
        assertFailsWith<IllegalArgumentException> {
            ReceiveEnvelope.info(slot, transfer, ByteArray(31))
        }
    }

    @Test
    fun envelopeIsBoundedAndRequiresExactVersionAndWrappedLength() {
        val wrapped = ByteArray(80) { it.toByte() }
        val cipher = ByteArray(28) { (it + 1).toByte() }
        val encoded = ReceiveEnvelope.encode(wrapped, cipher)
        assertEquals(116, encoded.size)
        assertEquals("PSSTRCV2", encoded.copyOfRange(0, 8).decodeToString())
        val parsed = ReceiveEnvelope.decode(encoded)
        assertContentEquals(wrapped, parsed.wrappedKey)
        assertContentEquals(cipher, parsed.encryptedManifest)
        ReceiveEnvelope.decode(
            ReceiveEnvelope.encode(wrapped, ByteArray(ReceiveEnvelope.MAX_BYTES - 88))
        )
        for (invalid in
            listOf(
                encoded.copyOf(115),
                ByteArray(ReceiveEnvelope.MAX_BYTES + 1),
                encoded.copyOf().also { it[7] = '1'.code.toByte() },
            )) {
            assertFailsWith<IllegalArgumentException> { ReceiveEnvelope.decode(invalid) }
        }
        assertFailsWith<IllegalArgumentException> { ReceiveEnvelope.encode(ByteArray(79), cipher) }
        assertFailsWith<IllegalArgumentException> { ReceiveEnvelope.encode(wrapped, ByteArray(27)) }
        assertFailsWith<IllegalArgumentException> {
            ReceiveEnvelope.encode(wrapped, ByteArray(ReceiveEnvelope.MAX_BYTES - 87))
        }
    }
}

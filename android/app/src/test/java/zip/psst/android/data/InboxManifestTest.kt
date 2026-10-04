package zip.psst.android.data

import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.crypto.ReceiveEnvelope
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
import kotlin.io.encoding.Base64
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class InboxManifestTest {
    private val slot = "11111111-1111-1111-1111-111111111111"
    private val child = "22222222-2222-2222-2222-222222222222"
    private val base64 = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)

    @Test
    fun unsupportedServersCannotSilentlyDiscardRequestedLinkProtections() {
        val publicKey = base64.encode(ByteArray(32))
        val slot =
            zip.psst.shared.model.DropSlot(
                this.slot,
                receiveProtocol = 2,
                recipientPublicKey = publicKey,
                maxFiles = 3,
            )
        verifyReceivePolicy(slot, publicKey, 3)
        for (bad in
            listOf(
                slot.copy(receiveProtocol = 1),
                slot.copy(recipientPublicKey = "other"),
                slot.copy(maxFiles = 0),
            )) {
            assertThrows(UnsupportedLinkPolicyException::class.java) {
                verifyReceivePolicy(bad, publicKey, 3)
            }
        }
        verifyDownloadPolicy(zip.psst.shared.model.Transfer(child, maxDownloads = 2), 2)
        assertThrows(UnsupportedLinkPolicyException::class.java) {
            verifyDownloadPolicy(zip.psst.shared.model.Transfer(child), 2)
        }
    }

    private val file =
        FileMetadata(
            "hello.txt",
            7,
            blobId = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
            encoding = "chunked-v1",
            chunkSize = 4194304,
            encryptionId = "a".repeat(32),
        )

    @Test
    fun ownerRecoversFreshSubmissionKeyAndOtherSendersCannot() {
        val pair = AndroidReceiveCrypto.generateKeyPair()
        val key = CryptoProvider.generateKey()
        val row =
            TransferHistoryEntity(
                slot,
                "received",
                0,
                0,
                "https://server.test",
                "v2.${base64.encode(pair.publicKey)}",
                "waiting",
                accountId = "alice",
            )
        val nonce = CryptoProvider.generateNonce()
        val bytes =
            ReceiveEnvelope.encode(
                AndroidReceiveCrypto.sealSubmissionKey(pair.publicKey, slot, child, key),
                nonce +
                    CryptoProvider.encrypt(
                        key,
                        nonce,
                        Json.encodeToString(Manifest(listOf(file))).encodeToByteArray(),
                    ),
            )
        val decoded = decryptInboxManifest(row, child, bytes, pair.privateKey)
        assertArrayEquals(key, decoded.key)
        assertEquals(listOf(file), decoded.manifest.files)
        assertThrows(Exception::class.java) { decryptInboxManifest(row, child, bytes, null) }
        assertThrows(Exception::class.java) {
            decryptInboxManifest(row, child, bytes, pair.publicKey)
        }
        assertThrows(Exception::class.java) {
            decryptInboxManifest(
                row,
                child,
                bytes,
                AndroidReceiveCrypto.generateKeyPair().privateKey,
            )
        }
        assertThrows(Exception::class.java) {
            decryptInboxManifest(row, slot, bytes, pair.privateKey)
        }
        assertThrows(Exception::class.java) {
            decryptInboxManifest(row.copy(id = child), child, bytes, pair.privateKey)
        }
    }

    @Test
    fun keyStoreScopeSeparatesAccountsOriginsAndSlots() {
        val a = inboxKeyScope("https://server.test", "alice", slot)
        assertArrayEquals(a, inboxKeyScope("https://SERVER.test:443/", "alice", slot))
        assertFalse(a.contentEquals(inboxKeyScope("https://server.test", "bob", slot)))
        assertFalse(a.contentEquals(inboxKeyScope("https://other.test", "alice", slot)))
        assertFalse(a.contentEquals(inboxKeyScope("https://server.test", "alice", child)))
        assertThrows(IllegalArgumentException::class.java) {
            inboxKeyScope("https://server.test", "alice\u0000bob", slot)
        }
    }

    @Test
    fun optionalLimitsRejectOverflowAndInvalidInput() {
        assertEquals(0, optionalLinkLimit(""))
        assertEquals(1, optionalLinkLimit("1"))
        assertEquals(Int.MAX_VALUE, optionalLinkLimit("2147483647"))
        for (value in listOf("0", "-1", "1.5", "2147483648", "99999999999999", "a")) assertThrows(
            IllegalArgumentException::class.java
        ) {
            optionalLinkLimit(value)
        }
    }
}

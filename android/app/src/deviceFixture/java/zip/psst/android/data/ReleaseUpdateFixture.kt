package zip.psst.android.data

import android.content.Context
import android.os.Build
import android.os.Bundle
import android.os.Process
import android.util.Base64
import java.io.File
import kotlinx.coroutines.runBlocking
import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.crypto.CryptoProvider

/**
 * One seed/read fixture executed across actual APK installations. It deliberately uses the real
 * Android Keystore, Room, guest index and HPKE/AES implementations in the installed target APK.
 * Nothing contacts a server, and no real account, capability or private transfer is used.
 */
object ReleaseUpdateFixture {
    @JvmStatic
    fun run(context: Context, arguments: Bundle) {
        ClientStateFixture(context, arguments).apply {
            requireDisposableEmulator()
            preservesClientState()
        }
    }
}

private class ClientStateFixture(private val context: Context, private val arguments: Bundle) {
    private val state
        get() = context.getSharedPreferences("release-update-device-fixture", 0)

    private val origin = "https://release-update.invalid"
    private val account = "disposable-release-owner"
    private val sendId = "11111111-1111-4111-8111-111111111111"
    private val slotId = "22222222-2222-4222-8222-222222222222"
    private val submissionId = "33333333-3333-4333-8333-333333333333"
    private val sendKey = ByteArray(32) { (it + 1).toByte() }
    private val guestKey = ByteArray(32) { (it + 42).toByte() }
    private val submissionKey = ByteArray(32) { (it + 80).toByte() }
    private val payload = "Disposable optimized APK update fixture".encodeToByteArray()

    fun requireDisposableEmulator() {
        check(arguments.getString("disposableUpdate") == "true") {
            "Explicit disposableUpdate=true is required"
        }
        check(Build.HARDWARE in setOf("ranchu", "goldfish")) {
            "Update fixtures require a disposable emulator"
        }
        check(context.packageName == "zip.psst.android")
    }

    fun preservesClientState() = runBlocking {
        val phase = arguments.getString("updatePhase")
        check(phase in setOf("seed", "verify")) { "Choose seed or verify" }
        val database = AppDatabase.create(context)
        val guests = GuestDownloadStore(context)
        try {
            if (phase == "seed") {
                check(!state.contains("seedUid")) { "Never overwrite an existing fixture" }
                assertTrue(
                    database
                        .transferHistoryDao()
                        .deviceHistoryPage(account, origin, "", Long.MAX_VALUE, "\uffff")
                        .isEmpty(),
                )
                SessionStorage(context)
                    .save(
                        origin,
                        "disposable-user",
                        "synthetic-session-not-a-real-credential",
                        account,
                        "user",
                    )
                val pair = AndroidReceiveCrypto.generateKeyPair()
                val marker = "v2." + encode(pair.publicKey)
                val send =
                    TransferHistoryEntity(
                        sendId,
                        "sent",
                        1,
                        payload.size.toLong(),
                        origin,
                        encode(sendKey),
                        "complete",
                        accountId = account,
                        title = "Retained Send history",
                    )
                val receive =
                    TransferHistoryEntity(
                        slotId,
                        "received",
                        1,
                        payload.size.toLong(),
                        origin,
                        marker,
                        "has_uploads",
                        accountId = account,
                        title = "Retained Receive history",
                    )
                database.transferHistoryDao().insert(send)
                database.transferHistoryDao().insert(receive)
                InboxKeyStore(context).save(origin, account, slotId, pair.privateKey)
                File(context.filesDir, "release-update-wrapped-key.bin")
                    .writeBytes(
                        AndroidReceiveCrypto.sealSubmissionKey(
                            pair.publicKey,
                            slotId,
                            submissionId,
                            submissionKey,
                        ),
                    )
                guests.open(origin, sendId, guestKey).let {
                    guests.save(it.copy(sharedTitle = "Retained guest history"))
                }
                saveCiphertext("send", sendKey)
                saveCiphertext("receive", submissionKey)
                saveCiphertext("guest", guestKey)
                assertTrue(state.edit().putInt("seedUid", Process.myUid()).commit())
            } else {
                assertTrue("Seed the previous installation first", state.contains("seedUid"))
                assertEquals(
                    "APK updates must preserve the target UID",
                    state.getInt("seedUid", -1),
                    Process.myUid(),
                )
            }
            val sessions = SessionStorage(context)
            assertEquals("synthetic-session-not-a-real-credential", sessions.token(origin))
            assertEquals(account, sessions.accountId())
            assertEquals("disposable-user", sessions.username())
            assertFalse(sessions.isAdmin())
            val send = requireNotNull(database.transferHistoryDao().getById(sendId))
            val receive = requireNotNull(database.transferHistoryDao().getById(slotId))
            assertEquals("Retained Send history", send.title)
            assertEquals("Retained Receive history", receive.title)
            assertEquals(account, send.accountId)
            val retainedSendKey = decode(send.encryptionKey)
            assertArrayEquals(sendKey, retainedSendKey)
            decryptCiphertext("send", retainedSendKey)
            val privateKey = requireNotNull(InboxKeyStore(context).read(receive))
            val publicKey = decode(receive.encryptionKey.removePrefix("v2."))
            val retainedSubmissionKey =
                AndroidReceiveCrypto.openSubmissionKey(
                    privateKey,
                    publicKey,
                    slotId,
                    submissionId,
                    File(context.filesDir, "release-update-wrapped-key.bin").readBytes(),
                )
            assertArrayEquals(submissionKey, retainedSubmissionKey)
            decryptCiphertext("receive", retainedSubmissionKey)
            val guestId = GuestDownloadStore.identity(origin, sendId)
            assertEquals("Retained guest history", guests.read(guestId).sharedTitle)
            val retainedGuestKey = guests.readKey(guestId)
            assertArrayEquals(guestKey, retainedGuestKey)
            decryptCiphertext("guest", retainedGuestKey)
        } finally {
            guests.close()
            database.close()
        }
    }

    private fun saveCiphertext(name: String, key: ByteArray) {
        val nonce = CryptoProvider.generateNonce()
        File(context.filesDir, "release-update-$name.bin")
            .writeBytes(
                nonce + CryptoProvider.encrypt(key, nonce, payload),
            )
    }

    private fun decryptCiphertext(name: String, key: ByteArray) {
        val bytes = File(context.filesDir, "release-update-$name.bin").readBytes()
        assertArrayEquals(
            payload,
            CryptoProvider.decrypt(
                key,
                bytes.copyOfRange(0, 12),
                bytes.copyOfRange(12, bytes.size),
            ),
        )
    }

    private fun encode(bytes: ByteArray): String =
        Base64.encodeToString(bytes, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)

    private fun decode(value: String): ByteArray =
        Base64.decode(value, Base64.URL_SAFE or Base64.NO_WRAP)

    private fun assertTrue(value: Boolean) {
        check(value) { "Protected state mismatch" }
    }

    private fun assertTrue(message: String, value: Boolean) {
        check(value) { message }
    }

    private fun assertFalse(value: Boolean) = assertTrue(!value)

    private fun assertEquals(expected: Any?, actual: Any?) = assertTrue(expected == actual)

    private fun assertEquals(message: String, expected: Any?, actual: Any?) =
        assertTrue(message, expected == actual)

    private fun assertArrayEquals(expected: ByteArray, actual: ByteArray) =
        assertTrue(expected.contentEquals(actual))
}

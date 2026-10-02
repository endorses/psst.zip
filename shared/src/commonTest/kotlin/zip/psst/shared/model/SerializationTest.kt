package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json

class SerializationTest {
    private val json = Json {
        ignoreUnknownKeys = true
        isLenient = true
    }

    @Test
    fun transferSerializationRoundTrip() {
        val transfer =
            Transfer(
                id = "xfer-1",
                fileCount = 3,
                totalSize = 1024,
                status = TransferStatus.COMPLETE,
                expiresAt = "2026-01-01T00:00:00Z",
                createdAt = "2025-12-31T00:00:00Z",
            )
        val jsonStr = json.encodeToString(Transfer.serializer(), transfer)
        val decoded = json.decodeFromString(Transfer.serializer(), jsonStr)
        assertEquals(transfer, decoded)
    }

    @Test
    fun transferDeserialization() {
        val jsonStr =
            """
            {
                "id": "abc",
                "file_count": 2,
                "total_size": 500,
                "status": "pending",
                "expires_at": null
            }
            """
                .trimIndent()
        val transfer = json.decodeFromString(Transfer.serializer(), jsonStr)
        assertEquals("abc", transfer.id)
        assertEquals(2, transfer.fileCount)
        assertEquals(500L, transfer.totalSize)
        assertEquals(TransferStatus.PENDING, transfer.status)
    }

    @Test
    fun transferStatusValues() {
        val jsonPending = """{"id":"a","status":"pending"}"""
        val jsonComplete = """{"id":"b","status":"complete"}"""
        val jsonExpired = """{"id":"c","status":"expired"}"""

        assertEquals(
            TransferStatus.PENDING,
            json.decodeFromString(Transfer.serializer(), jsonPending).status,
        )
        assertEquals(
            TransferStatus.COMPLETE,
            json.decodeFromString(Transfer.serializer(), jsonComplete).status,
        )
        assertEquals(
            TransferStatus.EXPIRED,
            json.decodeFromString(Transfer.serializer(), jsonExpired).status,
        )
    }

    @Test
    fun dropSlotSerializationRoundTrip() {
        val slot =
            DropSlot(
                id = "slot-1",
                status = DropSlotStatus.HAS_UPLOADS,
                transfers = listOf(SlotTransfer("transfer-1", TransferStatus.COMPLETE, 5)),
                expiresAt = "2026-01-01T00:00:00Z",
            )
        val jsonStr = json.encodeToString(DropSlot.serializer(), slot)
        val decoded = json.decodeFromString(DropSlot.serializer(), jsonStr)
        assertEquals(slot, decoded)
    }

    @Test
    fun dropSlotDeserialization() {
        val jsonStr =
            """
            {
                "id": "slot-x",
                "status": "waiting",
                "file_count": 0
            }
            """
                .trimIndent()
        val slot = json.decodeFromString(DropSlot.serializer(), jsonStr)
        assertEquals("slot-x", slot.id)
        assertEquals(DropSlotStatus.WAITING, slot.status)
        assertEquals(0, slot.fileCount)
    }

    @Test
    fun slotUsesCompletedTransferIdsAndIgnoresPendingUploads() {
        val slot =
            json.decodeFromString<DropSlot>(
                """
                {"id":"slot-1","status":"has_uploads","transfers":[
                    {"transfer_id":"pending-1","status":"pending","file_count":9},
                    {"transfer_id":"complete-1","status":"complete","file_count":2}
                ]}
                """
                    .trimIndent()
            )
        assertEquals(listOf("complete-1"), slot.completedTransfers.map { it.transferId })
        assertEquals(2, slot.fileCount)
    }

    @Test
    fun fileMetadataSerializationRoundTrip() {
        val meta =
            FileMetadata(
                name = "photo.jpg",
                size = 2048,
                mimeType = "image/jpeg",
                blobId = "blob-1",
            )
        val jsonStr = json.encodeToString(FileMetadata.serializer(), meta)
        val decoded = json.decodeFromString(FileMetadata.serializer(), jsonStr)
        assertEquals(meta, decoded)
        assertTrue(jsonStr.contains("\"mime_type\":\"image/jpeg\""))
        assertTrue(jsonStr.contains("\"blob_id\":\"blob-1\""))
    }

    @Test
    fun manifestSerializationRoundTrip() {
        val manifest =
            Manifest(
                files =
                    listOf(
                        FileMetadata(name = "a.txt", size = 100),
                        FileMetadata(name = "b.png", size = 200, mimeType = "image/png"),
                    )
            )
        val jsonStr = json.encodeToString(Manifest.serializer(), manifest)
        val decoded = json.decodeFromString(Manifest.serializer(), jsonStr)
        assertEquals(manifest, decoded)
    }

    @Test
    fun encryptedManifestToBytesAndBack() {
        val nonce = ByteArray(12) { it.toByte() }
        val ciphertext = ByteArray(64) { (it + 100).toByte() }
        val em = EncryptedManifest(ciphertext = ciphertext, nonce = nonce)

        val bytes = em.toBytes()
        assertEquals(12 + 64, bytes.size)

        val restored = EncryptedManifest.fromBytes(bytes)
        assertEquals(em, restored)
    }

    @Test
    fun serverConfigNormalization() {
        val config = ServerConfig(baseUrl = "https://example.com/")
        assertEquals("https://example.com", config.normalizedBaseUrl)
        assertEquals("https://example.com/api/v1", config.apiBaseUrl)
    }

    @Test
    fun serverConfigNoTrailingSlash() {
        val config = ServerConfig(baseUrl = "https://example.com")
        assertEquals("https://example.com", config.normalizedBaseUrl)
        assertEquals("https://example.com/api/v1", config.apiBaseUrl)
    }
}

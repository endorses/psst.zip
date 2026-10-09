package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ManifestValidatorTest {
    private val id = "01234567-89ab-cdef-0123-456789abcdef"

    private fun entry(name: String = "photo.jpg", size: Long = 12, blobId: String = id) =
        FileMetadata(
            name,
            size,
            "image/jpeg",
            blobId,
            "chunked-v1",
            4194304,
            blobId.replace("-", ""),
        )

    @Test
    fun removesAllHiddenDirectionControlsBeforeDisplayAndSave() {
        for (codepoint in listOf(0x061C, 0x200E, 0x200F, 0x202A, 0x202E, 0x2066, 0x2069)) {
            val control = codepoint.toChar()
            assertEquals(
                "photo_.jpg.exe",
                ManifestValidator.safeFilename("photo${control}.jpg.exe"),
            )
        }
        assertEquals("صورة.jpg", ManifestValidator.safeFilename("صورة.jpg"))
    }

    @Test
    fun perFilePoliciesMustMatchTheAuthenticatedManifest() {
        val manifest = Manifest(listOf(entry()))
        val file = TransferFile(id, 72, 1, 1)
        val transfer =
            Transfer(id, 1, 72, TransferStatus.COMPLETE, maxDownloads = 2, files = listOf(file))
        assertEquals(12L, ManifestValidator.validateForTransfer(manifest, transfer))
        for (invalid in
            listOf(
                transfer.copy(files = emptyList()),
                transfer.copy(files = listOf(file, file)),
                transfer.copy(
                    files = listOf(file.copy(id = "11234567-89ab-cdef-0123-456789abcdef")),
                ),
                transfer.copy(files = listOf(file.copy(size = 71))),
                transfer.copy(files = listOf(file.copy(downloadCount = -1))),
                transfer.copy(files = listOf(file.copy(remainingDownloads = -1))),
                transfer.copy(files = listOf(file.copy(remainingDownloads = 3))),
            )) assertFailsWith<IllegalArgumentException> {
            ManifestValidator.validateForTransfer(manifest, invalid)
        }
        val unknown = file.copy(downloadCount = null, remainingDownloads = null)
        assertEquals(
            12L,
            ManifestValidator.validateForTransfer(manifest, transfer.copy(files = listOf(unknown))),
        )
        kotlin.test.assertNull(unknown.remainingDownloads)
    }

    @Test
    fun validatesEncryptedSizeAndAllowsDuplicateFilenames() {
        val manifest =
            Manifest(listOf(entry(), entry(blobId = "11234567-89ab-cdef-0123-456789abcdef")))
        assertEquals(
            24L,
            ManifestValidator.validateForTransfer(
                manifest,
                Transfer(id, 2, 144, TransferStatus.COMPLETE),
            ),
        )
        assertFailsWith<IllegalArgumentException> {
            ManifestValidator.validateForTransfer(
                manifest,
                Transfer(id, 2, 24, TransferStatus.COMPLETE),
            )
        }
        assertFailsWith<IllegalArgumentException> {
            ManifestValidator.validateForTransfer(
                manifest,
                Transfer(id, 1, 144, TransferStatus.COMPLETE),
            )
        }
        assertFailsWith<IllegalArgumentException> {
            ManifestValidator.validateForTransfer(
                manifest,
                Transfer(id, 2, 144, TransferStatus.PENDING),
            )
        }
    }

    @Test
    fun rejectsUnsafeEntriesBeforeBlobRequests() {
        for (name in
            listOf(
                "../photo.jpg",
                "folder/photo.jpg",
                "folder\\photo.jpg",
                "..",
                ".",
                "  ",
                "name\u0000jpg",
            )) {
            assertFailsWith<IllegalArgumentException> {
                ManifestValidator.validate(Manifest(listOf(entry(name))))
            }
        }
        for (size in
            listOf(
                -1L,
                zip.psst.shared.crypto.ChunkedFileCrypto.MAX_FILE_SIZE + 1,
                Long.MAX_VALUE,
            )) {
            assertFailsWith<IllegalArgumentException> {
                ManifestValidator.validate(Manifest(listOf(entry(size = size))))
            }
        }
        for (manifest in
            listOf(
                Manifest(emptyList()),
                Manifest(listOf(entry(), entry())),
                Manifest(listOf(entry(blobId = "../evil"))),
                Manifest(List(101) { entry() }),
            )) {
            assertFailsWith<IllegalArgumentException> { ManifestValidator.validate(manifest) }
        }
    }

    @Test
    fun sanitizesPortableNamesAndRetainsExtension() {
        assertEquals("hello_world_.txt", ManifestValidator.safeFilename("hello:world?.txt"))
        assertEquals(
            "hello_world.txt",
            ManifestValidator.safeFilename("hello" + 0x202E.toChar() + "world.txt"),
        )
        val shortened = ManifestValidator.safeFilename("😀".repeat(100) + ".jpg")
        assertTrue(shortened.encodeToByteArray().size <= 200)
        assertTrue(shortened.endsWith(".jpg"))
        assertEquals(0L, ManifestValidator.validate(Manifest(listOf(entry(size = 0)))))
    }
}

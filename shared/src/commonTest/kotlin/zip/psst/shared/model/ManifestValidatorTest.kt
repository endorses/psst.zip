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
        assertEquals("hello_world.txt", ManifestValidator.safeFilename("hello\u202Eworld.txt"))
        val shortened = ManifestValidator.safeFilename("😀".repeat(100) + ".jpg")
        assertTrue(shortened.encodeToByteArray().size <= 200)
        assertTrue(shortened.endsWith(".jpg"))
        assertEquals(0L, ManifestValidator.validate(Manifest(listOf(entry(size = 0)))))
    }
}

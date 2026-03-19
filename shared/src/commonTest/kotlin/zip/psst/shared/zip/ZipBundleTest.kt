package zip.psst.shared.zip

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ZipBundleTest {
    @Test
    fun createAndExtractSingleFile() {
        val entries = listOf(
            ZipEntry("hello.txt", "Hello, world!".encodeToByteArray()),
        )
        val zipData = ZipBundle.create(entries)
        val extracted = ZipBundle.extract(zipData)

        assertEquals(1, extracted.size)
        assertEquals("hello.txt", extracted[0].name)
        assertEquals("Hello, world!", extracted[0].data.decodeToString())
    }

    @Test
    fun createAndExtractMultipleFiles() {
        val entries = listOf(
            ZipEntry("file1.txt", "Content one".encodeToByteArray()),
            ZipEntry("file2.txt", "Content two".encodeToByteArray()),
            ZipEntry("subdir/file3.bin", ByteArray(256) { it.toByte() }),
        )
        val zipData = ZipBundle.create(entries)
        val extracted = ZipBundle.extract(zipData)

        assertEquals(3, extracted.size)
        assertEquals("file1.txt", extracted[0].name)
        assertEquals("Content one", extracted[0].data.decodeToString())
        assertEquals("file2.txt", extracted[1].name)
        assertEquals("Content two", extracted[1].data.decodeToString())
        assertEquals("subdir/file3.bin", extracted[2].name)
        assertEquals(256, extracted[2].data.size)
        assertEquals(0.toByte(), extracted[2].data[0])
        assertEquals(255.toByte(), extracted[2].data[255])
    }

    @Test
    fun createAndExtractEmptyFile() {
        val entries = listOf(
            ZipEntry("empty.dat", ByteArray(0)),
        )
        val zipData = ZipBundle.create(entries)
        val extracted = ZipBundle.extract(zipData)

        assertEquals(1, extracted.size)
        assertEquals("empty.dat", extracted[0].name)
        assertEquals(0, extracted[0].data.size)
    }

    @Test
    fun createAndExtractBinaryData() {
        val binaryData = ByteArray(1024) { (it * 37 + 13).toByte() }
        val entries = listOf(
            ZipEntry("binary.bin", binaryData),
        )
        val zipData = ZipBundle.create(entries)
        val extracted = ZipBundle.extract(zipData)

        assertEquals(1, extracted.size)
        assertEquals(binaryData.toList(), extracted[0].data.toList())
    }

    @Test
    fun zipEntryEquality() {
        val data = "test".encodeToByteArray()
        val a = ZipEntry("a.txt", data)
        val b = ZipEntry("a.txt", data.copyOf())
        assertEquals(a, b)
        assertEquals(a.hashCode(), b.hashCode())
    }

    @Test
    fun createProducesValidZipBytes() {
        val entries = listOf(ZipEntry("test.txt", "data".encodeToByteArray()))
        val zipData = ZipBundle.create(entries)

        // ZIP files start with PK\x03\x04
        assertTrue(zipData.size > 4)
        assertEquals(0x50, zipData[0].toInt() and 0xFF) // 'P'
        assertEquals(0x4B, zipData[1].toInt() and 0xFF) // 'K'
    }
}

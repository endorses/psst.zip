package zip.psst.shared.zip

/**
 * Minimal ZIP implementation for iOS that avoids large memory allocations.
 *
 * This uses a pure-Kotlin STORE-only (no compression) ZIP writer/reader,
 * which is sufficient for our use case since the data is already encrypted
 * (and thus incompressible). This keeps memory usage low and predictable,
 * which is critical for iOS share extensions (~120 MB limit).
 */
actual object ZipBundle {

    actual fun create(entries: List<ZipEntry>): ByteArray {
        return MinimalZip.create(entries)
    }

    actual fun extract(zipData: ByteArray): List<ZipEntry> {
        return MinimalZip.extract(zipData)
    }
}

/**
 * Pure-Kotlin minimal ZIP format writer/reader.
 * Supports STORE method only (no compression) for minimal memory footprint.
 */
internal object MinimalZip {
    private const val LOCAL_FILE_HEADER_SIG = 0x04034b50
    private const val CENTRAL_DIR_HEADER_SIG = 0x02014b50
    private const val END_OF_CENTRAL_DIR_SIG = 0x06054b50
    private const val STORE_METHOD: Short = 0

    fun create(entries: List<ZipEntry>): ByteArray {
        val buffer = ByteArrayBuffer()
        val centralDirEntries = mutableListOf<CentralDirEntry>()

        // Write local file headers + data
        for (entry in entries) {
            val offset = buffer.position
            val nameBytes = entry.name.encodeToByteArray()
            val crc = crc32(entry.data)

            // Local file header
            buffer.writeInt(LOCAL_FILE_HEADER_SIG)
            buffer.writeShort(20) // version needed
            buffer.writeShort(0)  // flags
            buffer.writeShort(STORE_METHOD.toInt()) // method (STORE)
            buffer.writeShort(0)  // mod time
            buffer.writeShort(0)  // mod date
            buffer.writeInt(crc.toInt())
            buffer.writeInt(entry.data.size) // compressed size
            buffer.writeInt(entry.data.size) // uncompressed size
            buffer.writeShort(nameBytes.size)
            buffer.writeShort(0)  // extra field length
            buffer.writeBytes(nameBytes)
            buffer.writeBytes(entry.data)

            centralDirEntries.add(
                CentralDirEntry(nameBytes, crc, entry.data.size, offset),
            )
        }

        // Write central directory
        val centralDirOffset = buffer.position

        for (cde in centralDirEntries) {
            buffer.writeInt(CENTRAL_DIR_HEADER_SIG)
            buffer.writeShort(20) // version made by
            buffer.writeShort(20) // version needed
            buffer.writeShort(0)  // flags
            buffer.writeShort(STORE_METHOD.toInt())
            buffer.writeShort(0)  // mod time
            buffer.writeShort(0)  // mod date
            buffer.writeInt(cde.crc.toInt())
            buffer.writeInt(cde.size) // compressed size
            buffer.writeInt(cde.size) // uncompressed size
            buffer.writeShort(cde.nameBytes.size)
            buffer.writeShort(0) // extra field length
            buffer.writeShort(0) // comment length
            buffer.writeShort(0) // disk number
            buffer.writeShort(0) // internal attrs
            buffer.writeInt(0)   // external attrs
            buffer.writeInt(cde.localHeaderOffset)
            buffer.writeBytes(cde.nameBytes)
        }

        val centralDirSize = buffer.position - centralDirOffset

        // End of central directory record
        buffer.writeInt(END_OF_CENTRAL_DIR_SIG)
        buffer.writeShort(0) // disk number
        buffer.writeShort(0) // central dir disk
        buffer.writeShort(centralDirEntries.size)
        buffer.writeShort(centralDirEntries.size)
        buffer.writeInt(centralDirSize)
        buffer.writeInt(centralDirOffset)
        buffer.writeShort(0) // comment length

        return buffer.toByteArray()
    }

    fun extract(zipData: ByteArray): List<ZipEntry> {
        val entries = mutableListOf<ZipEntry>()
        var pos = 0

        while (pos + 4 <= zipData.size) {
            val sig = zipData.readIntLE(pos)
            if (sig != LOCAL_FILE_HEADER_SIG) break

            val compressedSize = zipData.readIntLE(pos + 18)
            val nameLen = zipData.readShortLE(pos + 26)
            val extraLen = zipData.readShortLE(pos + 28)

            val nameStart = pos + 30
            val name = zipData.copyOfRange(nameStart, nameStart + nameLen).decodeToString()

            val dataStart = nameStart + nameLen + extraLen
            val data = zipData.copyOfRange(dataStart, dataStart + compressedSize)

            if (!name.endsWith("/")) {
                entries.add(ZipEntry(name = name, data = data))
            }

            pos = dataStart + compressedSize
        }

        return entries
    }

    private data class CentralDirEntry(
        val nameBytes: ByteArray,
        val crc: UInt,
        val size: Int,
        val localHeaderOffset: Int,
    ) {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (other == null || other !is CentralDirEntry) return false
            return nameBytes.contentEquals(other.nameBytes) &&
                crc == other.crc && size == other.size &&
                localHeaderOffset == other.localHeaderOffset
        }

        override fun hashCode(): Int = localHeaderOffset
    }
}

/** CRC-32 computation (standard ZIP CRC). */
internal fun crc32(data: ByteArray): UInt {
    var crc = 0xFFFFFFFFu
    for (byte in data) {
        crc = crc xor (byte.toUInt() and 0xFFu)
        for (j in 0 until 8) {
            crc = if (crc and 1u != 0u) {
                (crc shr 1) xor 0xEDB88320u
            } else {
                crc shr 1
            }
        }
    }
    return crc xor 0xFFFFFFFFu
}

/** Minimal growable byte buffer. */
internal class ByteArrayBuffer {
    private var data = ByteArray(4096)
    var position = 0
        private set

    private fun ensureCapacity(needed: Int) {
        if (position + needed > data.size) {
            val newSize = maxOf(data.size * 2, position + needed)
            data = data.copyOf(newSize)
        }
    }

    fun writeBytes(bytes: ByteArray) {
        ensureCapacity(bytes.size)
        bytes.copyInto(data, position)
        position += bytes.size
    }

    fun writeInt(value: Int) {
        ensureCapacity(4)
        data[position++] = (value and 0xFF).toByte()
        data[position++] = ((value shr 8) and 0xFF).toByte()
        data[position++] = ((value shr 16) and 0xFF).toByte()
        data[position++] = ((value shr 24) and 0xFF).toByte()
    }

    fun writeShort(value: Int) {
        ensureCapacity(2)
        data[position++] = (value and 0xFF).toByte()
        data[position++] = ((value shr 8) and 0xFF).toByte()
    }

    fun toByteArray(): ByteArray = data.copyOf(position)
}

internal fun ByteArray.readIntLE(offset: Int): Int {
    return (this[offset].toInt() and 0xFF) or
        ((this[offset + 1].toInt() and 0xFF) shl 8) or
        ((this[offset + 2].toInt() and 0xFF) shl 16) or
        ((this[offset + 3].toInt() and 0xFF) shl 24)
}

internal fun ByteArray.readShortLE(offset: Int): Int {
    return (this[offset].toInt() and 0xFF) or
        ((this[offset + 1].toInt() and 0xFF) shl 8)
}

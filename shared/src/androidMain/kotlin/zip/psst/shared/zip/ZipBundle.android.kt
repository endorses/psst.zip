package zip.psst.shared.zip

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

actual object ZipBundle {
    actual fun create(entries: List<ZipEntry>): ByteArray {
        val baos = ByteArrayOutputStream()
        ZipOutputStream(baos).use { zos ->
            for (entry in entries) {
                val zipEntry = java.util.zip.ZipEntry(entry.name)
                zos.putNextEntry(zipEntry)
                zos.write(entry.data)
                zos.closeEntry()
            }
        }
        return baos.toByteArray()
    }

    actual fun extract(zipData: ByteArray): List<ZipEntry> {
        val entries = mutableListOf<ZipEntry>()
        ZipInputStream(ByteArrayInputStream(zipData)).use { zis ->
            var entry = zis.nextEntry
            while (entry != null) {
                if (!entry.isDirectory) {
                    val data = zis.readBytes()
                    entries.add(ZipEntry(name = entry.name, data = data))
                }
                zis.closeEntry()
                entry = zis.nextEntry
            }
        }
        return entries
    }
}

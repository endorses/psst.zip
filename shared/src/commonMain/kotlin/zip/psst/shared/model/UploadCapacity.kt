package zip.psst.shared.model

import zip.psst.shared.api.ClientFailureException
import zip.psst.shared.api.clientRequire
import zip.psst.shared.crypto.ChunkedFileCrypto
import kotlin.time.Clock
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Instant
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.buildClassSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.*

/** Advisory encrypted file bytes for one new submission, after reserving its manifest. */
@Serializable(with = UploadCapacitySerializer::class)
data class UploadCapacity(
    @SerialName("checked_at") val checkedAt: String,
    val state: String,
    val reason: String? = null,
    @SerialName("available_wire_bytes") val availableWireBytes: Long?,
    @SerialName("available_files") val availableFiles: Long?,
    @SerialName("manifest_reserve_bytes") val manifestReserveBytes: Long,
) {
    @Throws(Exception::class)
    fun validate() {
        clientRequire(
            checkedAt.endsWith("Z") && checkedAt.length <= 40,
            "receive_capacity_unavailable",
        ) {
            CAPACITY_RETRY
        }
        try {
            Instant.parse(checkedAt)
        } catch (_: IllegalArgumentException) {
            throw ClientFailureException(CAPACITY_RETRY, "receive_capacity_unavailable")
        }
        clientRequire(
            manifestReserveBytes in 1..TransferLimits.MAX_MANIFEST_BYTES.toLong(),
            "receive_capacity_unavailable",
        ) {
            CAPACITY_RETRY
        }
        when (state) {
            "ready" ->
                clientRequire(
                    reason == null &&
                        availableWireBytes != null &&
                        availableWireBytes in 60..(1L shl 50) &&
                        availableFiles != null &&
                        availableFiles in 1..TransferLimits.MAX_FILES.toLong() &&
                        availableFiles <= availableWireBytes / ChunkedFileCrypto.FRAME_OVERHEAD,
                    "receive_capacity_unavailable",
                ) {
                    CAPACITY_RETRY
                }
            "blocked" ->
                clientRequire(
                    reason in listOf("link_limit", "capacity_limit") &&
                        availableWireBytes == 0L &&
                        availableFiles == 0L,
                    "receive_capacity_unavailable",
                ) {
                    CAPACITY_RETRY
                }
            "unknown" ->
                clientRequire(
                    reason == "capacity_unavailable" &&
                        availableWireBytes == null &&
                        availableFiles == null,
                    "receive_capacity_unavailable",
                ) {
                    CAPACITY_RETRY
                }
            else -> throw ClientFailureException(CAPACITY_RETRY, "receive_capacity_unavailable")
        }
    }

    /** Refresh on each selection change and immediately before allocating a child transfer. */
    fun isFresh(): Boolean =
        try {
            val age = Clock.System.now() - Instant.parse(checkedAt)
            age >= -2.minutes && age <= 2.minutes
        } catch (_: Exception) {
            false
        }

    @Throws(Exception::class)
    fun validateSelection(fileCount: Int, totalWireBytes: Long) {
        validate()
        clientRequire(isFresh(), "receive_capacity_stale") {
            "Receive capacity is out of date. Refresh and try again; your files are still selected."
        }
        clientRequire(state != "unknown", "receive_capacity_unavailable") { CAPACITY_RETRY }
        clientRequire(
            state != "blocked",
            if (reason == "link_limit") "receive_file_limit" else "receive_capacity_exhausted",
        ) {
            if (reason == "link_limit")
                "This receive link cannot accept more files. Your files are still selected."
            else
                "This receive link has no upload capacity right now. Refresh and try again; your files are still selected."
        }
        clientRequire(
            fileCount in 0..TransferLimits.MAX_FILES && totalWireBytes >= 0,
            "invalid_upload_selection",
        ) {
            "Invalid upload selection"
        }
        clientRequire(
            totalWireBytes >= fileCount.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD,
            "invalid_upload_selection",
        ) {
            "Invalid encrypted file sizes"
        }
        clientRequire(
            fileCount.toLong() <= requireNotNull(availableFiles),
            "receive_selection_file_limit",
        ) {
            "Too many files for the remaining receive capacity. Remove files or refresh and try again."
        }
        clientRequire(
            totalWireBytes <= requireNotNull(availableWireBytes),
            "receive_selection_byte_limit",
        ) {
            "The selected files exceed the remaining receive capacity. Remove files or refresh and try again."
        }
    }
}

internal const val CAPACITY_RETRY =
    "Receive capacity could not be checked. Refresh and try again; your files are still selected."

object GuestUploadCapacity {
    /** Includes the authenticated frame of every empty file; never permits signed overflow. */
    @Throws(Exception::class)
    fun totalWireBytes(plainSizes: List<Long>): Long {
        clientRequire(
            plainSizes.size <= TransferLimits.MAX_FILES,
            "selection_file_limit",
            mapOf("count" to TransferLimits.MAX_FILES.toString()),
        ) {
            "Select at most 100 files"
        }
        var total = 0L
        for (size in plainSizes) {
            val wire = ChunkedFileCrypto.wireSize(size)
            clientRequire(wire <= Long.MAX_VALUE - total, "selection_too_large") {
                "Selected files are too large"
            }
            total += wire
        }
        return total
    }
}

/** JSON numbers must be actual integral numbers, never strings, fractions, or missing fields. */
internal object UploadCapacitySerializer : KSerializer<UploadCapacity> {
    override val descriptor = buildClassSerialDescriptor("UploadCapacity")

    override fun deserialize(decoder: Decoder): UploadCapacity {
        val obj = (decoder as JsonDecoder).decodeJsonElement().jsonObject
        fun text(name: String): String {
            val field = obj[name] as? JsonPrimitive
            clientRequire(field != null && field.isString, "receive_capacity_unavailable") {
                CAPACITY_RETRY
            }
            return field.content
        }
        fun number(name: String): Long? {
            clientRequire(obj.containsKey(name), "receive_capacity_unavailable") { CAPACITY_RETRY }
            val field = obj[name]
            if (field == JsonNull) return null
            clientRequire(
                field is JsonPrimitive && !field.isString && field.longOrNull != null,
                "receive_capacity_unavailable",
            ) {
                CAPACITY_RETRY
            }
            return field.long
        }
        val reason =
            if (obj["reason"] == null || obj["reason"] == JsonNull) null else text("reason")
        return UploadCapacity(
                text("checked_at"),
                text("state"),
                reason,
                number("available_wire_bytes"),
                number("available_files"),
                number("manifest_reserve_bytes")
                    ?: throw ClientFailureException(CAPACITY_RETRY, "receive_capacity_unavailable"),
            )
            .also { it.validate() }
    }

    override fun serialize(encoder: Encoder, value: UploadCapacity) {
        (encoder as JsonEncoder).encodeJsonElement(
            buildJsonObject {
                put("checked_at", value.checkedAt)
                put("state", value.state)
                value.reason?.let { put("reason", it) }
                put(
                    "available_wire_bytes",
                    value.availableWireBytes?.let(::JsonPrimitive) ?: JsonNull,
                )
                put("available_files", value.availableFiles?.let(::JsonPrimitive) ?: JsonNull)
                put("manifest_reserve_bytes", value.manifestReserveBytes)
            }
        )
    }
}

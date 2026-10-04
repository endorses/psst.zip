package zip.psst.shared.model

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
        require(checkedAt.endsWith("Z") && checkedAt.length <= 40) { CAPACITY_RETRY }
        Instant.parse(checkedAt)
        require(manifestReserveBytes in 1..TransferLimits.MAX_MANIFEST_BYTES.toLong()) {
            CAPACITY_RETRY
        }
        when (state) {
            "ready" ->
                require(
                    reason == null &&
                        availableWireBytes != null &&
                        availableWireBytes in 60..(1L shl 50) &&
                        availableFiles != null &&
                        availableFiles in 1..TransferLimits.MAX_FILES.toLong() &&
                        availableFiles <= availableWireBytes / ChunkedFileCrypto.FRAME_OVERHEAD
                ) {
                    CAPACITY_RETRY
                }
            "blocked" ->
                require(
                    reason in listOf("link_limit", "capacity_limit") &&
                        availableWireBytes == 0L &&
                        availableFiles == 0L
                ) {
                    CAPACITY_RETRY
                }
            "unknown" ->
                require(
                    reason == "capacity_unavailable" &&
                        availableWireBytes == null &&
                        availableFiles == null
                ) {
                    CAPACITY_RETRY
                }
            else -> error(CAPACITY_RETRY)
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
        require(isFresh()) {
            "Receive capacity is out of date. Refresh and try again; your files are still selected."
        }
        require(state != "unknown") { CAPACITY_RETRY }
        require(state != "blocked") {
            if (reason == "link_limit")
                "This receive link cannot accept more files. Your files are still selected."
            else
                "This receive link has no upload capacity right now. Refresh and try again; your files are still selected."
        }
        require(fileCount in 0..TransferLimits.MAX_FILES && totalWireBytes >= 0) {
            "Invalid upload selection"
        }
        require(totalWireBytes >= fileCount.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD) {
            "Invalid encrypted file sizes"
        }
        require(fileCount.toLong() <= requireNotNull(availableFiles)) {
            "Too many files for the remaining receive capacity. Remove files or refresh and try again."
        }
        require(totalWireBytes <= requireNotNull(availableWireBytes)) {
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
        require(plainSizes.size <= TransferLimits.MAX_FILES) { "Select at most 100 files" }
        var total = 0L
        for (size in plainSizes) {
            val wire = ChunkedFileCrypto.wireSize(size)
            require(wire <= Long.MAX_VALUE - total) { "Selected files are too large" }
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
            require(field != null && field.isString) { CAPACITY_RETRY }
            return field.content
        }
        fun number(name: String): Long? {
            require(obj.containsKey(name)) { CAPACITY_RETRY }
            val field = obj[name]
            if (field == JsonNull) return null
            require(field is JsonPrimitive && !field.isString && field.longOrNull != null) {
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
                requireNotNull(number("manifest_reserve_bytes")) { CAPACITY_RETRY },
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

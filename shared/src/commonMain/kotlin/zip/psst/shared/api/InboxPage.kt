package zip.psst.shared.api

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.TransferStatus
import zip.psst.shared.model.UrlHelper
import kotlin.io.encoding.Base64
import kotlinx.serialization.json.*

internal fun validInboxCursor(value: String?): Boolean =
    value == null || (value.length in 1..512 && value.matches(Regex("[A-Za-z0-9_-]+")))

internal fun decodeInboxPage(
    body: JsonObject,
    slotId: String,
    after: String?,
    limit: Int,
): DropSlot {
    fun JsonObject.number(name: String, nullable: Boolean = false): Long? {
        require(containsKey(name)) { "Missing inbox count" }
        val raw = getValue(name)
        if (nullable && raw == JsonNull) return null
        val value = raw as? JsonPrimitive
        require(value != null && !value.isString && value.content.matches(Regex("0|[1-9][0-9]*"))) {
            "Invalid inbox count"
        }
        return requireNotNull(value.longOrNull).also { require(it >= 0) }
    }
    require(body["paginated"] == JsonPrimitive(true) && body.containsKey("next_cursor")) {
        "This server does not support bounded inbox pages"
    }
    val rawCursor = body.getValue("next_cursor")
    require(rawCursor == JsonNull || (rawCursor is JsonPrimitive && rawCursor.isString))
    val next = if (rawCursor == JsonNull) null else rawCursor.jsonPrimitive.content
    require(validInboxCursor(next) && (next == null || next != after)) {
        "Invalid inbox continuation"
    }
    val summary = requireNotNull(body["summary"] as? JsonObject)
    val state = summary["state"]?.jsonPrimitive?.content
    require(state == "ready" || state == "updating") { "Invalid inbox summary" }
    val completed = summary.number("completed_files", true)
    val files = summary.number("file_count", true)
    val size = summary.number("total_size", true)
    require(
        if (state == "ready")
            completed != null && files != null && size != null && completed <= files
        else completed == null && files == null && size == null
    ) {
        "Invalid inbox summary totals"
    }
    val children = requireNotNull(body["transfers"] as? JsonArray)
    require(children.size <= limit)
    require(body.number("receive_protocol") in 1L..2L)
    require(body.number("max_files")!! <= Int.MAX_VALUE)
    body.number("reserved_files")
    body.number("remaining_files", true)
    require((body["recipient_public_key"] as? JsonPrimitive)?.isString == true)
    children.forEach { require(it.jsonObject.number("file_count")!! <= 100) }
    val page = Json { ignoreUnknownKeys = true }.decodeFromJsonElement<DropSlot>(body)
    require(
        page.id == slotId &&
            page.transfers.map { it.transferId }.toSet().size == page.transfers.size
    )
    require(page.transfers.all { UrlHelper.isResourceId(it.transferId) })
    require(
        page.transfers.all {
            it.status == TransferStatus.PENDING || it.status == TransferStatus.COMPLETE
        }
    )
    require(
        page.receiveProtocol in 1..2 &&
            page.maxFiles >= 0 &&
            page.reservedFiles >= 0 &&
            page.completedFiles >= 0 &&
            (page.remainingFiles == null || page.remainingFiles >= 0)
    )
    require(
        if (page.receiveProtocol == 1) page.recipientPublicKey.isEmpty()
        else page.recipientPublicKey.matches(Regex("[A-Za-z0-9_-]{43}"))
    )
    if (page.receiveProtocol == 2) {
        val codec = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
        val key = codec.decode(page.recipientPublicKey)
        require(
            key.size == 32 &&
                key.any { it != 0.toByte() } &&
                codec.encode(key) == page.recipientPublicKey
        )
    }
    if (state == "ready") {
        require(
            page.completedTransfers.sumOf { it.fileCount.toLong() } <= requireNotNull(completed)
        )
        require(page.transfers.sumOf { it.fileCount.toLong() } <= requireNotNull(files))
    }
    return page
}

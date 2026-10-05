package zip.psst.shared.api

import zip.psst.shared.model.LinkTitle
import zip.psst.shared.model.UrlHelper
import kotlin.time.Instant
import kotlinx.serialization.json.*

/** A page is never a complete account inventory, including a page with no rows. */
internal fun decodeResourcePage(body: JsonObject, after: String?, limit: Int): AuthResources {
    require(body["paginated"] == JsonPrimitive(true) && body.containsKey("next_cursor")) {
        "This server does not support bounded History pages"
    }
    val sync = body["sync_cursor"]
    val generation = body["generation"]
    require(
        (sync == null && generation == null) ||
            (sync is JsonPrimitive &&
                sync.isString &&
                validInboxCursor(sync.content) &&
                sync.content.isNotEmpty() &&
                generation is JsonPrimitive &&
                generation.isString &&
                UrlHelper.isResourceId(generation.content))
    )
    val rawNext = body.getValue("next_cursor")
    require(rawNext == JsonNull || (rawNext is JsonPrimitive && rawNext.isString))
    val next = if (rawNext == JsonNull) null else rawNext.jsonPrimitive.content
    require(validInboxCursor(next) && (next == null || next != after)) {
        "Invalid History continuation"
    }
    val transfers = requireNotNull(body["transfers"] as? JsonArray)
    val slots = requireNotNull(body["slots"] as? JsonArray)
    require(transfers.size + slots.size <= limit)
    fun JsonObject.number(name: String): Long? {
        require(containsKey(name)) { "Missing resource summary count" }
        val raw = getValue(name)
        if (raw == JsonNull) return null
        val number = raw as? JsonPrimitive
        require(
            number != null && !number.isString && number.content.matches(Regex("0|[1-9][0-9]*"))
        ) {
            "Invalid resource count"
        }
        return requireNotNull(number.longOrNull)
    }
    val identities = mutableSetOf<String>()
    for ((kind, rows) in listOf("transfer" to transfers, "slot" to slots)) {
        for (raw in rows) {
            val row = raw.jsonObject
            for (name in listOf("history_after", "history_after_kind")) row[name]?.let {
                require(
                    it is JsonPrimitive &&
                        it.isString &&
                        it.content.isNotEmpty() &&
                        validInboxCursor(it.content)
                )
            }
            if (sync != null || row.containsKey("revision")) historyRevision(row["revision"])
            for (name in listOf("created_at", "expires_at", "downloaded_at")) {
                val value = row[name]
                if (value != null && value != JsonNull) {
                    require(
                        value is JsonPrimitive && value.isString && value.content.length in 1..64
                    )
                    Instant.parse(value.content)
                }
            }
            if (sync != null) require(row["created_at"] != null && row["created_at"] != JsonNull)
            row["inactive_reason"]
                ?.takeIf { it != JsonNull }
                ?.let { require(it is JsonPrimitive && it.isString && it.content.length <= 64) }
            row["files"]?.let {
                require(it is JsonArray && it.isEmpty()) { "History must not embed file payloads" }
            }
            if (row.containsKey("download_count"))
                require(requireNotNull(row.number("download_count")) <= Int.MAX_VALUE)
            row["title"]
                ?.takeIf { it != JsonNull }
                ?.let { title ->
                    require(title is JsonPrimitive && title.isString)
                    require(LinkTitle.normalize(title.content) == title.content)
                }
            val id = row["id"]?.jsonPrimitive?.content.orEmpty()
            require(UrlHelper.isResourceId(id) && identities.add(id.lowercase())) {
                "Invalid or duplicate resource identity"
            }
            val status = row["status"]?.jsonPrimitive?.content
            require(
                status in
                    if (kind == "slot") listOf("waiting", "has_uploads", "expired", "revoked")
                    else listOf("pending", "complete", "expired", "revoked", "exhausted")
            )
            val summary = requireNotNull(row["summary"] as? JsonObject)
            val state = summary["state"]?.jsonPrimitive?.content
            val completed = summary.number("completed_files")
            val files = summary.number("file_count")
            val size = summary.number("total_size")
            require(
                when (state) {
                    "ready" ->
                        completed != null && files != null && size != null && completed <= files
                    "updating" -> completed == null && files == null && size == null
                    else -> false
                }
            ) {
                "Invalid resource summary"
            }
            require(row.number("file_count") == files && row.number("total_size") == size) {
                "Conflicting resource summary"
            }
            val capName = if (kind == "slot") "max_files" else "max_downloads"
            if (row.containsKey(capName)) {
                val cap = requireNotNull(row.number(capName)) { "Missing link limit" }
                require(cap <= Int.MAX_VALUE) { "Invalid link limit" }
            }
            if (kind == "slot") {
                if (row.containsKey("reserved_files")) {
                    val used = requireNotNull(row.number("reserved_files"))
                    val cap = row.takeIf { it.containsKey("max_files") }?.number("max_files")
                    require(cap == null || cap == 0L || used <= cap) { "Invalid receive allowance" }
                }
                require(row.number("completed_files") == completed)
                val children = row["transfers"]
                require(children == null || (children is JsonArray && children.isEmpty())) {
                    "History must not embed inbox children"
                }
            }
        }
    }
    return Json { ignoreUnknownKeys = true }.decodeFromJsonElement(body)
}

package zip.psst.shared.api

import zip.psst.shared.model.UrlHelper
import kotlinx.serialization.json.*

/** A page is never a complete account inventory, including a page with no rows. */
internal fun decodeResourcePage(body: JsonObject, after: String?, limit: Int): AuthResources {
    require(body["paginated"] == JsonPrimitive(true) && body.containsKey("next_cursor")) {
        "This server does not support bounded History pages"
    }
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
            val id = row["id"]?.jsonPrimitive?.content.orEmpty()
            require(UrlHelper.isResourceId(id) && identities.add(id.lowercase())) {
                "Invalid or duplicate resource identity"
            }
            val status = row["status"]?.jsonPrimitive?.content
            require(
                status in
                    if (kind == "slot") listOf("waiting", "has_uploads", "expired", "revoked")
                    else listOf("pending", "complete", "expired", "revoked")
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
            if (kind == "slot") {
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

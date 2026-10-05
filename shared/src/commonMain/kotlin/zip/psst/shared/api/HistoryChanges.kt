package zip.psst.shared.api

import zip.psst.shared.model.UrlHelper
import kotlinx.serialization.json.*

const val HISTORY_SAFE_REVISION = 9007199254740991L

class HistorySyncResetRequiredException :
    IllegalStateException("History sync needs a new snapshot")

class HistorySyncRateLimitedException(val retryAfterSeconds: Long) :
    IllegalStateException("History sync rate limited")

data class HistoryChange(
    val kind: String,
    val id: String,
    val revision: Long,
    val action: String,
    val resource: AuthResources? = null,
)

data class HistoryChanges(
    val version: Int,
    val generation: String,
    val changes: List<HistoryChange>,
    val nextCursor: String,
    val hasMore: Boolean,
)

internal fun decodeHistoryChanges(body: JsonObject, cursor: String, limit: Int): HistoryChanges {
    fun string(name: String): String {
        val value = body[name] as? JsonPrimitive
        require(value?.isString == true)
        return value.content
    }
    require(body["version"] == JsonPrimitive(1))
    val generation = string("generation")
    require(UrlHelper.isResourceId(generation))
    val next = string("next_cursor")
    require(validInboxCursor(next) && next.isNotEmpty())
    val rawMore = body["has_more"] as? JsonPrimitive
    require(rawMore != null && !rawMore.isString)
    val more = requireNotNull(rawMore.booleanOrNull)
    require(!more || next != cursor) { "History continuation made no progress" }
    val rows = requireNotNull(body["changes"] as? JsonArray)
    require(rows.size <= limit && (rows.isEmpty() || next != cursor))
    val seen = mutableSetOf<String>()
    val changes =
        rows.map { raw ->
            val row = raw.jsonObject
            fun text(name: String): String {
                val value = row[name] as? JsonPrimitive
                require(value?.isString == true)
                return value.content
            }
            val kind = text("kind")
            val id = text("id")
            val action = text("action")
            require(kind in listOf("transfer", "slot") && UrlHelper.isResourceId(id))
            require(seen.add("$kind/$id") && action in listOf("upsert", "remove"))
            val revision = historyRevision(row["revision"])
            val resource =
                if (action == "upsert") {
                    val fact = requireNotNull(row["resource"] as? JsonObject)
                    require(fact["created_at"] != null && fact["created_at"] != JsonNull)
                    require(
                        fact["id"] == JsonPrimitive(id) &&
                            historyRevision(fact["revision"]) == revision
                    )
                    decodeResourcePage(
                        buildJsonObject {
                            put("paginated", true)
                            put("next_cursor", JsonNull)
                            put(
                                "transfers",
                                JsonArray(if (kind == "transfer") listOf(fact) else emptyList()),
                            )
                            put(
                                "slots",
                                JsonArray(if (kind == "slot") listOf(fact) else emptyList()),
                            )
                        },
                        null,
                        1,
                    )
                } else {
                    require(row["resource"] == null || row["resource"] == JsonNull)
                    null
                }
            HistoryChange(kind, id, revision, action, resource)
        }
    return HistoryChanges(1, generation, changes, next, more)
}

internal fun historyRevision(raw: JsonElement?): Long {
    val value = raw as? JsonPrimitive
    require(value != null && !value.isString && value.content.matches(Regex("0|[1-9][0-9]*")))
    return requireNotNull(value.longOrNull).also { require(it in 0..HISTORY_SAFE_REVISION) }
}

/** Revalidate disposable persisted metadata before handing it to a native presenter. */
fun decodeCachedHistoryResources(raw: String): AuthResources {
    require(raw.length <= 1024 * 1024)
    val stored = Json.parseToJsonElement(raw).jsonObject
    fun rows(kind: String): JsonArray {
        val values =
            (stored[kind] ?: JsonArray(emptyList())) as? JsonArray
                ?: error("Invalid cached history")
        return JsonArray(
            values.map { item ->
                val row = item.jsonObject.toMutableMap()
                if (!row.containsKey("file_count")) row["file_count"] = JsonNull
                if (!row.containsKey("total_size")) row["total_size"] = JsonNull
                if (kind == "slots" && !row.containsKey("completed_files"))
                    row["completed_files"] = JsonNull
                JsonObject(row)
            }
        )
    }
    val page = buildJsonObject {
        put("paginated", true)
        put("next_cursor", stored["next_cursor"] ?: JsonNull)
        put("transfers", rows("transfers"))
        put("slots", rows("slots"))
        stored["sync_cursor"]?.takeUnless { it == JsonNull }?.let { put("sync_cursor", it) }
        stored["generation"]?.takeUnless { it == JsonNull }?.let { put("generation", it) }
    }
    return decodeResourcePage(page, null, 50)
}

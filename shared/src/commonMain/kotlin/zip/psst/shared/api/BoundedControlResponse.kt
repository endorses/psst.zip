package zip.psst.shared.api

import io.ktor.client.plugins.ClientRequestException
import io.ktor.client.plugins.ServerResponseException
import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.HttpHeaders
import io.ktor.utils.io.readAvailable
import kotlinx.serialization.json.Json

/** A hostile scanned server must not allocate unbounded metadata before recipient preflight. */
internal suspend inline fun <reified T> HttpResponse.readControlJson(
    maxBytes: Int = 128 * 1024
): T {
    if (status.value in 400..499) throw ClientRequestException(this, "Transfer request rejected")
    if (status.value in 500..599) throw ServerResponseException(this, "Transfer server unavailable")
    require(status.value in 200..299) { "Unexpected transfer response" }
    val declared = headers[HttpHeaders.ContentLength]?.toLongOrNull()
    require(declared == null || declared in 0..maxBytes.toLong()) {
        "Transfer metadata is too large"
    }
    val buffer = ByteArray(maxBytes + 1)
    val channel = bodyAsChannel()
    var count = 0
    while (count < buffer.size) {
        val read = channel.readAvailable(buffer, count, buffer.size - count)
        if (read < 0) break
        count += read
    }
    require(count <= maxBytes && (declared == null || declared == count.toLong())) {
        "Invalid transfer metadata length"
    }
    return Json { ignoreUnknownKeys = true }.decodeFromString(buffer.copyOf(count).decodeToString())
}

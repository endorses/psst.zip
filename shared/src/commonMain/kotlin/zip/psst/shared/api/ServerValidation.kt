package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.prepareGet
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.HttpHeaders
import io.ktor.http.Url
import io.ktor.utils.io.readAvailable
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** Read-only setup checks; neither transfers nor drop slots are created. */
internal suspend fun validateServer(config: ServerConfig, client: HttpClient) {
    val raw = config.baseUrl
    require(raw == raw.trim() && raw.none { it.isWhitespace() || it == '\\' }) {
        "Enter a clean server URL such as https://transfer.example.com."
    }
    require(Regex("^https?://[^/?#@]+/?$", RegexOption.IGNORE_CASE).matches(raw)) {
        "Enter the server origin only, without credentials, a path, query, or fragment."
    }
    val url =
        try {
            Url(raw)
        } catch (e: Exception) {
            throw IllegalArgumentException(
                "Enter a valid server URL such as https://transfer.example.com.",
                e,
            )
        }
    require(url.host.isNotBlank() && url.port in 1..65535) { "Enter a valid server host and port." }
    require(
        url.protocol.name == "https" ||
            url.host.lowercase() in setOf("localhost", "127.0.0.1", "::1", "[::1]")
    ) {
        "HTTPS is required for a LAN or public server. Configure HTTPS and enter its https:// address."
    }
    try {
        val completed =
            withTimeoutOrNull(10_000L) {
                val health =
                    readSetupResponse(
                        client,
                        "${config.apiBaseUrl}/health",
                        "application/json",
                        16 * 1024,
                    )
                val identity = Json.parseToJsonElement(health).jsonObject
                require(
                    identity["service"]?.jsonPrimitive?.content == "psst.zip" &&
                        identity["api_version"]?.jsonPrimitive?.let {
                            !it.isString && it.intOrNull == 1
                        } == true
                ) {
                    "This address does not serve the supported psst.zip API (version 1)."
                }
                for (route in listOf("d", "u")) {
                    val html =
                        readSetupResponse(
                            client,
                            "${config.normalizedBaseUrl}/$route/_connection_check",
                            "text/html",
                            512 * 1024,
                        )
                    require(hasWebMarker(html)) {
                        "The psst.zip web app is missing from /$route/. Deploy the web app and route its pages at this server origin."
                    }
                }
                true
            }
        check(completed == true) {
            "Server validation timed out. Check the address, network, and server availability."
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        throw IllegalStateException(
            "Server validation failed. Check the HTTPS address, certificate, and API/web deployment. ${e.message.orEmpty()}",
            e,
        )
    }
}

private suspend fun readSetupResponse(
    client: HttpClient,
    url: String,
    contentType: String,
    maxBytes: Int,
): String =
    client
        .prepareGet(url) { expectSuccess = false }
        .execute { response ->
            require(response.status.value == 200) {
                "Setup endpoint returned HTTP ${response.status.value}."
            }
            require(response.call.request.url == Url(url)) {
                "Use the final server origin directly; setup endpoints must not redirect."
            }
            require(
                response.headers[HttpHeaders.ContentType]
                    ?.substringBefore(';')
                    ?.trim()
                    ?.lowercase() == contentType
            ) {
                "Setup endpoint must return $contentType. Check API and web routing."
            }
            val channel = response.bodyAsChannel()
            val bytes = ByteArray(maxBytes + 1)
            var total = 0
            while (true) {
                val count = channel.readAvailable(bytes, total, bytes.size - total)
                if (count == -1) break
                total += count
                require(total <= maxBytes) {
                    "Setup response is unexpectedly large. Check the server routing."
                }
            }
            bytes.copyOf(total).decodeToString()
        }

private fun hasWebMarker(html: String): Boolean {
    val tags = Regex("<meta\\b[^>]*>", RegexOption.IGNORE_CASE)
    val attributes = Regex("([a-zA-Z][a-zA-Z0-9_-]*)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s>]+))")
    return tags.findAll(html).any { tag ->
        val values =
            attributes.findAll(tag.value).associate { match ->
                match.groupValues[1].lowercase() to
                    (match.groups[2]?.value ?: match.groups[3]?.value ?: match.groupValues[4])
            }
        values["name"] == "psst-web" && values["content"] == "1"
    }
}

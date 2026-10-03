package zip.psst.shared.api

import io.ktor.client.HttpClientConfig
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.serialization.kotlinx.json.json
import kotlinx.serialization.json.Json

/**
 * Shared by both production engines and transport tests. Native engines additionally disable
 * cookies.
 */
internal fun HttpClientConfig<*>.applyClientPolicy() {
    expectSuccess = true
    followRedirects = false
    install(ContentNegotiation) {
        json(
            Json {
                ignoreUnknownKeys = true
                isLenient = true
                encodeDefaults = true
            }
        )
    }
}

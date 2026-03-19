package zip.psst.shared.api

import io.ktor.client.HttpClient

/**
 * Platform-specific HTTP client factory.
 * Uses OkHttp engine on Android and Darwin engine on iOS.
 */
expect fun createPlatformHttpClient(): HttpClient

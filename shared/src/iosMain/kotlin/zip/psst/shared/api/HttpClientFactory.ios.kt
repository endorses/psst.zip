package zip.psst.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.engine.darwin.Darwin

actual fun createPlatformHttpClient(): HttpClient {
    return HttpClient(Darwin) {
        engine {
            configureSession {
                setHTTPShouldSetCookies(false)
                setHTTPCookieStorage(null)
                setURLCredentialStorage(null)
            }
        }
        applyClientPolicy()
    }
}

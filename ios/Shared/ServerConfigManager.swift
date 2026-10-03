import Foundation
import Shared

/// Manages server configuration, persisted in App Group UserDefaults.
@Observable
final class ServerConfigManager {
    var serverURL: String {
        didSet {
            AppConstants.sharedDefaults.set(serverURL, forKey: AppConstants.serverURLKey)
        }
    }

    var isConfigured: Bool {
        !serverURL.isEmpty
    }

    init() {
        serverURL = AppConstants.sharedDefaults.string(forKey: AppConstants.serverURLKey) ?? ""
    }

    /// Build a KMP ServerConfig from the current URL.
    func makeServerConfig() -> ServerConfig {
        ServerConfig(baseUrl: serverURL)
    }

    /// Build a KMP ApiClient from the current URL.
    func makeApiClient() -> ApiClient {
        ApiClient(config: makeServerConfig(), httpClient: HttpClientFactoryKt.createPlatformHttpClient())
    }

    /// Check a candidate address without changing the saved configuration.
    func testConnection(url: String) async throws {
        let client = ApiClient(
            config: ServerConfig(baseUrl: url),
            httpClient: HttpClientFactoryKt.createPlatformHttpClient()
        )
        defer { client.close() }
        try await client.validateServer()
    }
}

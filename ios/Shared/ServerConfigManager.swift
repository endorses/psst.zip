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

    /// Test the connection to the server. Returns true on success.
    func testConnection() async -> Bool {
        guard isConfigured else { return false }
        do {
            let client = makeApiClient()
            defer { client.close() }
            // Attempt to create and immediately check a transfer to verify connectivity.
            // A simple GET to the base URL would be better, but we use what the API offers.
            // We'll just try a GET to a non-existent transfer and check we get a proper HTTP error
            // rather than a network error.
            let _ = try await client.transfers.get(transferId: "00000000-0000-0000-0000-000000000000")
            return true
        } catch {
            // A 404 is fine — it means the server is reachable.
            let description = String(describing: error)
            if description.contains("404") || description.contains("Not Found") {
                return true
            }
            return false
        }
    }
}

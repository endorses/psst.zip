package zip.psst.shared.model

/**
 * Configuration for connecting to a self-hosted server instance.
 */
data class ServerConfig(
    /** Base URL of the server, e.g. "https://drop.example.com" */
    val baseUrl: String,
) {
    /** Base URL with trailing slash stripped */
    val normalizedBaseUrl: String
        get() = baseUrl.trimEnd('/')

    /** Full API base path */
    val apiBaseUrl: String
        get() = "$normalizedBaseUrl/api/v1"
}

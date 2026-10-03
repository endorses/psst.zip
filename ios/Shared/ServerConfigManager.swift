import Foundation
import Shared
import UIKit

@Observable
@MainActor
final class ServerConfigManager {
    private(set) var session: DeviceSession? = SecretStore.session
    var needsSignIn = false
    private var expiredObserver: NSObjectProtocol?
    init() {
        expiredObserver = NotificationCenter.default.addObserver(forName: .sessionExpired, object: nil, queue: .main) { [weak self] note in
            MainActor.assumeIsolated {
                guard let self, let server = note.object as? String, self.session?.serverURL == server else { return }
                self.needsSignIn = true
            }
        }
    }

    deinit {
        if let expiredObserver {
            NotificationCenter.default.removeObserver(expiredObserver)
        }
    }

    var serverURL: String {
        session?.serverURL ?? AppConstants.sharedDefaults.string(forKey: AppConstants.serverURLKey) ?? ""
    }

    var isConfigured: Bool {
        session != nil
    }

    var accountID: String? {
        session?.accountID
    }

    var indicator: String {
        session.map { $0.username + " · " + (URL(string: $0.serverURL)?.host ?? $0.serverURL) } ?? String(localized: "Not signed in")
    }

    func reload() {
        session = SecretStore.session
    }

    func requireSession() throws -> DeviceSession {
        reload()
        guard let session, !needsSignIn else { throw AccountError.signIn }
        return session
    }

    func check(_ original: DeviceSession) throws {
        guard JobIdentity(session: original).accepts(SecretStore.session, cancelled: Task.isCancelled) else { throw AccountError.changed }
        try Task.checkCancellation()
    }

    func makeApiClient(session: DeviceSession) -> ApiClient {
        ApiClient(config: ServerConfig(baseUrl: session.serverURL),
                  httpClient: HttpClientFactoryKt.createPlatformHttpClient(), sessionToken: session.token)
    }

    func makeApiClient() -> ApiClient {
        ApiClient(config: ServerConfig(baseUrl: serverURL),
                  httpClient: HttpClientFactoryKt.createPlatformHttpClient(), sessionToken: session?.token)
    }

    func login(server: String, username: String, password: String) async throws {
        let previous = SecretStore.session
        let server = try AccountHTTP.origin(server)
        try await validate(server: server)
        let data = try await AccountHTTP.request(server: server, path: "auth/login", method: "POST",
                                                 body: ["username": username, "password": password, "device_name": UIDevice.current.name, "session_type": "device"])
        guard SecretStore.session == previous else { throw AccountError.changed }
        try install(data, server: server)
    }

    func pair(raw: String) async throws {
        struct Code: Decodable { let type: String
            let version: Int
            let server_url: String
            let code: String
        }
        guard raw.utf8.count <= 4096, let code = try? JSONDecoder().decode(Code.self, from: Data(raw.utf8)),
              code.type == "psst-pairing", code.version == 1,
              code.code.range(of: "^[A-Za-z0-9_-]{32,128}$", options: .regularExpression) != nil else { throw AccountError.pairing }
        let previous = SecretStore.session
        let server = try AccountHTTP.origin(code.server_url)
        try await validate(server: server)
        let data = try await AccountHTTP.request(server: server, path: "auth/pairings/redeem", method: "POST",
                                                 body: ["code": code.code, "device_name": UIDevice.current.name])
        guard SecretStore.session == previous else { throw AccountError.changed }
        try install(data, server: server)
    }

    private func validate(server: String) async throws {
        let client = ApiClient(config: ServerConfig(baseUrl: server), httpClient: HttpClientFactoryKt.createPlatformHttpClient())
        defer { client.close() }
        try await client.validateServer()
    }

    private func install(_ data: Data, server: String) throws {
        struct Response: Decodable {
            struct User: Decodable { let id: String
                let username: String
                let role: String
            }

            let token: String
            let user: User
            let session_id: String
            let expires_at: String
        }
        let response = try JSONDecoder().decode(Response.self, from: data)
        guard !response.token.isEmpty, UUID(uuidString: response.user.id) != nil else { throw AccountError.signIn }
        let next = DeviceSession(serverURL: server, userID: response.user.id, username: response.user.username,
                                 token: response.token, sessionID: response.session_id, expiresAt: response.expires_at, role: response.user.role)
        try SecretStore.write(JSONEncoder().encode(next), name: "active-session")
        AppConstants.sharedDefaults.set(server, forKey: AppConstants.serverURLKey)
        session = next
        needsSignIn = false
    }

    func logout() async throws {
        guard let original = session else { return }
        do { _ = try await AccountHTTP.request(server: original.serverURL, path: "auth/logout", method: "POST", token: original.token) }
        catch AccountError.signIn { /* An expired session is already signed out. */ }
        guard SecretStore.session == original else { reload()
            return
        }
        SecretStore.remove("active-session")
        session = nil
        needsSignIn = false
    }
}

import Foundation
import Shared
import UIKit

@Observable
@MainActor
final class ServerConfigManager {
    private(set) var session: DeviceSession? = SecretStore.session
    var needsSignIn = false
    private(set) var passwordChanged = false
    private(set) var advertisedLimit: Int64?
    private(set) var limitOrigin: String?
    private var advertisedAbuseContact: String?
    var abuseContactEmail: String? { limitOrigin == serverURL ? advertisedAbuseContact : nil }
    var limitDescription: String {
        guard limitOrigin == serverURL, let advertisedLimit else { return "The server sets the maximum file size." }
        return "Up to " + ByteCountFormatter.string(fromByteCount: advertisedLimit, countStyle: .binary) + " per file."
    }

    func refreshLimit() async {
        let origin = serverURL
        limitOrigin = nil; advertisedLimit = nil; advertisedAbuseContact = nil
        guard !origin.isEmpty else { return }
        do {
            let client = try ApiClient.companion.anonymous(origin: origin)
            defer { client.close() }
            let limits = try await client.limits.get()
            guard serverURL == origin else { return }
            limitOrigin = origin; advertisedLimit = limits.maxFileSize
            advertisedAbuseContact = AbuseContact.validated(limits.abuseContactEmail)
        } catch {
            if serverURL == origin {
                limitOrigin = nil; advertisedLimit = nil; advertisedAbuseContact = nil
            }
        }
    }

    private var restrictedObserver: NSObjectProtocol?
    private var expiredObserver: NSObjectProtocol?
    init() {
        restrictedObserver = NotificationCenter.default.addObserver(forName: .accountRestricted, object: nil, queue: .main) { [weak self] note in
            MainActor.assumeIsolated {
                guard let self, let server = note.object as? String, var current = self.session, current.serverURL == server, current.token == note.userInfo?["sessionToken"] as? String else { return }
                if note.userInfo?["code"] as? String == "admin_transfer_forbidden" {
                    current.role = "admin"
                } else {
                    current.mustChangePassword = true
                }
                self.session = current
                try? SecretStore.write(JSONEncoder().encode(current), name: "active-session")
            }
        }
        expiredObserver = NotificationCenter.default.addObserver(forName: .sessionExpired, object: nil, queue: .main) { [weak self] note in
            MainActor.assumeIsolated {
                guard let self, let server = note.object as? String, self.session?.serverURL == server, self.session?.token == note.userInfo?["sessionToken"] as? String else { return }
                self.needsSignIn = true
            }
        }
    }

    deinit {
        if let restrictedObserver {
            NotificationCenter.default.removeObserver(restrictedObserver)
        }
        if let expiredObserver {
            NotificationCenter.default.removeObserver(expiredObserver)
        }
    }

    var serverURL: String {
        session?.serverURL ?? AppConstants.sharedDefaults.string(forKey: AppConstants.serverURLKey) ?? ""
    }

    var isConfigured: Bool {
        session?.canTransfer == true
    }

    var requiresPasswordChange: Bool {
        session?.mustChangePassword == true && session?.role != "admin" && !needsSignIn
    }

    var accountMessage: String? {
        if session?.role == "admin" {
            return AccountError.administrator.localizedDescription
        }
        if requiresPasswordChange {
            return AccountError.passwordChange.localizedDescription
        }
        return nil
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
        guard session.role != "admin" else { throw AccountError.administrator }
        guard session.mustChangePassword != true else { throw AccountError.passwordChange }
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
        try await install(data, server: server)
    }

    func pair(raw: String) async throws {
        guard let classified = ScanInputClassifier.shared.classify(raw: raw), classified.kind == .pairing,
              let code = classified.pairing else { throw AccountError.pairing }
        let previous = SecretStore.session
        let server = try AccountHTTP.origin(code.serverUrl)
        try await validate(server: server)
        let data = try await AccountHTTP.request(server: server, path: "auth/pairings/redeem", method: "POST",
                                                 body: ["code": code.code, "device_name": UIDevice.current.name])
        guard SecretStore.session == previous else { throw AccountError.changed }
        try await install(data, server: server)
    }

    func testConnection(server: String) async throws {
        try await validate(server: AccountHTTP.origin(server))
    }

    private func validate(server: String) async throws {
        let client = ApiClient(config: ServerConfig(baseUrl: server), httpClient: HttpClientFactoryKt.createPlatformHttpClient())
        defer { client.close() }
        try await client.validateServer()
    }

    private func install(_ data: Data, server: String) async throws {
        struct Response: Decodable {
            struct User: Decodable { let id: String
                let username: String
                let role: String
                let must_change_password: Bool?
            }

            let token: String
            let user: User
            let session_id: String
            let expires_at: String
        }
        let response = try JSONDecoder().decode(Response.self, from: data)
        guard !response.token.isEmpty, UUID(uuidString: response.user.id) != nil else { throw AccountError.signIn }
        guard response.user.role != "admin" else {
            _ = try? await AccountHTTP.request(server: server, path: "auth/logout", method: "POST", token: response.token)
            throw AccountError.administrator
        }
        let next = DeviceSession(serverURL: server, userID: response.user.id, username: response.user.username,
                                 token: response.token, sessionID: response.session_id, expiresAt: response.expires_at, role: response.user.role, mustChangePassword: response.user.must_change_password)
        try SecretStore.write(JSONEncoder().encode(next), name: "active-session")
        AppConstants.sharedDefaults.set(server, forKey: AppConstants.serverURLKey)
        session = next
        needsSignIn = false
        passwordChanged = false
    }

    /// Recheck stored sessions after role/password policy changes; failures never silently enable transfers.
    func refreshAccount() async {
        guard let original = session, !needsSignIn else { return }
        do {
            struct Response: Decodable {
                struct User: Decodable { let id: String; let role: String; let must_change_password: Bool? }
                let user: User
            }
            let data = try await AccountHTTP.request(server: original.serverURL, path: "auth/me", token: original.token)
            let response = try JSONDecoder().decode(Response.self, from: data)
            guard SecretStore.session == original, response.user.id == original.userID else { return }
            var updated = original
            updated.role = response.user.role
            updated.mustChangePassword = response.user.must_change_password
            try SecretStore.write(JSONEncoder().encode(updated), name: "active-session")
            session = updated
        } catch AccountError.signIn {
            if SecretStore.session == original {
                needsSignIn = true
            }
        } catch { /* Server authorization remains authoritative while offline. */ }
    }

    func changePassword(current: String, replacement: String) async throws {
        guard let original = session, original.role != "admin", !needsSignIn else { throw AccountError.signIn }
        _ = try await AccountHTTP.request(server: original.serverURL, path: "auth/password", method: "POST", token: original.token,
                                          body: ["current_password": current, "password": replacement])
        guard SecretStore.session == original else { throw AccountError.changed }
        // Preserve identity so pending selections survive reauthentication to the same account.
        // The revoked token cannot authorize work; requireSession also checks needsSignIn.
        needsSignIn = true
        passwordChanged = true
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

import Foundation
import Security
import Shared

struct DeviceSession: Codable, Equatable {
    let serverURL: String
    let userID: String
    let username: String
    let token: String
    let sessionID: String
    let expiresAt: String
    var role: String? = nil
    var accountID: String {
        serverURL + "|" + userID
    }
}

/// Accessible only to the signed app and extension, never to ordinary App Group preferences.
enum SecretStore {
    private static var group: String? {
        Bundle.main.object(forInfoDictionaryKey: "SharedKeychainGroup") as? String
    }

    private static func query(_ name: String) -> [String: Any] {
        var value: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecAttrService as String: "zip.psst.ios.sessions", kSecAttrAccount as String: name]
        if let group {
            value[kSecAttrAccessGroup as String] = group
        }
        return value
    }

    static func read(_ name: String) -> Data? {
        var value = query(name)
        value[kSecReturnData as String] = true
        value[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        guard SecItemCopyMatching(value as CFDictionary, &result) == errSecSuccess else { return nil }
        return result as? Data
    }

    static func write(_ data: Data, name: String) throws {
        let attributes: [String: Any] = [kSecValueData as String: data,
                                         kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        let status = SecItemUpdate(query(name) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            var item = query(name)
            attributes.forEach { item[$0.key] = $0.value }
            guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw AccountError.storage }
        } else if status != errSecSuccess {
            throw AccountError.storage
        }
    }

    static func remove(_ name: String) {
        SecItemDelete(query(name) as CFDictionary)
    }

    static var session: DeviceSession? {
        guard let data = read("active-session") else { return nil }
        return try? JSONDecoder().decode(DeviceSession.self, from: data)
    }
}

enum AccountError: LocalizedError {
    case signIn, changed, address, storage, request, pairing, unavailable
    var errorDescription: String? {
        switch self {
        case .signIn: String(localized: "Sign in again to continue.")
        case .changed: String(localized: "The account changed. Return to your original account to continue.")
        case .address: String(localized: "Enter a server origin such as https://files.example.com, without a path.")
        case .storage: String(localized: "Secure storage is unavailable. Check the app’s signing configuration.")
        case .request: String(localized: "Could not connect. Check your network and server address, then retry.")
        case .pairing: String(localized: "This login code is invalid, expired, or already used. Generate a new code on the website.")
        case .unavailable: String(localized: "This link expired or was revoked.")
        }
    }
}

/// Redirects are forbidden for all authenticated requests: credentials stay on their originating server.
final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_: URLSession, task _: URLSessionTask,
                    willPerformHTTPRedirection _: HTTPURLResponse, newRequest _: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void)
    {
        completionHandler(nil)
    }
}

enum AccountHTTP {
    static func origin(_ raw: String) throws -> String {
        guard let url = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
              let host = url.host, !host.isEmpty, url.user == nil, url.password == nil,
              url.query == nil, url.fragment == nil, url.path.isEmpty || url.path == "/",
              url.port == nil || (1 ... 65535).contains(url.port!) else { throw AccountError.address }
        var canonical = url
        canonical.scheme = url.scheme?.lowercased()
        canonical.host = host.lowercased()
        canonical.path = ""
        guard let value = canonical.url?.absoluteString else { throw AccountError.address }
        return value
    }

    static func request(server: String, path: String, method: String = "GET", token: String? = nil,
                        body: [String: String]? = nil) async throws -> Data
    {
        let origin = try origin(server)
        guard let url = URL(string: origin + "/api/v1/" + path) else { throw AccountError.address }
        var request = URLRequest(url: url, timeoutInterval: 15)
        request.httpMethod = method
        if let token {
            request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        }
        if let body {
            request.httpBody = try JSONEncoder().encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpCookieStorage = nil
        configuration.urlCredentialStorage = nil
        let session = URLSession(configuration: configuration, delegate: NoRedirects(), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (data, response) = try await session.data(for: request)
        try Task.checkCancellation()
        guard let response = response as? HTTPURLResponse else { throw AccountError.request }
        if method == "DELETE", response.statusCode == 404 {
            return Data()
        }
        if response.statusCode == 401 {
            if token != nil {
                NotificationCenter.default.post(name: .sessionExpired, object: origin)
            }
            throw AccountError.signIn
        }
        if response.statusCode == 404 || response.statusCode == 410 {
            throw AccountError.unavailable
        }
        guard (200 ... 299).contains(response.statusCode) else {
            throw path.contains("pairings") ? AccountError.pairing : AccountError.request
        }
        return data
    }
}

extension Notification.Name {
    static let sessionExpired = Notification.Name("zip.psst.ios.sessionExpired")
}

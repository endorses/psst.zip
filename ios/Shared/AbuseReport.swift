import Foundation

#if canImport(Darwin)
    import Darwin
#elseif canImport(Glibc)
    import Glibc
#endif

/// Contact-only reporting: this value never contains a capability, key, filename or full link.
struct AbuseReportContext: Equatable, Hashable, Identifiable {
    let origin: String
    let resourceType: String?
    let resourceID: String?
    var id: String { origin + "|" + (resourceType ?? "") + "|" + (resourceID ?? "") }

    init?(origin: String, resourceType: String? = nil, resourceID: String? = nil) {
        guard origin.utf8.count <= 2048,
            origin.range(
                of: #"^https?://(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(?::[0-9]{1,5})?/?$"#,
                options: [.regularExpression, .caseInsensitive]) != nil,
            let url = URLComponents(string: origin),
            ["http", "https"].contains(url.scheme?.lowercased()), let host = url.host,
            !host.isEmpty,
            url.user == nil, url.password == nil, url.query == nil, url.fragment == nil,
            url.path.isEmpty || url.path == "/", url.port == nil || (1...65535).contains(url.port!),
            !origin.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) })
        else { return nil }
        if resourceType != nil || resourceID != nil {
            guard let resourceType, ["transfer", "slot"].contains(resourceType), let resourceID,
                resourceID.count == 36, UUID(uuidString: resourceID) != nil
            else { return nil }
        }
        guard let canonicalHost = Self.validatedHost(host) else { return nil }
        let scheme = url.scheme!.lowercased()
        let defaultPort =
            (scheme == "https" && url.port == 443) || (scheme == "http" && url.port == 80)
        let suffix = url.port == nil || defaultPort ? "" : ":\(url.port!)"
        self.origin = "\(scheme)://\(canonicalHost)\(suffix)"
        self.resourceType = resourceType
        self.resourceID = resourceID?.lowercased()
    }

    private static func validatedHost(_ value: String) -> String? {
        let lower = value.lowercased()
        if lower.contains(":") {
            let host =
                lower.hasPrefix("[") && lower.hasSuffix("]")
                ? String(lower.dropFirst().dropLast()) : lower
            var address = in6_addr()
            guard host.withCString({ inet_pton(AF_INET6, $0, &address) }) == 1 else { return nil }
            let groups = withUnsafeBytes(of: address) { bytes in
                stride(from: 0, to: 16, by: 2).map {
                    String(Int(bytes[$0]) * 256 + Int(bytes[$0 + 1]), radix: 16)
                }
            }
            return "[" + groups.joined(separator: ":") + "]"
        }
        let host = lower.hasSuffix(".") ? String(lower.dropLast()) : lower
        let labels = host.split(separator: ".", omittingEmptySubsequences: false)
        let alphanumeric = Set("abcdefghijklmnopqrstuvwxyz0123456789")
        guard !host.isEmpty, host.count <= 253,
            labels.allSatisfy({ label in
                !label.isEmpty && label.count <= 63 && alphanumeric.contains(label.first!)
                    && alphanumeric.contains(label.last!)
                    && label.allSatisfy { alphanumeric.contains($0) || $0 == "-" }
            })
        else { return nil }
        if host.allSatisfy({ $0.isNumber || $0 == "." }) {
            guard labels.count == 4,
                labels.allSatisfy({ label in
                    (label.count == 1 || label.first != "0")
                        && Int(label).map { (0...255).contains($0) } == true
                })
            else { return nil }
        }
        return host
    }

    /// Report metadata is independent of key validity; secrets and queries never enter this value.
    static func fromLink(_ raw: String) -> AbuseReportContext? {
        guard raw.utf8.count <= 8192,
            var url = URLComponents(string: raw), url.user == nil, url.password == nil
        else { return nil }
        let parts = url.percentEncodedPath.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 3, parts[0].isEmpty, ["d", "u"].contains(parts[1]) else { return nil }
        let kind = parts[1] == "d" ? "transfer" : "slot"
        let identifier = String(parts[2])
        url.path = ""
        url.query = nil
        url.fragment = nil
        guard let origin = url.string else { return nil }
        return AbuseReportContext(origin: origin, resourceType: kind, resourceID: identifier)
    }

    var text: String {
        var result = "Instance: \(origin)"
        if let resourceType, let resourceID {
            result += "\nResource type: \(resourceType)\nResource ID: \(resourceID)"
        }
        return result
    }

    func mailURL(contact: String) -> URL? {
        guard let contact = AbuseContact.validated(contact) else { return nil }
        let unreserved = CharacterSet(
            charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")
        guard
            let address = contact.addingPercentEncoding(
                withAllowedCharacters: unreserved.union(CharacterSet(charactersIn: "@"))),
            let subject = "psst.zip abuse report".addingPercentEncoding(
                withAllowedCharacters: unreserved),
            let body =
                (text
                + "\n\nDescribe your concern here. Do not include link secrets, passwords or private files.")
                .addingPercentEncoding(withAllowedCharacters: unreserved)
        else { return nil }
        return URL(string: "mailto:\(address)?subject=\(subject)&body=\(body)")
    }
}

enum AbuseContact {
    static func validated(_ value: String) -> String? {
        guard !value.isEmpty, value.utf8.count <= 254,
            value.unicodeScalars.allSatisfy({ $0.value < 128 })
        else { return nil }
        let parts = value.split(separator: "@", omittingEmptySubsequences: false)
        guard parts.count == 2 else { return nil }
        let local = parts[0]
        let domain = parts[1]
        let letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
        let localAllowed = Set(letters + "._+%-")
        guard !local.isEmpty, local.count <= 64, local.first != ".", local.last != ".",
            !local.contains(".."), local.allSatisfy({ localAllowed.contains($0) })
        else { return nil }
        let labels = domain.split(separator: ".", omittingEmptySubsequences: false)
        let alphanumeric = Set(letters)
        let domainAllowed = Set(letters + "-")
        guard labels.count >= 2,
            labels.allSatisfy({ label in
                !label.isEmpty && label.count <= 63 && alphanumeric.contains(label.first!)
                    && alphanumeric.contains(label.last!)
                    && label.allSatisfy { domainAllowed.contains($0) }
            })
        else { return nil }
        return String(local) + "@" + domain.lowercased()
    }
}

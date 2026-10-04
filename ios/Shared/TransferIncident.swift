import Foundation
#if canImport(Shared)
    import Shared
#endif

/// Match a known status/code pair; never display a server response or URL as an error.
enum TransferIncident: Error, LocalizedError, Equatable {
    case paused, revoked
    case budget(retryAt: Date?)
    case accountingUnavailable
    case policyChanged

    static func response(status: Int, body: Data, codeHeader: String? = nil,
                         retryHeader: String? = nil) -> TransferIncident?
    {
        guard [409, 429, 503, 410].contains(status) else { return nil }
        struct Failure: Decodable {
            let code: String?
            let retryAt: String?
            enum CodingKeys: String, CodingKey { case code, retryAt = "retry_at" }
            init(from decoder: Decoder) throws {
                let fields = try decoder.container(keyedBy: CodingKeys.self)
                code = try? fields.decode(String.self, forKey: .code)
                retryAt = try? fields.decode(String.self, forKey: .retryAt)
            }
        }
        let failure = body.count <= 4096 ? try? JSONDecoder().decode(Failure.self, from: body) : nil
        let code: String? = if let codeHeader, codeHeader.utf8.count <= 64 {
            codeHeader
        } else {
            failure?.code
        }
        switch (status, code) {
        case (503, "public_transfers_paused"): return .paused
        case (410, "resource_revoked"): return .revoked
        case (429, "traffic_budget_exhausted"):
            return .budget(retryAt: retryDate(retryHeader) ?? retryDate(failure?.retryAt))
        case (503, "traffic_accounting_unavailable"): return .accountingUnavailable
        case (409, "traffic_policy_changed"): return .policyChanged
        default: return nil
        }
    }

    /// Parse a bounded UTC instant instead of interpolating untrusted response text.
    static func retryDate(_ text: String?) -> Date? {
        guard let text, text.utf8.count <= 64,
              text.range(of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$"#,
                         options: .regularExpression) != nil else { return nil }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = text.contains(".") ? [.withInternetDateTime, .withFractionalSeconds] : [.withInternetDateTime]
        return formatter.date(from: text)
    }

    /// Metadata-only diagnosis of an interrupted response; never grants permission to retry.
    static func trafficStatus(_ data: Data) -> TransferIncident? {
        guard data.count <= 4096 else { return nil }
        struct Status: Decodable {
            let state: String
            let retry_at: String?
        }
        guard let status = try? JSONDecoder().decode(Status.self, from: data) else { return nil }
        switch status.state {
        case "exhausted": return .budget(retryAt: retryDate(status.retry_at))
        case "unavailable": return .accountingUnavailable
        case "paused": return .paused
        case "revoked": return .revoked
        default: return nil
        }
    }

    static func from(_ error: Error) -> TransferIncident? {
        if let incident = error as? TransferIncident {
            return incident
        }
        #if canImport(Shared)
            // Kotlin/Native exports the original @Throws exception on NSError.
            // Match its type instead of parsing a localized or server-controlled message.
            switch (error as NSError).kotlinException {
            case is PublicTransfersPausedException: return .paused
            case is ResourceRevokedException: return .revoked
            case let budget as TrafficBudgetExhaustedException: return .budget(retryAt: retryDate(budget.retryAt))
            case is TrafficAccountingUnavailableException: return .accountingUnavailable
            case is TrafficPolicyChangedException: return .policyChanged
            default: break
            }
        #endif
        return nil
    }

    var errorDescription: String? {
        switch self {
        case .paused:
            "The server administrator has paused file transfers. Your saved files are safe. Retry after the administrator resumes transfers."
        case .revoked:
            "This link is no longer available. Ask the sender for a new link. Files already saved on this device are still available."
        case let .budget(retryAt):
            if let retryAt {
                "A transfer traffic budget has been reached. Your saved files are safe. Retry after \(retryAt.formatted(date: .abbreviated, time: .shortened)), or contact the server administrator."
            } else {
                "A transfer traffic budget has been reached. Your saved files are safe. Retry in the next billing cycle, or contact the server administrator."
            }
        case .accountingUnavailable:
            "Server traffic accounting is unavailable. Your saved files are safe. Retry once the server administrator has resolved it."
        case .policyChanged:
            "Server transfer limits changed. Your saved files are safe. Retry to use the new limits."
        }
    }
}

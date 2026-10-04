import Foundation
#if canImport(Shared)
    import Shared
#endif

/// Match a known status/code pair; never display a server response or URL as an error.
enum TransferIncident: Error, LocalizedError, Equatable {
    case paused, revoked

    static func response(status: Int, body: Data, codeHeader: String? = nil) -> TransferIncident? {
        guard status == 503 || status == 410 else { return nil }
        struct Failure: Decodable { let code: String? }
        let code: String? = if let codeHeader, codeHeader.utf8.count <= 64 {
            codeHeader
        } else if body.count <= 4096 {
            (try? JSONDecoder().decode(Failure.self, from: body))?.code
        } else {
            nil
        }
        switch (status, code) {
        case (503, "public_transfers_paused"): return .paused
        case (410, "resource_revoked"): return .revoked
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
        }
    }
}

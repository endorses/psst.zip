import Foundation
import Shared

/// Diagnose one failed transport using a small control response, never a second payload request.
enum TransferTrafficRecovery {
    enum Resource: String { case transfer = "transfers", slot = "slots" }
    enum Direction: String { case upload, download }

    static func inspect(_ error: Error, server: String, resource: Resource, id: String,
                        direction: Direction, token: String? = nil) async -> TransferIncident?
    {
        if let known = TransferIncident.from(error) {
            return known
        }
        guard !Task.isCancelled, UUID(uuidString: id) != nil else { return nil }
        let native = error as NSError
        let sharedFailure = native.kotlinException as? KotlinThrowable
        let eligible = sharedFailure.map { TrafficFailureClassifier.shared.shouldProbe(error: $0) } ?? false
        let networkCodes = [NSURLErrorTimedOut, NSURLErrorCannotFindHost, NSURLErrorCannotConnectToHost,
                            NSURLErrorNetworkConnectionLost, NSURLErrorNotConnectedToInternet]
        guard eligible || (native.domain == NSURLErrorDomain && networkCodes.contains(native.code)) else { return nil }
        do {
            let data = try await AccountHTTP.request(server: server,
                                                     path: resource.rawValue + "/" + id + "/traffic-status?direction=" + direction.rawValue,
                                                     token: token, maximumBytes: 4096, timeout: 1.5)
            guard !Task.isCancelled else { return nil }
            return TransferIncident.trafficStatus(data)
        } catch {
            // An unsupported endpoint, offline host or malformed body is not evidence
            // that capacity is exhausted. Preserve the original interruption message.
            return Task.isCancelled ? nil : TransferIncident.from(error)
        }
    }
}

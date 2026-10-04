import Shared
import SwiftUI

/// Account-scoped, one-shot information; the server decides admission at stream time.
struct AccountTrafficView: View {
    @Environment(ServerConfigManager.self) private var config
    @State private var snapshot: TrafficSnapshot?
    @State private var busy = false
    @State private var error: String?
    @State private var checkedAt: Date?

    var body: some View {
        Form {
            Section("Transfer traffic") {
                if let snapshot {
                    Text(status(snapshot)).foregroundStyle(snapshot.state == "ready" ? PsstTheme.secondary : PsstTheme.warning)
                    LabeledContent("Account budget per cycle", value: bytes(snapshot.usage.budgetBytes))
                    LabeledContent("Charged in this cycle", value: bytes(snapshot.usage.chargedBytes))
                    LabeledContent("Account budget remaining", value: bytes(snapshot.usage.remainingBytes))
                    if let end = TransferIncident.retryDate(snapshot.cycle.end) {
                        LabeledContent("Next cycle", value: end.formatted(date: .abbreviated, time: .shortened))
                    }
                    Text(snapshot.policy.basis == "outbound" ? "The budget counts downloads from the server." : "The budget counts uploads and downloads.")
                        .font(.footnote).foregroundStyle(PsstTheme.secondary)
                    DisclosureGroup("How traffic is counted") {
                        LabeledContent("Observed uploads", value: bytes(snapshot.usage.observedUploadedBytes))
                        LabeledContent("Observed downloads", value: bytes(snapshot.usage.observedDownloadedBytes))
                        LabeledContent("Reserved uploads", value: bytes(snapshot.usage.reservedUploadedBytes))
                        LabeledContent("Reserved downloads", value: bytes(snapshot.usage.reservedDownloadedBytes))
                        LabeledContent("Conservative upload charges", value: bytes(snapshot.usage.conservativeUploadedBytes))
                        LabeledContent("Conservative download charges", value: bytes(snapshot.usage.conservativeDownloadedBytes))
                        if let start = TransferIncident.retryDate(snapshot.recordingStartedAt) {
                            Text("Account traffic has been recorded since \(start.formatted(date: .abbreviated, time: .shortened)).")
                        }
                        Text("Charges include active reservations and conservative charges after an interrupted accounting operation. Deleting files does not restore traffic allowance.")
                    }.font(.footnote)
                    Text("Server-wide budgets, bandwidth, pauses and link limits also apply. These figures are not a reservation or a provider's bill.")
                        .font(.footnote).foregroundStyle(PsstTheme.secondary)
                    if let checkedAt {
                        Text("Checked \(checkedAt.formatted(date: .abbreviated, time: .shortened))")
                            .font(.caption).foregroundStyle(PsstTheme.secondary)
                    }
                }
                if let error {
                    Text(error).foregroundStyle(PsstTheme.warning)
                }
                Button(busy ? "Checking traffic…" : "Refresh traffic usage") {
                    Task { await refresh() }
                }.disabled(busy)
            }
        }
        .modifier(PsstStyle())
        .navigationTitle("Transfer traffic").navigationBarTitleDisplayMode(.inline)
        .task(id: config.session?.sessionID) {
            snapshot = nil
            checkedAt = nil
            error = nil
            await refresh()
        }
    }

    private func status(_ snapshot: TrafficSnapshot) -> String {
        if snapshot.state == "unavailable" {
            return "Traffic accounting is unavailable. Transfers may be stopped until the administrator resolves it."
        }
        if !snapshot.policy.enforcementEnabled {
            return "Traffic budget enforcement is off."
        }
        if snapshot.state == "exhausted" {
            return "A transfer traffic budget has been reached."
        }
        return "Traffic budget enforcement is on."
    }

    private func bytes(_ value: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: value, countStyle: .binary)
    }

    @MainActor
    private func refresh() async {
        guard !busy, let session = config.session, session.canTransfer, !config.needsSignIn else { return }
        busy = true
        defer { busy = false }
        do {
            let client = config.makeApiClient(session: session)
            defer { client.close() }
            let next = try await client.traffic.usage()
            try config.check(session)
            guard !Task.isCancelled else { return }
            snapshot = next
            checkedAt = Date()
            error = nil
        } catch {
            guard !Task.isCancelled, config.session == session else { return }
            self.error = "Traffic usage could not be refreshed. Previously shown figures may be out of date."
            if (error as NSError).kotlinException is AuthenticationRequiredException {
                await config.refreshAccount()
            }
        }
    }
}

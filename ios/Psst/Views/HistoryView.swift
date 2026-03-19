import SwiftUI

struct HistoryView: View {
    @Environment(TransferHistoryStore.self) private var historyStore

    var body: some View {
        NavigationStack {
            Group {
                if historyStore.records.isEmpty {
                    ContentUnavailableView(
                        "No Transfers",
                        systemImage: "clock",
                        description: Text("Your transfer history will appear here.")
                    )
                } else {
                    List {
                        ForEach(historyStore.records) { record in
                            TransferHistoryRow(record: record)
                        }
                        .onDelete { offsets in
                            historyStore.remove(at: offsets)
                        }
                    }
                }
            }
            .navigationTitle("History")
            .toolbar {
                if !historyStore.records.isEmpty {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Clear All", role: .destructive) {
                            historyStore.removeAll()
                        }
                    }
                }
            }
        }
    }
}

private struct TransferHistoryRow: View {
    let record: TransferRecord

    var body: some View {
        HStack {
            Image(systemName: record.direction == .sent ? "arrow.up.circle.fill" : "arrow.down.circle.fill")
                .foregroundStyle(iconColor)
                .font(.title2)

            VStack(alignment: .leading, spacing: 4) {
                Text(record.direction == .sent ? "Sent" : "Received")
                    .font(.headline)
                Text(record.summary)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Text(record.createdAt, style: .relative)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            Spacer()

            statusBadge
        }
        .padding(.vertical, 4)
    }

    private var iconColor: Color {
        switch record.state {
        case .complete: .green
        case .inProgress: .blue
        case .failed: .red
        case .expired: .gray
        }
    }

    @ViewBuilder
    private var statusBadge: some View {
        switch record.state {
        case .complete:
            if record.isExpired {
                Text("Expired")
                    .font(.caption)
                    .foregroundStyle(.gray)
            } else {
                Text("Complete")
                    .font(.caption)
                    .foregroundStyle(.green)
            }
        case .inProgress:
            Text("In Progress")
                .font(.caption)
                .foregroundStyle(.blue)
        case .failed:
            Text("Failed")
                .font(.caption)
                .foregroundStyle(.red)
        case .expired:
            Text("Expired")
                .font(.caption)
                .foregroundStyle(.gray)
        }
    }
}

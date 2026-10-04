import QuickLook
import SwiftUI

/// Local actions never use account revocation, even when the same link is also owned.
struct GuestDownloadDetail: View {
    let record: GuestDownload
    @Environment(GuestDownloadStore.self) private var store
    @Environment(GuestTransferModel.self) private var model
    @State private var preview: URL?
    @State private var redownload = false
    private var current: GuestDownload {
        store.records.first { $0.id == record.id } ?? record
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(current.origin).font(.caption).textSelection(.enabled)
            Text("Downloaded files").font(.title2.bold())
            Text("Files → psst.zip → Received").font(.footnote)
            if let consent = model.pendingConsent, consent.record.id == current.id {
                if consent.unavailableCount > 0 {
                    Text("\(consent.unavailableCount) files have reached their download limit. Receive only the available files?").font(.headline)
                } else {
                    Text("Receive \(consent.manifest.count) files (" + ByteCountFormatter.string(fromByteCount: consent.total, countStyle: .binary) + ")?").font(.headline)
                }
                Text("This transfer will use device storage and network data. Continue only if you trust the sender.").font(.footnote)
                Button(consent.availableOnly ? "Receive available files" : "Receive files") { model.confirmReceive() }.buttonStyle(PrimaryAction())
                Button("Cancel") { model.cancel() }
            }
            if current.receiptPending {
                Text("Saved. Delivery confirmation will retry when connected.").font(.footnote)
                Button("Retry confirmation") { Task { await store.flushReceipts() } }
            }
            if model.active, model.currentID == current.id {
                ProgressView(model.stage)
                Button("Stop", role: .destructive) { model.cancel() }
            }
            if model.currentID == current.id, let error = model.error {
                Text(error).foregroundStyle(PsstTheme.error)
            }
            ForEach(current.files) { file in
                HStack {
                    VStack(alignment: .leading) {
                        Text(file.name).lineLimit(2)
                        if let remaining = current.remainingDownloads?[file.id] {
                            Text(remaining == 0 ? "Download limit reached" : "\(remaining) download attempts remaining").font(.caption).foregroundStyle(PsstTheme.secondary)
                        }
                    }
                    Spacer()
                    if let url = store.url(file) {
                        Button { preview = url } label: { Image(systemName: "doc.viewfinder").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Open " + file.name)
                        ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Share " + file.name)
                    } else {
                        Text("Not saved").font(.caption)
                    }
                }
            }
            if model.pendingConsent == nil, current.files.isEmpty || current.files.contains(where: { store.url($0) == nil }) {
                Button(store.requiresRedownloadConsent(current) ? "Download missing files" : "Resume receiving") {
                    if store.requiresRedownloadConsent(current) {
                        redownload = true
                    } else {
                        model.resume(current)
                    }
                }.disabled(model.active || (!current.files.isEmpty && current.files.filter { store.url($0) == nil }.allSatisfy { current.remainingDownloads?[$0.id] == 0 }))
                Button("Refresh availability") { Task { await model.refreshAttempts(current) } }.disabled(model.active)
            }
        }
        .quickLookPreview($preview)
        .confirmationDialog("Download missing files again? The server may no longer permit another download.", isPresented: $redownload, titleVisibility: .visible) {
            Button("Download missing files") { model.resume(current, allowRedownload: true) }
        }
    }
}

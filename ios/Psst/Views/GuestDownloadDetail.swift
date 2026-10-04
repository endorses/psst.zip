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
                    Text(file.name).lineLimit(2)
                    Spacer()
                    if let url = store.url(file) {
                        Button { preview = url } label: { Image(systemName: "doc.viewfinder").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Open " + file.name)
                        ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Share " + file.name)
                    } else {
                        Text("Not saved").font(.caption)
                    }
                }
            }
            if current.files.isEmpty || current.files.contains(where: { store.url($0) == nil }) {
                Button(store.requiresRedownloadConsent(current) ? "Download missing files" : "Resume receiving") {
                    if store.requiresRedownloadConsent(current) {
                        redownload = true
                    } else {
                        model.resume(current)
                    }
                }.disabled(model.active)
            }
        }
        .quickLookPreview($preview)
        .confirmationDialog("Download missing files again? The server may no longer permit another download.", isPresented: $redownload, titleVisibility: .visible) {
            Button("Download missing files") { model.resume(current, allowRedownload: true) }
        }
    }
}

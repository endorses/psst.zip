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
        (try? store.find(record.id)) ?? record
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(verbatim: current.origin).font(.caption).textSelection(.enabled)
            if let context = AbuseReportContext(origin: current.origin, resourceType: "transfer", resourceID: current.transferID) {
                AbuseReportButton(context: context).id(context.id)
            }
            Text(verbatim: current.sharedTitle ?? L10n.text(current.complete ? (current.files.count == 1 ? "Saved file" : "Saved files") : (current.files.count == 1 ? "Save file" : "Save files")))
                .font(.title2.bold())
            if current.exhausted == true {
                Text(L10n.text("Download limit reached. Saved copies remain available.")).font(.footnote)
            }
            Text(L10n.text("Files → psst.zip → Received")).font(.footnote)
            if let consent = model.pendingConsent, consent.record.id == current.id {
                if consent.unavailableCount > 0 {
                    Text(L10n.text(L10n.format("%lld files have reached their download limit. Receive only the available files?", Int64(consent.unavailableCount)))).font(.headline)
                } else {
                    Text(L10n.text(L10n.format("Receive %lld files (%@)?", Int64(consent.manifest.count), L10n.bytes(consent.total, binary: true)))).font(.headline)
                }
                Text(L10n.text("This transfer will use device storage and network data. Continue only if you trust the sender.")).font(.footnote)
                Button(L10n.text(consent.availableOnly ? "Receive available files" : "Receive files")) { model.confirmReceive() }.buttonStyle(PrimaryAction())
                Button(L10n.text("Cancel")) { model.cancel() }
            }
            if current.receiptPending {
                Text(L10n.text("Saved. Delivery confirmation will retry when connected.")).font(.footnote)
                Button(L10n.text("Retry confirmation")) { Task { await store.flushReceipts() } }
            }
            if model.active, model.currentID == current.id {
                ProgressView(L10n.text(model.stage.rawValue))
                Button(L10n.text("Stop"), role: .destructive) { model.cancel() }
            }
            if model.currentID == current.id, let error = model.error {
                Text(L10n.text(error)).foregroundStyle(PsstTheme.error)
            }
            ForEach(current.files) { file in
                HStack {
                    VStack(alignment: .leading) {
                        Text(verbatim: GuestFiles.displayName(file.name)).lineLimit(2)
                        if let remaining = current.remainingDownloads?[file.id] {
                            Text(L10n.text(remaining == 0 ? "Download limit reached" : L10n.format("%lld download attempts remaining", Int64(remaining)))).font(.caption).foregroundStyle(PsstTheme.secondary)
                        }
                    }
                    Spacer()
                    if let url = store.url(file) {
                        Button {
                            preview = url
                        } label: {
                            Image(systemName: "doc.viewfinder").frame(minWidth: 44, minHeight: 44)
                        }.accessibilityLabel(L10n.text(L10n.format("Open %@", GuestFiles.displayName(file.name))))
                        ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel(L10n.text(L10n.format("Share %@", GuestFiles.displayName(file.name))))
                    } else {
                        Text(L10n.text("Not saved")).font(.caption)
                    }
                }
            }
            if model.pendingConsent == nil, current.exhausted != true, current.files.isEmpty || current.files.contains(where: { store.url($0) == nil }) {
                Button(L10n.text(store.requiresRedownloadConsent(current) ? "Download missing files" : "Resume receiving")) {
                    if store.requiresRedownloadConsent(current) {
                        redownload = true
                    } else {
                        model.resume(current)
                    }
                }.disabled(model.active || (!current.files.isEmpty && current.files.filter { store.url($0) == nil }.allSatisfy { current.remainingDownloads?[$0.id] == 0 }))
                Button(L10n.text("Refresh availability")) { Task { await model.refreshAttempts(current) } }.disabled(model.active)
            }
        }
        .quickLookPreview($preview)
        .confirmationDialog(L10n.text("Download missing files again? The server may no longer permit another download."), isPresented: $redownload, titleVisibility: .visible) {
            Button(L10n.text("Download missing files")) { model.resume(current, allowRedownload: true) }
        }
    }
}

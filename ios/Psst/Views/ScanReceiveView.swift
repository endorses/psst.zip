import QuickLook
import Shared
import SwiftUI
import UIKit

/// A guest destination exists independently of the account-gated Send / Receive tabs.
struct ScanReceiveView: View {
    var historyOnly = false
    @Environment(\.dismiss) private var dismiss
    @Environment(ServerConfigManager.self) private var config
    @Environment(GuestDownloadStore.self) private var store
    @Environment(GuestTransferModel.self) private var model
    @State private var scanning = false
    @State private var pasted = ""
    @State private var error: String?
    @State private var uploadLink: ParsedUrl?
    @State private var pairingRaw: String?
    @State private var pairingServer = ""
    @State private var pairing = false
    @State private var picking = false
    @State private var selected: [URL] = []
    @State private var preview: URL?
    @State private var removal: GuestDownload?
    @State private var redownload: GuestDownload?
    @State private var scanResult: String?
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    inputControls
                    if let error {
                        Text(error).foregroundStyle(PsstTheme.error)
                    }
                    if let message = model.error {
                        Text(message).foregroundStyle(PsstTheme.error)
                    }
                    if let message = store.error {
                        Text(message).foregroundStyle(PsstTheme.error)
                    }
                    pairingPanel
                    uploadPanel
                    progressPanel
                    if let id = model.currentID, let record = store.records.first(where: { $0.id == id }) {
                        receivedDetail(record)
                    }
                    historyEntries
                }.padding().frame(maxWidth: 620)
            }
            .navigationTitle(historyOnly ? "Received on this device" : "Scan QR code")
            .toolbar {
                if historyOnly {
                    Button("Done") { dismiss() }
                }
            }
            .sheet(isPresented: $scanning, onDismiss: {
                if let value = scanResult {
                    scanResult = nil; accept(value)
                }
            }) {
                NavigationStack {
                    PairingScanner(allowsPaste: true) { raw in scanResult = raw; scanning = false }
                        .navigationTitle("Scan QR code")
                        .toolbar { Button("Paste link") { scanning = false }; Button("Cancel") { scanning = false } }
                }.modifier(PsstAppearance())
            }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                do {
                    let values = try result.get()
                    _ = try BufferedUpload.sizes(values, limit: BufferedUpload.maxFileBytes)
                    selected = values
                } catch { self.error = "Could not select these files. Check access and the 25 MiB per-file limit." }
            }
            .confirmationDialog("Remove this local entry? Saved files will remain. The sender’s link will keep working.", isPresented: Binding(get: { removal != nil }, set: {
                if !$0 {
                    removal = nil
                }
            }), titleVisibility: .visible) {
                Button("Remove entry", role: .destructive) {
                    if let removal {
                        do { try store.remove(removal) } catch { self.error = "Could not update local history. Free storage and retry." }
                    }
                    removal = nil
                }
            }
            .confirmationDialog("Download missing files again? The server may no longer permit another download.", isPresented: Binding(get: { redownload != nil }, set: {
                if !$0 {
                    redownload = nil
                }
            }), titleVisibility: .visible) {
                Button("Download missing files") {
                    if let redownload {
                        model.resume(redownload, allowRedownload: true)
                    }; redownload = nil
                }
            }
            .quickLookPreview($preview)
        }.modifier(PsstStyle())
    }

    @ViewBuilder private var inputControls: some View {
        if !historyOnly {
            Text("Receive files without an account").font(.title2.bold())
            Text("Scan a psst.zip QR code or paste a link. Files are decrypted on this device and saved in Files → psst.zip → Received.")
                .foregroundStyle(PsstTheme.secondary)
            Button { scanning = true } label: { Label("Scan QR code", systemImage: "qrcode.viewfinder") }
                .buttonStyle(PrimaryAction()).disabled(model.active || pairing)
            TextField("Paste a complete link", text: $pasted, axis: .vertical)
                .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL)
                .textFieldStyle(.roundedBorder).privacySensitive()
            HStack {
                PasteButton(payloadType: String.self) { values in pasted = values.first ?? "" }
                    .disabled(model.active || pairing)
                Button("Receive files") { let value = pasted; pasted = ""; accept(value) }
                    .disabled(pasted.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.active || pairing)
            }
        }
    }

    @ViewBuilder private var pairingPanel: some View {
        if let raw = pairingRaw {
            VStack(alignment: .leading, spacing: 12) {
                Label("Connect account", systemImage: "person.crop.circle")
                Text(pairingServer).textSelection(.enabled)
                Text(config.isConfigured ? "Connecting will replace the active account on this device." : "Connect to this server using its single-use login code.")
                Button("Connect to this server") {
                    pairing = true
                    Task {
                        defer { pairing = false; pairingRaw = nil }
                        do { try await config.pair(raw: raw) }
                        catch { self.error = "This login code could not be used. Generate a fresh code and check the connection." }
                    }
                }.buttonStyle(PrimaryAction()).disabled(pairing)
                Button("Cancel") { pairingRaw = nil }.disabled(pairing)
            }
        }
    }

    @ViewBuilder private var uploadPanel: some View {
        if let uploadLink {
            VStack(alignment: .leading, spacing: 12) {
                Text("Send to this receive link").font(.headline)
                Text(uploadLink.origin).textSelection(.enabled)
                Text("Choose files to send to the owner of this link. Up to 25 MiB per file.")
                Button("Choose files") { picking = true }.disabled(model.active)
                ForEach(selected, id: \.self) { Text($0.lastPathComponent).lineLimit(2) }
                Button("Send files") { model.send(selected, to: uploadLink) }
                    .buttonStyle(PrimaryAction()).disabled(selected.isEmpty || model.active || model.uploadComplete)
                if model.uploadComplete {
                    Label("Files sent", systemImage: "checkmark.circle")
                }
                if model.cleanupPending {
                    Text("Partial server files will be removed when the connection is restored.").font(.footnote)
                }
                Button("Close receive link") { self.uploadLink = nil; selected = [] }.disabled(model.active)
            }
        }
    }

    @ViewBuilder private var progressPanel: some View {
        if model.active {
            Text(model.stage).font(.headline).accessibilityAddTraits(.updatesFrequently)
            if model.stage == "Downloading" || model.stage == "Uploading" {
                ProgressView(value: Double(model.bytes), total: Double(max(1, model.total)))
                Text(ByteCountFormatter.string(fromByteCount: model.bytes, countStyle: .file) + " / " + ByteCountFormatter.string(fromByteCount: model.total, countStyle: .file)).font(.caption)
                if model.fileNumber > 0 {
                    Text("File \(model.fileNumber)").font(.caption)
                }
            } else {
                ProgressView()
            }
            Button("Stop", role: .destructive) { model.cancel() }
        }
    }

    @ViewBuilder private var historyEntries: some View {
        Divider()
        Text("Received on this device").font(.title2.bold())
        Text("These local downloads stay available when you sign out. Removing an entry keeps saved files and does not revoke the sender’s link.").font(.footnote).foregroundStyle(PsstTheme.secondary)
        if store.records.isEmpty {
            Text("No received files yet")
        }
        ForEach(store.records) { record in
            VStack(alignment: .leading, spacing: 8) {
                Text(record.files.first?.name ?? "File transfer").font(.headline).lineLimit(2)
                Text(record.origin).font(.caption).textSelection(.enabled)
                Text(record.createdAt, style: .date).font(.caption)
                Text(summary(record))
                Text(ByteCountFormatter.string(fromByteCount: record.files.reduce(0) { $0 + $1.size }, countStyle: .file)).font(.caption)
                HStack {
                    Button("View files") { model.show(record) }.disabled(model.active)
                    Button("Remove entry", role: .destructive) { removal = record }.disabled(model.active)
                }
            }.padding(.vertical, 8)
            Divider()
        }
    }

    private func accept(_ raw: String) {
        guard !model.active, !pairing else { return }
        model.resetPresentation()
        error = nil; pairingRaw = nil; uploadLink = nil; selected = []
        guard let input = ScanInputClassifier.shared.classify(raw: raw) else { error = GuestError.input.localizedDescription; return }
        switch input.kind {
        case .download:
            guard let link = input.link else { return }
            let id = GuestDownload.identity(origin: link.origin, transferID: link.id)
            if let record = store.records.first(where: { $0.id == id }), store.requiresRedownloadConsent(record) {
                // Validate the key without changing the stored working key before offering redownload.
                do { _ = try store.prepare(origin: link.origin, transferID: link.id, key: link.key.toData()); redownload = record }
                catch { error = GuestError.conflictingKey.localizedDescription }
            } else {
                model.receive(link)
            }
        case .upload: uploadLink = input.link
        case .pairing:
            pairingRaw = raw
            pairingServer = input.pairing?.serverUrl ?? ""
        default: error = GuestError.input.localizedDescription
        }
    }

    private func summary(_ record: GuestDownload) -> String {
        let count = record.files.filter { store.url($0) != nil }.count
        return "\(count) of \(record.files.count) files saved" + (record.complete && count == record.files.count ? " · Saved" : " · Partial")
    }

    private func receivedDetail(_ record: GuestDownload) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(record.origin).font(.caption).textSelection(.enabled)
            let count = record.files.filter { store.url($0) != nil }.count
            Text(count == 1 ? "1 file saved" : "\(count) files saved").font(.title2.bold())
            Text("Files → psst.zip → Received").font(.footnote)
            if record.receiptPending {
                Text("Saved. Delivery confirmation will retry when connected.").font(.footnote)
            }
            ForEach(record.files) { file in
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
            if !model.active, record.files.isEmpty || record.files.contains(where: { store.url($0) == nil }) {
                Button(store.requiresRedownloadConsent(record) ? "Download missing files" : "Retry receiving") {
                    if store.requiresRedownloadConsent(record) {
                        redownload = record
                    } else {
                        model.resume(record)
                    }
                }
            }
        }
    }
}

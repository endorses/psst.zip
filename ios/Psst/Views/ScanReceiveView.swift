import ImageIO
import QuickLook
import Shared
import SwiftUI
import UIKit
import Vision

/// A guest destination exists independently of the account-gated Send / Receive tabs.
struct ScanReceiveView: View {
    var isSelected = true
    var onHistory: () -> Void = {}
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.openURL) private var openURL
    @State private var visible = false
    @State private var cameraEnabled = true
    @State private var imagePicking = false
    @Environment(\.dismiss) private var dismiss
    @Environment(ServerConfigManager.self) private var config
    @Environment(GuestDownloadStore.self) private var store
    @Environment(GuestTransferModel.self) private var model
    @State private var reportContext: AbuseReportContext?
    @State private var pasted = ""
    @State private var error: String?
    @State private var uploadLink: ParsedUrl?
    @State private var uploadLimit: Int64?
    @State private var capacityMessage = "Checking receive capacity…"
    @State private var capacityReady = false
    @State private var checkingCapacity = false
    @State private var capacityRequest = UUID()
    @State private var pairingRaw: String?
    @State private var pairingServer = ""
    @State private var pairing = false
    @State private var picking = false
    @State private var selected: [URL] = []
    @State private var redownload: GuestDownload?
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
                    if let reportContext, model.currentID == nil || !store.records.contains(where: { $0.id == model.currentID }) {
                        AbuseReportButton(context: reportContext).id(reportContext.id)
                    }
                    pairingPanel
                    uploadPanel
                    progressPanel
                    if let id = model.currentID, let record = store.records.first(where: { $0.id == id }) {
                        GuestDownloadDetail(record: record)
                        Button("View in History", action: onHistory)
                    }
                }.padding().frame(maxWidth: 620)
            }
            .navigationTitle("Scan QR code")
            .onAppear { visible = true }
            .onDisappear { visible = false }
            .fileImporter(isPresented: $imagePicking, allowedContentTypes: [.image]) { result in
                do {
                    guard let url = try result.get().first else { return }
                    let raw = try QRImageReader.read(url)
                    cameraEnabled = false
                    accept(raw)
                } catch { self.error = "Choose an image containing one readable psst.zip QR code." }
            }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                do {
                    let values = try result.get()
                    refreshCapacity(adding: values)
                } catch { self.error = "Could not select these files. Check file access and the server’s file-size limit." }
            }
            .confirmationDialog(
                "Download missing files again? The server may no longer permit another download.",
                isPresented: Binding(
                    get: { redownload != nil },
                    set: {
                        if !$0 {
                            redownload = nil
                        }
                    }), titleVisibility: .visible
            ) {
                Button("Download missing files") {
                    if let redownload {
                        model.resume(redownload, allowRedownload: true)
                    }
                    redownload = nil
                }
            }
        }.modifier(PsstStyle())
    }

    @ViewBuilder private var inputControls: some View {
        if cameraEnabled, !model.active, !pairing, uploadLink == nil, pairingRaw == nil {
            PairingScanner(allowsPaste: true, isActive: visible && isSelected && scenePhase == .active) { raw in
                cameraEnabled = false
                accept(raw)
            }
            .frame(height: 280).clipShape(RoundedRectangle(cornerRadius: 16))
            .accessibilityLabel("QR camera preview")
        } else {
            Button("Scan again") {
                model.resetPresentation()
                uploadLink = nil
                pairingRaw = nil
                clearUploadSelection()
                error = nil
                reportContext = nil
                cameraEnabled = true
            }.disabled(model.active || pairing)
        }
        Text("Scan a psst.zip code or paste a link. Files are saved in Files → psst.zip → Received.")
            .font(.footnote).foregroundStyle(PsstTheme.secondary)
        TextField("Paste a complete link", text: $pasted, axis: .vertical)
            .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL)
            .textFieldStyle(.roundedBorder).privacySensitive()
        HStack {
            PasteButton(payloadType: String.self) { values in pasted = values.first ?? "" }
            Button("Receive files") {
                let value = pasted
                pasted = ""
                cameraEnabled = false
                accept(value)
            }
            .disabled(pasted.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }.disabled(model.active || pairing)
        Button("Camera settings") {
            if let url = URL(string: UIApplication.openSettingsURLString) {
                openURL(url)
            }
        }.font(.footnote)
        Button {
            cameraEnabled = false
            imagePicking = true
        } label: {
            Label("Choose QR image", systemImage: "photo")
        }
        .disabled(model.active || pairing)
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
                        defer {
                            pairing = false
                            pairingRaw = nil
                        }
                        do { try await config.pair(raw: raw) } catch { self.error = "This login code could not be used. Generate a fresh code and check the connection." }
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
                Text("Choose files to send to the owner of this link.")
                if let uploadLimit {
                    Text("Up to " + ByteCountFormatter.string(fromByteCount: uploadLimit, countStyle: .binary) + " per file.").font(.footnote)
                }
                Text(capacityMessage).font(.footnote).accessibilityAddTraits(.updatesFrequently)
                Button("Refresh capacity") { refreshCapacity() }
                    .disabled(model.active || checkingCapacity)
                Button(selected.isEmpty ? "Choose files" : "Add files") { picking = true }
                    .disabled(model.active || checkingCapacity || model.uploadComplete)
                ForEach(selected, id: \.self) { url in
                    HStack {
                        Text(url.lastPathComponent).lineLimit(2)
                        Spacer()
                        Button {
                            selected.removeAll { $0 == url }
                            refreshCapacity()
                        } label: {
                            Image(systemName: "xmark.circle")
                        }
                        .accessibilityLabel("Remove " + url.lastPathComponent)
                        .disabled(model.active)
                    }
                }
                Button("Send files") { model.send(selected, to: uploadLink) }
                    .buttonStyle(PrimaryAction()).disabled(selected.isEmpty || !capacityReady || checkingCapacity || model.active || model.uploadComplete)
                if model.uploadComplete {
                    Label("Files sent", systemImage: "checkmark.circle")
                }
                if model.cleanupPending {
                    Text("Partial server files will be removed when the connection is restored.").font(.footnote)
                }
                Button("Close receive link") {
                    self.uploadLink = nil
                    reportContext = nil
                    clearUploadSelection()
                }.disabled(model.active)
            }
        }
    }

    @ViewBuilder private var progressPanel: some View {
        if model.active {
            Text(model.stage).font(.headline).accessibilityAddTraits(.updatesFrequently)
            if model.stage == "Downloading" || model.stage == "Uploading" {
                ProgressView(value: Double(model.bytes), total: Double(max(1, model.total)))
                Text(ByteCountFormatter.string(fromByteCount: model.bytes, countStyle: .file) + " / " + ByteCountFormatter.string(fromByteCount: model.total, countStyle: .file))
                    .font(.caption)
                if model.fileNumber > 0 {
                    Text("File \(model.fileNumber)").font(.caption)
                }
            } else {
                ProgressView()
            }
            Button("Stop", role: .destructive) { model.cancel() }
        }
    }

    private func accept(_ raw: String) {
        guard !model.active, !pairing else { return }
        model.resetPresentation()
        error = nil
        pairingRaw = nil
        uploadLink = nil
        clearUploadSelection()
        reportContext = AbuseReportContext.fromLink(raw)
        guard let input = ScanInputClassifier.shared.classify(raw: raw) else {
            error = GuestError.input.localizedDescription
            return
        }
        switch input.kind {
        case .download:
            guard let link = input.link else { return }
            reportContext = AbuseReportContext(origin: link.origin, resourceType: "transfer", resourceID: link.id)
            let id = GuestDownload.identity(origin: link.origin, transferID: link.id)
            if let record = store.records.first(where: { $0.id == id }), store.requiresRedownloadConsent(record) {
                // Validate the key without changing the stored working key before offering redownload.
                do {
                    _ = try store.prepare(origin: link.origin, transferID: link.id, key: link.key.toData())
                    redownload = record
                } catch { error = GuestError.conflictingKey.localizedDescription }
            } else {
                model.receive(link)
            }
        case .upload:
            uploadLink = input.link
            if let link = input.link { reportContext = AbuseReportContext(origin: link.origin, resourceType: "slot", resourceID: link.id) }
            refreshCapacity()
        case .pairing:
            pairingRaw = raw
            pairingServer = input.pairing?.serverUrl ?? ""
        default: error = GuestError.input.localizedDescription
        }
    }

    private func clearUploadSelection() {
        capacityRequest = UUID()
        checkingCapacity = false
        capacityReady = false
        uploadLimit = nil
        selected = []
        capacityMessage = "Checking receive capacity…"
    }

    private func refreshCapacity(adding additions: [URL] = []) {
        guard let link = uploadLink, !model.active else { return }
        let request = UUID()
        capacityRequest = request
        checkingCapacity = true
        capacityReady = false
        error = nil
        capacityMessage = "Checking receive capacity…"
        // Add is cumulative; a rejected addition leaves the previous selection intact.
        let candidate = additions.reduce(into: selected) { files, url in
            if !files.contains(url) { files.append(url) }
        }
        Task { @MainActor in
            defer { if capacityRequest == request { checkingCapacity = false } }
            do {
                let client = try ApiClient.companion.anonymous(origin: link.origin)
                defer { client.close() }
                let limit = try await client.limits.get().maxFileSize
                guard capacityRequest == request else { return }
                guard limit > 0, limit <= Int64(BufferedUpload.maxFileBytes) else { throw GuestUploadSelectionError.unavailable }
                uploadLimit = limit
                let sizes: [Int64]
                do { sizes = candidate.isEmpty ? [] : try BufferedUpload.sizes(candidate, limit: Int(limit)) } catch { throw GuestUploadSelectionError.invalidFiles }
                let availability = try await client.slots.availability(slotId: link.id)
                guard capacityRequest == request else { return }
                try GuestUploadPreflight.validate(availability, link: link, urls: candidate, sizes: sizes)
                guard let capacity = availability.uploadCapacity,
                    let files = capacity.availableFiles, let bytes = capacity.availableWireBytes
                else { throw GuestUploadSelectionError.unavailable }
                selected = candidate
                capacityReady = true
                capacityMessage =
                    "Available for this upload: \(files.int64Value) files · " + ByteCountFormatter.string(fromByteCount: bytes.int64Value, countStyle: .binary)
                    + " encrypted data. Capacity is checked again before sending."
            } catch {
                guard capacityRequest == request else { return }
                capacityMessage = (error as? GuestUploadSelectionError)?.localizedDescription ?? GuestUploadSelectionError.unavailable.localizedDescription
            }
        }
    }
}

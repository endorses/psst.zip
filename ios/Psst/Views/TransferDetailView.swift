import QuickLook
import SwiftUI

struct TransferDetailView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    var sendViewModel: SendViewModel?
    var receiveViewModel: ReceiveViewModel?
    @State private var stopping = false
    @State private var leaveAfterStop = false
    @State private var preview: URL?
    var active: Bool {
        sendViewModel?.active == true || receiveViewModel?.isSaving == true
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if let vm = sendViewModel {
                    sendStatus(vm)
                    Text(String(format: String(localized: "%lld files · %@"), Int64(vm.fileURLs.count), ByteCountFormatter.string(fromByteCount: vm.selectionSize, countStyle: .file))).font(.caption)
                    if let url = vm.shareURL {
                        LinkCard(url: url)
                    }
                    DisclosureGroup("Files") {
                        ForEach(Array(vm.fileNames.enumerated()), id: \.offset) { _, name in Text(verbatim: name).frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 4) }
                    }
                    if vm.maxDownloads > 0 {
                        Text("Up to \(vm.maxDownloads) download attempts per file").font(.footnote)
                    }
                    if let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if let vm = receiveViewModel {
                    receiveStatus(vm)
                    if let record = vm.record {
                        Text(record.displayTitle).font(.headline)
                        if !record.canDecryptInbox {
                            Text("This device has no private receive key. Save the files on the device that created this link. You can still revoke the link from History.").font(.footnote)
                        }
                        if record.receiveProtocol != 2 {
                            Text("This older receive link no longer accepts uploads. You can save existing files or create a new receive link.").font(.footnote)
                        }
                        if let limit = record.maxFiles, limit > 0 {
                            Text("\(record.reservedFiles ?? 0) of \(limit) file slots used").font(.footnote)
                            Text("Unfinished uploads also use a slot. Deleting files does not restore slots.").font(.caption)
                        }
                        Text(record.createdAt.formatted(date: .abbreviated, time: .shortened)).font(.caption)
                        DisclosureGroup("Technical details") { Text(record.id).font(.caption).textSelection(.enabled) }
                    }
                    if let url = vm.uploadURL {
                        LinkCard(url: url)
                    }
                    if let consent = vm.pendingConsent {
                        Text("Save \(consent.fileCount) files (" + ByteCountFormatter.string(fromByteCount: consent.total, countStyle: .binary) + ")?").font(.headline)
                        Text(vm.record?.serverURL ?? "").font(.caption)
                        Text("This uses device storage and network data. Only continue if you trust the senders.").font(.footnote)
                        Button("Save these files") { vm.confirmSaving() }.buttonStyle(PrimaryAction())
                        Button("Cancel") { vm.cancelSaving() }
                    }
                    if vm.canSave, !vm.isSaving, vm.pendingConsent == nil {
                        Button(LocalizedStringKey(vm.savingError == nil ? "Save files" : "Retry saving")) { vm.save() }.buttonStyle(PrimaryAction())
                    }
                    if let message = vm.savingError {
                        Text(message).foregroundStyle(PsstTheme.error)
                    }
                    if let message = vm.connectionError {
                        Text(message).foregroundStyle(PsstTheme.warning)
                    }
                    if let updated = vm.lastUpdated {
                        HStack { Text("Last updated")
                            Text(updated, style: .relative)
                        }.font(.caption).foregroundStyle(PsstTheme.secondary)
                    }
                    Button("Reconnect") { Task { _ = await vm.refresh() } }.disabled(vm.isSaving)
                    if !vm.receivedFileURLs.isEmpty {
                        Text(String(format: String(localized: "%lld files saved in psst.zip Documents"), Int64(vm.receivedFileURLs.count))).font(.headline)
                        ForEach(vm.receivedFileURLs, id: \.absoluteString) { url in
                            HStack {
                                Button { preview = url } label: { Label(url.lastPathComponent, systemImage: "doc").lineLimit(2).frame(minHeight: 44) }.accessibilityHint("Open file")
                                Spacer()
                                ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Export file")
                            }
                        }
                    }
                    if let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if active {
                    Button("Stop", role: .destructive) { leaveAfterStop = false
                        stopping = true
                    }.frame(minHeight: 44)
                }
            }.padding().frame(maxWidth: 600)
        }
        .navigationTitle(LocalizedStringKey(sendViewModel == nil ? "Receive link" : "Send files"))
        .navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(true)
        .toolbar { ToolbarItem(placement: .topBarLeading) { Button("Back") {
            if active {
                leaveAfterStop = true
                stopping = true
            } else {
                dismiss()
            }
        } } }
        .confirmationDialog(LocalizedStringKey(sendViewModel?.active == true ? "Stop upload?" : "Stop saving?"), isPresented: $stopping, titleVisibility: .visible) {
            Button("Stop", role: .destructive) {
                sendViewModel?.stop()
                receiveViewModel?.cancelSaving()
                if leaveAfterStop {
                    dismiss()
                }
            }
            Button("Keep going", role: .cancel) {}
        } message: { Text("Files already saved remain available. Unfinished uploads stay in History so you can revoke them.") }
        .task(id: scenePhase) {
            if scenePhase == .active {
                await receiveViewModel?.monitor()
            }
        }
        .quickLookPreview($preview)
        .modifier(PsstStyle())
    }

    @ViewBuilder private func sendStatus(_ vm: SendViewModel) -> some View {
        switch vm.state {
        case .idle: Text("Ready to send")
        case .encrypting: ProgressView("Preparing files")
            Text(verbatim: vm.currentFile).font(.caption)
        case let .uploading(value):
            ProgressView("Uploading", value: value)
            if let progress = vm.progress {
                Text(verbatim: progress.name).font(.caption)
                Text(ByteCountFormatter.string(fromByteCount: progress.sent, countStyle: .file) + " / " + ByteCountFormatter.string(fromByteCount: progress.total, countStyle: .file)).font(.caption)
            }
        case .complete: Label("Ready to download", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
        case let .failed(message):
            Text(message).foregroundStyle(PsstTheme.error)
            Button("Retry upload") { vm.start() }.buttonStyle(PrimaryAction())
        }
    }

    @ViewBuilder private func receiveStatus(_ vm: ReceiveViewModel) -> some View {
        switch vm.state {
        case .idle, .creating: ProgressView("Creating link")
        case .waiting: Text(vm.record?.statusText ?? String(localized: "Waiting for files")).font(.headline)
        case .downloading: ProgressView("Saving files") // Download byte progress is not exposed by the shared API.
        case .decrypting: ProgressView("Preparing files")
        case .complete: Label("Files saved locally", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
        case let .failed(message):
            Text(message).foregroundStyle(PsstTheme.error)
            if vm.record == nil {
                Button("Retry creating link") { Task { await vm.createDropSlot() } }
            }
        }
    }

    private func expiryLabel(_ date: Date) -> some View {
        HStack { Text("Expires")
            Text(date, style: .relative)
        }.font(.caption).foregroundStyle(PsstTheme.secondary)
    }
}

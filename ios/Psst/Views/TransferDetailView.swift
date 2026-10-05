import QuickLook
import SwiftUI

struct TransferDetailView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    var sendViewModel: SendViewModel?
    var receiveViewModel: ReceiveViewModel?
    var rootInbox = false
    var onCreateAnother: (() -> Void)?
    var onCreateReplacement: (() -> Void)?
    @State private var showingLink = false
    @State private var renaming = false
    @State private var titleDraft = ""
    @State private var titleError: String?
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
                    if let title = vm.record?.sharedTitle { Text(title).font(.title2.bold()) }
                    sendStatus(vm)
                    Text(
                        String(
                            format: String(localized: "%lld files · %@"), Int64(vm.fileURLs.count), ByteCountFormatter.string(fromByteCount: vm.selectionSize, countStyle: .file))
                    ).font(.caption)
                    if vm.record?.linkActive != false, let url = vm.shareURL {
                        LinkCard(url: url)
                    }
                    DisclosureGroup("Files") {
                        ForEach(Array(vm.fileNames.enumerated()), id: \.offset) { _, name in
                            Text(verbatim: name).frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 4)
                        }
                    }
                    if vm.maxDownloads > 0 {
                        Text("Up to \(vm.maxDownloads) download attempts per file").font(.footnote)
                    }
                    if vm.record?.linkActive != false, let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if let vm = receiveViewModel {
                    receiveStatus(vm)
                    if let record = vm.record {
                        Text(record.safeDisplayTitle).font(.headline)
                        if !record.canDecryptInbox {
                            Text("This device has no private receive key. Save the files on the device that created this link. You can still revoke the link from History.").font(
                                .footnote)
                        }
                        if record.receiveProtocol != 2 {
                            Text("This older receive link no longer accepts uploads. You can save existing files or create a new receive link.").font(.footnote)
                        }
                        if let limit = record.maxFiles, limit > 0 {
                            if let used = record.reservedFiles {
                                Text("\(max(0, Int64(limit) - used)) files remaining").font(.footnote)
                            } else {
                                Text("Remaining file allowance updating").font(.footnote)
                            }
                        }
                        HStack {
                            Button("Rename") {
                                titleDraft = record.sharedTitle ?? record.customTitle ?? ""
                                renaming = true
                            }
                            Spacer()
                            if vm.uploadURL != nil, (vm.totalReceivedFiles ?? Int64(vm.pageFileCount)) > 0 {
                                Button("Show QR / Share link") { showingLink = true }
                            }
                        }
                        if let titleError { Text(titleError).foregroundStyle(PsstTheme.error) }
                        DisclosureGroup("Details") {
                            Text(record.id).font(.caption).textSelection(.enabled)
                            if let policy = record.linkPolicySummary { Text(policy).font(.caption) }
                            Text(record.serverURL ?? "").font(.caption)
                        }
                    }
                    if let url = vm.uploadURL, (vm.totalReceivedFiles ?? Int64(vm.pageFileCount)) == 0 { LinkCard(url: url) }
                    if let consent = vm.pendingConsent {
                        Text("Save \(consent.fileCount) files (" + ByteCountFormatter.string(fromByteCount: consent.total, countStyle: .binary) + ")?").font(.headline)
                        Text(vm.record?.serverURL ?? "").font(.caption)
                        Text("This uses device storage and network data. Only continue if you trust the senders.").font(.footnote)
                        Button("Save these files") { vm.confirmSaving() }.buttonStyle(PrimaryAction())
                        Button("Cancel") { vm.cancelSaving() }
                    }
                    if vm.canSave, !vm.isSaving, vm.pendingConsent == nil {
                        Button(LocalizedStringKey(vm.savingError == nil ? "Save shown files" : "Retry shown files")) { vm.save() }.buttonStyle(PrimaryAction()).disabled(
                            vm.refreshing)
                    }
                    if vm.pageWindow.loaded {
                        VStack(spacing: 8) {
                            Text("Received files").font(.headline)
                            ForEach(Array(vm.arrivals.enumerated()), id: \.element.transferId) { index, arrival in
                                HStack {
                                    Image(systemName: "tray.and.arrow.down")
                                    Text("Submission \(index + 1)")
                                    Spacer()
                                    Text("\(arrival.fileCount) files").foregroundStyle(PsstTheme.secondary)
                                }.font(.subheadline)
                            }
                            if let total = vm.totalReceivedFiles {
                                Text("\(total) received files total").font(.caption).foregroundStyle(PsstTheme.secondary)
                            } else {
                                Text("Inbox totals are updating.").font(.caption).foregroundStyle(PsstTheme.secondary)
                            }
                            if vm.arrivals.isEmpty {
                                Text("No completed submissions on this page.").font(.footnote)
                            }
                            if vm.refreshing { ProgressView("Updating page") }
                        }
                    }
                    if let message = vm.savingError {
                        Text(message).foregroundStyle(PsstTheme.error)
                    }
                    if let message = vm.connectionError {
                        Text(message).foregroundStyle(PsstTheme.warning)
                    }
                    if let updated = vm.lastUpdated {
                        HStack {
                            Text("Last updated")
                            Text(updated, style: .relative)
                        }.font(.caption).foregroundStyle(PsstTheme.secondary)
                    }
                    Button("Refresh") { Task { _ = await vm.refresh() } }.disabled(vm.isSaving || vm.refreshing || vm.pendingConsent != nil)
                    if !vm.receivedFileURLs.isEmpty {
                        Text(String(format: String(localized: "%lld files saved in psst.zip Documents"), Int64(vm.receivedFileURLs.count))).font(.headline)
                        ForEach(vm.receivedFileURLs, id: \.absoluteString) { url in
                            HStack {
                                Button {
                                    preview = url
                                } label: {
                                    Label(GuestFiles.displayName(url.lastPathComponent), systemImage: "doc").lineLimit(2).frame(minHeight: 44)
                                }.accessibilityHint("Open file")
                                Spacer()
                                ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel("Export file")
                            }
                        }
                    }
                    if vm.pageWindow.canGoBack || vm.pageWindow.nextCursor != nil {
                        HStack {
                            if vm.pageWindow.canGoBack { Button("Previous") { Task { await vm.previousPage() } }.disabled(!vm.canGoPrevious) }
                            Spacer()
                            Text("Page \(vm.pageWindow.number)").font(.caption)
                            Spacer()
                            if vm.pageWindow.nextCursor != nil { Button("Next") { Task { await vm.nextPage() } }.disabled(!vm.canGoNext) }
                        }
                        if vm.pageWindow.number > 1 { Button("First page") { Task { await vm.firstPage() } }.disabled(!vm.canBrowse) }
                    }
                    if let onCreateAnother { Button("Create another link", action: onCreateAnother).disabled(active) }
                    if vm.record?.linkActive != false, let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if active {
                    Button("Stop", role: .destructive) {
                        leaveAfterStop = false
                        stopping = true
                    }.frame(minHeight: 44)
                }
            }.padding().frame(maxWidth: 600)
        }
        .navigationTitle(LocalizedStringKey(sendViewModel == nil ? "Receive link" : "Send files"))
        .navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(!rootInbox)
        .toolbar {
            if !rootInbox {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Back") {
                        if active {
                            leaveAfterStop = true
                            stopping = true
                        } else {
                            dismiss()
                        }
                    }
                }
            }
        }
        .sheet(isPresented: $showingLink) {
            NavigationStack {
                ScrollView { if let url = receiveViewModel?.uploadURL { LinkCard(url: url).padding() } }.navigationTitle("Share receive link").toolbar {
                    Button("Done") { showingLink = false }
                }
            }
        }
        .alert("Rename shared title", isPresented: $renaming) {
            TextField("Shared title (optional)", text: $titleDraft)
            Button("Save") {
                Task {
                    do {
                        try await receiveViewModel?.rename(titleDraft)
                        titleError = nil
                    } catch { titleError = (error as? SharedLinkTitle.Failure)?.localizedDescription ?? "Could not save the shared title. Reconnect and retry." }
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Shown to people using this link.")
        }
        .confirmationDialog(LocalizedStringKey(sendViewModel?.active == true ? "Stop upload?" : "Stop saving?"), isPresented: $stopping, titleVisibility: .visible) {
            Button("Stop", role: .destructive) {
                sendViewModel?.stop()
                receiveViewModel?.cancelSaving()
                if leaveAfterStop {
                    dismiss()
                }
            }
            Button("Keep going", role: .cancel) {}
        } message: {
            Text("Files already saved remain available. Unfinished uploads stay in History so you can revoke them.")
        }
        .task(id: "\(scenePhase)-\(receiveViewModel?.record?.localID ?? "")-\(receiveViewModel?.record?.receiveProtocol ?? 0)") {
            if scenePhase == .active {
                if let receiveViewModel {
                    await receiveViewModel.monitor()
                } else if let sendViewModel {
                    while !Task.isCancelled {
                        await sendViewModel.refreshLinkStatus()
                        do { try await Task.sleep(for: .seconds(5)) } catch { return }
                    }
                }
            }
        }
        .quickLookPreview($preview)
        .modifier(PsstStyle())
    }

    @ViewBuilder private func sendStatus(_ vm: SendViewModel) -> some View {
        switch vm.state {
        case .idle: Text("Ready to send")
        case .encrypting:
            ProgressView("Preparing files")
            Text(verbatim: vm.currentFile).font(.caption)
        case let .uploading(value):
            ProgressView("Uploading", value: value)
            if let progress = vm.progress {
                Text(verbatim: progress.name).font(.caption)
                Text(
                    ByteCountFormatter.string(fromByteCount: progress.sent, countStyle: .file) + " / " + ByteCountFormatter.string(fromByteCount: progress.total, countStyle: .file)
                ).font(.caption)
            }
        case .complete:
            if vm.record?.state == .exhausted {
                Label("Download limit reached", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.secondary)
                Text("Create a new send link to share these files again.").font(.footnote)
                if let onCreateReplacement { Button("Create replacement link", action: onCreateReplacement).buttonStyle(PrimaryAction()) }
            } else {
                Label(vm.record?.statusText ?? "Ready to download", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
            }
        case let .failed(message):
            Text(message).foregroundStyle(PsstTheme.error)
            Button("Retry upload") { vm.start() }.buttonStyle(PrimaryAction())
        }
    }

    @ViewBuilder private func receiveStatus(_ vm: ReceiveViewModel) -> some View {
        switch vm.state {
        case .idle, .creating: ProgressView("Creating link")
        case .waiting: Text(vm.record?.statusText ?? String(localized: "Waiting for files")).font(.headline)
        case .downloading: ProgressView("Saving files")  // Download byte progress is not exposed by the shared API.
        case .decrypting: ProgressView("Preparing files")
        case .complete: Label("Shown files saved locally", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
        case let .failed(message):
            Text(message).foregroundStyle(PsstTheme.error)
            if vm.record == nil {
                Button("Retry creating link") { Task { await vm.createDropSlot() } }
            }
        }
    }

    private func expiryLabel(_ date: Date) -> some View {
        HStack {
            Text("Expires")
            Text(date, style: .relative)
        }.font(.caption).foregroundStyle(PsstTheme.secondary)
    }
}

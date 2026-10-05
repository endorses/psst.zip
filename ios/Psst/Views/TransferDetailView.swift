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
                    if let title = vm.record?.sharedTitle {
                        Text(verbatim: title).font(.title2.bold())
                    }
                    sendStatus(vm)
                    Text(L10n.text(L10n.format("%lld files · %@", Int64(vm.fileURLs.count), L10n.bytes(vm.selectionSize)))).font(.caption)
                    if vm.record?.linkActive != false, let url = vm.shareURL {
                        LinkCard(url: url)
                    }
                    DisclosureGroup(L10n.text("Files")) {
                        ForEach(Array(vm.fileNames.enumerated()), id: \.offset) { _, name in
                            Text(verbatim: name).frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 4)
                        }
                    }
                    if vm.maxDownloads > 0 {
                        Text(L10n.text(L10n.format("Up to %lld download attempts per file", Int64(vm.maxDownloads)))).font(.footnote)
                    }
                    if vm.record?.linkActive != false, let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if let vm = receiveViewModel {
                    receiveStatus(vm)
                    if let record = vm.record {
                        Text(verbatim: record.safeDisplayTitle).font(.headline)
                        if !record.canDecryptInbox {
                            Text(L10n.text("This device has no private receive key. Save the files on the device that created this link. You can still revoke the link from History.")).font(
                                .footnote
                            )
                        }
                        if record.receiveProtocol != 2 {
                            Text(L10n.text("This older receive link no longer accepts uploads. You can save existing files or create a new receive link.")).font(.footnote)
                        }
                        if let limit = record.maxFiles, limit > 0 {
                            if let used = record.reservedFiles {
                                Text(L10n.text(L10n.format("%lld files remaining", Int64(max(0, Int64(limit) - used))))).font(.footnote)
                            } else {
                                Text(L10n.text("Remaining file allowance updating")).font(.footnote)
                            }
                        }
                        HStack {
                            Button(L10n.text("Rename")) {
                                titleDraft = record.sharedTitle ?? record.customTitle ?? ""
                                renaming = true
                            }
                            Spacer()
                            if vm.uploadURL != nil, (vm.totalReceivedFiles ?? Int64(vm.pageFileCount)) > 0 {
                                Button(L10n.text("Show QR / Share link")) { showingLink = true }
                            }
                        }
                        if let titleError {
                            Text(L10n.text(titleError)).foregroundStyle(PsstTheme.error)
                        }
                        DisclosureGroup(L10n.text("Details")) {
                            Text(verbatim: record.id).font(.caption).textSelection(.enabled)
                            if let policy = record.linkPolicySummary {
                                Text(L10n.text(policy)).font(.caption)
                            }
                            Text(verbatim: record.serverURL ?? "").font(.caption)
                        }
                    }
                    if let url = vm.uploadURL, (vm.totalReceivedFiles ?? Int64(vm.pageFileCount)) == 0 {
                        LinkCard(url: url)
                    }
                    if let consent = vm.pendingConsent {
                        Text(L10n.text(L10n.format("Save %lld files (%@)?", Int64(consent.fileCount), L10n.bytes(consent.total, binary: true)))).font(.headline)
                        Text(verbatim: vm.record?.serverURL ?? "").font(.caption)
                        Text(L10n.text("This uses device storage and network data. Only continue if you trust the senders.")).font(.footnote)
                        Button(L10n.text("Save these files")) { vm.confirmSaving() }.buttonStyle(PrimaryAction())
                        Button(L10n.text("Cancel")) { vm.cancelSaving() }
                    }
                    if vm.canSave, !vm.isSaving, vm.pendingConsent == nil {
                        Button(LocalizedStringKey(vm.savingError == nil ? "Save shown files" : "Retry shown files")) { vm.save() }.buttonStyle(PrimaryAction()).disabled(
                            vm.refreshing
                        )
                    }
                    if vm.pageWindow.loaded {
                        VStack(spacing: 8) {
                            Text(L10n.text("Received files")).font(.headline)
                            ForEach(Array(vm.arrivals.enumerated()), id: \.element.transferId) { index, arrival in
                                HStack {
                                    Image(systemName: "tray.and.arrow.down")
                                    Text(L10n.text(L10n.format("Submission %lld", Int64(index + 1))))
                                    Spacer()
                                    Text(L10n.text(L10n.format("%lld files", Int64(arrival.fileCount)))).foregroundStyle(PsstTheme.secondary)
                                }.font(.subheadline)
                            }
                            if let total = vm.totalReceivedFiles {
                                Text(L10n.text(L10n.format("%lld received files total", Int64(total)))).font(.caption).foregroundStyle(PsstTheme.secondary)
                            } else {
                                Text(L10n.text("Inbox totals are updating.")).font(.caption).foregroundStyle(PsstTheme.secondary)
                            }
                            if vm.arrivals.isEmpty {
                                Text(L10n.text("No completed submissions on this page.")).font(.footnote)
                            }
                            if vm.refreshing {
                                ProgressView(L10n.text("Updating page"))
                            }
                        }
                    }
                    if let message = vm.savingError {
                        Text(L10n.text(message)).foregroundStyle(PsstTheme.error)
                    }
                    if let message = vm.connectionError {
                        Text(L10n.text(message)).foregroundStyle(PsstTheme.warning)
                    }
                    if let updated = vm.lastUpdated {
                        HStack {
                            Text(L10n.text("Last updated"))
                            Text(updated, style: .relative)
                        }.font(.caption).foregroundStyle(PsstTheme.secondary)
                    }
                    Button(L10n.text("Refresh")) { Task { _ = await vm.refresh() } }.disabled(vm.isSaving || vm.refreshing || vm.pendingConsent != nil)
                    if !vm.receivedFileURLs.isEmpty {
                        Text(L10n.text(L10n.format("%lld files saved in psst.zip Documents", Int64(vm.receivedFileURLs.count)))).font(.headline)
                        ForEach(vm.receivedFileURLs, id: \.absoluteString) { url in
                            HStack {
                                Button {
                                    preview = url
                                } label: {
                                    Label(GuestFiles.displayName(url.lastPathComponent), systemImage: "doc").lineLimit(2).frame(minHeight: 44)
                                }.accessibilityHint(L10n.text("Open file"))
                                Spacer()
                                ShareLink(item: url) { Image(systemName: "square.and.arrow.up").frame(minWidth: 44, minHeight: 44) }.accessibilityLabel(L10n.text("Export file"))
                            }
                        }
                    }
                    if vm.pageWindow.canGoBack || vm.pageWindow.nextCursor != nil {
                        HStack {
                            if vm.pageWindow.canGoBack {
                                Button(L10n.text("Previous")) { Task { await vm.previousPage() } }.disabled(!vm.canGoPrevious)
                            }
                            Spacer()
                            Text(L10n.text(L10n.format("Page %lld", Int64(vm.pageWindow.number)))).font(.caption)
                            Spacer()
                            if vm.pageWindow.nextCursor != nil {
                                Button(L10n.text("Next")) { Task { await vm.nextPage() } }.disabled(!vm.canGoNext)
                            }
                        }
                        if vm.pageWindow.number > 1 {
                            Button(L10n.text("First page")) { Task { await vm.firstPage() } }.disabled(!vm.canBrowse)
                        }
                    }
                    if let onCreateAnother {
                        Button(L10n.text("Create another link"), action: onCreateAnother).disabled(active)
                    }
                    if vm.record?.linkActive != false, let expiry = vm.expiresAt {
                        expiryLabel(expiry)
                    }
                }
                if active {
                    Button(L10n.text("Stop"), role: .destructive) {
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
                    Button(L10n.text("Back")) {
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
                ScrollView {
                    if let url = receiveViewModel?.uploadURL {
                        LinkCard(url: url).padding()
                    }
                }.navigationTitle(L10n.text("Share receive link")).toolbar {
                    Button(L10n.text("Done")) { showingLink = false }
                }
            }
        }
        .alert(L10n.text("Rename shared title"), isPresented: $renaming) {
            TextField(L10n.text("Shared title (optional)"), text: $titleDraft)
            Button(L10n.text("Save")) {
                Task {
                    do {
                        try await receiveViewModel?.rename(titleDraft)
                        titleError = nil
                    } catch { titleError = (error as? SharedLinkTitle.Failure)?.localizedDescription ?? "Could not save the shared title. Reconnect and retry." }
                }
            }
            Button(L10n.text("Cancel"), role: .cancel) {}
        } message: {
            Text(L10n.text("Shown to people using this link."))
        }
        .confirmationDialog(LocalizedStringKey(sendViewModel?.active == true ? "Stop upload?" : "Stop saving?"), isPresented: $stopping, titleVisibility: .visible) {
            Button(L10n.text("Stop"), role: .destructive) {
                sendViewModel?.stop()
                receiveViewModel?.cancelSaving()
                if leaveAfterStop {
                    dismiss()
                }
            }
            Button(L10n.text("Keep going"), role: .cancel) {}
        } message: {
            Text(L10n.text("Files already saved remain available. Unfinished uploads stay in History so you can revoke them."))
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
        case .idle: Text(L10n.text("Ready to send"))
        case .encrypting:
            ProgressView(L10n.text("Preparing files"))
            Text(verbatim: vm.currentFile).font(.caption)
        case let .uploading(value):
            ProgressView(L10n.text("Uploading"), value: value)
            if let progress = vm.progress {
                Text(verbatim: progress.name).font(.caption)
                Text(L10n.text(L10n.bytes(progress.sent) + " / " + L10n.bytes(progress.total))).font(.caption)
            }
        case .complete:
            if vm.record?.state == .exhausted {
                Label(L10n.text("Download limit reached"), systemImage: "checkmark.circle").foregroundStyle(PsstTheme.secondary)
                Text(L10n.text("Create a new send link to share these files again.")).font(.footnote)
                if let onCreateReplacement {
                    Button(L10n.text("Create replacement link"), action: onCreateReplacement).buttonStyle(PrimaryAction())
                }
            } else {
                Label(L10n.text(vm.record?.statusText ?? "Ready to download"), systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
            }
        case let .failed(message):
            Text(L10n.text(message)).foregroundStyle(PsstTheme.error)
            Button(L10n.text("Retry upload")) { vm.start() }.buttonStyle(PrimaryAction())
        }
    }

    @ViewBuilder private func receiveStatus(_ vm: ReceiveViewModel) -> some View {
        switch vm.state {
        case .idle, .creating: ProgressView(L10n.text("Creating link"))
        case .waiting: Text(L10n.text(vm.record?.statusText ?? L10n.message("Waiting for files"))).font(.headline)
        case .downloading: ProgressView(L10n.text("Saving files")) // Download byte progress is not exposed by the shared API.
        case .decrypting: ProgressView(L10n.text("Preparing files"))
        case .complete: Label(L10n.text("Shown files saved locally"), systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
        case let .failed(message):
            Text(L10n.text(message)).foregroundStyle(PsstTheme.error)
            if vm.record == nil {
                Button(L10n.text("Retry creating link")) { Task { await vm.createDropSlot() } }
            }
        }
    }

    private func expiryLabel(_ date: Date) -> some View {
        HStack {
            Text(L10n.text("Expires"))
            Text(date, style: .relative)
        }.font(.caption).foregroundStyle(PsstTheme.secondary)
    }
}

import SwiftUI

struct ShareExtensionView: View {
    @Bindable var viewModel: ShareExtensionViewModel
    let onCancel: () -> Void
    let onComplete: () -> Void
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    Text(viewModel.config.indicator).font(.caption)
                    Text(viewModel.config.limitDescription + " Files are encrypted in chunks.")
                        .font(.footnote).foregroundStyle(PsstTheme.secondary)
                    Text(String(format: String(localized: "%lld files · %@"), Int64(viewModel.fileCount), ByteCountFormatter.string(fromByteCount: viewModel.totalSize, countStyle: .file)))
                    if !viewModel.config.isConfigured || viewModel.config.needsSignIn {
                        LoginFields()
                    } else if let send = viewModel.send {
                        switch send.state {
                        case .idle: Button("Send files") { viewModel.start() }.buttonStyle(PrimaryAction())
                        case .encrypting: ProgressView("Preparing files")
                            Text(verbatim: send.currentFile).font(.caption)
                        case let .uploading(value):
                            ProgressView("Uploading", value: value)
                            if let progress = send.progress {
                                Text(verbatim: progress.name).font(.caption)
                                Text(ByteCountFormatter.string(fromByteCount: progress.sent, countStyle: .file) + " / " + ByteCountFormatter.string(fromByteCount: progress.total, countStyle: .file)).font(.caption)
                            }
                        case .complete:
                            Label("Ready to download", systemImage: "checkmark.circle").foregroundStyle(PsstTheme.success)
                            if let link = send.shareURL {
                                LinkCard(url: link)
                            }
                        case let .failed(message):
                            Text(message).foregroundStyle(PsstTheme.error)
                            Button("Retry upload") { viewModel.start() }.buttonStyle(PrimaryAction())
                            DisclosureGroup("Sign in again") { LoginFields() }
                        }
                    } else {
                        Button("Send files") { viewModel.start() }.buttonStyle(PrimaryAction()).disabled(viewModel.files.isEmpty)
                    }
                    if let error = viewModel.error {
                        Text(error).foregroundStyle(PsstTheme.error)
                    }
                }.padding()
            }
            .task(id: viewModel.config.serverURL) { await viewModel.config.refreshLimit() }
            .navigationTitle("psst.zip")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") {
                    if viewModel.active {
                        viewModel.cancelRequested = true
                    } else {
                        onCancel()
                    }
                } }
                ToolbarItem(placement: .confirmationAction) {
                    if viewModel.send?.state == .complete {
                        Button("Done") { onComplete() }
                    }
                }
            }
            .confirmationDialog("Stop upload?", isPresented: $viewModel.cancelRequested, titleVisibility: .visible) {
                Button("Stop", role: .destructive) { viewModel.cancel()
                    onCancel()
                }
                Button("Keep going", role: .cancel) {}
            } message: { Text("The upload will stop when this extension closes. Unfinished uploads stay in History so you can revoke them.") }
            .task {
                while !Task.isCancelled {
                    viewModel.config.reload()
                    await viewModel.config.refreshAccount()
                    do { try await Task.sleep(for: .seconds(3)) } catch { return }
                }
            }
            .onChange(of: viewModel.config.accountID) { _, _ in viewModel.accountChanged() }
        }
        .environment(viewModel.config)
        .modifier(PsstStyle())
        .modifier(PsstAppearance())
    }
}

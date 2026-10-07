import SwiftUI

struct ShareExtensionView: View {
  @Bindable var viewModel: ShareExtensionViewModel
  let onCancel: () -> Void
  let onComplete: () -> Void
  var body: some View {
    NavigationStack {
      ScrollView {
        VStack(spacing: 16) {
          Text(L10n.text(viewModel.config.indicator)).font(.caption)
          Text(
            L10n.text(
              L10n.format(
                "%@ Files are encrypted in chunks.", L10n.text(viewModel.config.limitDescription)))
          )
          .font(.footnote).foregroundStyle(PsstTheme.secondary)
          Text(
            L10n.text(
              L10n.format(
                "%lld files · %@", Int64(viewModel.fileCount),
                L10n.bytes(viewModel.totalSize))))
          if viewModel.send == nil {
            TextField(L10n.text("Shared title (optional)"), text: $viewModel.sharedTitle)
              .textFieldStyle(.roundedBorder)
            Text(L10n.text("Shown to people using this link.")).font(.caption).foregroundStyle(
              PsstTheme.secondary)
            LinkLimitControl(
              receiving: false, enabled: $viewModel.limitEnabled, value: $viewModel.limitValue)
          }
          if !viewModel.config.isConfigured || viewModel.config.needsSignIn {
            LoginFields()
          } else if let send = viewModel.send {
            switch send.state {
            case .idle:
              Button(L10n.text("Send files")) { viewModel.start() }.buttonStyle(PrimaryAction())
            case .encrypting:
              ProgressView(L10n.text("Preparing files"))
              Text(verbatim: send.currentFile).font(.caption)
            case .uploading(let value):
              ProgressView(L10n.text("Uploading"), value: value)
              if let progress = send.progress {
                Text(verbatim: progress.name).font(.caption)
                Text(
                  L10n.text(
                    L10n.bytes(progress.sent) + " / "
                      + L10n.bytes(progress.total))
                ).font(.caption)
              }
            case .complete:
              if let title = send.record?.sharedTitle {
                Text(verbatim: title).font(.headline)
              }
              Label(
                L10n.text(send.record?.statusText ?? "Ready to download"),
                systemImage: "checkmark.circle"
              ).foregroundStyle(PsstTheme.success)
              if send.record?.state == .exhausted {
                Button(L10n.text("Create replacement link")) { viewModel.send = nil }.buttonStyle(
                  PrimaryAction())
              }
              if send.record?.linkActive != false, let link = send.shareURL {
                LinkCard(url: link)
              }
            case .failed(let message):
              Text(L10n.text(message)).foregroundStyle(PsstTheme.error)
              Button(L10n.text("Retry upload")) { viewModel.start() }.buttonStyle(PrimaryAction())
              DisclosureGroup(L10n.text("Sign in again")) { LoginFields() }
            }
          } else {
            Button(L10n.text("Send files")) { viewModel.start() }.buttonStyle(PrimaryAction())
              .disabled(
                viewModel.files.isEmpty
                  || LinkLimit.parse(viewModel.limitValue, enabled: viewModel.limitEnabled) == nil
              )
          }
          NavigationLink(L10n.text("Source & licenses")) {
            SourceLicensesView(serverURL: viewModel.config.serverURL)
          }
          if let error = viewModel.error {
            Text(L10n.text(error)).foregroundStyle(PsstTheme.error)
          }
        }.padding()
      }
      .task(id: viewModel.config.serverURL) { await viewModel.config.refreshLimit() }
      .navigationTitle(L10n.text("psst.zip"))
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) {
          Button(L10n.text("Cancel")) {
            if viewModel.active {
              viewModel.cancelRequested = true
            } else {
              onCancel()
            }
          }
        }
        ToolbarItem(placement: .confirmationAction) {
          if viewModel.send?.state == .complete {
            Button(L10n.text("Done")) { onComplete() }
          }
        }
      }
      .confirmationDialog(
        L10n.text("Stop upload?"), isPresented: $viewModel.cancelRequested,
        titleVisibility: .visible
      ) {
        Button(L10n.text("Stop"), role: .destructive) {
          viewModel.cancel()
          onCancel()
        }
        Button(L10n.text("Keep going"), role: .cancel) {}
      } message: {
        Text(
          L10n.text(
            "The upload will stop when this extension closes. Unfinished uploads stay in History so you can revoke them."
          ))
      }
      .task {
        while !Task.isCancelled {
          LanguageSettings.shared.reload()
          viewModel.config.reload()
          await viewModel.config.refreshAccount()
          await viewModel.send?.refreshLinkStatus()
          do { try await Task.sleep(for: .seconds(3)) } catch { return }
        }
      }
      .onChange(of: viewModel.config.accountID) { _, _ in viewModel.accountChanged() }
    }
    .environment(viewModel.config)
    .modifier(PsstStyle())
    .modifier(PsstAppearance()).modifier(PsstLanguage())
  }
}

import SwiftUI

struct ServerConfigView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    @Environment(\.dismiss) private var dismiss
    @AppStorage(AppConstants.appearanceKey, store: AppConstants.sharedDefaults) private var appearance = AppAppearance.system
    @Bindable private var language = LanguageSettings.shared
    @State private var editing = false
    @State private var checking = false
    @State private var connection: String?
    var body: some View {
        Form {
            Section(L10n.text("Appearance")) {
                Picker(L10n.text("Theme"), selection: $appearance) {
                    ForEach(AppAppearance.allCases, id: \.self) { Text($0.title).tag($0) }
                }.pickerStyle(.menu)
                Picker(L10n.text("Language"), selection: $language.preference) {
                    ForEach(AppLanguage.allCases, id: \.self) { Text(verbatim: $0.title).tag($0) }
                }.pickerStyle(.menu)
            }
            Section(L10n.text("Server & account")) {
                if let session = serverConfig.session {
                    LabeledContent(L10n.text("Username"), value: session.username)
                    LabeledContent(L10n.text("Server")) { Text(verbatim: session.serverURL).textSelection(.enabled) }
                    if let message = serverConfig.accountMessage {
                        Text(L10n.text(message)).foregroundStyle(PsstTheme.warning)
                    }
                    if serverConfig.needsSignIn {
                        Text(L10n.text(serverConfig.passwordChanged ? "Password changed. Sign in with your new password." : "Your session expired. Sign in again.")).foregroundStyle(
                            PsstTheme.warning
                        )
                    }
                } else {
                    Text(L10n.text("Not signed in"))
                }
                Button(L10n.text(serverConfig.requiresPasswordChange ? "Change password" : serverConfig.isConfigured ? "Change server or account" : "Sign in")) { editing = true }
            }
            Section(L10n.text("Connection")) {
                if !serverConfig.serverURL.isEmpty {
                    Label(L10n.text(serverConfig.serverURL.hasPrefix("https://") ? "Encrypted connection" : "HTTP · Unencrypted connection"),
                          systemImage: serverConfig.serverURL.hasPrefix("https://") ? "lock" : "lock.open")
                    Button(L10n.text("Test connection")) {
                        checking = true
                        Task {
                            defer { checking = false }
                            do {
                                try await serverConfig.testConnection(server: serverConfig.serverURL)
                                connection = "Connected"
                            } catch { connection = "Could not connect. Check the server address and network." }
                        }
                    }.disabled(checking)
                }
                if checking {
                    ProgressView()
                }
                if let connection {
                    Text(L10n.text(connection)).font(.footnote)
                }
            }
            if serverConfig.isConfigured, !serverConfig.needsSignIn {
                Section(L10n.text("Usage")) {
                    NavigationLink {
                        AccountTrafficView()
                    } label: {
                        Label(L10n.text("Usage & traffic"), systemImage: "chart.bar")
                    }
                }
                Section(L10n.text("Security")) { NavigationLink("Change password") { Form { PasswordReplacementFields() }.navigationTitle(L10n.text("Change password")) } }
            }
            if let context = AbuseReportContext(origin: serverConfig.serverURL), let contact = serverConfig.abuseContactEmail {
                Section(L10n.text("Help & contact")) {
                    Text(verbatim: contact).textSelection(.enabled)
                    AbuseReportButton(context: context, configuredContact: serverConfig.abuseContactEmail, fetchContact: false).id(context.id)
                }
            }
            if serverConfig.session != nil {
                Section { LogoutButton() }
            }
        }
        .task(id: serverConfig.serverURL) { await serverConfig.refreshLimit() }
        .modifier(PsstStyle()).modifier(PsstAppearance())
        .sheet(isPresented: $editing) { NavigationStack { AccountSetupView() } }
        .navigationTitle(L10n.text("Settings")).navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button(L10n.text("Done")) { dismiss() } } }
    }
}

/// Fields are local drafts; only successful authentication installs a new session.
struct AccountSetupView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    var body: some View {
        Form {
            Section(L10n.text("Server login")) { LoginFields(onSuccess: { dismiss() }, onBusyChanged: { busy = $0 }) }
            Section { Text(L10n.text("Use a server address other people can reach. Your files are encrypted automatically before upload.")).foregroundStyle(PsstTheme.secondary) }
        }
        .navigationTitle(L10n.text("Server & account")).navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(busy)
        .toolbar { ToolbarItem(placement: .cancellationAction) { Button(L10n.text("Cancel")) { dismiss() }.disabled(busy) } }
        .interactiveDismissDisabled(busy)
        .modifier(PsstStyle()).modifier(PsstAppearance())
    }
}

private struct LogoutButton: View {
    @Environment(ServerConfigManager.self) private var config
    @State private var error: String?
    @State private var busy = false
    var body: some View {
        Button(L10n.text("Sign out"), role: .destructive) {
            busy = true
            Task {
                defer { busy = false }
                do { try await config.logout() } catch { self.error = L10n.message("Could not revoke your session. Reconnect and retry signing out.") }
            }
        }.disabled(busy)
        if let error {
            Text(L10n.text(error)).foregroundStyle(PsstTheme.error)
        }
    }
}

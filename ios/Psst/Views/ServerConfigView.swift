import SwiftUI

struct ServerConfigView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    @Environment(\.dismiss) private var dismiss
    @AppStorage(AppConstants.appearanceKey, store: AppConstants.sharedDefaults) private var appearance = AppAppearance.system
    @State private var editing = false
    @State private var checking = false
    @State private var connection: String?
    var body: some View {
        Form {
            Section("Appearance") {
                Picker("Theme", selection: $appearance) {
                    ForEach(AppAppearance.allCases, id: \.self) { Text($0.title).tag($0) }
                }.pickerStyle(.menu)
            }
            Section("Server & account") {
                if let session = serverConfig.session {
                    LabeledContent("Username", value: session.username)
                    LabeledContent("Server") { Text(session.serverURL).textSelection(.enabled) }
                    if let message = serverConfig.accountMessage {
                        Text(message).foregroundStyle(PsstTheme.warning)
                    }
                    if serverConfig.needsSignIn {
                        Text(serverConfig.passwordChanged ? "Password changed. Sign in with your new password." : "Your session expired. Sign in again.").foregroundStyle(PsstTheme.warning)
                    }
                } else {
                    Text("Not signed in")
                }
                Button(serverConfig.requiresPasswordChange ? "Change password" : serverConfig.isConfigured ? "Change server or account" : "Sign in") { editing = true }
            }
            Section("Connection") {
                if !serverConfig.serverURL.isEmpty {
                    Label(serverConfig.serverURL.hasPrefix("https://") ? "Encrypted connection" : "HTTP · Unencrypted connection", systemImage: serverConfig.serverURL.hasPrefix("https://") ? "lock" : "lock.open")
                    Button("Test connection") {
                        checking = true
                        Task {
                            defer { checking = false }
                            do { try await serverConfig.testConnection(server: serverConfig.serverURL); connection = "Connected" }
                            catch { connection = "Could not connect. Check the server address and network." }
                        }
                    }.disabled(checking)
                }
                if checking {
                    ProgressView()
                }
                if let connection {
                    Text(connection).font(.footnote)
                }
            }
            if serverConfig.isConfigured, !serverConfig.needsSignIn {
                Section("Usage") {
                    NavigationLink("Transfer traffic") { AccountTrafficView() }
                }
                Section("Password") { PasswordReplacementFields() }
            }
            if let context = AbuseReportContext(origin: serverConfig.serverURL), let contact = serverConfig.abuseContactEmail {
                Section("Help & contact") {
                    Text(contact).textSelection(.enabled)
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
        .navigationTitle("Settings").navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
    }
}

/// Fields are local drafts; only successful authentication installs a new session.
struct AccountSetupView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    var body: some View {
        Form {
            Section("Server login") { LoginFields(onSuccess: { dismiss() }, onBusyChanged: { busy = $0 }) }
            Section { Text("Use a server address other people can reach. Your files are encrypted automatically before upload.").foregroundStyle(PsstTheme.secondary) }
        }
        .navigationTitle("Server & account").navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(busy)
        .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(busy) } }
        .interactiveDismissDisabled(busy)
        .modifier(PsstStyle()).modifier(PsstAppearance())
    }
}

private struct LogoutButton: View {
    @Environment(ServerConfigManager.self) private var config
    @State private var error: String?
    @State private var busy = false
    var body: some View {
        Button("Sign out", role: .destructive) {
            busy = true
            Task {
                defer { busy = false }
                do { try await config.logout() } catch { self.error = String(localized: "Could not revoke your session. Reconnect and retry signing out.") }
            }
        }.disabled(busy)
        if let error {
            Text(error).foregroundStyle(PsstTheme.error)
        }
    }
}

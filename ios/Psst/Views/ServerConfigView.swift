import SwiftUI

struct ServerConfigView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    @Environment(\.dismiss) private var dismiss
    @AppStorage(AppConstants.appearanceKey, store: AppConstants.sharedDefaults)
    private var appearance = AppAppearance.system
    var isInitialSetup = false
    var body: some View {
        Form {
            Section("Appearance") {
                Picker("Theme", selection: $appearance) {
                    ForEach(AppAppearance.allCases, id: \.self) { value in
                        Text(value.title).tag(value)
                    }
                }
                .pickerStyle(.menu)
            }
            if serverConfig.isConfigured {
                Section("Account") {
                    Text(serverConfig.indicator).textSelection(.enabled)
                    LogoutButton()
                }
            }
            Section("Server login") { LoginFields() }
            Section {
                Text("Use a server address other people can reach. Your files are encrypted automatically before upload.")
                    .foregroundStyle(PsstTheme.secondary)
            }
        }
        .modifier(PsstStyle())
        .modifier(PsstAppearance())
        .navigationTitle(LocalizedStringKey(isInitialSetup ? "psst.zip" : "Settings"))
        .toolbar {
            if !isInitialSetup {
                Button("Done") { dismiss() }
            }
        }
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

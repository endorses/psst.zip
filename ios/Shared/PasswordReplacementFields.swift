import SwiftUI

/// Shared by account setup in the app and extension. Never resumes a transfer automatically.
struct PasswordReplacementFields: View {
    @Environment(ServerConfigManager.self) private var config
    @State private var current = ""
    @State private var replacement = ""
    @State private var confirmation = ""
    @State private var visible = false
    @State private var busy = false
    @State private var error: String?
    var onBusyChanged: (Bool) -> Void = { _ in }
    var onChanged: () -> Void = {}
    var body: some View {
        Text(config.indicator).font(.caption).foregroundStyle(PsstTheme.secondary)
        Text(config.requiresPasswordChange ? "Choose your own password" : "Change password").font(.headline)
        Text("Use 12–72 UTF-8 bytes and a password different from your current one. Afterwards, sign in again with the new password.").font(.footnote)
        SecureField("Current password", text: $current).textContentType(.password)
        Group {
            if visible {
                TextField("New password", text: $replacement)
                TextField("Confirm password", text: $confirmation)
            } else {
                SecureField("New password", text: $replacement)
                SecureField("Confirm password", text: $confirmation)
            }
        }.textContentType(.newPassword).textInputAutocapitalization(.never).autocorrectionDisabled()
        Button { visible.toggle() } label: { Label(visible ? "Hide passwords" : "Show passwords", systemImage: visible ? "eye.slash" : "eye") }
        if !confirmation.isEmpty, confirmation != replacement {
            Text("Passwords do not match.").foregroundStyle(PsstTheme.error)
        }
        if let error {
            Text(error).foregroundStyle(PsstTheme.error)
        }
        Button("Change password") {
            busy = true; error = nil; onBusyChanged(true)
            Task {
                defer { busy = false; onBusyChanged(false) }
                do {
                    try await config.changePassword(current: current, replacement: replacement)
                    current = ""; replacement = ""; confirmation = ""
                    onChanged()
                } catch { self.error = (error as? AccountError)?.localizedDescription ?? "Could not change your password. Reconnect and retry." }
            }
        }.buttonStyle(PrimaryAction()).disabled(busy || !PasswordReplacementPolicy.valid(current: current, replacement: replacement, confirmation: confirmation))
        if busy {
            ProgressView("Changing password")
        }
        Text("Your other sessions will be signed out.").font(.caption)
    }
}

enum PasswordReplacementPolicy {
    static func valid(current: String, replacement: String, confirmation: String) -> Bool {
        !current.isEmpty && replacement == confirmation && replacement != current && (12 ... 72).contains(replacement.utf8.count)
    }
}

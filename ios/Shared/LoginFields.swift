import SwiftUI

/// Also used inside the extension so selected attachments stay available while signing in.
struct LoginFields: View {
    @Environment(ServerConfigManager.self) private var config
    @State private var server = ""
    @State private var username = ""
    @State private var password = ""
    @State private var visible = false
    @State private var busy = false
    @State private var error: String?
    @State private var scanning = false
    @State private var scannedCode: String?
    @FocusState private var passwordFocused: Bool
    var body: some View {
        TextField("Server URL", text: $server).keyboardType(.URL).textContentType(.URL)
            .textInputAutocapitalization(.never).autocorrectionDisabled()
        TextField("Username", text: $username).textContentType(.username)
            .textInputAutocapitalization(.never).autocorrectionDisabled()
        HStack {
            Group {
                if visible {
                    TextField("Password", text: $password)
                } else {
                    SecureField("Password", text: $password)
                }
            }.textContentType(.password).focused($passwordFocused).submitLabel(.go).onSubmit { login() }
            Button { visible.toggle()
                passwordFocused = true
            } label: { Image(systemName: visible ? "eye.slash" : "eye").frame(minWidth: 44, minHeight: 44) }
                .accessibilityLabel(LocalizedStringKey(visible ? "Hide password" : "Show password"))
        }
        Button("Sign in") { login() }.disabled(busy || server.isEmpty || username.isEmpty || password.isEmpty)
            .buttonStyle(PrimaryAction())
        Button { scanning = true } label: { Label("Scan server login QR code", systemImage: "qrcode.viewfinder").frame(minHeight: 44) }.disabled(busy)
        if busy {
            ProgressView("Signing in")
        }
        if let error {
            Text(error).foregroundStyle(PsstTheme.error).accessibilityAddTraits(.isStaticText)
        }
        Text("To connect with a QR code, sign in on the website and open Settings → Connected devices.").font(.footnote).foregroundStyle(PsstTheme.secondary)
            .onAppear { server = config.serverURL }
            .sheet(isPresented: $scanning) {
                NavigationStack {
                    PairingScanner { raw in scannedCode = raw
                        scanning = false
                    }
                    .navigationTitle("Scan server login QR code")
                    .toolbar { Button("Cancel") { scanning = false } }
                }
            }
            .onChange(of: scannedCode) { _, code in
                guard let code else { return }
                busy = true
                Task {
                    defer { busy = false
                        scannedCode = nil
                    }
                    do { try await config.pair(raw: code)
                        password = ""
                    } catch { self.error = String(localized: "This login code is invalid, expired, or already used. Generate a new code on the website.") }
                }
            }
    }

    private func login() {
        guard !busy, !server.isEmpty, !username.isEmpty, !password.isEmpty else { return }
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do { try await config.login(server: server, username: username, password: password)
                password = ""
            } catch { self.error = String(localized: "Sign-in failed. Check your credentials, connection, and server address. HTTPS is required unless development HTTP is enabled.") }
        }
    }
}

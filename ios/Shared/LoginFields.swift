import Shared
import SwiftUI
import UniformTypeIdentifiers

/// Also used inside the extension so selected attachments stay available while signing in.
struct LoginFields: View {
    var onSuccess: () -> Void = {}
    var onBusyChanged: (Bool) -> Void = { _ in }
    @State private var pairingServer = ""
    @State private var confirmPairing = false
    @State private var connection: String?
    @Environment(ServerConfigManager.self) private var config
    @State private var server = ""
    @State private var username = ""
    @State private var password = ""
    @State private var visible = false
    @State private var busy = false
    @State private var error: String?
    @State private var scanning = false
    @State private var scannedCode: String?
    @State private var choosingImage = false
    @State private var imageError: String?
    @State private var pairingText = ""
    @FocusState private var passwordFocused: Bool
    var body: some View {
        if config.requiresPasswordChange {
            PasswordReplacementFields(onBusyChanged: onBusyChanged)
        } else {
            if config.passwordChanged {
                Text(L10n.text("Password changed. Sign in with your new password to continue.")).foregroundStyle(PsstTheme.success)
            }
            if let message = config.accountMessage {
                Text(L10n.text(message)).foregroundStyle(PsstTheme.warning)
            }
            TextField(L10n.text("Server URL"), text: $server).keyboardType(.URL).textContentType(.URL)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            if server.lowercased().hasPrefix("http://") {
                Text(L10n.text("HTTP sends login credentials without transport encryption. Use it only for local development on a trusted network; the server must explicitly allow it.")).font(.footnote).foregroundStyle(PsstTheme.warning)
            }
            TextField(L10n.text("Username"), text: $username).textContentType(.username)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            HStack {
                Group {
                    if visible {
                        TextField(L10n.text("Password"), text: $password)
                    } else {
                        SecureField(L10n.text("Password"), text: $password)
                    }
                }.textContentType(.password).focused($passwordFocused).submitLabel(.go).onSubmit { login() }
                Button { visible.toggle()
                    passwordFocused = true
                } label: { Image(systemName: visible ? "eye.slash" : "eye").frame(minWidth: 44, minHeight: 44) }
                    .accessibilityLabel(LocalizedStringKey(visible ? "Hide password" : "Show password"))
            }
            Button(L10n.text("Sign in")) { login() }.disabled(busy || server.isEmpty || username.isEmpty || password.isEmpty)
                .buttonStyle(PrimaryAction())
            Button { scanning = true } label: { Label(L10n.text("Scan server login QR code"), systemImage: "qrcode.viewfinder").frame(minHeight: 44) }.disabled(busy)
            Button(L10n.text("Test connection")) {
                busy = true
                onBusyChanged(true)
                Task {
                    defer { busy = false; onBusyChanged(false) }
                    do { try await config.testConnection(server: server); connection = "Connected" }
                    catch { connection = "Could not connect. Check the server address and network." }
                }
            }.disabled(busy || server.isEmpty)
            if let connection {
                Text(L10n.text(connection)).font(.footnote)
            }
            if busy {
                ProgressView(L10n.text("Signing in"))
            }
            if let error {
                Text(L10n.text(error)).foregroundStyle(PsstTheme.error).accessibilityAddTraits(.isStaticText)
            }
            Text(L10n.text("To connect with a QR code, sign in on the website and open Settings → Connected devices.")).font(.footnote).foregroundStyle(PsstTheme.secondary)
                .onAppear { server = config.serverURL; username = config.session?.username ?? "" }
                .sheet(isPresented: $scanning, onDismiss: classifyPairing) {
                    NavigationStack {
                        VStack {
                            PairingScanner(allowsPaste: true, isActive: !choosingImage) { raw in scannedCode = raw; scanning = false }
                            HStack {
                                TextField(L10n.text("Paste server login code"), text: $pairingText).textInputAutocapitalization(.never).autocorrectionDisabled()
                                PasteButton(payloadType: String.self) { values in pairingText = values.first ?? "" }
                            }.padding(.horizontal)
                            Button(L10n.text("Use login code")) { scannedCode = pairingText; scanning = false }.disabled(pairingText.isEmpty)
                            Button(L10n.text("Choose QR image")) { imageError = nil; choosingImage = true }
                            if let imageError {
                                Text(L10n.text(imageError)).foregroundStyle(PsstTheme.error)
                            }
                        }
                        .fileImporter(isPresented: $choosingImage, allowedContentTypes: [.image]) { result in
                            do { scannedCode = try QRImageReader.read(result.get()); scanning = false }
                            catch { imageError = "Choose an image containing one server login QR code." }
                        }
                        .navigationTitle(L10n.text("Scan server login QR code"))
                        .toolbar { Button(L10n.text("Cancel")) { scanning = false } }
                    }
                }
                .confirmationDialog(L10n.text("Connect to this server?"), isPresented: $confirmPairing, titleVisibility: .visible) {
                    Button(L10n.text("Connect to this server")) {
                        guard let code = scannedCode else { return }
                        busy = true
                        onBusyChanged(true)
                        Task {
                            defer { busy = false; onBusyChanged(false); scannedCode = nil }
                            do {
                                try await config.pair(raw: code); password = ""; if !config.requiresPasswordChange {
                                    onSuccess()
                                }
                            } catch { self.error = (error as? AccountError)?.localizedDescription ?? AccountError.pairing.localizedDescription }
                        }
                    }
                    Button(L10n.text("Cancel"), role: .cancel) { scannedCode = nil }
                } message: {
                    Text(L10n.text(pairingServer + (config.isConfigured ? L10n.text("\nThis replaces the current account.") : "") + (pairingServer.hasPrefix("http://") ? L10n.text("\nHTTP sends login credentials without transport encryption. Use only on a trusted development network.") : "")))
                }
        }
    }

    private func classifyPairing() {
        guard let code = scannedCode else { return }
        guard let parsed = ScanInputClassifier.shared.classify(raw: code), parsed.kind == .pairing, let pairing = parsed.pairing else {
            error = "Scan a server login QR code. Transfer links belong in Scan."; scannedCode = nil; return
        }
        pairingServer = pairing.serverUrl
        confirmPairing = true
    }

    private func login() {
        guard !busy, !server.isEmpty, !username.isEmpty, !password.isEmpty else { return }
        busy = true
        onBusyChanged(true)
        error = nil
        Task {
            defer { busy = false; onBusyChanged(false) }
            do { try await config.login(server: server, username: username, password: password)
                password = ""
                if !config.requiresPasswordChange {
                    onSuccess()
                }
            } catch { self.error = (error as? AccountError)?.localizedDescription ?? L10n.message("Sign-in failed. Check your credentials, connection, and server address. HTTPS is required unless development HTTP is enabled.") }
        }
    }
}

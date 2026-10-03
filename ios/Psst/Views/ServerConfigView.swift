import SwiftUI

struct ServerConfigView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    var isInitialSetup = false

    @State private var urlText = ""
    @State private var isTesting = false
    @State private var testResult: TestResult?
    @State private var connectionTask: Task<Void, Never>?

    enum TestResult {
        case success
        case failure(String)
    }

    var body: some View {
        Form {
            Section {
                TextField("Server URL", text: $urlText)
                    .textContentType(.URL)
                    .keyboardType(.URL)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
            } header: {
                Text("Server")
            } footer: {
                Text("Enter your self-hosted website's HTTPS address, e.g. https://drop.example.com. Shared links use this address too.")
            }

            Section {
                Button {
                    connectionTask = Task { await testConnection(saveOnSuccess: false) }
                } label: {
                    HStack {
                        Text("Test Connection")
                        Spacer()
                        if isTesting {
                            ProgressView()
                        }
                    }
                }
                .disabled(urlText.isEmpty || isTesting)

                if let testResult {
                    switch testResult {
                    case .success:
                        Label("Connection successful", systemImage: "checkmark.circle.fill")
                            .foregroundStyle(.green)
                    case let .failure(message):
                        Label(message, systemImage: "xmark.circle.fill")
                            .foregroundStyle(.red)
                    }
                }
            }

            Section {
                Button("Save") {
                    connectionTask = Task { await testConnection(saveOnSuccess: true) }
                }
                .disabled(urlText.isEmpty || isTesting)
                .frame(maxWidth: .infinity)
            }
        }
        .navigationTitle(isInitialSetup ? "Welcome to Psst" : "Server Settings")
        .onAppear {
            urlText = serverConfig.serverURL
        }
        .onChange(of: urlText) { _, _ in
            connectionTask?.cancel()
            testResult = nil
        }
        .onDisappear {
            connectionTask?.cancel()
        }
    }

    @MainActor
    private func testConnection(saveOnSuccess: Bool) async {
        guard !isTesting, !Task.isCancelled else { return }
        isTesting = true
        testResult = nil
        defer { isTesting = false }

        let trimmed = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            try await serverConfig.testConnection(url: trimmed)
            try Task.checkCancellation()
            guard urlText.trimmingCharacters(in: .whitespacesAndNewlines) == trimmed else { return }
            testResult = .success
            if saveOnSuccess {
                serverConfig.serverURL = trimmed
            }
        } catch is CancellationError {
            return
        } catch {
            guard !Task.isCancelled else { return }
            guard urlText.trimmingCharacters(in: .whitespacesAndNewlines) == trimmed else { return }
            testResult = .failure(error.localizedDescription)
        }
    }
}

import SwiftUI

struct ServerConfigView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    var isInitialSetup = false

    @State private var urlText = ""
    @State private var isTesting = false
    @State private var testResult: TestResult?

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
                Text("Enter the URL of your self-hosted psst server, e.g. https://drop.example.com")
            }

            Section {
                Button {
                    Task { await testConnection() }
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
                    saveURL()
                }
                .disabled(urlText.isEmpty)
                .frame(maxWidth: .infinity)
            }
        }
        .navigationTitle(isInitialSetup ? "Welcome to Psst" : "Server Settings")
        .onAppear {
            urlText = serverConfig.serverURL
        }
    }

    private func testConnection() async {
        isTesting = true
        testResult = nil

        let trimmed = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard URL(string: trimmed) != nil else {
            testResult = .failure("Invalid URL format")
            isTesting = false
            return
        }

        // Temporarily set the URL for the test.
        let previousURL = serverConfig.serverURL
        serverConfig.serverURL = trimmed

        let success = await serverConfig.testConnection()

        if !success {
            // Restore previous URL if test failed.
            serverConfig.serverURL = previousURL
            testResult = .failure("Could not reach server")
        } else {
            testResult = .success
        }

        isTesting = false
    }

    private func saveURL() {
        let trimmed = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        serverConfig.serverURL = trimmed
    }
}

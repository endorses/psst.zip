import SwiftUI

struct HomeView: View {
    @Environment(ServerConfigManager.self) private var serverConfig
    @Environment(TransferHistoryStore.self) private var historyStore

    @State private var showDocumentPicker = false
    @State private var sendViewModel: SendViewModel?
    @State private var receiveViewModel: ReceiveViewModel?
    @State private var navigateToSend = false
    @State private var navigateToReceive = false

    var body: some View {
        NavigationStack {
            VStack(spacing: 32) {
                Spacer()

                Image(systemName: "arrow.up.arrow.down.circle.fill")
                    .font(.system(size: 80))
                    .foregroundStyle(.tint)

                Text("psst.zip")
                    .font(.largeTitle.bold())

                Text("End-to-end encrypted file transfer")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)

                Spacer()

                VStack(spacing: 16) {
                    Button {
                        showDocumentPicker = true
                    } label: {
                        Label("Share Files", systemImage: "square.and.arrow.up")
                            .frame(maxWidth: .infinity)
                            .padding()
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)

                    Button {
                        startReceiving()
                    } label: {
                        Label("Receive Files", systemImage: "square.and.arrow.down")
                            .frame(maxWidth: .infinity)
                            .padding()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.large)
                }
                .padding(.horizontal, 32)

                Spacer()
            }
            .navigationTitle("")
            .fileImporter(
                isPresented: $showDocumentPicker,
                allowedContentTypes: [.item],
                allowsMultipleSelection: true
            ) { result in
                handlePickedFiles(result)
            }
            .navigationDestination(isPresented: $navigateToSend) {
                if let sendViewModel {
                    TransferDetailView(sendViewModel: sendViewModel)
                }
            }
            .navigationDestination(isPresented: $navigateToReceive) {
                if let receiveViewModel {
                    TransferDetailView(receiveViewModel: receiveViewModel)
                }
            }
        }
    }

    private func handlePickedFiles(_ result: Result<[URL], Error>) {
        guard case .success(let urls) = result, !urls.isEmpty else { return }
        let vm = SendViewModel(
            fileURLs: urls,
            serverConfig: serverConfig,
            historyStore: historyStore
        )
        sendViewModel = vm
        navigateToSend = true
        Task {
            await vm.startUpload()
        }
    }

    private func startReceiving() {
        let vm = ReceiveViewModel(
            serverConfig: serverConfig,
            historyStore: historyStore
        )
        receiveViewModel = vm
        navigateToReceive = true
        Task {
            await vm.createDropSlot()
        }
    }
}

import SwiftUI

struct HomeView: View {
    @Environment(ServerConfigManager.self) private var config
    @Environment(TransferHistoryStore.self) private var history
    @Environment(\.scenePhase) private var scenePhase
    var receiving = false
    @State private var picking = false
    @State private var accountSetup = false
    @State private var send: SendViewModel?
    @State private var receive: ReceiveViewModel?
    @State private var showing = false
    @State private var error: String?
    @State private var selected: [URL] = []
    @State private var receiveName = ""
    @State private var limitEnabled = false
    @State private var limitValue = "1"
    @State private var creatingReceive = false
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 20) {
                    HStack { Image("BrandSymbol").resizable().scaledToFit().frame(width: 40, height: 40).accessibilityHidden(true); Text("psst.zip").font(.largeTitle.bold()) }
                    Text(LocalizedStringKey(receiving ? "Create a receive link for someone to send you files, nearby or elsewhere." : "Send encrypted files with a link or QR code, nearby or elsewhere."))
                        .foregroundStyle(PsstTheme.secondary)
                    Text(config.limitDescription + " Files are encrypted automatically.").font(.footnote)
                    if config.isConfigured, !config.needsSignIn {
                        if receiving {
                            TextField("Name this receive link (optional)", text: $receiveName).disabled(creatingReceive)
                            Text("Names are saved only on this device.").font(.caption).foregroundStyle(PsstTheme.secondary)
                        }
                        LinkLimitControl(receiving: receiving, enabled: $limitEnabled, value: $limitValue).disabled(creatingReceive)
                        Button(LocalizedStringKey(receiving ? "Create receive link" : "Choose files")) {
                            if receiving {
                                createReceive()
                            } else {
                                picking = true
                            }
                        }.buttonStyle(PrimaryAction()).disabled(creatingReceive || LinkLimit.parse(limitValue, enabled: limitEnabled) == nil)
                        if !selected.isEmpty, send == nil {
                            Text(String(format: String(localized: "%lld files selected"), Int64(selected.count)))
                            Button("Send files") { startSend() }.buttonStyle(PrimaryAction())
                        }
                        if send != nil || receive != nil {
                            Button("Return to transfer") { showing = true }.frame(minHeight: 44)
                        }
                    } else {
                        Text(config.accountMessage ?? "Sign in to continue this task.").font(.headline)
                        Button(config.requiresPasswordChange ? "Change password" : "Sign in") { accountSetup = true }.buttonStyle(PrimaryAction())
                    }
                    if let error {
                        Text(error).foregroundStyle(PsstTheme.error)
                    }
                }.padding(24).frame(maxWidth: 600)
            }
            .navigationTitle(LocalizedStringKey(receiving ? "Receive" : "Send"))
            .navigationDestination(isPresented: $showing) {
                TransferDetailView(sendViewModel: send, receiveViewModel: receive)
            }
            .task(id: config.serverURL) { await config.refreshLimit() }
            .sheet(isPresented: $accountSetup) { NavigationStack { AccountSetupView() } }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                do {
                    let urls = try result.get()
                    _ = try BufferedUpload.sizes(urls, limit: BufferedUpload.maxFileBytes)
                    selected = urls
                    startSend()
                } catch { self.error = String(localized: "Could not select these files. Check file access and the server’s file-size limit.") }
            }
            .onChange(of: config.accountID) { old, next in
                if old != nil, old != next {
                    send?.clearForAccountChange()
                    receive?.cancelSaving()
                    send = nil
                    receive = nil
                    selected = []
                    limitEnabled = false
                    limitValue = "1"
                    receiveName = ""
                    showing = false
                }
            }
            .onChange(of: scenePhase) { _, next in
                if next == .background {
                    send?.stop()
                    receive?.cancelSaving()
                }
            }
        }.modifier(PsstStyle())
    }

    private func startSend() {
        guard !selected.isEmpty, let limit = LinkLimit.parse(limitValue, enabled: limitEnabled) else { return }
        let vm = SendViewModel(fileURLs: selected, serverConfig: config, historyStore: history, maxDownloads: limit)
        limitEnabled = false
        limitValue = "1"
        send = vm
        receive = nil
        showing = true
        vm.start()
    }

    private func createReceive() {
        guard !creatingReceive, let limit = LinkLimit.parse(limitValue, enabled: limitEnabled) else { return }
        creatingReceive = true
        let vm = ReceiveViewModel(serverConfig: config, historyStore: history, localName: receiveName, maxFiles: limit)
        limitEnabled = false
        limitValue = "1"
        receiveName = ""
        receive = vm
        send = nil
        showing = true
        Task {
            defer { creatingReceive = false }
            await vm.createDropSlot()
        }
    }
}

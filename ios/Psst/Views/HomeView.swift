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
    @State private var sendName = ""
    @State private var limitEnabled = false
    @State private var limitValue = "1"
    @State private var creatingReceive = false
    var body: some View {
        NavigationStack {
            Group {
                if receiving, let receive {
                    TransferDetailView(
                        receiveViewModel: receive, rootInbox: true,
                        onCreateAnother: {
                            receive.cancelSaving()
                            self.receive = nil
                            receiveName = ""
                            limitEnabled = false
                            limitValue = "1"
                        }
                    )
                } else {
                    ScrollView {
                        VStack(spacing: 20) {
                            HStack {
                                Image("BrandSymbol").resizable().scaledToFit().frame(width: 50, height: 50).accessibilityHidden(true)
                                Text(L10n.text("psst.zip")).font(.largeTitle.bold())
                            }
                            Text(
                                LocalizedStringKey(
                                    receiving
                                        ? "Create a receive link for someone to send you files, nearby or elsewhere."
                                        : "Send encrypted files with a link or QR code, nearby or elsewhere."
                                )
                            )
                            .foregroundStyle(PsstTheme.secondary)
                            Text(L10n.text(L10n.format("%@ Files are encrypted automatically.", L10n.text(config.limitDescription)))).font(.footnote)
                            if config.isConfigured, !config.needsSignIn {
                                if receiving {
                                    TextField(L10n.text("Name this receive link (optional)"), text: $receiveName).disabled(creatingReceive)
                                    Text(L10n.text("Shown to people using this link.")).font(.caption).foregroundStyle(PsstTheme.secondary)
                                } else {
                                    TextField(L10n.text("Shared title (optional)"), text: $sendName)
                                    Text(L10n.text("Shown to people using this link.")).font(.caption).foregroundStyle(PsstTheme.secondary)
                                }
                                LinkLimitControl(receiving: receiving, enabled: $limitEnabled, value: $limitValue).disabled(creatingReceive)
                                Button(LocalizedStringKey(receiving ? "Create receive link" : "Choose files")) {
                                    if receiving {
                                        createReceive()
                                    } else {
                                        picking = true
                                    }
                                }.buttonStyle(PrimaryAction()).disabled(creatingReceive || send?.active == true || LinkLimit.parse(limitValue, enabled: limitEnabled) == nil)
                                if !selected.isEmpty, send == nil {
                                    Text(L10n.text(L10n.format("%lld files selected", Int64(selected.count))))
                                    ForEach(selected, id: \.self) { url in
                                        HStack {
                                            Text(verbatim: url.lastPathComponent).lineLimit(2)
                                            Spacer()
                                            Button {
                                                selected.removeAll { $0 == url }
                                            } label: {
                                                Image(systemName: "xmark.circle")
                                            }.accessibilityLabel(L10n.text(L10n.format("Remove %@", url.lastPathComponent)))
                                        }
                                    }
                                    Button(L10n.text("Send files")) { startSend() }.buttonStyle(PrimaryAction())
                                }
                                if send != nil || receive != nil {
                                    Button(L10n.text("Return to transfer")) { showing = true }.frame(minHeight: 44)
                                }
                            } else {
                                Text(L10n.text(config.accountMessage ?? "Sign in to continue this task.")).font(.headline)
                                Button(L10n.text(config.requiresPasswordChange ? "Change password" : "Sign in")) { accountSetup = true }.buttonStyle(PrimaryAction())
                            }
                            if let error {
                                Text(L10n.text(error)).foregroundStyle(PsstTheme.error)
                            }
                        }.padding(24).frame(maxWidth: 600)
                    }
                }
            }
            .navigationTitle(LocalizedStringKey(receiving ? "Receive" : "Send"))
            .navigationDestination(isPresented: $showing) {
                TransferDetailView(
                    sendViewModel: send, receiveViewModel: receive,
                    onCreateReplacement: {
                        showing = false
                        send = nil
                        error = nil
                    }
                )
            }
            .task(id: config.serverURL) { await config.refreshLimit() }
            .sheet(isPresented: $accountSetup) { NavigationStack { AccountSetupView() } }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                do {
                    let urls = try result.get()
                    _ = try BufferedUpload.sizes(urls, limit: BufferedUpload.maxFileBytes)
                    selected = urls
                    if send?.active != true {
                        send = nil
                    }
                } catch { self.error = L10n.message("Could not select these files. Check file access and the server’s file-size limit.") }
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
                    sendName = ""
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
        do { _ = try SharedLinkTitle.normalize(sendName) } catch {
            self.error = L10n.failure(error, fallback: "Could not select these files. Check file access and the server’s file-size limit.")
            return
        }
        let vm = SendViewModel(fileURLs: selected, serverConfig: config, historyStore: history, maxDownloads: limit, sharedTitle: sendName)
        limitEnabled = false
        limitValue = "1"
        send = vm
        receive = nil
        showing = true
        vm.start()
    }

    private func createReceive() {
        guard !creatingReceive, let limit = LinkLimit.parse(limitValue, enabled: limitEnabled) else { return }
        do { _ = try SharedLinkTitle.normalize(receiveName) } catch {
            self.error = L10n.failure(error, fallback: "Could not select these files. Check file access and the server’s file-size limit.")
            return
        }
        creatingReceive = true
        let vm = ReceiveViewModel(serverConfig: config, historyStore: history, localName: receiveName, maxFiles: limit)
        limitEnabled = false
        limitValue = "1"
        receiveName = ""
        receive = vm
        send = nil
        showing = false
        Task {
            defer { creatingReceive = false }
            await vm.createDropSlot()
        }
    }
}
